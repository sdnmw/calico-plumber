package diag

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"syscall"
	"time"

	"calico-netdiag/pkg/agent/netns"
	"calico-netdiag/pkg/model"
)

const (
	linuxIPMTUDiscover = 10 // IP_MTU_DISCOVER on Linux
	linuxIPPMTUDiscDo  = 2  // IP_PMTUDISC_DO: always set DF bit
)

// RunPMTUDProbe executes an ICMP Don't Fragment (DF) Path MTU Discovery probe from inside a Pod's netns.
// It detects classic Calico IPIP/VXLAN MTU blackholes ("small pings work, large TLS/HTTP packets stall").
func RunPMTUDProbe(ctx context.Context, podUID, podIP, podDisplay string, req model.PMTUDProbeRequest) (*model.PMTUDProbeResult, error) {
	info, err := netns.LocatePod(podUID, podIP)
	if err != nil && (info == nil || info.NetnsPath == "") {
		return nil, fmt.Errorf("locate pod netns: %w", err)
	}
	targetIP := net.ParseIP(req.TargetIP).To4()
	if targetIP == nil {
		return nil, fmt.Errorf("invalid IPv4 target %q", req.TargetIP)
	}
	timeoutMs := req.TimeoutMs
	if timeoutMs <= 0 || timeoutMs > 4000 {
		timeoutMs = 900
	}
	maxMTU := req.MaxMTU
	if maxMTU <= 0 || maxMTU > 9000 {
		maxMTU = 1500
	}

	res := &model.PMTUDProbeResult{
		SrcPod:   podDisplay,
		SrcIP:    podIP,
		TargetIP: targetIP.String(),
	}

	var attempts []model.PMTUDAttempt
	var podMTU int

	runErr := netns.RunInNetns(info.NetnsPath, func() error {
		if iface, ierr := net.InterfaceByName("eth0"); ierr == nil {
			podMTU = iface.MTU
		}
		if podMTU > 0 && podMTU < maxMTU {
			maxMTU = podMTU
		}

		fd, serr := syscall.Socket(syscall.AF_INET, syscall.SOCK_RAW, syscall.IPPROTO_ICMP)
		if serr != nil {
			return fmt.Errorf("open raw ICMP socket: %w", serr)
		}
		defer syscall.Close(fd)

		// Force IPv4 Don't Fragment (DF) bit on all outgoing probe packets!
		_ = syscall.SetsockoptInt(fd, syscall.IPPROTO_IP, linuxIPMTUDiscover, linuxIPPMTUDiscDo)

		tv := syscall.Timeval{
			Sec:  int64(timeoutMs / 1000),
			Usec: int64((timeoutMs % 1000) * 1000),
		}
		_ = syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)

		var dstAddr [4]byte
		copy(dstAddr[:], targetIP)
		sa := &syscall.SockaddrInet4{Addr: dstAddr}
		baseID := uint16(time.Now().UnixNano() & 0xffff)
		if baseID == 0 {
			baseID = 0x5151
		}
		seq := uint16(1)

		probeSize := func(totalIPBytes int) model.PMTUDAttempt {
			payloadLen := totalIPBytes - 28 // 20B IPv4 + 8B ICMP header
			if payloadLen < 8 {
				payloadLen = 8
			}
			payload := make([]byte, payloadLen)
			copy(payload, []byte("CALICO-PMTUD"))
			pkt := buildICMPEchoRequest(baseID, seq, payload)
			curSeq := seq
			seq++

			st := time.Now()
			if err := syscall.Sendto(fd, pkt, 0, sa); err != nil {
				lat := round2ms(time.Since(st))
				if errors.Is(err, syscall.EMSGSIZE) {
					return model.PMTUDAttempt{
						PacketSize: totalIPBytes,
						Success:    false,
						Result:     "FRAG_NEEDED",
						LatencyMs:  lat,
						Detail:     "本地网卡/内核 PMTU 拒绝分片 (EMSGSIZE)",
					}
				}
				return model.PMTUDAttempt{
					PacketSize: totalIPBytes,
					Success:    false,
					Result:     "ERROR",
					LatencyMs:  lat,
					Detail:     err.Error(),
				}
			}

			buf := make([]byte, 2048)
			deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
			for time.Now().Before(deadline) {
				if ctx.Err() != nil {
					return model.PMTUDAttempt{PacketSize: totalIPBytes, Result: "TIMEOUT", LatencyMs: round2ms(time.Since(st))}
				}
				n, _, rerr := syscall.Recvfrom(fd, buf, 0)
				if rerr != nil {
					return model.PMTUDAttempt{
						PacketSize: totalIPBytes,
						Success:    false,
						Result:     "TIMEOUT",
						LatencyMs:  round2ms(time.Since(st)),
						Detail:     "DF 大包无回包（疑似路径 MTU 黑洞或丢弃 ICMP Fragmentation Needed）",
					}
				}
				if n < 28 {
					continue
				}
				ihl := int(buf[0]&0x0f) * 4
				if n < ihl+8 {
					continue
				}
				icmpType := buf[ihl]
				icmpCode := buf[ihl+1]
				if icmpType == 0 { // Echo Reply
					rid := binary.BigEndian.Uint16(buf[ihl+4 : ihl+6])
					rseq := binary.BigEndian.Uint16(buf[ihl+6 : ihl+8])
					if rid == baseID && rseq == curSeq {
						return model.PMTUDAttempt{
							PacketSize: totalIPBytes,
							Success:    true,
							Result:     "ECHO_REPLY",
							LatencyMs:  round2ms(time.Since(st)),
							Detail:     fmt.Sprintf("DF %dB 往返成功", totalIPBytes),
						}
					}
				} else if icmpType == 3 && icmpCode == 4 { // Dest Unreachable: Fragmentation Needed
					nextHopMTU := binary.BigEndian.Uint16(buf[ihl+6 : ihl+8])
					return model.PMTUDAttempt{
						PacketSize: totalIPBytes,
						Success:    false,
						Result:     "FRAG_NEEDED",
						LatencyMs:  round2ms(time.Since(st)),
						Detail:     fmt.Sprintf("收到 ICMP Fragmentation Needed (Next-Hop MTU=%d)", nextHopMTU),
					}
				}
			}
			return model.PMTUDAttempt{
				PacketSize: totalIPBytes,
				Success:    false,
				Result:     "TIMEOUT",
				LatencyMs:  round2ms(time.Since(st)),
				Detail:     "等待回包超时",
			}
		}

		// Step 1: Baseline small packet (64B) to verify basic ICMP reachability
		base := probeSize(64)
		attempts = append(attempts, base)
		if !base.Success {
			return nil
		}

		// Step 2: Fast-path test at maxMTU (typically Pod eth0 MTU, e.g. 1500 / 1480 / 1450)
		full := probeSize(maxMTU)
		attempts = append(attempts, full)
		if full.Success {
			res.DiscoveredMTU = maxMTU
			return nil
		}

		// Step 3: Binary search exact Path MTU between 576 and maxMTU-1 (converges in ~7 probes!)
		lo, hi := 576, maxMTU-1
		best := 64
		// Quick check common tunnel boundaries first for fast clarity
		for _, cand := range []int{1480, 1450, 1440, 1400, 1280} {
			if cand >= maxMTU {
				continue
			}
			a := probeSize(cand)
			attempts = append(attempts, a)
			if a.Success {
				best = cand
				lo = cand + 1
				break
			}
			hi = cand - 1
		}
		for lo <= hi && len(attempts) < 12 {
			mid := (lo + hi) / 2
			a := probeSize(mid)
			attempts = append(attempts, a)
			if a.Success {
				best = mid
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
		res.DiscoveredMTU = best
		return nil
	})
	if runErr != nil {
		return nil, runErr
	}

	res.PodMTU = podMTU
	res.Attempts = attempts
	switch {
	case res.DiscoveredMTU == 0:
		res.Status = "critical"
		res.Diagnosis = fmt.Sprintf("64B 基础 ICMP 探测即未收到回包，请先确认到 %s 的路由/NetworkPolicy/防火墙 ICMP 放行状态。", res.TargetIP)
	case podMTU > 0 && res.DiscoveredMTU < podMTU:
		res.Blackhole = true
		res.Status = "critical"
		res.Diagnosis = fmt.Sprintf("检出路径 MTU 黑洞！源 Pod eth0 MTU=%d，但实际可通最大 DF 报文仅 %dB（差值 %dB）。请核查 Calico IPIP(-20B)/VXLAN(-50B) 隧道开销、物理交换机 MTU 或中间防火墙 ICMP Fragmentation Needed 策略。",
			podMTU, res.DiscoveredMTU, podMTU-res.DiscoveredMTU)
	default:
		res.Status = "pass"
		res.Diagnosis = fmt.Sprintf("路径 MTU 健康：DF 大包 %dB（匹配 Pod eth0 MTU=%d）端到端无分片直通，不存在 MTU 黑洞。",
			res.DiscoveredMTU, podMTU)
	}
	return res, nil
}

func round2ms(d time.Duration) float64 {
	return math.Round(float64(d.Microseconds())/10.0) / 100.0
}
