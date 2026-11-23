package diag

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"calico-netdiag/pkg/agent/dataplane"
	"calico-netdiag/pkg/agent/netns"
	"calico-netdiag/pkg/model"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcapgo"
)

const (
	pcapDir            = "/tmp/calico-netdiag-pcaps"
	maxConcurrentDumps = 4
)

var (
	dumpSem = make(chan struct{}, maxConcurrentDumps)
	pcapMu  sync.Mutex
)

// RunTCPDump streams parsed PacketFrames to emitFn while simultaneously saving a downloadable .pcap file.
func RunTCPDump(
	ctx context.Context,
	podUID, podIP string,
	req model.TCPDumpRequest,
	emitFn func(model.TCPDumpControlMessage) error,
) error {
	select {
	case dumpSem <- struct{}{}:
		defer func() { <-dumpSem }()
	default:
		return fmt.Errorf("too many concurrent tcpdump sessions on this node (max %d)", maxConcurrentDumps)
	}

	if req.MaxPackets <= 0 || req.MaxPackets > 5000 {
		req.MaxPackets = 500
	}
	if req.DurationSec <= 0 || req.DurationSec > 120 {
		req.DurationSec = 30
	}
	if req.SnapLen <= 0 || req.SnapLen > 65535 {
		req.SnapLen = 256
	}

	// Resolve capture target interface and netns
	var nsPath, iface string
	target := strings.TrimSpace(req.TargetIface)
	if target == "" {
		target = "pod:eth0"
	}

	hostInfo := dataplane.InspectHost("")
	switch {
	case strings.HasPrefix(target, "pod:"):
		info, err := netns.LocatePod(podUID, podIP)
		if err != nil {
			return fmt.Errorf("locate pod netns: %w", err)
		}
		nsPath = info.NetnsPath
		iface = strings.TrimPrefix(target, "pod:")
		if iface == "" {
			iface = "eth0"
		}
	case target == "host:veth":
		veth := netns.FindHostVethByIP(podIP)
		if veth == "" {
			return fmt.Errorf("calico host veth not found for pod IP %s", podIP)
		}
		iface = veth
	case target == "host:primary":
		iface = hostInfo.PrimaryNIC
		if iface == "" {
			iface = "any"
		}
	case strings.HasPrefix(target, "host:"):
		iface = strings.TrimPrefix(target, "host:")
	default:
		iface = target
	}

	// Prepare pcap storage directory and cleanup old files
	_ = os.MkdirAll(pcapDir, 0o755)
	cleanupOldPcaps()

	captureID := randomID()
	pcapPath := filepath.Join(pcapDir, captureID+".pcap")
	pcapFile, err := os.Create(pcapPath)
	if err != nil {
		return fmt.Errorf("create pcap file: %w", err)
	}
	defer pcapFile.Close()

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(req.DurationSec)*time.Second)
	defer cancel()

	// Build tcpdump command safely (no shell expansion!)
	tcpdumpArgs := []string{
		"-i", iface,
		"-nn", "-U",
		"-s", fmt.Sprintf("%d", req.SnapLen),
		"-c", fmt.Sprintf("%d", req.MaxPackets),
		"-w", "-",
	}
	if f := strings.TrimSpace(req.BPFFilter); f != "" {
		// Pass filter expression tokens safely as args (tcpdump joins remaining args into BPF filter)
		tcpdumpArgs = append(tcpdumpArgs, strings.Fields(f)...)
	}

	var cmd *exec.Cmd
	if nsPath != "" {
		args := append([]string{"--net=" + nsPath, "--", "tcpdump"}, tcpdumpArgs...)
		cmd = exec.CommandContext(runCtx, "nsenter", args...)
	} else {
		cmd = exec.CommandContext(runCtx, "tcpdump", tcpdumpArgs...)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start tcpdump: %w", err)
	}

	_ = emitFn(model.TCPDumpControlMessage{
		Type:      "started",
		CaptureID: captureID,
		Interface: iface,
		Message:   fmt.Sprintf("Capturing on %s (max %d pkts / %ds)", iface, req.MaxPackets, req.DurationSec),
	})

	// Tee raw pcap stream to file while decoding frames in real time
	tee := io.TeeReader(stdout, pcapFile)
	reader, err := pcapgo.NewReader(tee)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		errMsg := strings.TrimSpace(stderrBuf.String())
		if errMsg != "" {
			return fmt.Errorf("tcpdump failed: %s", errMsg)
		}
		return fmt.Errorf("read pcap stream: %w", err)
	}

	startTime := time.Now()
	pktNum := 0
	for {
		data, ci, err := reader.ReadPacketData()
		if err != nil {
			break
		}
		pktNum++
		frame := decodePacketFrame(pktNum, captureID, iface, startTime, ci, data, reader.LinkType())
		if sendErr := emitFn(model.TCPDumpControlMessage{
			Type:      "packet",
			CaptureID: captureID,
			PacketCnt: pktNum,
			Frame:     &frame,
		}); sendErr != nil {
			cancel()
			break
		}
	}

	_ = cmd.Wait()

	_ = emitFn(model.TCPDumpControlMessage{
		Type:      "done",
		CaptureID: captureID,
		Interface: iface,
		PacketCnt: pktNum,
		PcapReady: pktNum > 0,
		Message:   fmt.Sprintf("Capture finished: %d packets saved", pktNum),
	})
	return nil
}

// GetPcapFilePath returns a safe validated path for downloading a saved .pcap file.
func GetPcapFilePath(captureID string) (string, error) {
	if len(captureID) < 6 || len(captureID) > 32 {
		return "", fmt.Errorf("invalid captureId")
	}
	for _, c := range captureID {
		if !((c >= 'a' && c <= 'f') || (c >= '0' && c <= '9')) {
			return "", fmt.Errorf("invalid captureId characters")
		}
	}
	p := filepath.Join(pcapDir, captureID+".pcap")
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}

func decodePacketFrame(
	num int,
	captureID, iface string,
	start time.Time,
	ci gopacket.CaptureInfo,
	data []byte,
	linkType layers.LinkType,
) model.PacketFrame {
	ts := ci.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	relMs := float64(ts.Sub(start).Microseconds()) / 1000.0
	if relMs < 0 {
		relMs = 0
	}

	frame := model.PacketFrame{
		Number:    num,
		Timestamp: ts,
		RelTimeMs: float64(int(relMs*100)) / 100,
		Interface: iface,
		Length:    ci.Length,
		Protocol:  "OTHER",
		HexDump:   formatHexDump(data, 128),
		CaptureID: captureID,
	}

	var firstLayer gopacket.LayerType = layers.LayerTypeEthernet
	switch linkType {
	case layers.LinkTypeRaw, layers.LinkTypeIPv4:
		firstLayer = layers.LayerTypeIPv4
	case layers.LinkTypeLinuxSLL:
		firstLayer = layers.LayerTypeLinuxSLL
	}

	pkt := gopacket.NewPacket(data, firstLayer, gopacket.NoCopy)
	if ip4L := pkt.Layer(layers.LayerTypeIPv4); ip4L != nil {
		ip4 := ip4L.(*layers.IPv4)
		frame.SrcIP = ip4.SrcIP.String()
		frame.DstIP = ip4.DstIP.String()
		frame.Protocol = ip4.Protocol.String()
	} else if ip6L := pkt.Layer(layers.LayerTypeIPv6); ip6L != nil {
		ip6 := ip6L.(*layers.IPv6)
		frame.SrcIP = ip6.SrcIP.String()
		frame.DstIP = ip6.DstIP.String()
		frame.Protocol = ip6.NextHeader.String()
	} else if arpL := pkt.Layer(layers.LayerTypeARP); arpL != nil {
		arp := arpL.(*layers.ARP)
		frame.Protocol = "ARP"
		frame.SrcIP = fmt.Sprintf("%d.%d.%d.%d", arp.SourceProtAddress[0], arp.SourceProtAddress[1], arp.SourceProtAddress[2], arp.SourceProtAddress[3])
		frame.DstIP = fmt.Sprintf("%d.%d.%d.%d", arp.DstProtAddress[0], arp.DstProtAddress[1], arp.DstProtAddress[2], arp.DstProtAddress[3])
		frame.Info = fmt.Sprintf("ARP op=%d who-has %s tell %s", arp.Operation, frame.DstIP, frame.SrcIP)
		return frame
	}

	if tcpL := pkt.Layer(layers.LayerTypeTCP); tcpL != nil {
		tcp := tcpL.(*layers.TCP)
		frame.Protocol = "TCP"
		frame.SrcPort = uint16(tcp.SrcPort)
		frame.DstPort = uint16(tcp.DstPort)
		frame.TCPFlags = formatTCPFlags(tcp)
		frame.Info = fmt.Sprintf("%d → %d [%s] Seq=%d Ack=%d Win=%d Len=%d",
			tcp.SrcPort, tcp.DstPort, frame.TCPFlags, tcp.Seq, tcp.Ack, tcp.Window, len(tcp.Payload))
		return frame
	}
	if udpL := pkt.Layer(layers.LayerTypeUDP); udpL != nil {
		udp := udpL.(*layers.UDP)
		frame.Protocol = "UDP"
		frame.SrcPort = uint16(udp.SrcPort)
		frame.DstPort = uint16(udp.DstPort)
		if dnsL := pkt.Layer(layers.LayerTypeDNS); dnsL != nil {
			dns := dnsL.(*layers.DNS)
			frame.Protocol = "DNS"
			qname := ""
			if len(dns.Questions) > 0 {
				qname = string(dns.Questions[0].Name)
			}
			if dns.QR {
				frame.Info = fmt.Sprintf("DNS Response %s (%d answers, rcode=%s)", qname, len(dns.Answers), dns.ResponseCode.String())
			} else {
				frame.Info = fmt.Sprintf("DNS Query %s", qname)
			}
			return frame
		}
		if udp.DstPort == 4789 || udp.SrcPort == 4789 {
			frame.Protocol = "VXLAN"
		}
		frame.Info = fmt.Sprintf("%d → %d Len=%d", udp.SrcPort, udp.DstPort, len(udp.Payload))
		return frame
	}
	if icmpL := pkt.Layer(layers.LayerTypeICMPv4); icmpL != nil {
		icmp := icmpL.(*layers.ICMPv4)
		frame.Protocol = "ICMP"
		frame.Info = fmt.Sprintf("ICMP %s id=%d seq=%d", icmp.TypeCode.String(), icmp.Id, icmp.Seq)
		return frame
	}

	if frame.Info == "" {
		frame.Info = fmt.Sprintf("%s %s → %s len=%d", frame.Protocol, frame.SrcIP, frame.DstIP, frame.Length)
	}
	return frame
}

func formatHexDump(data []byte, limit int) string {
	if len(data) > limit {
		data = data[:limit]
	}
	return hex.Dump(data)
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func cleanupOldPcaps() {
	pcapMu.Lock()
	defer pcapMu.Unlock()
	entries, err := os.ReadDir(pcapDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-15 * time.Minute)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(pcapDir, e.Name()))
		}
	}
}
