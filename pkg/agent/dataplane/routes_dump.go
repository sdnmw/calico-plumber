package dataplane

import (
	"fmt"
	"sort"
	"strings"
	"syscall"
	"time"

	"calico-netdiag/pkg/model"

	"github.com/vishvananda/netlink"
)

// DumpHostRoutes returns all live IPv4 routes from the local node's main routing table
// along with all local cali* veth interface names.
func DumpHostRoutes(nodeName string) model.AgentHostRoutesResponse {
	hostInfo := InspectHost(nodeName)
	out := model.AgentHostRoutesResponse{
		NodeName:    nodeName,
		PrimaryNIC:  hostInfo.PrimaryNIC,
		PrimaryIP:   hostInfo.PrimaryIP,
		CollectedAt: time.Now(),
	}

	linkNames := make(map[int]string)
	if links, err := netlink.LinkList(); err == nil {
		for _, l := range links {
			name := l.Attrs().Name
			linkNames[l.Attrs().Index] = name
			if strings.HasPrefix(name, "cali") {
				out.LocalVeths = append(out.LocalVeths, name)
			}
		}
	}
	sort.Strings(out.LocalVeths)

	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return out
	}

	for _, r := range routes {
		dst := "0.0.0.0/0"
		if r.Dst != nil {
			dst = r.Dst.String()
		}
		gw := ""
		if r.Gw != nil {
			gw = r.Gw.String()
		}
		dev := linkNames[r.LinkIndex]
		var flags []string
		if r.Flags&int(netlink.FLAG_ONLINK) != 0 {
			flags = append(flags, "onlink")
		}
		if r.Flags&int(netlink.FLAG_PERVASIVE) != 0 {
			flags = append(flags, "pervasive")
		}
		out.Routes = append(out.Routes, model.HostRouteEntry{
			Dst:      dst,
			Gateway:  gw,
			Dev:      dev,
			Protocol: routeProtocolName(int(r.Protocol)),
			Type:     routeTypeName(r.Type),
			Scope:    routeScopeName(r.Scope),
			Metric:   r.Priority,
			Flags:    strings.Join(flags, ","),
		})
	}

	sort.Slice(out.Routes, func(i, j int) bool {
		if out.Routes[i].Protocol != out.Routes[j].Protocol {
			return out.Routes[i].Protocol < out.Routes[j].Protocol
		}
		return out.Routes[i].Dst < out.Routes[j].Dst
	})
	return out
}

func routeProtocolName(proto int) string {
	switch proto {
	case syscall.RTPROT_KERNEL:
		return "kernel"
	case syscall.RTPROT_BOOT:
		return "boot"
	case syscall.RTPROT_STATIC:
		return "static"
	case 11:
		return "zebra"
	case 12, 17:
		return "bird"
	case 80:
		return "calico-felix"
	default:
		return fmt.Sprintf("proto-%d", proto)
	}
}

func routeTypeName(t int) string {
	switch t {
	case syscall.RTN_UNICAST:
		return "unicast"
	case syscall.RTN_LOCAL:
		return "local"
	case syscall.RTN_BROADCAST:
		return "broadcast"
	case syscall.RTN_BLACKHOLE:
		return "blackhole"
	case syscall.RTN_UNREACHABLE:
		return "unreachable"
	case syscall.RTN_PROHIBIT:
		return "prohibit"
	default:
		return fmt.Sprintf("type-%d", t)
	}
}

func routeScopeName(s netlink.Scope) string {
	switch s {
	case netlink.SCOPE_UNIVERSE:
		return "global"
	case netlink.SCOPE_SITE:
		return "site"
	case netlink.SCOPE_LINK:
		return "link"
	case netlink.SCOPE_HOST:
		return "host"
	case netlink.SCOPE_NOWHERE:
		return "nowhere"
	default:
		return fmt.Sprintf("scope-%d", int(s))
	}
}
