package telemetry

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"calico-netdiag/pkg/agent/netns"
	"calico-netdiag/pkg/model"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// natMapping tracks Service ClusterIP -> Backend PodIP DNAT from conntrack.
type natMapping struct {
	OrigDstIP   string
	OrigDstPort uint16
}

type natKey struct {
	proto   string
	srcIP   string
	srcPort uint16
	dstIP   string
	dstPort uint16
}

// readConntrackNATIndex builds a fast lookup table of active DNAT (Service ClusterIP -> PodIP) mappings.
func readConntrackNATIndex() map[natKey]natMapping {
	out := make(map[natKey]natMapping)
	flows, err := netlink.ConntrackTableList(netlink.ConntrackTable, unix.AF_INET)
	if err != nil {
		return out
	}
	for _, f := range flows {
		proto := protoNumToName(f.Forward.Protocol)
		if proto == "" {
			continue
		}
		origSrc := f.Forward.SrcIP.String()
		origDst := f.Forward.DstIP.String()
		origSPort := f.Forward.SrcPort
		origDPort := f.Forward.DstPort

		// In Linux conntrack, Reply.SrcIP:Reply.SrcPort is the post-DNAT destination (actual Pod IP:TargetPort)
		realDst := f.Reverse.SrcIP.String()
		realDPort := f.Reverse.SrcPort
		realSrc := f.Reverse.DstIP.String()
		realSPort := f.Reverse.DstPort

		if origDst != realDst || origDPort != realDPort {
			m := natMapping{OrigDstIP: origDst, OrigDstPort: origDPort}
			// Index by both pre-SNAT src and post-SNAT src, and both directions
			out[natKey{proto, origSrc, origSPort, realDst, realDPort}] = m
			out[natKey{proto, realSrc, realSPort, realDst, realDPort}] = m
			out[natKey{proto, realDst, realDPort, origSrc, origSPort}] = m
			out[natKey{proto, realDst, realDPort, realSrc, realSPort}] = m
		}
	}
	return out
}

func lookupNAT(idx map[natKey]natMapping, proto, srcIP string, srcPort uint16, dstIP string, dstPort uint16) (natMapping, bool) {
	if len(idx) == 0 {
		return natMapping{}, false
	}
	m, ok := idx[natKey{proto, srcIP, srcPort, dstIP, dstPort}]
	return m, ok
}

// conntrackCollector implements Mode 2 (Fallback for kernels without eBPF/BTF):
// Combines Netlink Conntrack (all TCP/UDP/ICMP 5-tuples + Service DNAT + optional conntrack bytes)
// with Netlink INET_DIAG (struct tcp_info across Pod & Host netns for real kernel RTT, bytes, and retransmits).
type conntrackCollector struct {
	nodeName string
	mu       sync.Mutex
	prevCT   map[string]ctCounters
	prevSock map[string]sockCounters
	lastPoll time.Time
}

type ctCounters struct {
	txBytes uint64
	rxBytes uint64
	txPkts  uint64
	rxPkts  uint64
}

type sockCounters struct {
	bytesAcked uint64
	bytesRecv  uint64
	segsOut    uint32
	segsIn     uint32
	retrans    uint32
}

type tcpDiagEntry struct {
	SrcIP      string
	DstIP      string
	SrcPort    uint16
	DstPort    uint16
	State      string
	RTTUs      uint32
	BytesAcked uint64
	BytesRecv  uint64
	SegsOut    uint32
	SegsIn     uint32
	Retrans    uint32
}

func newConntrackCollector(nodeName string) Collector {
	return &conntrackCollector{
		nodeName: nodeName,
		prevCT:   make(map[string]ctCounters),
		prevSock: make(map[string]sockCounters),
		lastPoll: time.Now(),
	}
}

func (c *conntrackCollector) Mode() model.TelemetryEngineMode {
	return model.TelemetryConntrack
}

func (c *conntrackCollector) Poll() ([]model.AgentFlowSample, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	interval := now.Sub(c.lastPoll).Seconds()
	if interval <= 0.1 {
		interval = 3.0
	}
	c.lastPoll = now

	// 1. Collect kernel tcp_info across all Pod + Host netns via Netlink INET_DIAG (no eBPF required!).
	sockStats := collectAllNetnsTCPDiag()
	curSock := make(map[string]sockCounters, len(sockStats))
	sockByTuple := make(map[string]tcpDiagEntry, len(sockStats))
	for _, s := range sockStats {
		k := fmt.Sprintf("TCP|%s:%d|%s:%d", s.SrcIP, s.SrcPort, s.DstIP, s.DstPort)
		sockByTuple[k] = s
		curSock[k] = sockCounters{
			bytesAcked: s.BytesAcked,
			bytesRecv:  s.BytesRecv,
			segsOut:    s.SegsOut,
			segsIn:     s.SegsIn,
			retrans:    s.Retrans,
		}
	}

	// 2. Read Netlink Conntrack table for full 5-tuples + NAT + UDP/ICMP visibility.
	flows, err := netlink.ConntrackTableList(netlink.ConntrackTable, unix.AF_INET)
	if err != nil {
		// If conntrack fails, still emit pure INET_DIAG TCP flows so Mode 2 remains functional.
		return c.emitSockOnlyFlows(sockStats, curSock, interval, now), nil
	}

	curCT := make(map[string]ctCounters, len(flows))
	seenTuples := make(map[string]bool, len(flows))
	var samples []model.AgentFlowSample

	for _, f := range flows {
		proto := protoNumToName(f.Forward.Protocol)
		if proto == "" {
			continue
		}
		srcIP := f.Forward.SrcIP.String()
		origDstIP := f.Forward.DstIP.String()
		srcPort := f.Forward.SrcPort
		origDstPort := f.Forward.DstPort

		realDstIP := f.Reverse.SrcIP.String()
		realDstPort := f.Reverse.SrcPort

		if isLoopbackIP(srcIP) || isLoopbackIP(realDstIP) {
			continue
		}

		ctKey := fmt.Sprintf("%s|%s:%d|%s:%d", proto, srcIP, srcPort, realDstIP, realDstPort)
		if seenTuples[ctKey] {
			continue
		}
		seenTuples[ctKey] = true

		curCounters := ctCounters{
			txBytes: f.Forward.Bytes,
			rxBytes: f.Reverse.Bytes,
			txPkts:  f.Forward.Packets,
			rxPkts:  f.Reverse.Packets,
		}
		curCT[ctKey] = curCounters

		prev, existed := c.prevCT[ctKey]
		dTxBytes := diffU64(curCounters.txBytes, prev.txBytes, existed)
		dRxBytes := diffU64(curCounters.rxBytes, prev.rxBytes, existed)
		dTxPkts := diffU64(curCounters.txPkts, prev.txPkts, existed)
		dRxPkts := diffU64(curCounters.rxPkts, prev.rxPkts, existed)

		var rttUs uint32
		var retrans uint32
		state := "ESTABLISHED"

		// Enrich with INET_DIAG tcp_info (gives real RTT + real byte counters even when nf_conntrack_acct=0!)
		if proto == "TCP" {
			revKey := fmt.Sprintf("TCP|%s:%d|%s:%d", realDstIP, realDstPort, srcIP, srcPort)
			if sd, ok := sockByTuple[ctKey]; ok {
				rttUs = sd.RTTUs
				state = sd.State
				ps, pOk := c.prevSock[ctKey]
				retrans = diffU32(sd.Retrans, ps.retrans, pOk)
				if dTxBytes == 0 && dRxBytes == 0 {
					dTxBytes = diffU64(sd.BytesAcked, ps.bytesAcked, pOk)
					dRxBytes = diffU64(sd.BytesRecv, ps.bytesRecv, pOk)
					dTxPkts = uint64(diffU32(sd.SegsOut, ps.segsOut, pOk))
					dRxPkts = uint64(diffU32(sd.SegsIn, ps.segsIn, pOk))
				}
			} else if sd, ok := sockByTuple[revKey]; ok {
				rttUs = sd.RTTUs
				state = sd.State
				ps, pOk := c.prevSock[revKey]
				retrans = diffU32(sd.Retrans, ps.retrans, pOk)
				if dTxBytes == 0 && dRxBytes == 0 {
					dRxBytes = diffU64(sd.BytesAcked, ps.bytesAcked, pOk)
					dTxBytes = diffU64(sd.BytesRecv, ps.bytesRecv, pOk)
					dRxPkts = uint64(diffU32(sd.SegsOut, ps.segsOut, pOk))
					dTxPkts = uint64(diffU32(sd.SegsIn, ps.segsIn, pOk))
				}
			}
		}

		// Estimate minimal packet activity for active conntrack entry if accounting is disabled on UDP
		if dTxPkts == 0 && dRxPkts == 0 && dTxBytes == 0 && dRxBytes == 0 && !existed {
			dTxPkts = 1
			dTxBytes = 128
		}

		sample := model.AgentFlowSample{
			NodeName:    c.nodeName,
			Engine:      model.TelemetryConntrack,
			SrcIP:       srcIP,
			DstIP:       realDstIP,
			SrcPort:     srcPort,
			DstPort:     realDstPort,
			Protocol:    proto,
			TxBytes:     dTxBytes,
			RxBytes:     dRxBytes,
			TxPackets:   dTxPkts,
			RxPackets:   dRxPkts,
			RTTAvgUs:    rttUs,
			RTTMaxUs:    rttUs,
			RTTSamples:  boolToU32(rttUs > 0),
			Retransmits: retrans,
			State:       state,
			IntervalSec: interval,
			Timestamp:   now,
		}
		if origDstIP != realDstIP || origDstPort != realDstPort {
			sample.ServiceIP = origDstIP
			sample.ServicePort = origDstPort
		}
		samples = append(samples, sample)
	}

	c.prevCT = curCT
	c.prevSock = curSock
	return samples, nil
}

func (c *conntrackCollector) emitSockOnlyFlows(sockStats []tcpDiagEntry, curSock map[string]sockCounters, interval float64, now time.Time) []model.AgentFlowSample {
	var samples []model.AgentFlowSample
	for _, sd := range sockStats {
		k := fmt.Sprintf("TCP|%s:%d|%s:%d", sd.SrcIP, sd.SrcPort, sd.DstIP, sd.DstPort)
		ps, pOk := c.prevSock[k]
		dTx := diffU64(sd.BytesAcked, ps.bytesAcked, pOk)
		dRx := diffU64(sd.BytesRecv, ps.bytesRecv, pOk)
		samples = append(samples, model.AgentFlowSample{
			NodeName:    c.nodeName,
			Engine:      model.TelemetryConntrack,
			SrcIP:       sd.SrcIP,
			DstIP:       sd.DstIP,
			SrcPort:     sd.SrcPort,
			DstPort:     sd.DstPort,
			Protocol:    "TCP",
			TxBytes:     dTx,
			RxBytes:     dRx,
			TxPackets:   uint64(diffU32(sd.SegsOut, ps.segsOut, pOk)),
			RxPackets:   uint64(diffU32(sd.SegsIn, ps.segsIn, pOk)),
			RTTAvgUs:    sd.RTTUs,
			RTTMaxUs:    sd.RTTUs,
			RTTSamples:  boolToU32(sd.RTTUs > 0),
			Retransmits: diffU32(sd.Retrans, ps.retrans, pOk),
			State:       sd.State,
			IntervalSec: interval,
			Timestamp:   now,
		})
	}
	c.prevSock = curSock
	return samples
}

func (c *conntrackCollector) Close() error { return nil }

// collectAllNetnsTCPDiag runs Netlink INET_DIAG across all unique Pod & Host network namespaces.
func collectAllNetnsTCPDiag() []tcpDiagEntry {
	paths := netns.ListUniqueNetnsPaths()
	var all []tcpDiagEntry
	for _, p := range paths {
		_ = netns.RunInNetns(p, func() error {
			entries, err := queryLocalTCPDiag()
			if err == nil {
				all = append(all, entries...)
			}
			return nil
		})
	}
	return all
}

// Netlink SOCK_DIAG constants (Linux UAPI)
const (
	sockDiagByFamily = 20
	inetDiagInfoAttr = 2
)

// queryLocalTCPDiag queries NETLINK_SOCK_DIAG in the current network namespace to fetch
// active TCP connections along with kernel struct tcp_info (RTT, bytes_acked, bytes_received, retrans).
func queryLocalTCPDiag() ([]tcpDiagEntry, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, unix.NETLINK_SOCK_DIAG)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(fd)

	tv := syscall.Timeval{Sec: 1, Usec: 0}
	_ = syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)

	sa := &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}
	if err := syscall.Bind(fd, sa); err != nil {
		return nil, err
	}

	// Build nlmsghdr (16B) + inet_diag_req_v2 (56B) = 72 bytes
	req := make([]byte, 72)
	binary.LittleEndian.PutUint32(req[0:4], 72)                                      // nlmsg_len
	binary.LittleEndian.PutUint16(req[4:6], sockDiagByFamily)                        // nlmsg_type
	binary.LittleEndian.PutUint16(req[6:8], syscall.NLM_F_REQUEST|syscall.NLM_F_DUMP) // nlmsg_flags
	binary.LittleEndian.PutUint32(req[8:12], 1)                                      // nlmsg_seq

	req[16] = syscall.AF_INET                  // sdiag_family
	req[17] = syscall.IPPROTO_TCP              // sdiag_protocol
	req[18] = 1 << (inetDiagInfoAttr - 1)      // idiag_ext = INET_DIAG_INFO
	req[19] = 0                                // pad
	binary.LittleEndian.PutUint32(req[20:24], 0x013E) // ESTABLISHED, SYN_SENT, SYN_RECV, FIN_WAIT1/2, CLOSE_WAIT

	if err := syscall.Sendto(fd, req, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return nil, err
	}

	buf := make([]byte, 65536)
	var out []tcpDiagEntry

	for {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			break
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			break
		}
		done := false
		for _, m := range msgs {
			if m.Header.Type == syscall.NLMSG_DONE {
				done = true
				break
			}
			if m.Header.Type == syscall.NLMSG_ERROR {
				done = true
				break
			}
			if len(m.Data) < 72 {
				continue
			}
			entry, ok := parseInetDiagMsg(m.Data)
			if ok && !isLoopbackIP(entry.SrcIP) && !isLoopbackIP(entry.DstIP) {
				out = append(out, entry)
			}
		}
		if done {
			break
		}
	}
	return out, nil
}

// parseInetDiagMsg decodes inet_diag_msg (72 bytes) + trailing rtattr(INET_DIAG_INFO -> struct tcp_info).
func parseInetDiagMsg(data []byte) (tcpDiagEntry, bool) {
	if len(data) < 72 {
		return tcpDiagEntry{}, false
	}
	family := data[0]
	if family != syscall.AF_INET {
		return tcpDiagEntry{}, false
	}
	state := tcpStateName(data[1])
	sport := binary.BigEndian.Uint16(data[4:6])
	dport := binary.BigEndian.Uint16(data[6:8])
	srcIP := net.IP(data[8:12]).String()
	dstIP := net.IP(data[24:28]).String()

	e := tcpDiagEntry{
		SrcIP:   srcIP,
		DstIP:   dstIP,
		SrcPort: sport,
		DstPort: dport,
		State:   state,
	}

	// Parse netlink rtattrs starting at offset 72
	off := 72
	for off+4 <= len(data) {
		rtaLen := int(binary.LittleEndian.Uint16(data[off : off+2]))
		rtaType := binary.LittleEndian.Uint16(data[off+2 : off+4])
		if rtaLen < 4 || off+rtaLen > len(data) {
			break
		}
		if rtaType == inetDiagInfoAttr {
			payload := data[off+4 : off+rtaLen]
			decodeTCPInfo(payload, &e)
		}
		aligned := (rtaLen + 3) &^ 3
		off += aligned
	}
	return e, true
}

// decodeTCPInfo reads standard Linux UAPI struct tcp_info fields safely by size.
func decodeTCPInfo(b []byte, e *tcpDiagEntry) {
	// Also use unix.TCPInfo when size matches, or fixed UAPI offsets (little-endian x86_64/arm64)
	_ = unsafe.Sizeof(unix.TCPInfo{})
	if len(b) >= 72 {
		e.RTTUs = binary.LittleEndian.Uint32(b[68:72])
	}
	if len(b) >= 104 {
		e.Retrans = binary.LittleEndian.Uint32(b[100:104])
	}
	if len(b) >= 128 {
		e.BytesAcked = binary.LittleEndian.Uint64(b[120:128])
	}
	if len(b) >= 136 {
		e.BytesRecv = binary.LittleEndian.Uint64(b[128:136])
	}
	if len(b) >= 144 {
		e.SegsOut = binary.LittleEndian.Uint32(b[136:140])
		e.SegsIn = binary.LittleEndian.Uint32(b[140:144])
	}
}

func tcpStateName(st uint8) string {
	switch st {
	case 1:
		return "ESTABLISHED"
	case 2:
		return "SYN_SENT"
	case 3:
		return "SYN_RECV"
	case 4:
		return "FIN_WAIT1"
	case 5:
		return "FIN_WAIT2"
	case 6:
		return "TIME_WAIT"
	case 7:
		return "CLOSE"
	case 8:
		return "CLOSE_WAIT"
	case 9:
		return "LAST_ACK"
	case 10:
		return "LISTEN"
	default:
		return "ACTIVE"
	}
}

func protoNumToName(p uint8) string {
	switch p {
	case 6:
		return "TCP"
	case 17:
		return "UDP"
	case 1:
		return "ICMP"
	default:
		return ""
	}
}

func isLoopbackIP(ip string) bool {
	return strings.HasPrefix(ip, "127.") || ip == "0.0.0.0" || ip == "::1"
}

func diffU64(cur, prev uint64, existed bool) uint64 {
	if !existed || cur < prev {
		return cur
	}
	return cur - prev
}

func diffU32(cur, prev uint32, existed bool) uint32 {
	if !existed || cur < prev {
		return cur
	}
	return cur - prev
}

func boolToU32(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}
