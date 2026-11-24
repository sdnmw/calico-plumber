package diag

import (
	"net"
	"strings"
	"testing"

	"calico-netdiag/pkg/model"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

func buildTestTCPFrame(t *testing.T, srcIP, dstIP string, srcPort, dstPort uint16, syn, ack bool) []byte {
	t.Helper()
	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
		DstMAC:       net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x02},
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := &layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.ParseIP(srcIP).To4(),
		DstIP:    net.ParseIP(dstIP).To4(),
	}
	tcp := &layers.TCP{
		SrcPort: layers.TCPPort(srcPort),
		DstPort: layers.TCPPort(dstPort),
		SYN:     syn,
		ACK:     ack,
		Window:  65535,
	}
	_ = tcp.SetNetworkLayerForChecksum(ip4)
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	if err := gopacket.SerializeLayers(buf, opts, eth, ip4, tcp); err != nil {
		t.Fatalf("serialize tcp frame: %v", err)
	}
	return buf.Bytes()
}

func buildTestUDPFrame(t *testing.T, srcIP, dstIP string, srcPort, dstPort uint16, payload []byte) []byte {
	t.Helper()
	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
		DstMAC:       net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x02},
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := &layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP(srcIP).To4(),
		DstIP:    net.ParseIP(dstIP).To4(),
	}
	udp := &layers.UDP{
		SrcPort: layers.UDPPort(srcPort),
		DstPort: layers.UDPPort(dstPort),
	}
	_ = udp.SetNetworkLayerForChecksum(ip4)
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	if err := gopacket.SerializeLayers(buf, opts, eth, ip4, udp, gopacket.Payload(payload)); err != nil {
		t.Fatalf("serialize udp frame: %v", err)
	}
	return buf.Bytes()
}

func TestMatchTracePacketTCPSNATRandomFully(t *testing.T) {
	req := model.AgentTraceCaptureRequest{
		SrcIP:    "172.31.226.40",
		DstIP:    "192.168.112.51",
		Protocol: "TCP",
		SrcPort:  50123,
		DstPort:  80,
	}
	targetIPs := map[string]bool{"192.168.112.51": true}
	// Calico MASQUERADE --random-fully rewrote srcIP to node IP and srcPort to 61888
	frame := buildTestTCPFrame(t, "192.168.118.207", "192.168.112.51", 61888, 80, true, false)

	pkt, ok := matchTracePacket(frame, false, "ens4", "src_phy_nic", 1000, req, targetIPs, "192.168.118.207")
	if !ok {
		t.Fatalf("expected SNAT TCP SYN packet to match on src_phy_nic")
	}
	if pkt.Direction != "forward" {
		t.Fatalf("expected forward direction, got %s", pkt.Direction)
	}
	if !strings.Contains(pkt.OuterHeader, "SNAT 172.31.226.40:50123 → 192.168.118.207:61888") {
		t.Fatalf("expected SNAT outer header annotation, got %q", pkt.OuterHeader)
	}
}

func TestMatchTracePacketServiceDNATBackendPortOnLo(t *testing.T) {
	req := model.AgentTraceCaptureRequest{
		SrcIP:       "172.31.226.40",
		DstIP:       "10.250.252.1",
		BackendIPs:  []string{"192.168.118.206"},
		Protocol:    "TCP",
		SrcPort:     49743,
		DstPort:     443,
		BackendPort: 6443,
	}
	targetIPs := map[string]bool{"10.250.252.1": true, "192.168.118.206": true}
	frame := buildTestTCPFrame(t, "172.31.226.40", "192.168.118.206", 49743, 6443, true, false)

	pkt, ok := matchTracePacket(frame, false, "lo", "dst_host_lo", 2000, req, targetIPs, "192.168.118.206")
	if !ok {
		t.Fatalf("expected DNAT Service packet to match on dst_host_lo")
	}
	if pkt.DstIP != "192.168.118.206" || pkt.DstPort != 6443 {
		t.Fatalf("unexpected DNAT target %s:%d", pkt.DstIP, pkt.DstPort)
	}
}

func TestMatchTracePacketUDPSNAT(t *testing.T) {
	req := model.AgentTraceCaptureRequest{
		SrcIP:    "172.31.226.40",
		DstIP:    "223.5.5.5",
		Protocol: "UDP",
		SrcPort:  51111,
		DstPort:  53,
	}
	targetIPs := map[string]bool{"223.5.5.5": true}
	frame := buildTestUDPFrame(t, "192.168.118.207", "223.5.5.5", 62222, 53, []byte("CALICO-NETDIAG-TRACEFLOW"))

	pkt, ok := matchTracePacket(frame, false, "ens4", "src_phy_nic", 3000, req, targetIPs, "192.168.118.207")
	if !ok {
		t.Fatalf("expected SNAT UDP packet with traceflow payload to match")
	}
	if !strings.Contains(pkt.OuterHeader, "SNAT 172.31.226.40:51111 → 192.168.118.207:62222") {
		t.Fatalf("expected SNAT outer header on UDP, got %q", pkt.OuterHeader)
	}
}

func TestMatchTracePacketICMPSNATRandomizedID(t *testing.T) {
	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
		DstMAC:       net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x02},
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := &layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.ParseIP("192.168.118.206").To4(),
		DstIP:    net.ParseIP("192.168.118.202").To4(),
	}
	icmp := &layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       59999, // randomized by MASQUERADE --random-fully
		Seq:      1,
	}
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	if err := gopacket.SerializeLayers(buf, opts, eth, ip4, icmp, gopacket.Payload([]byte("CALICO-NETDIAG-TF"))); err != nil {
		t.Fatalf("serialize icmp frame: %v", err)
	}

	req := model.AgentTraceCaptureRequest{
		SrcIP:    "172.31.225.39",
		DstIP:    "192.168.118.202",
		Protocol: "ICMP",
		ICMPID:   12345,
	}
	targetIPs := map[string]bool{"192.168.118.202": true}

	pkt, ok := matchTracePacket(buf.Bytes(), false, "ens4", "src_phy_nic", 4000, req, targetIPs, "192.168.118.206")
	if !ok {
		t.Fatalf("expected SNAT ICMP Echo Request with randomized ID to match via payload signature")
	}
	if !strings.Contains(pkt.OuterHeader, "SNAT 172.31.225.39 → 192.168.118.206") {
		t.Fatalf("expected SNAT outer header on ICMP, got %q", pkt.OuterHeader)
	}
}
