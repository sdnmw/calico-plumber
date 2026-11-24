package dataplane

import (
	"fmt"
	"net"
	"os"
	"os/exec"
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
