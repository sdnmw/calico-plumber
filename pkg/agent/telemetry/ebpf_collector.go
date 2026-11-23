//go:build linux && amd64 && ebpf

package telemetry

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"calico-netdiag/pkg/agent/telemetry/bpf"
	"calico-netdiag/pkg/model"

	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
)

type ebpfCollector struct {
	nodeName string
	objs     bpf.FlowObjects
	links    []link.Link
	prev     map[bpf.FlowFlowKey]bpf.FlowFlowMetrics
	lastPoll time.Time
}

func newEBPFCollector(nodeName string) (Collector, error) {
	// Verify CO-RE BTF is available before attempting load.
	if st, err := os.Stat("/sys/kernel/btf/vmlinux"); err != nil || st.Size() == 0 {
		return nil, fmt.Errorf("kernel BTF (/sys/kernel/btf/vmlinux) unavailable: %v", err)
	}
	if err := rlimit.RemoveMemlock(); err != nil {
		log.Printf("[telemetry/ebpf] RemoveMemlock warning (non-fatal on >=5.11): %v", err)
	}

	var objs bpf.FlowObjects
	if err := bpf.LoadFlowObjects(&objs, nil); err != nil {
		return nil, fmt.Errorf("load CO-RE eBPF objects: %w", err)
	}

	var attached []link.Link
	cleanup := func() {
		for _, l := range attached {
			_ = l.Close()
		}
		_ = objs.Close()
	}

	// Required core TCP kprobes
	coreProbes := []struct {
		sym  string
		prog interface{}
	}{
		{"tcp_sendmsg", objs.KprobeTcpSendmsg},
		{"tcp_cleanup_rbuf", objs.KprobeTcpCleanupRbuf},
		{"tcp_rcv_established", objs.KprobeTcpRcvEstablished},
	}
	for _, p := range coreProbes {
		var l link.Link
		var err error
		switch p.sym {
		case "tcp_sendmsg":
			l, err = link.Kprobe(p.sym, objs.KprobeTcpSendmsg, nil)
		case "tcp_cleanup_rbuf":
			l, err = link.Kprobe(p.sym, objs.KprobeTcpCleanupRbuf, nil)
		case "tcp_rcv_established":
			l, err = link.Kprobe(p.sym, objs.KprobeTcpRcvEstablished, nil)
		}
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("attach required kprobe %s: %w", p.sym, err)
		}
		attached = append(attached, l)
	}

	// Optional kprobes (best effort across kernel variants)
	optionalAttach := func(sym string, fn func() (link.Link, error)) {
		if l, err := fn(); err == nil {
			attached = append(attached, l)
		} else {
			log.Printf("[telemetry/ebpf] optional kprobe %s skipped: %v", sym, err)
		}
	}
	optionalAttach("tcp_retransmit_skb", func() (link.Link, error) {
		return link.Kprobe("tcp_retransmit_skb", objs.KprobeTcpRetransmitSkb, nil)
	})
	optionalAttach("tcp_reset", func() (link.Link, error) {
		return link.Kprobe("tcp_reset", objs.KprobeTcpReset, nil)
	})
	optionalAttach("udp_sendmsg", func() (link.Link, error) {
		return link.Kprobe("udp_sendmsg", objs.KprobeUdpSendmsg, nil)
	})
	optionalAttach("skb_consume_udp", func() (link.Link, error) {
		return link.Kprobe("skb_consume_udp", objs.KprobeSkbConsumeUdp, nil)
	})

	log.Printf("[telemetry/ebpf] CO-RE eBPF engine initialized with %d attached kprobes", len(attached))
	return &ebpfCollector{
		nodeName: nodeName,
		objs:     objs,
		links:    attached,
		prev:     make(map[bpf.FlowFlowKey]bpf.FlowFlowMetrics),
		lastPoll: time.Now(),
	}, nil
}

func (c *ebpfCollector) Mode() model.TelemetryEngineMode {
	return model.TelemetryEBPF
}

func (c *ebpfCollector) Poll() ([]model.AgentFlowSample, error) {
	now := time.Now()
	interval := now.Sub(c.lastPoll).Seconds()
	if interval <= 0.1 {
		interval = 2.0
	}
	c.lastPoll = now

	// Snapshot conntrack NAT table so Service ClusterIPs can be correlated with post-NAT Pod IPs.
	natMap := readConntrackNATIndex()

	iter := c.objs.FlowStats.Iterate()
	var key bpf.FlowFlowKey
	var val bpf.FlowFlowMetrics

	current := make(map[bpf.FlowFlowKey]bpf.FlowFlowMetrics, len(c.prev))
	var samples []model.AgentFlowSample

	for iter.Next(&key, &val) {
		current[key] = val
		prev, existed := c.prev[key]

		dTxBytes := safeSubU64(val.TxBytes, prev.TxBytes, existed)
		dRxBytes := safeSubU64(val.RxBytes, prev.RxBytes, existed)
		dTxPkts := safeSubU64(val.TxPackets, prev.TxPackets, existed)
		dRxPkts := safeSubU64(val.RxPackets, prev.RxPackets, existed)
		dRetrans := safeSubU32(val.Retransmits, prev.Retransmits, existed)
		dResets := safeSubU32(val.Resets, prev.Resets, existed)
		dRttSum := safeSubU64(val.SrttUsSum, prev.SrttUsSum, existed)
		dRttCnt := safeSubU32(val.SrttSamples, prev.SrttSamples, existed)

		if dTxBytes == 0 && dRxBytes == 0 && dTxPkts == 0 && dRxPkts == 0 && dRetrans == 0 && dResets == 0 {
			continue
		}

		srcIP := ipv4FromLEUint32(key.SrcIp)
		dstIP := ipv4FromLEUint32(key.DstIp)
		proto := "TCP"
		if key.Proto == 17 {
			proto = "UDP"
		}

		var rttAvg uint32
		if dRttCnt > 0 {
			rttAvg = uint32(dRttSum / uint64(dRttCnt))
		} else if val.SrttSamples > 0 {
			rttAvg = uint32(val.SrttUsSum / uint64(val.SrttSamples))
		}

		sample := model.AgentFlowSample{
			NodeName:    c.nodeName,
			Engine:      model.TelemetryEBPF,
			SrcIP:       srcIP,
			DstIP:       dstIP,
			SrcPort:     key.SrcPort,
			DstPort:     key.DstPort,
			Protocol:    proto,
			TxBytes:     dTxBytes,
			RxBytes:     dRxBytes,
			TxPackets:   dTxPkts,
			RxPackets:   dRxPkts,
			RTTAvgUs:    rttAvg,
			RTTMaxUs:    val.SrttMaxUs,
			RTTSamples:  dRttCnt,
			Retransmits: dRetrans,
			Resets:      dResets,
			State:       "ACTIVE",
			IntervalSec: interval,
			Timestamp:   now,
		}

		if nat, ok := lookupNAT(natMap, proto, srcIP, key.SrcPort, dstIP, key.DstPort); ok {
			sample.ServiceIP = nat.OrigDstIP
			sample.ServicePort = nat.OrigDstPort
		}

		samples = append(samples, sample)
	}

	c.prev = current
	return samples, nil
}

func (c *ebpfCollector) Close() error {
	for _, l := range c.links {
		_ = l.Close()
	}
	return c.objs.Close()
}

func ipv4FromLEUint32(v uint32) string {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return net.IP(b[:]).String()
}

func safeSubU64(cur, prev uint64, existed bool) uint64 {
	if !existed || cur < prev {
		return cur
	}
	return cur - prev
}

func safeSubU32(cur, prev uint32, existed bool) uint32 {
	if !existed || cur < prev {
		return cur
	}
	return cur - prev
}
