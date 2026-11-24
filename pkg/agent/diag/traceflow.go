package diag

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"calico-netdiag/pkg/agent/dataplane"
	"calico-netdiag/pkg/agent/netns"
	"calico-netdiag/pkg/model"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	vnetns "github.com/vishvananda/netns"
)

// ExecuteTraceCapture arms multi-interface AF_PACKET observers on the local node,
// optionally injects the traceable probe packet from inside the source Pod's netns,
// and returns all observed packets plus host route/NAT/policy metadata.
func ExecuteTraceCapture(ctx context.Context, nodeName string, req model.AgentTraceCaptureRequest) (*model.AgentTraceCaptureResponse, error) {
	if req.TimeoutMs <= 0 || req.TimeoutMs > 8000 {
		req.TimeoutMs = 2500
	}
	if req.Protocol == "" {
		req.Protocol = "TCP"
	}
	req.Protocol = strings.ToUpper(req.Protocol)

	hostInfo := dataplane.InspectHost(nodeName)
	resp := &model.AgentTraceCaptureResponse{
		NodeName:      nodeName,
		KubeProxyMode: hostInfo.KubeProxyMode,
	}

	var srcPodInfo, dstPodInfo *netns.PodNetInfo
	if req.Role == "source" || req.Role == "both" {
		if info, err := netns.LocatePod(req.PodUID, req.PodIP); info != nil {
			srcPodInfo = info
			resp.HostVeth = info.HostVeth
		} else if err != nil && req.Inject {
			return nil, fmt.Errorf("locate source pod: %w", err)
		}
	}
	if req.Role == "dest" || req.Role == "both" {
		pUID := req.PeerPodUID
		pIP := req.PeerPodIP
		if req.Role == "dest" {
			pUID = req.PodUID
			pIP = req.PodIP
		}
		if info, _ := netns.LocatePod(pUID, pIP); info != nil {
			dstPodInfo = info
			resp.PeerHostVeth = info.HostVeth
		}
	}

	// Host route, kube-proxy NAT, and Calico policy inspection on source or dest node
	routeTarget := req.DstIP
	if req.Role == "source" || req.Role == "both" {
		if len(req.BackendIPs) > 0 {
			// Also inspect kube-proxy NAT rule for the Service ClusterIP
			if rule := dataplane.LookupKubeProxyService(hostInfo.KubeProxyMode, req.DstIP, req.DstPort, req.Protocol); rule != nil {
				resp.NATRuleMatched = rule.RawSummary
			}
			// Route lookup against first backend IP as representative path
			routeTarget = req.BackendIPs[0]
		}
		rd := dataplane.LookupHostRoute(routeTarget)
		resp.EgressInterface = rd.Interface
		resp.RouteSummary = rd.Summary
		resp.RouteProtocol = rd.Protocol
		resp.NextHop = rd.Gateway
		resp.EncapDetected = rd.EncapDetected
		resp.MasqueradeNAT = dataplane.CheckCalicoNATOutgoing()
		if resp.HostVeth != "" {
			resp.PolicyChainInfo = dataplane.InspectCalicoPolicyChains(resp.HostVeth)
		}
	} else if req.Role == "dest" && resp.PeerHostVeth != "" {
		resp.PolicyChainInfo = dataplane.InspectCalicoPolicyChains(resp.PeerHostVeth)
	}

	// Determine interfaces to observe concurrently
	type sniffSpec struct {
		nsPath string
		iface  string
		stage  string
	}
	var specs []sniffSpec
	added := make(map[string]bool)
	addSpec := func(nsPath, iface, stage string) {
		if iface == "" {
			return
		}
		k := nsPath + "|" + iface + "|" + stage
		if added[k] {
			return
		}
		added[k] = true
		specs = append(specs, sniffSpec{nsPath: nsPath, iface: iface, stage: stage})
	}

	// Tunnel interface helper
	tunnelIface := ""
	for _, tif := range hostInfo.TunnelInterfaces {
		if tif.Type == "ipip" || tif.Type == "vxlan" {
			tunnelIface = tif.Name
			break
		}
	}

	if req.Role == "source" || req.Role == "both" {
		if srcPodInfo != nil && !srcPodInfo.HostNet {
			addSpec(srcPodInfo.NetnsPath, "eth0", "src_pod_eth0")
		}
		if resp.HostVeth != "" {
			addSpec("", resp.HostVeth, "src_cali_veth")
		}
		if req.Role == "source" {
			if tunnelIface != "" {
				addSpec("", tunnelIface, "src_tunnel")
			}
			if resp.EgressInterface != "" &&
				resp.EgressInterface != resp.HostVeth &&
				resp.EgressInterface != tunnelIface &&
				resp.EgressInterface != "lo" &&
				!strings.HasPrefix(resp.EgressInterface, "cali") &&
				!strings.HasPrefix(resp.EgressInterface, "veth") {
				addSpec("", resp.EgressInterface, "src_phy_nic")
			} else if hostInfo.PrimaryNIC != "" {
				addSpec("", hostInfo.PrimaryNIC, "src_phy_nic")
			}
		} else if resp.EgressInterface == "lo" {
			// Same-node hostNetwork backend (e.g. default/kubernetes kube-apiserver via DNAT -> lo)
			addSpec("", "lo", "dst_host_lo")
		}
	}
	if req.Role == "dest" || req.Role == "both" {
		if req.Role == "dest" {
			if hostInfo.PrimaryNIC != "" {
				addSpec("", hostInfo.PrimaryNIC, "dst_phy_nic")
			}
			if tunnelIface != "" {
				addSpec("", tunnelIface, "dst_tunnel")
			}
		}
		if resp.PeerHostVeth != "" {
			addSpec("", resp.PeerHostVeth, "dst_cali_veth")
		}
		if dstPodInfo != nil && !dstPodInfo.HostNet {
			addSpec(dstPodInfo.NetnsPath, "eth0", "dst_pod_eth0")
		}
	}

	// For multi-replica Services, also arm up to 4 local backend Pods' cali* veths (or lo for local hostNetwork backends)
	// so whichever replica kube-proxy DNAT selects on this node is wire-observed!
	if len(req.BackendIPs) > 0 {
		armedExtra := 0
		for _, bip := range req.BackendIPs {
			if bip == "" || bip == req.PodIP {
				continue
			}
			if hostInfo.PrimaryIP != "" && bip == hostInfo.PrimaryIP {
				addSpec("", "lo", "dst_host_lo")
				continue
			}
			if armedExtra >= 4 {
				continue
			}
			if veth := netns.FindHostVethByIP(bip); veth != "" && veth != resp.HostVeth {
				addSpec("", veth, "dst_cali_veth")
				armedExtra++
			}
		}
	}

	// Build matching target IP set (supports Service ClusterIP + all Backend Pod IPs)
	targetIPs := map[string]bool{req.DstIP: true}
	for _, bip := range req.BackendIPs {
		if bip != "" {
			targetIPs[bip] = true
		}
	}

	captureDur := time.Duration(req.TimeoutMs) * time.Millisecond
	capCtx, cancel := context.WithTimeout(ctx, captureDur+400*time.Millisecond)
	defer cancel()

	var (
		mu       sync.Mutex
		captured []model.CapturedTracePacket
		wg       sync.WaitGroup
		readyWg  sync.WaitGroup
	)

	for _, sp := range specs {
		wg.Add(1)
		readyWg.Add(1)
		go func(spec sniffSpec) {
			defer wg.Done()
			pkts := sniffTraceInterface(capCtx, &readyWg, spec.nsPath, spec.iface, spec.stage, req, targetIPs, hostInfo.PrimaryIP)
			if len(pkts) > 0 {
				mu.Lock()
				captured = append(captured, pkts...)
				mu.Unlock()
			}
		}(sp)
	}

	// Wait briefly for all AF_PACKET sniffers to bind before injecting
	readyDone := make(chan struct{})
	go func() {
		readyWg.Wait()
		close(readyDone)
	}()
	select {
	case <-readyDone:
	case <-time.After(300 * time.Millisecond):
	}

	// Inject the traceable probe packet from inside source Pod netns
	if req.Inject && srcPodInfo != nil {
		// Give remote node sniffers 120ms head start in cross-node traces
		time.Sleep(120 * time.Millisecond)
		res, err := injectTraceProbe(capCtx, srcPodInfo.NetnsPath, req)
		resp.InjectResult = res
		if err != nil {
			resp.InjectError = err.Error()
		}
		// Let sniffers flush return packets cleanly
		time.Sleep(250 * time.Millisecond)
		cancel()
	}

	wg.Wait()

	// Detect actual DNAT translated destination IP from observed packets (if Dst was a Service)
	for _, p := range captured {
		if p.Direction == "forward" && p.DstIP != req.DstIP && targetIPs[p.DstIP] {
			resp.TranslatedDstIP = p.DstIP
			resp.TranslatedPort = int(p.DstPort)
			break
		}
	}

	// If kube-proxy actually DNAT'd to a different backend IP than BackendIPs[0], refresh host route metadata
	// so EgressInterface, RouteSummary, and EncapDetected reflect the exact selected backend path.
	if (req.Role == "source" || req.Role == "both") && resp.TranslatedDstIP != "" && resp.TranslatedDstIP != routeTarget {
		rd := dataplane.LookupHostRoute(resp.TranslatedDstIP)
		resp.EgressInterface = rd.Interface
		resp.RouteSummary = rd.Summary
		resp.RouteProtocol = rd.Protocol
		resp.NextHop = rd.Gateway
		resp.EncapDetected = rd.EncapDetected
	}

	resp.CapturedPackets = captured
	return resp, nil
}

func sniffTraceInterface(
	ctx context.Context,
	readyWg *sync.WaitGroup,
	nsPath, ifaceName, stage string,
	req model.AgentTraceCaptureRequest,
	targetIPs map[string]bool,
	hostPrimaryIP string,
) []model.CapturedTracePacket {
	var results []model.CapturedTracePacket

	var fd int
	var rawIPMode bool
	var openErr error

	// Open AF_PACKET socket inside target netns (or host netns)
	openFn := func() error {
		iface, err := net.InterfaceByName(ifaceName)
		if err != nil {
			return err
		}
		if ifaceName == "tunl0" || strings.HasPrefix(ifaceName, "ipip") {
			rawIPMode = true
		}
		proto := htons(syscall.ETH_P_ALL)
		sfd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(proto))
		if err != nil {
			return err
		}
		sll := &syscall.SockaddrLinklayer{
			Protocol: proto,
			Ifindex:  iface.Index,
		}
		if err := syscall.Bind(sfd, sll); err != nil {
			_ = syscall.Close(sfd)
			return err
		}
		tv := syscall.Timeval{Sec: 0, Usec: 150000} // 150ms read timeout so ctx cancellation is prompt
		_ = syscall.SetsockoptTimeval(sfd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
		fd = sfd
		return nil
	}

	if nsPath != "" {
		openErr = netns.RunInNetns(nsPath, openFn)
	} else {
		openErr = openFn()
	}
	readyWg.Done()

	if openErr != nil || fd <= 0 {
		return nil
	}
	defer syscall.Close(fd)

	buf := make([]byte, 4096)
	seenForward := false
	seenReturn := false

	for {
		select {
		case <-ctx.Done():
			return results
		default:
		}
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK || err == syscall.EINTR {
				continue
			}
			return results
		}
		if n <= 0 {
			continue
		}
		nowNs := time.Now().UnixNano()
		pkt, matched := matchTracePacket(buf[:n], rawIPMode, ifaceName, stage, nowNs, req, targetIPs, hostPrimaryIP)
		if !matched {
			continue
		}
		if pkt.Direction == "forward" && !seenForward {
			seenForward = true
			results = append(results, pkt)
		} else if pkt.Direction == "return" && !seenReturn {
			seenReturn = true
			results = append(results, pkt)
		}
		if seenForward && seenReturn {
			return results
		}
	}
}

func matchTracePacket(
	raw []byte,
	rawIP bool,
	ifaceName, stage string,
	tsNs int64,
	req model.AgentTraceCaptureRequest,
	targetIPs map[string]bool,
	hostPrimaryIP string,
) (model.CapturedTracePacket, bool) {
	outerHeader := ""
	ipBytes := raw

	if !rawIP {
		if len(raw) < 14 {
			return model.CapturedTracePacket{}, false
		}
		ethType := binary.BigEndian.Uint16(raw[12:14])
		if ethType == 0x8100 && len(raw) >= 18 {
			ethType = binary.BigEndian.Uint16(raw[16:18])
			ipBytes = raw[18:]
		} else {
			ipBytes = raw[14:]
		}
		if ethType != 0x0800 {
			return model.CapturedTracePacket{}, false
		}
	}

	if len(ipBytes) < 20 || (ipBytes[0]>>4) != 4 {
		return model.CapturedTracePacket{}, false
	}

	// Unwrap IPIP (IP protocol 4) or VXLAN (UDP port 4789) if encapsulated
	ihl := int(ipBytes[0]&0x0f) * 4
	if ihl < 20 || len(ipBytes) < ihl {
		return model.CapturedTracePacket{}, false
	}
	outerProto := ipBytes[9]
	outerSrc := net.IP(ipBytes[12:16]).String()
	outerDst := net.IP(ipBytes[16:20]).String()

	if outerProto == 4 && len(ipBytes) >= ihl+20 {
		// IPIP encapsulation!
		outerHeader = fmt.Sprintf("IPIP %s → %s", outerSrc, outerDst)
		ipBytes = ipBytes[ihl:]
	} else if outerProto == 17 && len(ipBytes) >= ihl+8+8+14+20 {
		udpOff := ihl
		udpSrc := binary.BigEndian.Uint16(ipBytes[udpOff : udpOff+2])
		udpDst := binary.BigEndian.Uint16(ipBytes[udpOff+2 : udpOff+4])
		if udpDst == 4789 || udpSrc == 4789 {
			vxlanOff := udpOff + 8
			vni := uint32(ipBytes[vxlanOff+4])<<16 | uint32(ipBytes[vxlanOff+5])<<8 | uint32(ipBytes[vxlanOff+6])
			outerHeader = fmt.Sprintf("VXLAN VNI=%d %s:%d → %s:%d", vni, outerSrc, udpSrc, outerDst, udpDst)
			innerEth := ipBytes[vxlanOff+8:]
			if len(innerEth) >= 34 && binary.BigEndian.Uint16(innerEth[12:14]) == 0x0800 {
				ipBytes = innerEth[14:]
			}
		}
	}

	// Decode inner IPv4 packet with gopacket
	packet := gopacket.NewPacket(ipBytes, layers.LayerTypeIPv4, gopacket.NoCopy)
	ipLayer := packet.Layer(layers.LayerTypeIPv4)
	if ipLayer == nil {
		return model.CapturedTracePacket{}, false
	}
	ip4 := ipLayer.(*layers.IPv4)
	srcIP := ip4.SrcIP.String()
	dstIP := ip4.DstIP.String()

	switch req.Protocol {
	case "TCP":
		tcpLayer := packet.Layer(layers.LayerTypeTCP)
		if tcpLayer == nil {
			return model.CapturedTracePacket{}, false
		}
		tcp := tcpLayer.(*layers.TCP)
		sp := uint16(tcp.SrcPort)
		dp := uint16(tcp.DstPort)
		flags := formatTCPFlags(tcp)

		// Match forward direction (also allow SNAT srcIP & randomized srcPort from Calico MASQUERADE --random-fully on phy NIC)
		dstPortMatches := int(dp) == req.DstPort || (req.BackendPort > 0 && int(dp) == req.BackendPort)
		portMatchesFwd := int(sp) == req.SrcPort && dstPortMatches
		snatMatchesFwd := stage == "src_phy_nic" && hostPrimaryIP != "" && srcIP == hostPrimaryIP && targetIPs[dstIP] && dstPortMatches && tcp.SYN && !tcp.ACK
		if (portMatchesFwd && (srcIP == req.SrcIP || targetIPs[dstIP])) || snatMatchesFwd {
			oh := outerHeader
			if snatMatchesFwd && oh == "" {
				oh = fmt.Sprintf("SNAT %s:%d → %s:%d", req.SrcIP, req.SrcPort, srcIP, sp)
			}
			return model.CapturedTracePacket{
				Interface:   ifaceName,
				Stage:       stage,
				Direction:   "forward",
				TimestampNs: tsNs,
				SrcIP:       srcIP,
				DstIP:       dstIP,
				SrcPort:     sp,
				DstPort:     dp,
				Protocol:    "TCP",
				TCPFlags:    flags,
				OuterHeader: oh,
				Length:      len(raw),
				Summary:     fmt.Sprintf("TCP %s:%d → %s:%d [%s] len=%d", srcIP, sp, dstIP, dp, flags, len(raw)),
			}, true
		}
		// Match return direction (SYN-ACK or RST)
		portMatchesRet := int(dp) == req.SrcPort && (int(sp) == req.DstPort || (req.BackendPort > 0 && int(sp) == req.BackendPort))
		if portMatchesRet && (dstIP == req.SrcIP || targetIPs[srcIP]) && (tcp.SYN || tcp.RST) {
			return model.CapturedTracePacket{
				Interface:   ifaceName,
				Stage:       stage,
				Direction:   "return",
				TimestampNs: tsNs,
				SrcIP:       srcIP,
				DstIP:       dstIP,
				SrcPort:     sp,
				DstPort:     dp,
				Protocol:    "TCP",
				TCPFlags:    flags,
				OuterHeader: outerHeader,
				Length:      len(raw),
				Summary:     fmt.Sprintf("TCP %s:%d → %s:%d [%s] len=%d", srcIP, sp, dstIP, dp, flags, len(raw)),
			}, true
		}

	case "UDP":
		udpLayer := packet.Layer(layers.LayerTypeUDP)
		if udpLayer == nil {
			return model.CapturedTracePacket{}, false
		}
		udp := udpLayer.(*layers.UDP)
		sp := uint16(udp.SrcPort)
		dp := uint16(udp.DstPort)
		dstPortMatches := int(dp) == req.DstPort || (req.BackendPort > 0 && int(dp) == req.BackendPort)
		payloadSig := bytes.Contains(udp.Payload, []byte("CALICO-NETDIAG-TRACEFLOW"))
		portMatchesFwd := int(sp) == req.SrcPort && dstPortMatches
		snatMatchesFwd := (stage == "src_phy_nic" || stage == "dst_phy_nic") && targetIPs[dstIP] && dstPortMatches && payloadSig && (hostPrimaryIP == "" || srcIP == hostPrimaryIP || stage == "dst_phy_nic")
		if (portMatchesFwd && (srcIP == req.SrcIP || targetIPs[dstIP])) || snatMatchesFwd {
			oh := outerHeader
			if snatMatchesFwd && srcIP != req.SrcIP && oh == "" {
				oh = fmt.Sprintf("SNAT %s:%d → %s:%d", req.SrcIP, req.SrcPort, srcIP, sp)
			}
			return model.CapturedTracePacket{
				Interface:   ifaceName,
				Stage:       stage,
				Direction:   "forward",
				TimestampNs: tsNs,
				SrcIP:       srcIP,
				DstIP:       dstIP,
				SrcPort:     sp,
				DstPort:     dp,
				Protocol:    "UDP",
				OuterHeader: oh,
				Length:      len(raw),
				Summary:     fmt.Sprintf("UDP %s:%d → %s:%d len=%d", srcIP, sp, dstIP, dp, len(raw)),
			}, true
		}
		if int(dp) == req.SrcPort && (int(sp) == req.DstPort || (req.BackendPort > 0 && int(sp) == req.BackendPort)) && (dstIP == req.SrcIP || targetIPs[srcIP]) {
			return model.CapturedTracePacket{
				Interface:   ifaceName,
				Stage:       stage,
				Direction:   "return",
				TimestampNs: tsNs,
				SrcIP:       srcIP,
				DstIP:       dstIP,
				SrcPort:     sp,
				DstPort:     dp,
				Protocol:    "UDP",
				OuterHeader: outerHeader,
				Length:      len(raw),
				Summary:     fmt.Sprintf("UDP %s:%d → %s:%d len=%d", srcIP, sp, dstIP, dp, len(raw)),
			}, true
		}

	case "ICMP":
		icmpLayer := packet.Layer(layers.LayerTypeICMPv4)
		if icmpLayer == nil {
			return model.CapturedTracePacket{}, false
		}
		icmp := icmpLayer.(*layers.ICMPv4)
		idMatches := icmp.Id == req.ICMPID
		payloadSig := bytes.Contains(icmp.LayerPayload(), []byte("CALICO-NETDIAG-TF"))
		if !idMatches && !payloadSig {
			return model.CapturedTracePacket{}, false
		}
		if icmp.TypeCode.Type() == layers.ICMPv4TypeEchoRequest && (srcIP == req.SrcIP || targetIPs[dstIP]) {
			oh := outerHeader
			if !idMatches && srcIP != req.SrcIP && oh == "" {
				oh = fmt.Sprintf("SNAT %s → %s (icmp_id=%d)", req.SrcIP, srcIP, icmp.Id)
			}
			return model.CapturedTracePacket{
				Interface:   ifaceName,
				Stage:       stage,
				Direction:   "forward",
				TimestampNs: tsNs,
				SrcIP:       srcIP,
				DstIP:       dstIP,
				Protocol:    "ICMP",
				ICMPType:    "EchoRequest",
				OuterHeader: oh,
				Length:      len(raw),
				Summary:     fmt.Sprintf("ICMP Echo Request %s → %s id=%d seq=%d", srcIP, dstIP, icmp.Id, icmp.Seq),
			}, true
		}
		if icmp.TypeCode.Type() == layers.ICMPv4TypeEchoReply && (dstIP == req.SrcIP || targetIPs[srcIP]) {
			return model.CapturedTracePacket{
				Interface:   ifaceName,
				Stage:       stage,
				Direction:   "return",
				TimestampNs: tsNs,
				SrcIP:       srcIP,
				DstIP:       dstIP,
				Protocol:    "ICMP",
				ICMPType:    "EchoReply",
				OuterHeader: outerHeader,
				Length:      len(raw),
				Summary:     fmt.Sprintf("ICMP Echo Reply %s → %s id=%d seq=%d", srcIP, dstIP, icmp.Id, icmp.Seq),
			}, true
		}
	}

	return model.CapturedTracePacket{}, false
}

func injectTraceProbe(ctx context.Context, nsPath string, req model.AgentTraceCaptureRequest) (string, error) {
	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	if timeout > 2500*time.Millisecond {
		timeout = 2500 * time.Millisecond
	}

	switch req.Protocol {
	case "TCP":
		localAddr := &net.TCPAddr{Port: req.SrcPort}
		target := net.JoinHostPort(req.DstIP, fmt.Sprintf("%d", req.DstPort))
		conn, err := netns.DialInNetns(ctx, nsPath, "tcp4", target, localAddr, timeout)
		if err != nil {
			if strings.Contains(err.Error(), "connection refused") {
				return "RST_REFUSED", nil
			}
			return "TIMEOUT_OR_ERROR", err
		}
		_ = conn.Close()
		return "CONNECTED", nil

	case "UDP":
		localAddr := &net.UDPAddr{Port: req.SrcPort}
		target := net.JoinHostPort(req.DstIP, fmt.Sprintf("%d", req.DstPort))
		conn, err := netns.DialInNetns(ctx, nsPath, "udp4", target, localAddr, timeout)
		if err != nil {
			return "ERROR", err
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("CALICO-NETDIAG-TRACEFLOW"))
		return "SENT", nil

	case "ICMP":
		var result string
		err := netns.RunInNetns(nsPath, func() error {
			fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_RAW, syscall.IPPROTO_ICMP)
			if err != nil {
				return err
			}
			defer syscall.Close(fd)

			tv := syscall.Timeval{Sec: 1, Usec: 500000}
			_ = syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)

			dst4 := net.ParseIP(req.DstIP).To4()
			if dst4 == nil {
				return fmt.Errorf("invalid IPv4 destination %s", req.DstIP)
			}
			pkt := buildICMPEchoRequest(req.ICMPID, 1, []byte("CALICO-NETDIAG-TF"))
			var addr [4]byte
			copy(addr[:], dst4)
			if err := syscall.Sendto(fd, pkt, 0, &syscall.SockaddrInet4{Addr: addr}); err != nil {
				return err
			}
			// Wait for Echo Reply
			buf := make([]byte, 512)
			deadline := time.Now().Add(timeout)
			for time.Now().Before(deadline) {
				n, _, rerr := syscall.Recvfrom(fd, buf, 0)
				if rerr != nil {
					result = "TIMEOUT"
					return nil
				}
				if n >= 28 {
					ihl := int(buf[0]&0x0f) * 4
					if n >= ihl+8 && buf[ihl] == 0 { // Echo Reply
						id := binary.BigEndian.Uint16(buf[ihl+4 : ihl+6])
						if id == req.ICMPID {
							result = "ECHO_REPLY"
							return nil
						}
					}
				}
			}
			result = "TIMEOUT"
			return nil
		})
		return result, err
	default:
		return "UNSUPPORTED", fmt.Errorf("unsupported protocol %s", req.Protocol)
	}
}

func buildICMPEchoRequest(id, seq uint16, payload []byte) []byte {
	msg := make([]byte, 8+len(payload))
	msg[0] = 8 // Echo Request
	msg[1] = 0 // Code 0
	binary.BigEndian.PutUint16(msg[4:6], id)
	binary.BigEndian.PutUint16(msg[6:8], seq)
	copy(msg[8:], payload)
	csum := icmpChecksum(msg)
	binary.BigEndian.PutUint16(msg[2:4], csum)
	return msg
}

func icmpChecksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i : i+2]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func formatTCPFlags(tcp *layers.TCP) string {
	var f []string
	if tcp.SYN {
		f = append(f, "SYN")
	}
	if tcp.ACK {
		f = append(f, "ACK")
	}
	if tcp.PSH {
		f = append(f, "PSH")
	}
	if tcp.FIN {
		f = append(f, "FIN")
	}
	if tcp.RST {
		f = append(f, "RST")
	}
	if tcp.URG {
		f = append(f, "URG")
	}
	if len(f) == 0 {
		return "NONE"
	}
	return strings.Join(f, ",")
}

func htons(v uint16) uint16 {
	return (v << 8) | (v >> 8)
}

// Unused import guard for vnetns on non-linux tooling
var _ = vnetns.None
var _ = runtime.GOOS
