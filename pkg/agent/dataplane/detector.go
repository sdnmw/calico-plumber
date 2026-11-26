package dataplane

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"calico-netdiag/pkg/agent/netns"
	"calico-netdiag/pkg/model"

	"github.com/vishvananda/netlink"
)

// HostDataplaneInfo summarizes the live networking stack detected on a Kubernetes node.
type HostDataplaneInfo struct {
	KernelVersion    string
	BTFAvailable     bool
	ConntrackAcct    bool
	KubeProxyMode    model.KubeProxyMode
	PrimaryNIC       string
	PrimaryIP        string
	NICMTU           int
	TunnelInterfaces []model.TunnelIfaceInfo
	BGPProtocols     []model.BIRDProtocol
}

// InspectHost inspects live node interfaces, kernel capabilities, kube-proxy mode, and BIRD status.
func InspectHost(nodeName string) HostDataplaneInfo {
	info := HostDataplaneInfo{
		KubeProxyMode: model.KubeProxyIptables,
	}

	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		info.KernelVersion = strings.TrimSpace(string(b))
	}
	if st, err := os.Stat("/sys/kernel/btf/vmlinux"); err == nil && st.Size() > 0 {
		info.BTFAvailable = true
	}
	if b, err := os.ReadFile("/proc/sys/net/netfilter/nf_conntrack_acct"); err == nil {
		info.ConntrackAcct = strings.TrimSpace(string(b)) == "1"
	}

	info.PrimaryNIC, info.NICMTU = detectPrimaryNIC()
	info.TunnelInterfaces, info.KubeProxyMode = detectLinksAndKubeProxy(info.PrimaryNIC)
	for _, tif := range info.TunnelInterfaces {
		if tif.Type == "physical" && tif.IP != "" {
			info.PrimaryIP = tif.IP
			break
		}
	}
	info.BGPProtocols = QueryBIRDProtocols("", nodeName)

	return info
}

func detectPrimaryNIC() (string, int) {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err == nil {
		for _, r := range routes {
			if r.Dst == nil || r.Dst.String() == "0.0.0.0/0" {
				if link, err := netlink.LinkByIndex(r.LinkIndex); err == nil {
					return link.Attrs().Name, link.Attrs().MTU
				}
			}
		}
	}
	return "", 0
}

func detectLinksAndKubeProxy(primaryNIC string) ([]model.TunnelIfaceInfo, model.KubeProxyMode) {
	var ifaces []model.TunnelIfaceInfo
	kpMode := model.KubeProxyIptables
	hasIPVS := false

	links, err := netlink.LinkList()
	if err == nil {
		for _, l := range links {
			attrs := l.Attrs()
			name := attrs.Name
			ifType := ""
			switch {
			case name == primaryNIC && primaryNIC != "":
				ifType = "physical"
			case name == "tunl0" || strings.HasPrefix(name, "ipip"):
				ifType = "ipip"
			case name == "vxlan.calico" || strings.Contains(name, "vxlan"):
				ifType = "vxlan"
			case name == "wireguard.cali":
				ifType = "wireguard"
			case name == "kube-ipvs0":
				ifType = "ipvs"
				hasIPVS = true
			}
			if ifType == "" {
				continue
			}

			ipStr := ""
			if addrs, err := netlink.AddrList(l, netlink.FAMILY_V4); err == nil && len(addrs) > 0 {
				ipStr = addrs[0].IP.String()
			}
			state := attrs.OperState.String()
			if attrs.Flags&net.FlagUp != 0 && state == "unknown" {
				state = "UP"
			}
			ifaces = append(ifaces, model.TunnelIfaceInfo{
				Name:  name,
				Type:  ifType,
				IP:    ipStr,
				MAC:   attrs.HardwareAddr.String(),
				MTU:   attrs.MTU,
				State: strings.ToUpper(state),
			})
		}
	}

	if hasIPVS {
		kpMode = model.KubeProxyIPVS
	} else if checkNFTablesKubeProxy() {
		kpMode = model.KubeProxyNFTables
	}
	return ifaces, kpMode
}

func checkNFTablesKubeProxy() bool {
	if _, err := exec.LookPath("nft"); err != nil {
		return false
	}
	out, err := exec.Command("nft", "list", "tables").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "kube-proxy")
}

// RouteDecision holds the host kernel route lookup result for a destination IP.
type RouteDecision struct {
	DstIP         string
	Interface     string
	Gateway       string
	SrcIP         string
	Protocol      string
	EncapDetected string // Direct (BGP), IPIP, VXLAN, Local Pod, Host Local
	Summary       string
}

// LookupHostRoute runs a netlink RouteGet lookup on the host for dstIP.
func LookupHostRoute(dstIP string) RouteDecision {
	dec := RouteDecision{DstIP: dstIP, EncapDetected: "Unknown"}
	ip := net.ParseIP(dstIP)
	if ip == nil {
		dec.Summary = "invalid destination IP"
		return dec
	}
	routes, err := netlink.RouteGet(ip)
	if err != nil || len(routes) == 0 {
		dec.Summary = fmt.Sprintf("no route to %s (%v)", dstIP, err)
		return dec
	}
	r := routes[0]
	if link, err := netlink.LinkByIndex(r.LinkIndex); err == nil {
		dec.Interface = link.Attrs().Name
	}
	if r.Gw != nil {
		dec.Gateway = r.Gw.String()
	}
	if r.Src != nil {
		dec.SrcIP = r.Src.String()
	}
	dec.Protocol = routeProtoName(int(r.Protocol))

	switch {
	case strings.HasPrefix(dec.Interface, "cali"):
		dec.EncapDetected = "Local Pod (veth)"
	case dec.Interface == "tunl0":
		dec.EncapDetected = "IPIP (tunl0)"
	case dec.Interface == "vxlan.calico" || strings.Contains(dec.Interface, "vxlan"):
		dec.EncapDetected = "VXLAN (vxlan.calico)"
	case dec.Interface == "lo":
		dec.EncapDetected = "Host Local (lo)"
	default:
		if dec.Protocol == "bird" {
			dec.EncapDetected = "BGP Direct"
		} else {
			dec.EncapDetected = "Direct / Underlay"
		}
	}

	if dec.Gateway != "" {
		dec.Summary = fmt.Sprintf("%s via %s dev %s proto %s", dstIP, dec.Gateway, dec.Interface, dec.Protocol)
	} else {
		dec.Summary = fmt.Sprintf("%s dev %s scope link proto %s", dstIP, dec.Interface, dec.Protocol)
	}
	return dec
}

func routeProtoName(proto int) string {
	switch proto {
	case 2:
		return "kernel"
	case 3:
		return "boot"
	case 4:
		return "static"
	case 12:
		return "bird"
	case 80:
		return "calico-felix"
	default:
		return fmt.Sprintf("proto-%d", proto)
	}
}

// InspectPodNetns returns interface MAC/MTU and routing table from inside a Pod's netns.
func InspectPodNetns(podUID, podIP string) (hostVeth, podIface, podMAC string, podMTU int, routes []model.RouteEntry, hostRoute string, err error) {
	info, locErr := netns.LocatePod(podUID, podIP)
	if info != nil {
		hostVeth = info.HostVeth
	}
	if podIP != "" {
		hostRoute = LookupHostRoute(podIP).Summary
	}
	if locErr != nil || info == nil {
		return hostVeth, "", "", 0, nil, hostRoute, locErr
	}

	runErr := netns.RunInNetns(info.NetnsPath, func() error {
		links, lerr := netlink.LinkList()
		if lerr == nil {
			for _, l := range links {
				if l.Attrs().Name == "lo" {
					continue
				}
				if l.Attrs().Name == "eth0" || podIface == "" {
					podIface = l.Attrs().Name
					podMAC = l.Attrs().HardwareAddr.String()
					podMTU = l.Attrs().MTU
					if podIface == "eth0" {
						break
					}
				}
			}
		}
		rList, rerr := netlink.RouteList(nil, netlink.FAMILY_V4)
		if rerr == nil {
			for _, r := range rList {
				dst := "default"
				if r.Dst != nil {
					dst = r.Dst.String()
				}
				gw := ""
				if r.Gw != nil {
					gw = r.Gw.String()
				}
				src := ""
				if r.Src != nil {
					src = r.Src.String()
				}
				dev := ""
				if l, err := netlink.LinkByIndex(r.LinkIndex); err == nil {
					dev = l.Attrs().Name
				}
				routes = append(routes, model.RouteEntry{
					Dst:      dst,
					Gateway:  gw,
					Dev:      dev,
					Protocol: routeProtoName(int(r.Protocol)),
					Src:      src,
				})
			}
		}
		return nil
	})
	return hostVeth, podIface, podMAC, podMTU, routes, hostRoute, runErr
}

// InspectPodListeningSockets enters a Pod's netns (or reads host netns if hostNetwork) and parses
// /proc/thread-self/net/{tcp,tcp6,udp,udp6} to return all actively listening sockets without needing ss/netstat.
func InspectPodListeningSockets(podUID, podIP string, hostNetwork bool) ([]model.ListeningSocket, error) {
	readAll := func() []model.ListeningSocket {
		var out []model.ListeningSocket
		seen := make(map[string]bool)
		add := func(s model.ListeningSocket) {
			k := fmt.Sprintf("%s|%s|%d", s.Protocol, s.LocalAddr, s.Port)
			if !seen[k] {
				seen[k] = true
				out = append(out, s)
			}
		}
		for _, spec := range []struct {
			file  string
			proto string
			v6    bool
			isUDP bool
		}{
			{"tcp", "TCP", false, false},
			{"tcp6", "TCP6", true, false},
			{"udp", "UDP", false, true},
			{"udp6", "UDP6", true, true},
		} {
			raw, err := os.ReadFile("/proc/thread-self/net/" + spec.file)
			if err != nil {
				raw, err = os.ReadFile("/proc/self/net/" + spec.file)
				if err != nil {
					continue
				}
			}
			for _, s := range parseProcNetListening(string(raw), spec.proto, spec.v6, spec.isUDP) {
				add(s)
			}
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Port != out[j].Port {
				return out[i].Port < out[j].Port
			}
			return out[i].Protocol < out[j].Protocol
		})
		return out
	}

	if hostNetwork {
		return readAll(), nil
	}
	info, err := netns.LocatePod(podUID, podIP)
	if err != nil || info == nil {
		return nil, fmt.Errorf("locate pod netns: %w", err)
	}
	var sockets []model.ListeningSocket
	runErr := netns.RunInNetns(info.NetnsPath, func() error {
		sockets = readAll()
		return nil
	})
	return sockets, runErr
}

// parseProcNetListening parses Linux /proc/net/{tcp,tcp6,udp,udp6} lines for LISTEN sockets.
func parseProcNetListening(content, proto string, v6, isUDP bool) []model.ListeningSocket {
	var out []model.ListeningSocket
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if i == 0 {
			continue // header
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		state := strings.ToUpper(fields[3])
		// TCP LISTEN = 0A; UDP unbound listener = 07
		if !isUDP && state != "0A" {
			continue
		}
		if isUDP && state != "07" {
			continue
		}
		parts := strings.SplitN(fields[1], ":", 2)
		if len(parts) != 2 {
			continue
		}
		port64, err := strconv.ParseInt(parts[1], 16, 32)
		if err != nil || port64 <= 0 {
			continue
		}
		addr := decodeProcNetIP(parts[0], v6)
		loopback := addr == "127.0.0.1" || addr == "::1" || strings.HasPrefix(addr, "127.")
		out = append(out, model.ListeningSocket{
			Protocol:  proto,
			LocalAddr: addr,
			Port:      int(port64),
			Loopback:  loopback,
		})
	}
	return out
}

func decodeProcNetIP(hexStr string, v6 bool) string {
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return hexStr
	}
	if !v6 && len(b) == 4 {
		// Linux /proc/net/tcp stores IPv4 in little-endian host order on x86_64/arm64
		return net.IPv4(b[3], b[2], b[1], b[0]).String()
	}
	if v6 && len(b) == 16 {
		// Each 32-bit word is little-endian
		var ip [16]byte
		for w := 0; w < 4; w++ {
			ip[w*4+0] = b[w*4+3]
			ip[w*4+1] = b[w*4+2]
			ip[w*4+2] = b[w*4+1]
			ip[w*4+3] = b[w*4+0]
		}
		return net.IP(ip[:]).String()
	}
	return hexStr
}
