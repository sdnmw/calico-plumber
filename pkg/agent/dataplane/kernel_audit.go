package dataplane

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"calico-netdiag/pkg/model"

	"github.com/vishvananda/netlink"
)

// InspectNodeKernelAudit performs a comprehensive live L2 (MAC/ARP/ProxyARP), MTU, NAT,
// Conntrack, and sysctl network audit on the local Kubernetes node.
func InspectNodeKernelAudit(nodeName string) model.NodeKernelAudit {
	hostInfo := InspectHost(nodeName)
	out := model.NodeKernelAudit{
		NodeName:      nodeName,
		InternalIP:    hostInfo.PrimaryIP,
		KernelVersion: hostInfo.KernelVersion,
		Score:         100,
		Status:        "healthy",
		CollectedAt:   time.Now(),
	}

	// 1. L2 MAC / Default Gateway / ARP Neighbor Table & Calico Proxy ARP
	out.L2 = inspectNodeL2(hostInfo)

	// 2. Conntrack & NAT stats
	ct := inspectConntrackStats()
	ct.AcctEnabled = hostInfo.ConntrackAcct
	natOut, randomFully := inspectCalicoNATDetail()
	ct.NATOutgoingEnabled = natOut
	ct.SNATRandomFully = randomFully
	out.Conntrack = ct

	// 3. Sysctl audit
	out.Sysctls = inspectKernelSysctls()

	// 4. Multi-layer MTU audit
	out.MTU = inspectNodeMTU(hostInfo)

	// 5. Synthesize network-readable findings with underlying kernel drilldown evidence
	out.Findings, out.Score, out.Status = evaluateKernelAuditFindings(nodeName, out.L2, out.Conntrack, out.Sysctls, out.MTU)
	return out
}

func inspectNodeL2(hostInfo HostDataplaneInfo) model.L2NetworkInfo {
	l2 := model.L2NetworkInfo{
		PrimaryNIC: hostInfo.PrimaryNIC,
		PrimaryIP:  hostInfo.PrimaryIP,
	}

	linkNames := make(map[int]string)
	if links, err := netlink.LinkList(); err == nil {
		for _, l := range links {
			attrs := l.Attrs()
			name := attrs.Name
			linkNames[attrs.Index] = name
			if name == hostInfo.PrimaryNIC && len(attrs.HardwareAddr) > 0 {
				l2.PrimaryMAC = attrs.HardwareAddr.String()
			}
			if strings.HasPrefix(name, "cali") {
				l2.TotalCaliVeths++
				if v, ok := readProcStr(fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/proxy_arp", name)); ok && v == "1" {
					l2.ProxyARPVeths++
				}
			}
		}
	}

	// Default gateway from main IPv4 routing table
	if routes, err := netlink.RouteList(nil, netlink.FAMILY_V4); err == nil {
		for _, r := range routes {
			if (r.Dst == nil || r.Dst.String() == "0.0.0.0/0") && r.Gw != nil {
				l2.DefaultGateway = r.Gw.String()
				break
			}
		}
	}

	// ARP neighbor table capacity (gc_thresh3)
	if v, ok := readProcStr("/proc/sys/net/ipv4/neigh/default/gc_thresh3"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			l2.ARPGCThresh3 = n
		}
	}

	// Live IPv4 ARP / Neighbor cache via Netlink
	if neighs, err := netlink.NeighList(0, netlink.FAMILY_V4); err == nil {
		for _, n := range neighs {
			if n.IP == nil || n.IP.IsMulticast() || n.IP.IsLoopback() {
				continue
			}
			state := nudStateName(n.State)
			if state == "NOARP" || state == "NONE" {
				continue
			}
			ipStr := n.IP.String()
			macStr := ""
			if len(n.HardwareAddr) > 0 {
				macStr = n.HardwareAddr.String()
			}
			dev := linkNames[n.LinkIndex]
			entry := model.ARPEntry{
				IP:    ipStr,
				MAC:   macStr,
				Dev:   dev,
				State: state,
			}
			if strings.HasPrefix(dev, "cali") {
				entry.Role = "pod-veth"
			}
			if ipStr == l2.DefaultGateway {
				entry.Role = "gateway"
				l2.GatewayMAC = macStr
				l2.GatewayARPState = state
			}
			if state == "FAILED" || state == "INCOMPLETE" {
				l2.ARPFailedCount++
			}
			l2.Neighbors = append(l2.Neighbors, entry)
		}
	}

	l2.ARPCount = len(l2.Neighbors)
	if l2.ARPGCThresh3 > 0 {
		l2.ARPUsagePct = math.Round(float64(l2.ARPCount)/float64(l2.ARPGCThresh3)*10000) / 100
	}

	sort.Slice(l2.Neighbors, func(i, j int) bool {
		if l2.Neighbors[i].Role != l2.Neighbors[j].Role {
			return arpRoleRank(l2.Neighbors[i].Role) < arpRoleRank(l2.Neighbors[j].Role)
		}
		return l2.Neighbors[i].IP < l2.Neighbors[j].IP
	})
	return l2
}

func nudStateName(state int) string {
	switch {
	case state&netlink.NUD_PERMANENT != 0:
		return "PERMANENT"
	case state&netlink.NUD_REACHABLE != 0:
		return "REACHABLE"
	case state&netlink.NUD_STALE != 0:
		return "STALE"
	case state&netlink.NUD_DELAY != 0:
		return "DELAY"
	case state&netlink.NUD_PROBE != 0:
		return "PROBE"
	case state&netlink.NUD_INCOMPLETE != 0:
		return "INCOMPLETE"
	case state&netlink.NUD_FAILED != 0:
		return "FAILED"
	case state&netlink.NUD_NOARP != 0:
		return "NOARP"
	default:
		return "NONE"
	}
}

func arpRoleRank(role string) int {
	switch role {
	case "gateway":
		return 0
	case "cluster-node":
		return 1
	case "other":
		return 2
	case "pod-veth":
		return 3
	default:
		return 2
	}
}

func inspectConntrackStats() model.ConntrackStats {
	ct := model.ConntrackStats{}
	ct.Count = readProcInt64("/proc/sys/net/netfilter/nf_conntrack_count")
	ct.Max = readProcInt64("/proc/sys/net/netfilter/nf_conntrack_max")
	ct.Buckets = readProcInt64("/proc/sys/net/netfilter/nf_conntrack_buckets")
	if ct.Max > 0 {
		ct.UsagePct = math.Round(float64(ct.Count)/float64(ct.Max)*10000) / 100
	}
	if raw, err := os.ReadFile("/proc/net/stat/nf_conntrack"); err == nil {
		ParseConntrackStat(string(raw), &ct)
	}
	return ct
}

// ParseConntrackStat sums per-CPU hexadecimal counters from /proc/net/stat/nf_conntrack.
// Exported for unit testing across kernel versions.
func ParseConntrackStat(raw string, out *model.ConntrackStats) {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) < 2 {
		return
	}
	headers := strings.Fields(lines[0])
	colIdx := make(map[string]int, len(headers))
	for i, h := range headers {
		colIdx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	parseCol := func(fields []string, name string) uint64 {
		idx, ok := colIdx[name]
		if !ok || idx >= len(fields) {
			return 0
		}
		v, err := strconv.ParseUint(fields[idx], 16, 64)
		if err != nil {
			return 0
		}
		return v
	}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		out.Searched += parseCol(fields, "searched")
		out.Found += parseCol(fields, "found")
		out.New += parseCol(fields, "new")
		out.Invalid += parseCol(fields, "invalid")
		out.Ignore += parseCol(fields, "ignore")
		out.Insert += parseCol(fields, "insert")
		out.InsertFailed += parseCol(fields, "insert_failed")
		out.Drop += parseCol(fields, "drop")
		out.EarlyDrop += parseCol(fields, "early_drop")
		out.ICMPError += parseCol(fields, "icmp_error")
		out.SearchRestart += parseCol(fields, "search_restart")
	}
}

func inspectCalicoNATDetail() (natOutgoing bool, randomFully bool) {
	cmd := exec.Command("iptables-save", "-t", "nat")
	b, err := cmd.Output()
	if err != nil {
		return CheckCalicoNATOutgoing(), false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "cali-nat-outgoing") && strings.Contains(line, "MASQUERADE") {
			natOutgoing = true
			if strings.Contains(line, "--random-fully") {
				randomFully = true
			}
		}
	}
	return natOutgoing, randomFully
}

func inspectKernelSysctls() []model.SysctlCheck {
	var checks []model.SysctlCheck

	// 1. net.ipv4.ip_forward
	if v, ok := readProcStr("/proc/sys/net/ipv4/ip_forward"); ok {
		st := "pass"
		rem := ""
		if v != "1" {
			st = "critical"
			rem = "sysctl -w net.ipv4.ip_forward=1"
		}
		checks = append(checks, model.SysctlCheck{
			Key:         "net.ipv4.ip_forward",
			Value:       v,
			Expected:    "1",
			Category:    "routing",
			Status:      st,
			Explanation: "Kubernetes/Calico 宿主机三层转发开关，关闭将导致跨 Pod / 跨节点流量全部中断",
			Remediation: rem,
		})
	}

	// 2. rp_filter (all & default)
	for _, sub := range []string{"all", "default"} {
		key := fmt.Sprintf("net.ipv4.conf.%s.rp_filter", sub)
		path := fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/rp_filter", sub)
		if v, ok := readProcStr(path); ok {
			st := "pass"
			rem := ""
			exp := "0 或 2 (Loose)"
			if v == "1" {
				st = "warn"
				rem = fmt.Sprintf("sysctl -w %s=2", key)
			}
			checks = append(checks, model.SysctlCheck{
				Key:         key,
				Value:       v,
				Expected:    exp,
				Category:    "routing",
				Status:      st,
				Explanation: "严格反向路径校验 (1) 在多网卡/CrossSubnet/非对称路由/NodePort 场景下容易静默丢包，建议使用宽松模式 (2) 或关闭 (0)",
				Remediation: rem,
			})
		}
	}

	// 3. net.ipv4.tcp_tw_recycle (legacy kernel killer)
	if v, ok := readProcStr("/proc/sys/net/ipv4/tcp_tw_recycle"); ok {
		st := "pass"
		rem := ""
		if v == "1" {
			st = "critical"
			rem = "sysctl -w net.ipv4.tcp_tw_recycle=0"
		}
		checks = append(checks, model.SysctlCheck{
			Key:         "net.ipv4.tcp_tw_recycle",
			Value:       v,
			Expected:    "0",
			Category:    "tcp",
			Status:      st,
			Explanation: "开启后在 Calico SNAT / NAT 网关场景会触发 PAWS 时间戳校验失败，导致同 NAT 源 IP 的新建 SYN 被静默丢弃",
			Remediation: rem,
		})
	} else {
		checks = append(checks, model.SysctlCheck{
			Key:         "net.ipv4.tcp_tw_recycle",
			Value:       "N/A (Kernel ≥ 4.12 已移除)",
			Expected:    "0 / 已移除",
			Category:    "tcp",
			Status:      "pass",
			Explanation: "Linux 4.12+ 内核已彻底移除 tcp_tw_recycle，不存在本地节点触发 PAWS 丢包风险（但访问老旧 CentOS 7 外部服务仍需注意）",
		})
	}

	// 4. net.ipv4.tcp_timestamps
	if v, ok := readProcStr("/proc/sys/net/ipv4/tcp_timestamps"); ok {
		st := "pass"
		if v == "0" {
			st = "info"
		}
		checks = append(checks, model.SysctlCheck{
			Key:         "net.ipv4.tcp_timestamps",
			Value:       v,
			Expected:    "1",
			Category:    "tcp",
			Status:      st,
			Explanation: "TCP 时间戳选项，用于 RTT 精准估算与高吞吐序列号回绕保护（PAWS）",
		})
	}

	// 5. net.netfilter.nf_conntrack_tcp_be_liberal
	if v, ok := readProcStr("/proc/sys/net/netfilter/nf_conntrack_tcp_be_liberal"); ok {
		st := "pass"
		if v == "0" {
			st = "info"
		}
		checks = append(checks, model.SysctlCheck{
			Key:         "net.netfilter.nf_conntrack_tcp_be_liberal",
			Value:       v,
			Expected:    "1 (推荐) / 0",
			Category:    "conntrack",
			Status:      st,
			Explanation: "开启 (1) 可降低因网络乱序或重传导致的 Conntrack INVALID 误丢包（仅 RST 才严格校验窗口）",
		})
	}

	// 6. net.bridge.bridge-nf-call-iptables (if loaded)
	if v, ok := readProcStr("/proc/sys/net/bridge/bridge-nf-call-iptables"); ok {
		st := "pass"
		rem := ""
		if v != "1" {
			st = "warn"
			rem = "sysctl -w net.bridge.bridge-nf-call-iptables=1"
		}
		checks = append(checks, model.SysctlCheck{
			Key:         "net.bridge.bridge-nf-call-iptables",
			Value:       v,
			Expected:    "1",
			Category:    "bridge",
			Status:      st,
			Explanation: "br_netfilter 已加载时，确保二层桥接流量也经过 iptables/kube-proxy 规则处理",
			Remediation: rem,
		})
	}

	// 7. ARP neighbor cache limit (gc_thresh3)
	if v, ok := readProcStr("/proc/sys/net/ipv4/neigh/default/gc_thresh3"); ok {
		num, _ := strconv.Atoi(v)
		st := "pass"
		rem := ""
		if num > 0 && num < 2048 {
			st = "warn"
			rem = "sysctl -w net.ipv4.neigh.default.gc_thresh1=1024 net.ipv4.neigh.default.gc_thresh2=2048 net.ipv4.neigh.default.gc_thresh3=4096"
		}
		checks = append(checks, model.SysctlCheck{
			Key:         "net.ipv4.neigh.default.gc_thresh3",
			Value:       v,
			Expected:    "≥ 2048 (大集群推荐 4096+)",
			Category:    "neighbor",
			Status:      st,
			Explanation: "宿主机 ARP 邻居表上限；大规模同二层集群过小易触发 neighbour: arp_cache: neighbor table overflow",
			Remediation: rem,
		})
	}

	// 8. Ephemeral port range (SNAT / outbound concurrency)
	if v, ok := readProcStr("/proc/sys/net/ipv4/ip_local_port_range"); ok {
		fields := strings.Fields(v)
		st := "pass"
		rem := ""
		if len(fields) == 2 {
			lo, _ := strconv.Atoi(fields[0])
			hi, _ := strconv.Atoi(fields[1])
			if hi-lo < 20000 {
				st = "warn"
				rem = "sysctl -w net.ipv4.ip_local_port_range='10240 65535'"
			}
		}
		checks = append(checks, model.SysctlCheck{
			Key:         "net.ipv4.ip_local_port_range",
			Value:       strings.Join(fields, " - "),
			Expected:    "跨度 ≥ 20000",
			Category:    "tcp",
			Status:      st,
			Explanation: "宿主机出向临时端口池范围，过窄在高并发访问外部服务/SNAT 场景下容易端口耗尽",
			Remediation: rem,
		})
	}

	// 9. somaxconn
	if v, ok := readProcStr("/proc/sys/net/core/somaxconn"); ok {
		num, _ := strconv.Atoi(v)
		st := "pass"
		rem := ""
		if num > 0 && num < 1024 {
			st = "warn"
			rem = "sysctl -w net.core.somaxconn=4096"
		}
		checks = append(checks, model.SysctlCheck{
			Key:         "net.core.somaxconn",
			Value:       v,
			Expected:    "≥ 1024",
			Category:    "tcp",
			Status:      st,
			Explanation: "TCP 监听全连接队列上限，过低在突发建连时易触发 SYN 丢弃或连接超时",
			Remediation: rem,
		})
	}

	return checks
}

func inspectNodeMTU(hostInfo HostDataplaneInfo) model.MTUCheck {
	mc := model.MTUCheck{
		PrimaryNIC: hostInfo.PrimaryNIC,
		PrimaryMTU: hostInfo.NICMTU,
		Status:     "pass",
	}

	expectedPodMTU := hostInfo.NICMTU
	for _, tif := range hostInfo.TunnelInterfaces {
		if tif.Type == "vxlan" || tif.Type == "ipip" || tif.Type == "wireguard" {
			mc.TunnelIface = tif.Name
			mc.TunnelType = tif.Type
			mc.TunnelMTU = tif.MTU
			overhead := 0
			switch tif.Type {
			case "ipip":
				overhead = 20
			case "vxlan":
				overhead = 50
			case "wireguard":
				overhead = 60
			}
			if hostInfo.NICMTU > overhead {
				mc.ExpectedTunnelMTU = hostInfo.NICMTU - overhead
				expectedPodMTU = mc.ExpectedTunnelMTU
			}
			break
		}
	}

	links, err := netlink.LinkList()
	if err == nil {
		for _, l := range links {
			name := l.Attrs().Name
			if !strings.HasPrefix(name, "cali") {
				continue
			}
			mtu := l.Attrs().MTU
			mc.SampledVethCount++
			if mc.MinVethMTU == 0 || mtu < mc.MinVethMTU {
				mc.MinVethMTU = mtu
			}
			if mtu > mc.MaxVethMTU {
				mc.MaxVethMTU = mtu
			}
			if expectedPodMTU > 0 && mtu > expectedPodMTU && len(mc.MismatchedVeths) < 10 {
				mc.MismatchedVeths = append(mc.MismatchedVeths, fmt.Sprintf("%s(MTU=%d)", name, mtu))
			}
		}
	}

	// Evaluate MTU hierarchy
	switch {
	case mc.TunnelIface != "" && mc.ExpectedTunnelMTU > 0 && mc.TunnelMTU > mc.ExpectedTunnelMTU:
		mc.Status = "critical"
		mc.Detail = fmt.Sprintf("隧道口 %s MTU=%d 超过物理口 %s(%d) 减去封装开销后的安全上限 %d，极易引发大包黑洞！",
			mc.TunnelIface, mc.TunnelMTU, mc.PrimaryNIC, mc.PrimaryMTU, mc.ExpectedTunnelMTU)
	case len(mc.MismatchedVeths) > 0:
		mc.Status = "critical"
		mc.Detail = fmt.Sprintf("发现 %d 个 Pod veth MTU 超过安全上限 %d（例如 %s），大包跨节点时将触发分片丢包/TLS 卡死！",
			len(mc.MismatchedVeths), expectedPodMTU, strings.Join(mc.MismatchedVeths[:minInt(3, len(mc.MismatchedVeths))], ", "))
	case mc.SampledVethCount > 0 && mc.MinVethMTU != mc.MaxVethMTU:
		mc.Status = "warn"
		mc.Detail = fmt.Sprintf("同节点 Pod veth MTU 存在差异（范围 %d ~ %d），可能是历史修改 Felix vethMTU 后旧 Pod 未重建所致。",
			mc.MinVethMTU, mc.MaxVethMTU)
	default:
		if mc.TunnelIface != "" {
			mc.Detail = fmt.Sprintf("物理口 %s(%d) → 隧道口 %s(%d) → %d 个 Pod veth(%d) 层级 MTU 完全匹配",
				mc.PrimaryNIC, mc.PrimaryMTU, mc.TunnelIface, mc.TunnelMTU, mc.SampledVethCount, mc.MaxVethMTU)
		} else {
			mc.Detail = fmt.Sprintf("物理口 %s(%d) 与 %d 个 Pod veth(%d) MTU 一致（BGP 直连无封装开销）",
				mc.PrimaryNIC, mc.PrimaryMTU, mc.SampledVethCount, mc.MaxVethMTU)
		}
	}
	return mc
}

// evaluateKernelAuditFindings scores one node and produces network-domain findings with kernel drilldown evidence.
func evaluateKernelAuditFindings(
	nodeName string,
	l2 model.L2NetworkInfo,
	ct model.ConntrackStats,
	sysctls []model.SysctlCheck,
	mtu model.MTUCheck,
) ([]model.AuditFinding, int, string) {
	findings := make([]model.AuditFinding, 0)
	score := 100
	hasCritical := false
	hasWarn := false

	// 1. L2 MAC / ARP / Default Gateway / Calico Proxy ARP checks
	if l2.DefaultGateway != "" && (l2.GatewayARPState == "FAILED" || l2.GatewayARPState == "INCOMPLETE") {
		hasCritical = true
		score -= 25
		findings = append(findings, model.AuditFinding{
			ID:             nodeName + "-gw-arp-failed",
			NodeName:       nodeName,
			Severity:       "critical",
			Category:       "l2",
			Title:          fmt.Sprintf("二层默认网关 %s 的 ARP 解析失败 (%s)", l2.DefaultGateway, l2.GatewayARPState),
			Detail:         fmt.Sprintf("宿主机物理网卡 %s 无法完成对默认网关 %s 的 MAC 地址解析，跨网段路由和集群外向访问将中断。", l2.PrimaryNIC, l2.DefaultGateway),
			KernelEvidence: fmt.Sprintf("ip neigh: gateway=%s dev=%s mac=%s state=%s", l2.DefaultGateway, l2.PrimaryNIC, l2.GatewayMAC, l2.GatewayARPState),
			Remediation:    fmt.Sprintf("检查物理网卡链路与 VLAN/网关连通性: arping -I %s %s && ip neigh show %s", l2.PrimaryNIC, l2.DefaultGateway, l2.DefaultGateway),
		})
	}
	if l2.ARPUsagePct >= 75 {
		hasWarn = true
		score -= 10
		findings = append(findings, model.AuditFinding{
			ID:             nodeName + "-arp-table-high",
			NodeName:       nodeName,
			Severity:       "warning",
			Category:       "l2",
			Title:          fmt.Sprintf("二层 ARP 邻居表使用率偏高 %.1f%% (%d / %d)", l2.ARPUsagePct, l2.ARPCount, l2.ARPGCThresh3),
			Detail:         "二层同网段邻居 MAC 缓存接近内核上限，若继续增长将触发 ARP 表溢出并导致新通信对端无法解析 MAC 地址。",
			KernelEvidence: fmt.Sprintf("net.ipv4.neigh.default.gc_thresh3=%d, 当前 IPv4 邻居条目=%d", l2.ARPGCThresh3, l2.ARPCount),
			Remediation:    "调大 ARP 邻居表上限: sysctl -w net.ipv4.neigh.default.gc_thresh1=2048 net.ipv4.neigh.default.gc_thresh2=4096 net.ipv4.neigh.default.gc_thresh3=8192",
		})
	}
	if l2.TotalCaliVeths > 0 && l2.ProxyARPVeths < l2.TotalCaliVeths {
		hasWarn = true
		score -= 8
		findings = append(findings, model.AuditFinding{
			ID:             nodeName + "-proxy-arp-off",
			NodeName:       nodeName,
			Severity:       "warning",
			Category:       "l2",
			Title:          fmt.Sprintf("部分容器 cali* 网卡未开启 Proxy ARP (%d/%d 已开启)", l2.ProxyARPVeths, l2.TotalCaliVeths),
			Detail:         "Calico 依赖宿主机 cali* veth 的 Proxy ARP 代答容器内对虚拟网关 169.254.1.1 的 ARP 请求；关闭该开关会导致受影响 Pod 无法解析默认网关 MAC。",
			KernelEvidence: fmt.Sprintf("/proc/sys/net/ipv4/conf/cali*/proxy_arp=1 命中 %d / %d", l2.ProxyARPVeths, l2.TotalCaliVeths),
			Remediation:    "检查节点 calico-node Felix 日志，确认是否被其他网络工具覆盖了 cali* 接口 sysctl。",
		})
	}

	// 2. NAT & Connection State Table (Network-first description + Conntrack drilldown evidence)
	ctEvidence := fmt.Sprintf(
		"nf_conntrack_count=%d / %d (%.2f%%) · buckets=%d · insert_failed=%d · drop=%d · early_drop=%d · invalid=%d · search_restart=%d · SNAT --random-fully=%v",
		ct.Count, ct.Max, ct.UsagePct, ct.Buckets, ct.InsertFailed, ct.Drop, ct.EarlyDrop, ct.Invalid, ct.SearchRestart, ct.SNATRandomFully,
	)

	if ct.Max > 0 {
		if ct.UsagePct >= 85 {
			hasCritical = true
			score -= 30
			findings = append(findings, model.AuditFinding{
				ID:             nodeName + "-ct-high",
				NodeName:       nodeName,
				Severity:       "critical",
				Category:       "nat",
				Title:          fmt.Sprintf("NAT 与连接状态表接近饱和 %.1f%% (%d / %d)", ct.UsagePct, ct.Count, ct.Max),
				Detail:         "宿主机负责记录 NAT 转换映射与连接状态的会话表水位已超 85%，继续增长将导致新建 TCP/UDP 连接及 Service 访问被内核静默丢弃。",
				KernelEvidence: ctEvidence,
				Remediation:    "sysctl -w net.netfilter.nf_conntrack_max=524288 && echo 131072 > /sys/module/nf_conntrack/parameters/hashsize",
			})
		} else if ct.UsagePct >= 65 {
			hasWarn = true
			score -= 12
			findings = append(findings, model.AuditFinding{
				ID:             nodeName + "-ct-warn",
				NodeName:       nodeName,
				Severity:       "warning",
				Category:       "nat",
				Title:          fmt.Sprintf("NAT 与连接状态表水位偏高 %.1f%% (%d / %d)", ct.UsagePct, ct.Count, ct.Max),
				Detail:         "活跃 NAT 与连接状态会话数已超容量 65%，在短连接或 UDP DNS 突发峰值期间存在建表压力。",
				KernelEvidence: ctEvidence,
				Remediation:    "sysctl -w net.netfilter.nf_conntrack_max=524288",
			})
		}
		if ct.Buckets > 0 && ct.Max/ct.Buckets > 8 {
			hasWarn = true
			score -= 5
			findings = append(findings, model.AuditFinding{
				ID:             nodeName + "-ct-hash",
				NodeName:       nodeName,
				Severity:       "warning",
				Category:       "nat",
				Title:          fmt.Sprintf("NAT 状态表哈希桶比例偏低 (max/buckets = %d)", ct.Max/ct.Buckets),
				Detail:         "连接状态查询哈希链表偏长，高吞吐 NAT/Service 转发场景下会增加软中断延迟。",
				KernelEvidence: ctEvidence,
				Remediation:    fmt.Sprintf("echo %d > /sys/module/nf_conntrack/parameters/hashsize", ct.Max/4),
			})
		}
	}

	// Unified NAT / SNAT port race & state-table drop finding
	if ct.EarlyDrop > 0 {
		hasCritical = true
		score -= 25
		findings = append(findings, model.AuditFinding{
			ID:             nodeName + "-nat-early-drop",
			NodeName:       nodeName,
			Severity:       "critical",
			Category:       "nat",
			Title:          fmt.Sprintf("NAT 状态表曾因满载强制淘汰活跃连接（early_drop=%d）", ct.EarlyDrop),
			Detail:         "内核连接跟踪表曾出现容量耗尽并提前驱逐活跃会话，直接引发业务偶发断连、Service 超时或 NAT 回包丢失。",
			KernelEvidence: ctEvidence,
			Remediation:    "立即检查 dmesg | grep conntrack 并调大 net.netfilter.nf_conntrack_max 与 hashsize。",
		})
	} else if ct.Drop > 0 || ct.InsertFailed > 0 {
		isSevere := ct.Drop >= 100 || ct.InsertFailed >= 100 || ct.UsagePct >= 65
		sev := "warning"
		if isSevere {
			sev = "critical"
			hasCritical = true
			score -= 20
		} else {
			hasWarn = true
			score -= 8
		}
		cnt := ct.Drop
		if ct.InsertFailed > cnt {
			cnt = ct.InsertFailed
		}
		findings = append(findings, model.AuditFinding{
			ID:             nodeName + "-nat-port-race",
			NodeName:       nodeName,
			Severity:       sev,
			Category:       "nat",
			Title:          fmt.Sprintf("出向 NAT / UDP 端口复用曾发生 %d 次偶发冲突丢包", cnt),
			Detail:         "多核并发通过 Calico SNAT 出集群或访问 CoreDNS (UDP:53) 时，源端口分配竞争会触发状态表插入冲突并丢弃个别报文（业务侧常表现为偶发 DNS 5 秒延迟或短连接重传）。当前表水位健康，属于低频并发竞态。",
			KernelEvidence: ctEvidence,
			Remediation:    "保持 Calico MASQUERADE --random-fully 开启；若业务 DNS 偶发 5 秒超时，建议部署 NodeLocal DNSCache 或在 resolv.conf 开启 single-request-reopen。",
		})
	}

	if ct.NATOutgoingEnabled && !ct.SNATRandomFully {
		hasWarn = true
		score -= 8
		findings = append(findings, model.AuditFinding{
			ID:             nodeName + "-snat-no-random-fully",
			NodeName:       nodeName,
			Severity:       "warning",
			Category:       "nat",
			Title:          "出集群 SNAT (cali-nat-outgoing) 未启用全随机源端口 (--random-fully)",
			Detail:         "Pod 访问集群外地址做 SNAT 转换时若未开启全随机源端口映射，高并发短连接更容易撞端口丢包。",
			KernelEvidence: "iptables-save -t nat 中 cali-nat-outgoing MASQUERADE 规则缺少 --random-fully 参数",
			Remediation:    "确认内核与 iptables 支持 --random-fully，并检查 Calico FelixConfiguration 未关闭该能力。",
		})
	}

	// 3. Routing / Forwarding Sysctl findings (translated into network terminology)
	for _, sc := range sysctls {
		cat := "routing"
		if sc.Category == "neighbor" {
			cat = "l2"
		} else if sc.Category == "conntrack" {
			cat = "nat"
		}
		switch sc.Status {
		case "critical":
			hasCritical = true
			score -= 25
			findings = append(findings, model.AuditFinding{
				ID:             nodeName + "-" + sc.Key,
				NodeName:       nodeName,
				Severity:       "critical",
				Category:       cat,
				Title:          friendlySysctlTitle(sc),
				Detail:         sc.Explanation,
				KernelEvidence: fmt.Sprintf("sysctl %s = %s (期望 %s)", sc.Key, sc.Value, sc.Expected),
				Remediation:    sc.Remediation,
			})
		case "warn":
			hasWarn = true
			score -= 8
			findings = append(findings, model.AuditFinding{
				ID:             nodeName + "-" + sc.Key,
				NodeName:       nodeName,
				Severity:       "warning",
				Category:       cat,
				Title:          friendlySysctlTitle(sc),
				Detail:         sc.Explanation,
				KernelEvidence: fmt.Sprintf("sysctl %s = %s (期望 %s)", sc.Key, sc.Value, sc.Expected),
				Remediation:    sc.Remediation,
			})
		}
	}

	// 4. MTU findings
	if mtu.Status == "critical" {
		hasCritical = true
		score -= 25
		findings = append(findings, model.AuditFinding{
			ID:             nodeName + "-mtu-mismatch",
			NodeName:       nodeName,
			Severity:       "critical",
			Category:       "mtu",
			Title:          "物理网卡 / 隧道接口 / 容器 veth 三层 MTU 不匹配（大包黑洞风险）",
			Detail:         mtu.Detail,
			KernelEvidence: fmt.Sprintf("NIC %s=%dB · Tunnel %s=%dB · cali* veth(%d)=%d..%dB", mtu.PrimaryNIC, mtu.PrimaryMTU, mtu.TunnelIface, mtu.TunnelMTU, mtu.SampledVethCount, mtu.MinVethMTU, mtu.MaxVethMTU),
			Remediation:    "核查 Calico Installation/FelixConfiguration 的 vethMTU 配置，确保 IPIP ≤ 物理MTU-20、VXLAN ≤ 物理MTU-50，并滚动重建 MTU 异常的 Pod。",
		})
	} else if mtu.Status == "warn" {
		hasWarn = true
		score -= 6
		findings = append(findings, model.AuditFinding{
			ID:             nodeName + "-mtu-drift",
			NodeName:       nodeName,
			Severity:       "warning",
			Category:       "mtu",
			Title:          "节点内新旧 Pod 虚拟网卡 MTU 不统一",
			Detail:         mtu.Detail,
			KernelEvidence: fmt.Sprintf("NIC %s=%dB · cali* veth(%d)=%d..%dB", mtu.PrimaryNIC, mtu.PrimaryMTU, mtu.SampledVethCount, mtu.MinVethMTU, mtu.MaxVethMTU),
			Remediation:    "建议在维护窗口重建 MTU 偏大的旧 Pod 以统一路径 MTU。",
		})
	}

	if score < 0 {
		score = 0
	}
	status := "healthy"
	if hasCritical {
		status = "critical"
	} else if hasWarn {
		status = "warning"
	}
	return findings, score, status
}

func friendlySysctlTitle(sc model.SysctlCheck) string {
	switch sc.Key {
	case "net.ipv4.ip_forward":
		return fmt.Sprintf("三层 IP 路由转发开关未开启 (%s=%s)", sc.Key, sc.Value)
	case "net.ipv4.conf.all.rp_filter", "net.ipv4.conf.default.rp_filter":
		return fmt.Sprintf("反向路径校验 (rp_filter=%s) 处于严格模式，多网卡/非对称路由易静默丢包", sc.Value)
	case "net.ipv4.tcp_tw_recycle":
		return fmt.Sprintf("高危 NAT 冲突项：tcp_tw_recycle=%s 已开启（NAT 后大量连接超时）", sc.Value)
	case "net.ipv4.ip_local_port_range":
		return fmt.Sprintf("宿主机出向 NAT 临时端口池偏窄 (%s)", sc.Value)
	case "net.ipv4.neigh.default.gc_thresh3":
		return fmt.Sprintf("二层 ARP 邻居表上限偏低 (gc_thresh3=%s)", sc.Value)
	default:
		return fmt.Sprintf("网络内核参数异常 %s=%s（期望 %s）", sc.Key, sc.Value, sc.Expected)
	}
}

func readProcStr(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

func readProcInt64(path string) int64 {
	s, ok := readProcStr(path)
	if !ok {
		return 0
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
