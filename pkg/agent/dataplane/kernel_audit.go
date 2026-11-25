package dataplane

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"calico-netdiag/pkg/model"

	"github.com/vishvananda/netlink"
)

// InspectNodeKernelAudit performs a comprehensive live kernel network, conntrack, NAT, sysctl,
// and multi-layer MTU audit on the local Kubernetes node.
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

	// 1. Conntrack & NAT stats
	ct := inspectConntrackStats()
	ct.AcctEnabled = hostInfo.ConntrackAcct
	natOut, randomFully := inspectCalicoNATDetail()
	ct.NATOutgoingEnabled = natOut
	ct.SNATRandomFully = randomFully
	out.Conntrack = ct

	// 2. Sysctl audit
	out.Sysctls = inspectKernelSysctls()

	// 3. Multi-layer MTU audit
	out.MTU = inspectNodeMTU(hostInfo)

	// 4. Synthesize actionable findings & score
	out.Findings, out.Score, out.Status = evaluateKernelAuditFindings(nodeName, out.Conntrack, out.Sysctls, out.MTU)
	return out
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

// EvaluateKernelAuditFindings scores one node and produces structured actionable findings.
func evaluateKernelAuditFindings(
	nodeName string,
	ct model.ConntrackStats,
	sysctls []model.SysctlCheck,
	mtu model.MTUCheck,
) ([]model.AuditFinding, int, string) {
	var findings []model.AuditFinding
	score := 100
	hasCritical := false
	hasWarn := false

	// Conntrack capacity check
	if ct.Max > 0 {
		if ct.UsagePct >= 85 {
			hasCritical = true
			score -= 30
			findings = append(findings, model.AuditFinding{
				ID:          nodeName + "-ct-high",
				NodeName:    nodeName,
				Severity:    "critical",
				Category:    "conntrack",
				Title:       fmt.Sprintf("Conntrack 表使用率高达 %.1f%% (%d / %d)", ct.UsagePct, ct.Count, ct.Max),
				Detail:      "连接跟踪表即将打满，一旦溢出内核将直接静默丢弃新建连接（dmesg: nf_conntrack: table full, dropping packet）。",
				Remediation: "sysctl -w net.netfilter.nf_conntrack_max=524288 && echo 131072 > /sys/module/nf_conntrack/parameters/hashsize",
			})
		} else if ct.UsagePct >= 65 {
			hasWarn = true
			score -= 12
			findings = append(findings, model.AuditFinding{
				ID:          nodeName + "-ct-warn",
				NodeName:    nodeName,
				Severity:    "warning",
				Category:    "conntrack",
				Title:       fmt.Sprintf("Conntrack 表水位偏高 %.1f%% (%d / %d)", ct.UsagePct, ct.Count, ct.Max),
				Detail:      "建议关注短连接与 UDP DNS 峰值并提前调大 nf_conntrack_max 与 buckets（推荐 max/buckets ≤ 4）。",
				Remediation: "sysctl -w net.netfilter.nf_conntrack_max=524288",
			})
		}
		if ct.Buckets > 0 && ct.Max/ct.Buckets > 8 {
			hasWarn = true
			score -= 5
			findings = append(findings, model.AuditFinding{
				ID:          nodeName + "-ct-hash",
				NodeName:    nodeName,
				Severity:    "warning",
				Category:    "conntrack",
				Title:       fmt.Sprintf("Conntrack 哈希桶比例偏低 (max/buckets = %d)", ct.Max/ct.Buckets),
				Detail:      fmt.Sprintf("nf_conntrack_max=%d 但 buckets=%d，单桶链表偏长会增大查找冲突与 CPU 软中断开销。", ct.Max, ct.Buckets),
				Remediation: fmt.Sprintf("echo %d > /sys/module/nf_conntrack/parameters/hashsize", ct.Max/4),
			})
		}
	}

	// Conntrack drop / insert_failed / race checks
	if ct.Drop > 0 {
		hasCritical = true
		score -= 20
		findings = append(findings, model.AuditFinding{
			ID:          nodeName + "-ct-drop",
			NodeName:    nodeName,
			Severity:    "critical",
			Category:    "conntrack",
			Title:       fmt.Sprintf("内核 Conntrack 累计发生 %d 次丢包 (drop=%d, early_drop=%d)", ct.Drop, ct.Drop, ct.EarlyDrop),
			Detail:      "通常由 Conntrack 表满、SNAT 源端口冲突或内存压力引起，会导致业务偶发建连超时或 DNS 5 秒卡顿。",
			Remediation: "检查 dmesg | grep conntrack，核查 nf_conntrack_max 及 Calico natOutgoing --random-fully 配置。",
		})
	}
	if ct.InsertFailed > 0 {
		sev := "warning"
		if ct.InsertFailed > 100 {
			sev = "critical"
			hasCritical = true
			score -= 15
		} else {
			hasWarn = true
			score -= 8
		}
		findings = append(findings, model.AuditFinding{
			ID:          nodeName + "-ct-insert-failed",
			NodeName:    nodeName,
			Severity:    sev,
			Category:    "conntrack",
			Title:       fmt.Sprintf("Conntrack 插入冲突 insert_failed=%d（典型 SNAT/UDP 端口竞争特征）", ct.InsertFailed),
			Detail:      "多核并发通过 Calico SNAT 或 kube-proxy 访问同一目标（如 CoreDNS UDP:53）时，容易因源端口竞争导致 insert_failed 丢包。",
			Remediation: "确保 Calico 开启 --random-fully (FelixConfiguration natPortRange/iptablesMarkMask) 并在业务侧启用 NodeLocal DNSCache 或 TCP DNS。",
		})
	}
	if ct.NATOutgoingEnabled && !ct.SNATRandomFully {
		hasWarn = true
		score -= 8
		findings = append(findings, model.AuditFinding{
			ID:          nodeName + "-snat-no-random-fully",
			NodeName:    nodeName,
			Severity:    "warning",
			Category:    "conntrack",
			Title:       "Calico cali-nat-outgoing 未启用 --random-fully",
			Detail:      "未启用 --random-fully 时高并发短连接 SNAT 更易触发 Conntrack 源端口冲突（insert_failed）。",
			Remediation: "升级 iptables/Calico 并确认 FelixConfiguration 未禁用 random-fully SNAT。",
		})
	}

	// Sysctl findings
	for _, sc := range sysctls {
		switch sc.Status {
		case "critical":
			hasCritical = true
			score -= 25
			findings = append(findings, model.AuditFinding{
				ID:          nodeName + "-" + sc.Key,
				NodeName:    nodeName,
				Severity:    "critical",
				Category:    "sysctl",
				Title:       fmt.Sprintf("高危内核参数 %s=%s（期望 %s）", sc.Key, sc.Value, sc.Expected),
				Detail:      sc.Explanation,
				Remediation: sc.Remediation,
			})
		case "warn":
			hasWarn = true
			score -= 8
			findings = append(findings, model.AuditFinding{
				ID:          nodeName + "-" + sc.Key,
				NodeName:    nodeName,
				Severity:    "warning",
				Category:    "sysctl",
				Title:       fmt.Sprintf("内核参数风险 %s=%s（期望 %s）", sc.Key, sc.Value, sc.Expected),
				Detail:      sc.Explanation,
				Remediation: sc.Remediation,
			})
		}
	}

	// MTU findings
	if mtu.Status == "critical" {
		hasCritical = true
		score -= 25
		findings = append(findings, model.AuditFinding{
			ID:          nodeName + "-mtu-mismatch",
			NodeName:    nodeName,
			Severity:    "critical",
			Category:    "mtu",
			Title:       "宿主机/隧道/Pod 多层 MTU 不匹配（存在大包黑洞风险）",
			Detail:      mtu.Detail,
			Remediation: "核查 Calico Installation/FelixConfiguration 的 vethMTU 配置，确保 IPIP ≤ NIC-20、VXLAN ≤ NIC-50，并滚动重建 MTU 异常的 Pod。",
		})
	} else if mtu.Status == "warn" {
		hasWarn = true
		score -= 6
		findings = append(findings, model.AuditFinding{
			ID:          nodeName + "-mtu-drift",
			NodeName:    nodeName,
			Severity:    "warning",
			Category:    "mtu",
			Title:       "节点内 Pod veth MTU 存在新旧不一致",
			Detail:      mtu.Detail,
			Remediation: "建议在维护窗口重建 MTU 偏大的旧 Pod 以统一路径 MTU。",
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
