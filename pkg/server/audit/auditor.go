package audit

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"calico-netdiag/pkg/k8s"
	"calico-netdiag/pkg/model"
	"calico-netdiag/pkg/server/agentclient"
	"calico-netdiag/pkg/server/flowstore"
)

// Auditor orchestrates cluster-wide Kernel/Conntrack/MTU audits and Route/BGP consistency audits.
type Auditor struct {
	k8sStore *k8s.Store
	flows    *flowstore.Store
	agents   *agentclient.Client
}

// NewAuditor creates a new Auditor instance.
func NewAuditor(k8sStore *k8s.Store, flows *flowstore.Store, agents *agentclient.Client) *Auditor {
	return &Auditor{
		k8sStore: k8sStore,
		flows:    flows,
		agents:   agents,
	}
}

// RunKernelAudit fans out kernel/conntrack/NAT/MTU inspection to all cluster nodes
// and evaluates cross-node consistency anomalies (MTU drift, rp_filter drift).
func (a *Auditor) RunKernelAudit(ctx context.Context) *model.ClusterKernelAuditReport {
	nodes := a.k8sStore.ListNodes()
	encap := a.k8sStore.PrimaryEncapMode()

	results := make([]model.NodeKernelAudit, len(nodes))
	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Add(1)
		go func(idx int, nodeName, internalIP, kernelVer string) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			auditRes, err := a.agents.GetKernelAudit(cctx, nodeName)
			if err != nil {
				results[idx] = model.NodeKernelAudit{
					NodeName:      nodeName,
					InternalIP:    internalIP,
					KernelVersion: kernelVer,
					Score:         0,
					Status:        "unreachable",
					Error:         err.Error(),
					CollectedAt:   time.Now(),
					Findings: []model.AuditFinding{
						{
							ID:          "agent-unreachable-" + nodeName,
							NodeName:    nodeName,
							Severity:    "critical",
							Category:    "conntrack",
							Title:       "节点 Agent 不可达，无法采集内核网络状态",
							Detail:      fmt.Sprintf("请求节点 %s (%s) Agent /api/dataplane/kernel-audit 失败: %v", nodeName, internalIP, err),
							Remediation: "检查该节点 calico-netdiag-agent DaemonSet Pod 状态及日志: kubectl -n calico-netdiag get pods -o wide",
						},
					},
				}
				return
			}
			if auditRes.InternalIP == "" {
				auditRes.InternalIP = internalIP
			}
			if auditRes.KernelVersion == "" {
				auditRes.KernelVersion = kernelVer
			}
			results[idx] = *auditRes
		}(i, n.Name, k8s.NodeInternalIP(n), n.Status.NodeInfo.KernelVersion)
	}
	wg.Wait()

	// Build IP -> K8s Node / Pod lookup maps for L2 ARP Neighbor & BGP Peer enrichment
	nodeIPToName := make(map[string]string, len(nodes))
	for _, n := range nodes {
		if ip := k8s.NodeInternalIP(n); ip != "" {
			nodeIPToName[ip] = n.Name
		}
	}
	podIPToName := make(map[string]string)
	for _, p := range a.k8sStore.ListPods("") {
		if p.IP != "" && !p.HostNetwork {
			podIPToName[p.IP] = p.Namespace + "/" + p.Name
		}
	}

	// Enrich L2 ARP neighbors with K8s peer identities and detect failed cluster-node ARP
	for i := range results {
		n := &results[i]
		for j := range n.L2.Neighbors {
			nb := &n.L2.Neighbors[j]
			if peerNode, ok := nodeIPToName[nb.IP]; ok {
				nb.Role = "cluster-node"
				nb.PeerName = peerNode
				if nb.State == "FAILED" || nb.State == "INCOMPLETE" {
					n.Findings = append(n.Findings, model.AuditFinding{
						ID:             fmt.Sprintf("%s-node-arp-fail-%s", n.NodeName, peerNode),
						NodeName:       n.NodeName,
						Severity:       "critical",
						Category:       "l2",
						Title:          fmt.Sprintf("对端集群节点 %s (%s) 二层 ARP 解析失败 (%s)", peerNode, nb.IP, nb.State),
						Detail:         fmt.Sprintf("节点 %s 无法在二层解析同网段集群节点 %s 的 MAC 地址，跨节点 BGP / Pod 流量可能中断。", n.NodeName, peerNode),
						KernelEvidence: fmt.Sprintf("ip neigh: %s dev %s lladdr %s %s", nb.IP, nb.Dev, nb.MAC, nb.State),
						Remediation:    fmt.Sprintf("在节点 %s 上排查物理交换机/VLAN/安全组: arping -I %s %s", n.NodeName, nb.Dev, nb.IP),
					})
					if n.Status != "critical" {
						n.Status = "critical"
					}
					n.Score -= 20
					if n.Score < 0 {
						n.Score = 0
					}
				}
			} else if podName, ok := podIPToName[nb.IP]; ok {
				if nb.Role == "" || nb.Role == "other" {
					nb.Role = "pod-veth"
				}
				nb.PeerName = podName
			} else if nb.Role == "" {
				nb.Role = "other"
			}
		}
		sort.Slice(n.L2.Neighbors, func(aIdx, bIdx int) bool {
			ra, rb := arpEntrySortRank(n.L2.Neighbors[aIdx]), arpEntrySortRank(n.L2.Neighbors[bIdx])
			if ra != rb {
				return ra < rb
			}
			return n.L2.Neighbors[aIdx].IP < n.L2.Neighbors[bIdx].IP
		})
	}

	report := AggregateKernelAudit(encap, results)

	// Populate Calico NetworkModeProfile & BGPNeighbors so UI shows network architecture first
	_, inst, felix, bgpCfg, rawPeers, kpMode, _, _, _, _, _ := a.k8sStore.GetClusterMetadata()
	ipam := a.k8sStore.GetIPAMSnapshot()
	snaps := a.flows.ListNodeSnapshots()

	detectedKP := kpMode
	var bgpNeighbors []model.BGPPeerSummary
	for _, n := range nodes {
		if sn, ok := snaps[n.Name]; ok {
			if (detectedKP == model.KubeProxyUnknown || detectedKP == "") && sn.KubeProxyMode != "" {
				detectedKP = sn.KubeProxyMode
			}
			for _, bp := range sn.BGPProtocols {
				if !strings.EqualFold(bp.Proto, "BGP") {
					continue
				}
				scope := "Global"
				if strings.HasPrefix(bp.Name, "Mesh_") {
					scope = "Mesh"
				} else if strings.HasPrefix(bp.Name, "Node_") {
					scope = "Node"
				}
				state := bp.Info
				if state == "" {
					state = bp.State
				}
				bgpNeighbors = append(bgpNeighbors, model.BGPPeerSummary{
					Name:       bp.Name,
					SourceNode: n.Name,
					PeerIP:     bp.PeerIP,
					PeerNode:   nodeIPToName[bp.PeerIP],
					ASNumber:   bgpCfg.ASNumber,
					Scope:      scope,
					State:      state,
					Since:      bp.Since,
				})
			}
		}
	}
	for _, rp := range rawPeers {
		bgpNeighbors = append(bgpNeighbors, model.BGPPeerSummary{
			Name:       rp.Name,
			SourceNode: rp.Node,
			PeerIP:     rp.PeerIP,
			ASNumber:   rp.ASNumber,
			Scope:      "CRD Peer",
			State:      "Configured",
		})
	}
	report.BGPNeighbors = bgpNeighbors

	linuxDP := inst.LinuxDataplane
	if felix.BPFEnabled {
		linuxDP = "eBPF"
	} else if strings.EqualFold(felix.NFTablesMode, "Enabled") {
		linuxDP = "NFTables"
	}

	minPhyMTU := 0
	for _, n := range report.Nodes {
		if n.MTU.PrimaryMTU > 0 && (minPhyMTU == 0 || n.MTU.PrimaryMTU < minPhyMTU) {
			minPhyMTU = n.MTU.PrimaryMTU
		}
	}
	if minPhyMTU == 0 {
		minPhyMTU = 1500
	}

	overhead := 0
	modeSummary := ""
	switch encap {
	case model.EncapIPIPAlways:
		overhead = 20
		modeSummary = "IPIP 全隧道封装：跨节点报文外层增加 20 字节 IPv4 头部（经 tunl0 接口），Pod MTU 必须 ≤ 物理网卡 MTU - 20B，否则触发大包分片或黑洞。"
	case model.EncapIPIPCrossSubnet:
		overhead = 20
		modeSummary = "IPIP CrossSubnet 混合模式：同子网节点直连 BGP 路由，跨子网节点自动走 IPIP 隧道（+20B 包头），Pod MTU 需按最窄路径 ≤ 物理 MTU - 20B 对齐。"
	case model.EncapVXLANAlways:
		overhead = 50
		modeSummary = "VXLAN 全叠加隧道：跨节点报文封装 UDP/4789 + VXLAN + 内层以太帧（+50B 包头，经 vxlan.calico），Pod MTU 必须 ≤ 物理网卡 MTU - 50B。"
	case model.EncapVXLANCrossSubnet:
		overhead = 50
		modeSummary = "VXLAN CrossSubnet 混合模式：同子网直连，跨子网走 VXLAN 封装（+50B 包头），Pod MTU 建议 ≤ 物理网卡 MTU - 50B。"
	default:
		overhead = 0
		modeSummary = "BGP 纯三层直连模式：节点间通过 BIRD BGP 交换路由，直接经物理网卡原生转发 Pod IP 报文，零隧道封装开销（0B），Pod MTU 可与物理网卡 MTU 保持 1:1 一致。"
	}

	natPools := 0
	for _, p := range ipam.Pools {
		if p.NATOutgoing {
			natPools++
		}
	}

	report.NetworkMode = model.NetworkModeProfile{
		EncapMode:          encap,
		Dataplane:          linuxDP,
		KubeProxyMode:      detectedKP,
		BGPEnabled:         inst.BGPEnabled,
		BGPASNumber:        bgpCfg.ASNumber,
		NodeToNodeMesh:     bgpCfg.NodeToNodeMesh,
		ConfiguredMTU:      inst.MTU,
		PhysicalMTU:        minPhyMTU,
		EncapOverheadBytes: overhead,
		RecommendedPodMTU:  minPhyMTU - overhead,
		Summary:            modeSummary,
		NATOutgoingPools:   natPools,
		TotalPools:         len(ipam.Pools),
	}

	return report
}

func arpEntrySortRank(e model.ARPEntry) int {
	if e.State == "FAILED" || e.State == "INCOMPLETE" {
		return -1
	}
	switch e.Role {
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

// AggregateKernelAudit computes cluster summary metrics and cross-node consistency findings.
func AggregateKernelAudit(encap model.CalicoEncapMode, nodes []model.NodeKernelAudit) *model.ClusterKernelAuditReport {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].NodeName < nodes[j].NodeName
	})

	report := &model.ClusterKernelAuditReport{
		GeneratedAt:   time.Now(),
		OverallStatus: "healthy",
		EncapMode:     encap,
		Nodes:         nodes,
		Findings:      make([]model.AuditFinding, 0),
	}

	var scoreSum int
	primaryMTUs := make(map[int][]string)
	tunnelMTUs := make(map[int][]string)
	rpFilterVals := make(map[string][]string)

	for i := range nodes {
		n := &nodes[i]
		scoreSum += n.Score
		for _, f := range n.Findings {
			if f.NodeName == "" {
				f.NodeName = n.NodeName
			}
			report.Findings = append(report.Findings, f)
		}
		if n.Status == "unreachable" {
			continue
		}
		if n.MTU.PrimaryMTU > 0 {
			primaryMTUs[n.MTU.PrimaryMTU] = append(primaryMTUs[n.MTU.PrimaryMTU], n.NodeName)
		}
		if n.MTU.TunnelMTU > 0 {
			tunnelMTUs[n.MTU.TunnelMTU] = append(tunnelMTUs[n.MTU.TunnelMTU], n.NodeName)
		}
		for _, sc := range n.Sysctls {
			if sc.Key == "net.ipv4.conf.all.rp_filter" {
				rpFilterVals[sc.Value] = append(rpFilterVals[sc.Value], n.NodeName)
			}
		}
	}

	// Cross-node consistency check 1: Physical NIC MTU mismatch across nodes
	if len(primaryMTUs) > 1 {
		var parts []string
		for mtu, ns := range primaryMTUs {
			parts = append(parts, fmt.Sprintf("MTU=%d (%s)", mtu, strings.Join(ns, ",")))
		}
		sort.Strings(parts)
		report.Findings = append(report.Findings, model.AuditFinding{
			ID:          "cluster-primary-mtu-drift",
			Severity:    "critical",
			Category:    "mtu",
			Title:       "集群节点物理网卡 MTU 配置不一致",
			Detail:      "检测到不同节点的物理主网卡 MTU 存在差异: " + strings.Join(parts, " vs ") + "。跨节点传输大包（如 TLS 握手、大响应体）时极易触发静默 PMTU 黑洞。",
			Remediation: "统一全集群节点物理网卡 MTU 并确保 Calico FelixConfiguration / IPPool MTU 与最低物理 MTU 对齐。",
		})
	}

	// Cross-node consistency check 2: Tunnel MTU mismatch across nodes
	if len(tunnelMTUs) > 1 {
		var parts []string
		for mtu, ns := range tunnelMTUs {
			parts = append(parts, fmt.Sprintf("TunnelMTU=%d (%s)", mtu, strings.Join(ns, ",")))
		}
		sort.Strings(parts)
		report.Findings = append(report.Findings, model.AuditFinding{
			ID:          "cluster-tunnel-mtu-drift",
			Severity:    "warning",
			Category:    "mtu",
			Title:       "集群节点 Calico 隧道接口 MTU 不一致",
			Detail:      "不同节点的 Calico 隧道接口 MTU 存在偏差: " + strings.Join(parts, " vs ") + "。",
			Remediation: "检查各节点 calico-node 的 FELIX_IPINIPMTU / FELIX_VXLANMTU 或 Calico Installation.spec.calicoNetwork.mtu 配置。",
		})
	}

	// Cross-node consistency check 3: rp_filter mismatch across nodes
	if len(rpFilterVals) > 1 {
		var parts []string
		for val, ns := range rpFilterVals {
			parts = append(parts, fmt.Sprintf("rp_filter=%s (%s)", val, strings.Join(ns, ",")))
		}
		sort.Strings(parts)
		report.Findings = append(report.Findings, model.AuditFinding{
			ID:          "cluster-rpfilter-drift",
			Severity:    "warning",
			Category:    "sysctl",
			Title:       "集群节点 rp_filter 内核参数不一致",
			Detail:      "节点间 net.ipv4.conf.all.rp_filter 配置不统一: " + strings.Join(parts, " vs ") + "。在非对称路由或 NodePort 回程场景下会导致部分节点丢包。",
			Remediation: "建议全集群节点统一配置 sysctl -w net.ipv4.conf.all.rp_filter=2 (宽松模式) 或 0，并固化至 /etc/sysctl.d/99-calico.conf。",
		})
	}

	sortFindings(report.Findings)

	for _, f := range report.Findings {
		switch f.Severity {
		case "critical":
			report.CriticalCount++
		case "warning":
			report.WarningCount++
		}
	}

	if len(nodes) > 0 {
		report.AverageScore = scoreSum / len(nodes)
	}
	if report.CriticalCount > 0 {
		report.OverallStatus = "critical"
	} else if report.WarningCount > 0 {
		report.OverallStatus = "warning"
	}
	return report
}

// NodeRouteInput holds the raw data collected for a single node prior to route diff comparison.
type NodeRouteInput struct {
	NodeName      string
	InternalIP    string
	PodCIDR       string
	Ready         bool
	Unreachable   bool
	Error         string
	Routes        []model.HostRouteEntry
	LocalVeths    []string
	BGPPeersUp    int
	BGPPeersTotal int
}

// RunRouteAudit collects live kernel routing tables from every node and diffs them against
// Expected Calico routes (IPAMBlock affinities + local Running Pods + cross-node symmetry).
func (a *Auditor) RunRouteAudit(ctx context.Context) *model.ClusterRouteAuditReport {
	nodes := a.k8sStore.ListNodes()
	pods := a.k8sStore.ListPods("")
	ipam := a.k8sStore.GetIPAMSnapshot()
	snaps := a.flows.ListNodeSnapshots()
	bgpEvents := a.flows.BGPEvents()
	encap := a.k8sStore.PrimaryEncapMode()

	inputs := make([]NodeRouteInput, len(nodes))
	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Add(1)
		go func(idx int, nodeName, internalIP, podCIDR string, ready bool) {
			defer wg.Done()
			in := NodeRouteInput{
				NodeName:   nodeName,
				InternalIP: internalIP,
				PodCIDR:    podCIDR,
				Ready:      ready,
			}
			if sn, ok := snaps[nodeName]; ok {
				for _, bp := range sn.BGPProtocols {
					if !strings.EqualFold(bp.Proto, "BGP") {
						continue
					}
					in.BGPPeersTotal++
					state := strings.ToLower(bp.Info + " " + bp.State)
					if strings.Contains(state, "established") || strings.Contains(state, "up") {
						in.BGPPeersUp++
					}
				}
			}

			cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			resp, err := a.agents.GetHostRoutes(cctx, nodeName)
			if err != nil {
				in.Unreachable = true
				in.Error = err.Error()
			} else {
				in.Routes = resp.Routes
				in.LocalVeths = resp.LocalVeths
			}
			inputs[idx] = in
		}(i, n.Name, k8s.NodeInternalIP(n), n.Spec.PodCIDR, k8s.IsNodeReady(n))
	}
	wg.Wait()

	return CompareClusterRoutes(encap, inputs, pods, ipam, bgpEvents)
}

// CompareClusterRoutes is the pure, unit-testable Expected vs Actual Route Diff engine.
func CompareClusterRoutes(
	encap model.CalicoEncapMode,
	inputs []NodeRouteInput,
	pods []model.PodBrief,
	ipam model.IPAMSnapshot,
	bgpEvents []model.BGPEvent,
) *model.ClusterRouteAuditReport {
	sort.Slice(inputs, func(i, j int) bool {
		return inputs[i].NodeName < inputs[j].NodeName
	})

	report := &model.ClusterRouteAuditReport{
		GeneratedAt:   time.Now(),
		OverallStatus: "healthy",
		EncapMode:     encap,
		Nodes:         make([]model.NodeRouteAudit, 0, len(inputs)),
		Drifts:        make([]model.RouteDriftItem, 0),
		BGPEvents:     bgpEvents,
	}

	poolMap := make(map[string]model.IPPoolSummary, len(ipam.Pools))
	for _, p := range ipam.Pools {
		poolMap[p.Name] = p
	}

	nodeMap := make(map[string]NodeRouteInput, len(inputs))
	for _, in := range inputs {
		nodeMap[in.NodeName] = in
	}

	// Map IPAM blocks by affinity node
	blocksByNode := make(map[string][]model.IPAMBlockDetail)
	localBlockSet := make(map[string]map[string]bool) // nodeName -> cidr -> true
	ipamKnownPodIPs := make(map[string]bool)          // any allocated pod/tunnel IP in IPAM
	for _, b := range ipam.Blocks {
		if pool, ok := poolMap[b.PoolName]; ok && pool.Disabled {
			continue
		}
		for _, slot := range b.Slots {
			if slot.Status == model.IPStatusPod || slot.Status == model.IPStatusBorrowed || slot.Status == model.IPStatusTunnel {
				ipamKnownPodIPs[slot.IP] = true
			}
		}
		if b.AffinityNode == "" {
			continue
		}
		if b.AffinityState == "pending" && b.AllocatedSlots == 0 {
			continue
		}
		normCIDR := normalizeCIDR(b.CIDR)
		blocksByNode[b.AffinityNode] = append(blocksByNode[b.AffinityNode], b)
		if localBlockSet[b.AffinityNode] == nil {
			localBlockSet[b.AffinityNode] = make(map[string]bool)
		}
		localBlockSet[b.AffinityNode][normCIDR] = true
	}

	// Group local Pods per node
	runningPodsByNode := make(map[string][]model.PodBrief)
	allActivePodIPsByNode := make(map[string]map[string]bool)
	for _, p := range pods {
		if p.HostNetwork || p.IP == "" || p.NodeName == "" {
			continue
		}
		if p.Phase == "Succeeded" || p.Phase == "Failed" {
			continue
		}
		if allActivePodIPsByNode[p.NodeName] == nil {
			allActivePodIPsByNode[p.NodeName] = make(map[string]bool)
		}
		allActivePodIPsByNode[p.NodeName][p.IP] = true
		if p.Phase == "Running" {
			runningPodsByNode[p.NodeName] = append(runningPodsByNode[p.NodeName], p)
		}
	}

	// Track nodeA -> nodeB observed transport path for asymmetric routing detection
	type pathObs struct {
		cidr string
		mode string // direct, ipip, vxlan, other
		desc string
	}
	pairPaths := make(map[string]map[string]pathObs) // srcNode -> dstNode -> pathObs

	for _, in := range inputs {
		na := model.NodeRouteAudit{
			NodeName:         in.NodeName,
			InternalIP:       in.InternalIP,
			Status:           "healthy",
			Error:            in.Error,
			TotalRoutes:      len(in.Routes),
			BGPPeersUp:       in.BGPPeersUp,
			BGPPeersTotal:    in.BGPPeersTotal,
			Routes:           in.Routes,
			ExpectedLocalPod: len(runningPodsByNode[in.NodeName]),
		}

		if in.Unreachable {
			na.Status = "unreachable"
			drift := model.RouteDriftItem{
				NodeName:    in.NodeName,
				Kind:        "missing_block",
				Severity:    "critical",
				CIDR:        "-",
				Expected:    "Agent 可达并返回主路由表",
				Actual:      "unreachable: " + in.Error,
				Detail:      fmt.Sprintf("无法拉取节点 %s 的 Linux 内核路由表，跳过该节点细粒度 diff。", in.NodeName),
				Remediation: "检查节点 calico-netdiag-agent Pod 是否运行正常。",
			}
			na.Drifts = append(na.Drifts, drift)
			na.DriftCount = len(na.Drifts)
			report.Drifts = append(report.Drifts, drift)
			report.Nodes = append(report.Nodes, na)
			continue
		}

		// Index actual routes on this node
		unicastByDst := make(map[string][]model.HostRouteEntry)
		blackholeByDst := make(map[string]model.HostRouteEntry)
		localPodRouteByIP := make(map[string]model.HostRouteEntry)

		for _, rt := range in.Routes {
			dst := normalizeCIDR(rt.Dst)
			if rt.Protocol == "bird" {
				na.BIRDRoutes++
			}
			if rt.Type == "blackhole" {
				na.BlackholeRoutes++
				blackholeByDst[dst] = rt
				continue
			}
			if rt.Type == "unicast" {
				unicastByDst[dst] = append(unicastByDst[dst], rt)
				if strings.HasSuffix(dst, "/32") && strings.HasPrefix(rt.Dev, "cali") {
					na.LocalPodRoutes++
					podIP := strings.TrimSuffix(dst, "/32")
					localPodRouteByIP[podIP] = rt
				}
			}
		}

		// Check 1: Local IPAMBlock blackhole routes (BGP / IPIP modes)
		for _, blk := range blocksByNode[in.NodeName] {
			normCIDR := normalizeCIDR(blk.CIDR)
			pool := poolMap[blk.PoolName]
			if isVXLANMode(pool.EncapMode) || (pool.EncapMode == "" && isVXLANMode(encap)) {
				continue
			}
			if _, ok := blackholeByDst[normCIDR]; !ok {
				na.Drifts = append(na.Drifts, model.RouteDriftItem{
					NodeName:    in.NodeName,
					Kind:        "missing_blackhole",
					Severity:    "warning",
					CIDR:        normCIDR,
					Expected:    fmt.Sprintf("blackhole %s proto bird", normCIDR),
					Actual:      "缺失",
					Detail:      fmt.Sprintf("本节点归属的 Calico IPAMBlock %s 缺少防环路 blackhole 聚合路由，访问该网段内未分配 IP 时可能触发发夹路由环路。", normCIDR),
					Remediation: fmt.Sprintf("检查节点 %s 上 calico-node BIRD 进程与 Felix 状态: kubectl -n calico-system logs -l k8s-app=calico-node --tail=100", in.NodeName),
				})
			}
		}

		// Check 2: Unexpected blackhole routes not matching any local IPAMBlock or PodCIDR
		for bhCIDR := range blackholeByDst {
			if localBlockSet[in.NodeName][bhCIDR] {
				continue
			}
			if in.PodCIDR != "" && normalizeCIDR(in.PodCIDR) == bhCIDR {
				continue
			}
			na.Drifts = append(na.Drifts, model.RouteDriftItem{
				NodeName:    in.NodeName,
				Kind:        "unexpected_blackhole",
				Severity:    "warning",
				CIDR:        bhCIDR,
				Expected:    "仅对本节点归属 IPAMBlock 存在 blackhole 路由",
				Actual:      fmt.Sprintf("blackhole %s", bhCIDR),
				Detail:      fmt.Sprintf("节点 %s 存在未知或残留的 blackhole 路由 %s（不属于该节点当前 IPAMBlock 归属），可能导致发往该网段的流量被内核直接丢弃！", in.NodeName, bhCIDR),
				Remediation: fmt.Sprintf("确认 IPAMBlock 归属后检查是否为残留路由: ip route show type blackhole (必要时排查 BIRD/calico-node 同步)"),
			})
		}

		// Check 3: Remote IPAMBlock routes (Expected from every other Ready node's blocks)
		if pairPaths[in.NodeName] == nil {
			pairPaths[in.NodeName] = make(map[string]pathObs)
		}
		for remoteNodeName, remoteBlocks := range blocksByNode {
			if remoteNodeName == in.NodeName {
				continue
			}
			remoteNode, exists := nodeMap[remoteNodeName]
			if !exists || !remoteNode.Ready {
				continue
			}
			for _, blk := range remoteBlocks {
				pool := poolMap[blk.PoolName]
				if pool.DisableBGPExport && !isVXLANMode(pool.EncapMode) {
					continue
				}
				na.ExpectedBlocks++
				normCIDR := normalizeCIDR(blk.CIDR)

				matched, ok := findMatchingRemoteRoute(normCIDR, remoteNode.InternalIP, unicastByDst, in.Routes)
				if !ok {
					na.Drifts = append(na.Drifts, model.RouteDriftItem{
						NodeName:    in.NodeName,
						Kind:        "missing_block",
						Severity:    "critical",
						CIDR:        normCIDR,
						Expected:    fmt.Sprintf("%s via %s (目标节点 %s)", normCIDR, remoteNode.InternalIP, remoteNodeName),
						Actual:      "路由缺失",
						Detail:      fmt.Sprintf("节点 %s 缺少通往对端节点 %s (%s) 归属网段 %s 的内核路由，从本节点访问该网段 Pod 将跨节点不通。", in.NodeName, remoteNodeName, remoteNode.InternalIP, normCIDR),
						Remediation: fmt.Sprintf("检查节点 %s 与 %s 之间的 BGP Peer 会话（TCP/179）或 VXLAN Felix 同步日志。", in.NodeName, remoteNodeName),
					})
					continue
				}

				// Record observed transport path to remoteNode for asymmetric routing analysis
				if _, seen := pairPaths[in.NodeName][remoteNodeName]; !seen {
					mode, desc := classifyRouteTransport(matched)
					pairPaths[in.NodeName][remoteNodeName] = pathObs{
						cidr: normCIDR,
						mode: mode,
						desc: desc,
					}
				}
			}
		}

		// Check 4: Missing local Running Pod /32 routes
		for _, pod := range runningPodsByNode[in.NodeName] {
			if _, ok := localPodRouteByIP[pod.IP]; !ok {
				// Also tolerate non-cali dev if exact /32 unicast exists (e.g. custom interface)
				if _, anyExact := unicastByDst[pod.IP+"/32"]; !anyExact {
					na.Drifts = append(na.Drifts, model.RouteDriftItem{
						NodeName:    in.NodeName,
						Kind:        "missing_pod",
						Severity:    "critical",
						CIDR:        pod.IP + "/32",
						Expected:    fmt.Sprintf("%s/32 dev cali* scope link", pod.IP),
						Actual:      "宿主机 /32 路由缺失",
						Detail:      fmt.Sprintf("运行中的本地 Pod %s/%s (%s) 在宿主机 %s 上缺少 /32 主机路由，外部进入本节点的回包/请求无法送达该 Pod。", pod.Namespace, pod.Name, pod.IP, in.NodeName),
						Remediation: fmt.Sprintf("检查节点 %s 上 Felix 是否同步异常，或重建受影响 Pod: kubectl -n %s delete pod %s", in.NodeName, pod.Namespace, pod.Name),
					})
				}
			}
		}

		// Check 5: Ghost local Pod /32 routes (dev cali* pointing to IP with no active Pod)
		activeSet := allActivePodIPsByNode[in.NodeName]
		for podIP, rt := range localPodRouteByIP {
			if activeSet[podIP] {
				continue
			}
			// Guard against short K8s store refresh race if IPAM still shows active allocation
			if ipamKnownPodIPs[podIP] {
				continue
			}
			na.Drifts = append(na.Drifts, model.RouteDriftItem{
				NodeName:    in.NodeName,
				Kind:        "ghost_pod_route",
				Severity:    "warning",
				CIDR:        podIP + "/32",
				Expected:    "无活跃 Pod 对应，路由应已清理",
				Actual:      fmt.Sprintf("%s/32 dev %s", podIP, rt.Dev),
				Detail:      fmt.Sprintf("节点 %s 残留幽灵 Pod 路由 %s/32 dev %s，但集群内无对应活跃 Pod 或 IPAM 分配记录；若该 IP 后续分配给其他节点 Pod，本节点将误路由到本地失效 veth 导致局部丢包！", in.NodeName, podIP, rt.Dev),
				Remediation: fmt.Sprintf("在节点 %s 上确认 veth %s 是否已失效，并清理残留路由: ip route del %s/32 dev %s", in.NodeName, rt.Dev, podIP, rt.Dev),
			})
		}

		// Summarize node status
		na.DriftCount = len(na.Drifts)
		for _, d := range na.Drifts {
			if d.Severity == "critical" {
				na.Status = "critical"
				break
			}
			if d.Severity == "warning" && na.Status == "healthy" {
				na.Status = "warning"
			}
		}
		report.Drifts = append(report.Drifts, na.Drifts...)
		report.Nodes = append(report.Nodes, na)
	}

	// Check 6: Cross-node Asymmetric Path detection (A->B vs B->A encap mismatch)
	for i := 0; i < len(inputs); i++ {
		for j := i + 1; j < len(inputs); j++ {
			nodeA := inputs[i].NodeName
			nodeB := inputs[j].NodeName
			pAB, okAB := pairPaths[nodeA][nodeB]
			pBA, okBA := pairPaths[nodeB][nodeA]
			if !okAB || !okBA {
				continue
			}
			if pAB.mode != pBA.mode && pAB.mode != "other" && pBA.mode != "other" {
				asym := model.AsymmetricPair{
					NodeA:    nodeA,
					NodeB:    nodeB,
					AToBCIDR: pAB.cidr,
					AToBPath: pAB.desc,
					BToACIDR: pBA.cidr,
					BToAPath: pBA.desc,
					Detail:   fmt.Sprintf("节点 %s → %s 走 %s (%s)，而反向 %s → %s 走 %s (%s)，去回程封装路径不对称，开启 rp_filter 或有状态防火墙时极易触发单向丢包。", nodeA, nodeB, pAB.mode, pAB.desc, nodeB, nodeA, pBA.mode, pBA.desc),
				}
				report.AsymmetricPairs = append(report.AsymmetricPairs, asym)
				drift := model.RouteDriftItem{
					NodeName:    nodeA + " ↔ " + nodeB,
					Kind:        "asymmetric_path",
					Severity:    "critical",
					CIDR:        pAB.cidr + " / " + pBA.cidr,
					Expected:    "双节点去回程封装模式一致 (Direct↔Direct 或 Tunnel↔Tunnel)",
					Actual:      fmt.Sprintf("%s vs %s", pAB.desc, pBA.desc),
					Detail:      asym.Detail,
					Remediation: "检查两节点子网掩码、Calico Node.spec.bgp.ipv4Address 及 IPPool ipipMode/vxlanMode (CrossSubnet) 是否一致。",
				}
				report.Drifts = append(report.Drifts, drift)
			}
		}
	}

	sortRouteDrifts(report.Drifts)

	report.TotalDrifts = len(report.Drifts)
	report.AsymmetricCount = len(report.AsymmetricPairs)
	for _, d := range report.Drifts {
		switch d.Kind {
		case "missing_block", "missing_pod", "missing_blackhole":
			report.MissingCount++
		case "ghost_pod_route":
			report.GhostPodCount++
		}
	}

	for _, d := range report.Drifts {
		if d.Severity == "critical" {
			report.OverallStatus = "critical"
			break
		}
		if d.Severity == "warning" && report.OverallStatus == "healthy" {
			report.OverallStatus = "warning"
		}
	}

	return report
}

func findMatchingRemoteRoute(
	blockCIDR string,
	remoteNodeIP string,
	unicastByDst map[string][]model.HostRouteEntry,
	allRoutes []model.HostRouteEntry,
) (model.HostRouteEntry, bool) {
	// 1. Exact CIDR match (standard Calico per-block route)
	if rts, ok := unicastByDst[blockCIDR]; ok && len(rts) > 0 {
		return rts[0], true
	}

	// 2. Check if a non-default supernet route (e.g., node PodCIDR aggregation) covers blockCIDR
	blockIP, blockNet, err := net.ParseCIDR(blockCIDR)
	if err != nil {
		return model.HostRouteEntry{}, false
	}
	blockOnes, _ := blockNet.Mask.Size()

	for _, rt := range allRoutes {
		if rt.Type != "unicast" || rt.Dst == "0.0.0.0/0" || rt.Dst == "" {
			continue
		}
		_, rtNet, err := net.ParseCIDR(rt.Dst)
		if err != nil {
			continue
		}
		rtOnes, _ := rtNet.Mask.Size()
		if rtOnes == 0 || rtOnes > blockOnes {
			continue
		}
		if rtNet.Contains(blockIP) {
			// Ensure it actually routes toward remote node or Calico tunnel
			if rt.Gateway == remoteNodeIP || rt.Dev == "tunl0" || rt.Dev == "vxlan.calico" || rt.Protocol == "bird" {
				return rt, true
			}
		}
	}
	return model.HostRouteEntry{}, false
}

func classifyRouteTransport(rt model.HostRouteEntry) (mode string, desc string) {
	dev := strings.ToLower(rt.Dev)
	switch {
	case strings.HasPrefix(dev, "tunl"):
		mode = "ipip"
	case strings.Contains(dev, "vxlan"):
		mode = "vxlan"
	case rt.Gateway != "" && rt.Dev != "":
		mode = "direct"
	default:
		mode = "other"
	}
	if rt.Gateway != "" {
		desc = fmt.Sprintf("via %s dev %s (%s)", rt.Gateway, rt.Dev, rt.Protocol)
	} else {
		desc = fmt.Sprintf("dev %s (%s)", rt.Dev, rt.Protocol)
	}
	return mode, desc
}

func normalizeCIDR(cidr string) string {
	cidr = strings.TrimSpace(cidr)
	if cidr == "" {
		return ""
	}
	if !strings.Contains(cidr, "/") {
		return cidr + "/32"
	}
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return cidr
	}
	return ipNet.String()
}

func isVXLANMode(mode model.CalicoEncapMode) bool {
	return mode == model.EncapVXLANAlways || mode == model.EncapVXLANCrossSubnet
}

func severityRank(s string) int {
	switch s {
	case "critical":
		return 0
	case "warning":
		return 1
	default:
		return 2
	}
}

func sortFindings(findings []model.AuditFinding) {
	sort.Slice(findings, func(i, j int) bool {
		ri, rj := severityRank(findings[i].Severity), severityRank(findings[j].Severity)
		if ri != rj {
			return ri < rj
		}
		if findings[i].NodeName != findings[j].NodeName {
			return findings[i].NodeName < findings[j].NodeName
		}
		return findings[i].ID < findings[j].ID
	})
}

func sortRouteDrifts(drifts []model.RouteDriftItem) {
	sort.Slice(drifts, func(i, j int) bool {
		ri, rj := severityRank(drifts[i].Severity), severityRank(drifts[j].Severity)
		if ri != rj {
			return ri < rj
		}
		if drifts[i].NodeName != drifts[j].NodeName {
			return drifts[i].NodeName < drifts[j].NodeName
		}
		return drifts[i].CIDR < drifts[j].CIDR
	})
}
