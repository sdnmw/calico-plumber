package diag

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	"calico-netdiag/pkg/agent/netns"
	"calico-netdiag/pkg/model"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

// RunTCPHandshake enters the source Pod's netns, resolves the target host,
// captures real wire-level SYN / SYN-ACK / ACK / RST packets on eth0, and measures
// microsecond-accurate TCP 3-way handshake timing across N rounds.
func RunTCPHandshake(ctx context.Context, podUID, podIP, podDisplay string, req model.TCPHandshakeRequest) (*model.TCPHandshakeResponse, error) {
	if req.Count <= 0 || req.Count > 10 {
		req.Count = 3
	}
	if req.TimeoutMs <= 0 || req.TimeoutMs > 10000 {
		req.TimeoutMs = 3000
	}
	if req.TargetPort <= 0 || req.TargetPort > 65535 {
		return nil, fmt.Errorf("invalid targetPort %d", req.TargetPort)
	}

	info, err := netns.LocatePod(podUID, podIP)
	if err != nil {
		return nil, fmt.Errorf("locate source pod: %w", err)
	}

	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	resp := &model.TCPHandshakeResponse{
		SrcPod:     podDisplay,
		SrcIP:      podIP,
		TargetHost: req.TargetHost,
		TargetPort: req.TargetPort,
		TotalCount: req.Count,
	}

	// Resolve hostname inside Pod netns if not already an IP
	resolvedIP := req.TargetHost
	var dnsMs float64
	if net.ParseIP(req.TargetHost) == nil {
		dnsStart := time.Now()
		resolver := netns.NewNetnsResolver(info.NetnsPath, timeout)
		ips, derr := resolver.LookupIP(ctx, "ip4", req.TargetHost)
		dnsMs = roundMs(time.Since(dnsStart))
		if derr != nil || len(ips) == 0 {
			resp.Diagnosis = fmt.Sprintf("DNS 解析失败 (%s): %v — 请检查 Pod /etc/resolv.conf 与 CoreDNS 健康状态", req.TargetHost, derr)
			resp.Attempts = []model.TCPHandshakeAttempt{{
				Round:       1,
				Success:     false,
				Result:      "DNS_FAILED",
				DNSLookupMs: dnsMs,
				Error:       fmt.Sprintf("%v", derr),
				Steps: []model.TCPHandshakeStep{{
					Step:        "DNS",
					Direction:   "local",
					TimestampMs: dnsMs,
					DeltaMs:     dnsMs,
					Detail:      fmt.Sprintf("DNS lookup failed for %s: %v", req.TargetHost, derr),
				}},
			}}
			return resp, nil
		}
		resolvedIP = ips[0].String()
	}
	resp.ResolvedIP = resolvedIP

	var rtts []float64
	refusedCnt := 0
	timeoutCnt := 0

	for round := 1; round <= req.Count; round++ {
		attempt := runSingleHandshakeRound(ctx, info, resolvedIP, req.TargetHost, req.TargetPort, dnsMs, round, timeout)
		resp.Attempts = append(resp.Attempts, attempt)
		if attempt.Success {
			resp.SuccessCount++
			rtts = append(rtts, attempt.SynAckRTTMs)
		} else if attempt.Result == "RST_REFUSED" {
			refusedCnt++
		} else if attempt.Result == "SYN_TIMEOUT" {
			timeoutCnt++
		}
		if round < req.Count {
			time.Sleep(120 * time.Millisecond)
		}
	}

	if len(rtts) > 0 {
		minV, maxV, sum := rtts[0], rtts[0], 0.0
		for _, v := range rtts {
			if v < minV {
				minV = v
			}
			if v > maxV {
				maxV = v
			}
			sum += v
		}
		resp.MinRTTMs = round2f(minV)
		resp.MaxRTTMs = round2f(maxV)
		resp.AvgRTTMs = round2f(sum / float64(len(rtts)))
	}

	switch {
	case resp.SuccessCount == resp.TotalCount:
		resp.Diagnosis = fmt.Sprintf("TCP 三次握手全部成功 (%d/%d)，SYN→SYN-ACK 平均延迟 %.2f ms", resp.SuccessCount, resp.TotalCount, resp.AvgRTTMs)
	case refusedCnt > 0 && resp.SuccessCount == 0:
		resp.Diagnosis = fmt.Sprintf("目标 %s:%d 返回 TCP RST (Connection Refused)：网络路由与策略已通，但目标端口未监听或应用拒绝连接", resolvedIP, req.TargetPort)
	case timeoutCnt > 0 && resp.SuccessCount == 0:
		resp.Diagnosis = fmt.Sprintf("SYN 超时无应答 (%d/%d)：报文可能被 Calico NetworkPolicy / 防火墙丢弃，或存在跨节点路由/隧道异常，建议立即执行 TraceFlow 定位丢包跳点", timeoutCnt, resp.TotalCount)
	default:
		resp.Diagnosis = fmt.Sprintf("TCP 握手部分成功 (%d/%d)，存在连接抖动或部分异常后端（建议检查 Service Endpoint 健康状态）", resp.SuccessCount, resp.TotalCount)
	}

	return resp, nil
}

func runSingleHandshakeRound(
	ctx context.Context,
	info *netns.PodNetInfo,
	dstIP, origHost string,
	dstPort int,
	dnsMs float64,
	round int,
	timeout time.Duration,
) model.TCPHandshakeAttempt {
	attempt := model.TCPHandshakeAttempt{
		Round:       round,
		ResolvedIP:  dstIP,
		DNSLookupMs: dnsMs,
	}
	if origHost != dstIP {
		attempt.Steps = append(attempt.Steps, model.TCPHandshakeStep{
			Step:        "DNS",
			Direction:   "local",
			TimestampMs: dnsMs,
			DeltaMs:     dnsMs,
			Detail:      fmt.Sprintf("%s → %s (%.2f ms)", origHost, dstIP, dnsMs),
		})
	}

	// Arm a short AF_PACKET sniffer on Pod eth0 to observe real SYN / SYN-ACK / ACK / RST wire packets
	capCtx, capCancel := context.WithTimeout(ctx, timeout+350*time.Millisecond)
	defer capCancel()

	type wireEvt struct {
		step   string
		dir    string
		tsNs   int64
		seq    uint32
		ack    uint32
		win    uint16
		detail string
	}
	var (
		evtMu   sync.Mutex
		wireEvs []wireEvt
		wg      sync.WaitGroup
		ready   = make(chan struct{})
	)

	sniffIface := "eth0"
	if info.HostNet {
		sniffIface = ""
	}
	if sniffIface != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var fd int
			openErr := netns.RunInNetns(info.NetnsPath, func() error {
				iface, err := net.InterfaceByName(sniffIface)
				if err != nil {
					return err
				}
				proto := htons(syscall.ETH_P_ALL)
				sfd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(proto))
				if err != nil {
					return err
				}
				if err := syscall.Bind(sfd, &syscall.SockaddrLinklayer{Protocol: proto, Ifindex: iface.Index}); err != nil {
					_ = syscall.Close(sfd)
					return err
				}
				tv := syscall.Timeval{Sec: 0, Usec: 100000}
				_ = syscall.SetsockoptTimeval(sfd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
				fd = sfd
				return nil
			})
			close(ready)
			if openErr != nil || fd <= 0 {
				return
			}
			defer syscall.Close(fd)

			buf := make([]byte, 2048)
			for {
				select {
				case <-capCtx.Done():
					return
				default:
				}
				n, _, err := syscall.Recvfrom(fd, buf, 0)
				if err != nil {
					continue
				}
				if n < 54 || binary.BigEndian.Uint16(buf[12:14]) != 0x0800 {
					continue
				}
				pkt := gopacket.NewPacket(buf[14:n], layers.LayerTypeIPv4, gopacket.NoCopy)
				ipL := pkt.Layer(layers.LayerTypeIPv4)
				tcpL := pkt.Layer(layers.LayerTypeTCP)
				if ipL == nil || tcpL == nil {
					continue
				}
				ip4 := ipL.(*layers.IPv4)
				tcp := tcpL.(*layers.TCP)
				sIP, dIP := ip4.SrcIP.String(), ip4.DstIP.String()
				nowNs := time.Now().UnixNano()

				var ev *wireEvt
				if dIP == dstIP && int(tcp.DstPort) == dstPort && tcp.SYN && !tcp.ACK {
					ev = &wireEvt{step: "SYN", dir: "outbound", tsNs: nowNs, seq: tcp.Seq, win: tcp.Window, detail: fmt.Sprintf("%s:%d → %s:%d [SYN] Seq=%d Win=%d", sIP, tcp.SrcPort, dIP, tcp.DstPort, tcp.Seq, tcp.Window)}
				} else if sIP == dstIP && int(tcp.SrcPort) == dstPort && tcp.SYN && tcp.ACK {
					ev = &wireEvt{step: "SYN-ACK", dir: "inbound", tsNs: nowNs, seq: tcp.Seq, ack: tcp.Ack, win: tcp.Window, detail: fmt.Sprintf("%s:%d → %s:%d [SYN,ACK] Seq=%d Ack=%d Win=%d", sIP, tcp.SrcPort, dIP, tcp.DstPort, tcp.Seq, tcp.Ack, tcp.Window)}
				} else if dIP == dstIP && int(tcp.DstPort) == dstPort && tcp.ACK && !tcp.SYN && !tcp.FIN && !tcp.RST {
					ev = &wireEvt{step: "ACK", dir: "outbound", tsNs: nowNs, seq: tcp.Seq, ack: tcp.Ack, win: tcp.Window, detail: fmt.Sprintf("%s:%d → %s:%d [ACK] Seq=%d Ack=%d (Handshake Complete)", sIP, tcp.SrcPort, dIP, tcp.DstPort, tcp.Seq, tcp.Ack)}
				} else if sIP == dstIP && int(tcp.SrcPort) == dstPort && tcp.RST {
					ev = &wireEvt{step: "RST", dir: "inbound", tsNs: nowNs, seq: tcp.Seq, ack: tcp.Ack, detail: fmt.Sprintf("%s:%d → %s:%d [RST] Connection refused", sIP, tcp.SrcPort, dIP, tcp.DstPort)}
				}
				if ev != nil {
					evtMu.Lock()
					if len(wireEvs) < 6 {
						wireEvs = append(wireEvs, *ev)
					}
					evtMu.Unlock()
				}
			}
		}()
		select {
		case <-ready:
		case <-time.After(200 * time.Millisecond):
		}
	}

	start := time.Now()
	conn, dialErr := netns.DialInNetns(ctx, info.NetnsPath, "tcp4", net.JoinHostPort(dstIP, fmt.Sprintf("%d", dstPort)), nil, timeout)
	dialDur := time.Since(start)
	if conn != nil {
		_ = conn.Close()
	}
	time.Sleep(40 * time.Millisecond)
	capCancel()
	wg.Wait()

	attempt.TotalMs = roundMs(dialDur)
	if dialErr == nil {
		attempt.Success = true
		attempt.Result = "ESTABLISHED"
		attempt.SynAckRTTMs = attempt.TotalMs
	} else if strings.Contains(dialErr.Error(), "connection refused") {
		attempt.Result = "RST_REFUSED"
		attempt.Error = dialErr.Error()
	} else if strings.Contains(dialErr.Error(), "timeout") || strings.Contains(dialErr.Error(), "deadline") {
		attempt.Result = "SYN_TIMEOUT"
		attempt.Error = dialErr.Error()
	} else {
		attempt.Result = "ERROR"
		attempt.Error = dialErr.Error()
	}

	// Build step timeline from real wire packets (or clean fallback from dial timings)
	if len(wireEvs) > 0 {
		baseNs := wireEvs[0].tsNs
		var synNs, synAckNs int64
		prevNs := baseNs
		for _, ev := range wireEvs {
			relMs := float64(ev.tsNs-baseNs) / 1e6
			deltaMs := float64(ev.tsNs-prevNs) / 1e6
			prevNs = ev.tsNs
			if ev.step == "SYN" && synNs == 0 {
				synNs = ev.tsNs
			}
			if ev.step == "SYN-ACK" && synAckNs == 0 {
				synAckNs = ev.tsNs
			}
			attempt.Steps = append(attempt.Steps, model.TCPHandshakeStep{
				Step:        ev.step,
				Direction:   ev.dir,
				TimestampMs: round2f(relMs + dnsMs),
				DeltaMs:     round2f(deltaMs),
				Seq:         ev.seq,
				Ack:         ev.ack,
				Window:      ev.win,
				Detail:      ev.detail,
			})
		}
		if synNs > 0 && synAckNs > synNs {
			attempt.SynAckRTTMs = round2f(float64(synAckNs-synNs) / 1e6)
		}
	} else {
		// Fallback steps when running on hostNetwork or if AF_PACKET missed
		attempt.Steps = append(attempt.Steps, model.TCPHandshakeStep{
			Step: "SYN", Direction: "outbound", TimestampMs: round2f(dnsMs), DeltaMs: 0,
			Detail: fmt.Sprintf("SYN sent to %s:%d", dstIP, dstPort),
		})
		if attempt.Success {
			attempt.Steps = append(attempt.Steps,
				model.TCPHandshakeStep{Step: "SYN-ACK", Direction: "inbound", TimestampMs: round2f(dnsMs + attempt.TotalMs), DeltaMs: attempt.TotalMs, Detail: fmt.Sprintf("SYN-ACK received from %s:%d", dstIP, dstPort)},
				model.TCPHandshakeStep{Step: "ACK", Direction: "outbound", TimestampMs: round2f(dnsMs + attempt.TotalMs), DeltaMs: 0, Detail: "ACK sent — TCP connection established"},
			)
		} else if attempt.Result == "RST_REFUSED" {
			attempt.Steps = append(attempt.Steps,
				model.TCPHandshakeStep{Step: "RST", Direction: "inbound", TimestampMs: round2f(dnsMs + attempt.TotalMs), DeltaMs: attempt.TotalMs, Detail: "TCP RST received (Port not listening / refused)"},
			)
		} else {
			attempt.Steps = append(attempt.Steps,
				model.TCPHandshakeStep{Step: "TIMEOUT", Direction: "inbound", TimestampMs: round2f(dnsMs + attempt.TotalMs), DeltaMs: attempt.TotalMs, Detail: "No SYN-ACK within timeout (likely dropped)"},
			)
		}
	}

	return attempt
}

func roundMs(d time.Duration) float64 {
	return round2f(float64(d.Microseconds()) / 1000.0)
}

func round2f(v float64) float64 {
	return math.Round(v*100) / 100
}
