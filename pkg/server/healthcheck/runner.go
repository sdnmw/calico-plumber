package healthcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"calico-netdiag/pkg/k8s"
	"calico-netdiag/pkg/model"
	"calico-netdiag/pkg/server/agentclient"
	"calico-netdiag/pkg/server/flowstore"

	corev1 "k8s.io/api/core/v1"
)

// Runner orchestrates periodic and on-demand cluster-wide network health checks.
type Runner struct {
	namespace  string
	k8sStore   *k8s.Store
	flows      *flowstore.Store
	agents     *agentclient.Client
	httpClient *http.Client

	mu     sync.RWMutex
	latest *model.GlobalHealthReport
}

// NewRunner constructs a global network health check runner.
func NewRunner(namespace string, k8sStore *k8s.Store, flows *flowstore.Store, agents *agentclient.Client) *Runner {
	if namespace == "" {
		namespace = "calico-netdiag"
	}
	return &Runner{
		namespace:  namespace,
		k8sStore:   k8sStore,
		flows:      flows,
		agents:     agents,
		httpClient: &http.Client{Timeout: 8 * time.Second},
	}
}

// Start runs an initial check shortly after boot and schedules periodic checks.
func (r *Runner) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	go func() {
		// Short delay so probe pods & initial agent heartbeats settle
		select {
		case <-ctx.Done():
			return
		case <-time.After(4 * time.Second):
		}
		if _, err := r.RunNow(ctx); err != nil {
			log.Printf("[healthcheck] initial run warning: %v", err)
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := r.RunNow(ctx); err != nil {
					log.Printf("[healthcheck] periodic run warning: %v", err)
				}
			}
		}
	}()
}

// Latest returns the most recent cached GlobalHealthReport (running one if none cached).
func (r *Runner) Latest(ctx context.Context) (*model.GlobalHealthReport, error) {
	r.mu.RLock()
	rep := r.latest
	r.mu.RUnlock()
	if rep != nil {
		return rep, nil
	}
	return r.RunNow(ctx)
}

type probeTask struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Target    string `json:"target"`
	TimeoutMs int    `json:"timeoutMs"`
}

type probeResp struct {
	NodeName string `json:"nodeName"`
	PodName  string `json:"podName"`
	PodIP    string `json:"podIP"`
	Results  []struct {
		ID        string  `json:"id"`
		OK        bool    `json:"ok"`
		LatencyMs float64 `json:"latencyMs"`
		Detail    string  `json:"detail"`
		Error     string  `json:"error"`
	} `json:"results"`
}

// RunNow executes the full 5-dimension cluster network health check immediately.
func (r *Runner) RunNow(ctx context.Context) (*model.GlobalHealthReport, error) {
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	nodes := r.k8sStore.ListNodes()
	probePods := r.k8sStore.ListComponentPods(r.namespace, "app.kubernetes.io/component", "probe")

	// Map nodeName -> ready probe Pod
	probeByNode := make(map[string]*corev1.Pod)
	for _, p := range probePods {
		if p.Status.PodIP != "" && p.Spec.NodeName != "" && p.Status.Phase == corev1.PodRunning {
			probeByNode[p.Spec.NodeName] = p
		}
	}

	var (
		podMesh   model.PodMeshHealth
		podNode   model.PodNodeHealth
		podSvc    model.PodServiceHealth
		svcAudit  model.ServiceBackendSummary
		infraSum  model.InfraHealthSummary
		wg        sync.WaitGroup
	)

	// 1. Run Pod-Pod, Pod-Node, and Pod-SVC/DNS probes concurrently via each node's Probe Pod
	wg.Add(1)
	go func() {
		defer wg.Done()
		podMesh, podNode, podSvc = r.runProbeMatrix(cctx, nodes, probeByNode)
	}()

	// 2. Run Full Cluster Service & Backend Endpoint Audit
	wg.Add(1)
	go func() {
		defer wg.Done()
		svcAudit = r.auditServiceBackends(cctx, nodes)
	}()

	// 3. Run Calico Infrastructure Health Checks
	wg.Add(1)
	go func() {
		defer wg.Done()
		infraSum = r.auditInfrastructure(nodes)
	}()

	wg.Wait()

	score := computeOverallScore(podMesh, podNode, podSvc, svcAudit, infraSum)
	status := "healthy"
	if score < 70 {
		status = "critical"
	} else if score < 90 {
		status = "degraded"
	}

	report := &model.GlobalHealthReport{
		CheckedAt:       time.Now(),
		DurationMs:      time.Since(start).Milliseconds(),
		OverallScore:    score,
		OverallStatus:   status,
		PodToPod:        podMesh,
		PodToNode:       podNode,
		PodToService:    podSvc,
		ServiceBackends: svcAudit,
		Infrastructure:  infraSum,
	}

	r.mu.Lock()
	r.latest = report
	r.mu.Unlock()
	return report, nil
}

func (r *Runner) runProbeMatrix(ctx context.Context, nodes []*corev1.Node, probeByNode map[string]*corev1.Pod) (model.PodMeshHealth, model.PodNodeHealth, model.PodServiceHealth) {
	nodeNames := make([]string, 0, len(nodes))
	for _, n := range nodes {
		nodeNames = append(nodeNames, n.Name)
	}
	sort.Strings(nodeNames)

	mesh := model.PodMeshHealth{Nodes: nodeNames}
	pn := model.PodNodeHealth{}
	ps := model.PodServiceHealth{}

	// Find kube-dns & kubernetes ClusterIPs
	kubeAPIIP := ""
	if svc, _, ok := r.k8sStore.GetService("default", "kubernetes"); ok {
		kubeAPIIP = svc.Spec.ClusterIP
	}
	kubeDNSIP := ""
	if svc, _, ok := r.k8sStore.GetService("kube-system", "kube-dns"); ok {
		kubeDNSIP = svc.Spec.ClusterIP
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	var meshLatSum, pnLatSum, dnsLatSum float64
	var meshLatCnt, pnLatCnt, dnsLatCnt int

	for _, srcNode := range nodes {
		srcProbe := probeByNode[srcNode.Name]
		if srcProbe == nil {
			continue
		}

		// Build task batch for this source probe pod
		var tasks []probeTask
		for _, dstNode := range nodes {
			if dstProbe := probeByNode[dstNode.Name]; dstProbe != nil {
				tasks = append(tasks, probeTask{
					ID:        "p2p|" + dstNode.Name,
					Kind:      "http",
					Target:    fmt.Sprintf("http://%s:8099/ping", dstProbe.Status.PodIP),
					TimeoutMs: 2000,
				})
			}
			dstIP := k8s.NodeInternalIP(dstNode)
			if dstIP != "" {
				tasks = append(tasks, probeTask{
					ID:        "p2n|" + dstNode.Name,
					Kind:      "tcp",
					Target:    fmt.Sprintf("%s:9191", dstIP),
					TimeoutMs: 2000,
				})
			}
		}
		tasks = append(tasks, probeTask{
			ID:        "svc|dns",
			Kind:      "dns",
			Target:    "kubernetes.default.svc.cluster.local",
			TimeoutMs: 2000,
		})
		if kubeAPIIP != "" {
			tasks = append(tasks, probeTask{
				ID:        "svc|k8s-api",
				Kind:      "tcp",
				Target:    fmt.Sprintf("%s:443", kubeAPIIP),
				TimeoutMs: 2000,
			})
		}
		if kubeDNSIP != "" {
			tasks = append(tasks, probeTask{
				ID:        "svc|coredns-tcp",
				Kind:      "tcp",
				Target:    fmt.Sprintf("%s:53", kubeDNSIP),
				TimeoutMs: 2000,
			})
		}

		wg.Add(1)
		go func(sNode *corev1.Node, sProbe *corev1.Pod, tList []probeTask) {
			defer wg.Done()
			resp, err := r.callProbePod(ctx, sProbe.Status.PodIP, tList)
			if err != nil {
				return
			}
			resMap := make(map[string]struct {
				OK        bool
				LatencyMs float64
				Detail    string
				Error     string
			})
			for _, item := range resp.Results {
				resMap[item.ID] = struct {
					OK        bool
					LatencyMs float64
					Detail    string
					Error     string
				}{item.OK, item.LatencyMs, item.Detail, item.Error}
			}

			mu.Lock()
			defer mu.Unlock()

			for _, dNode := range nodes {
				if dProbe := probeByNode[dNode.Name]; dProbe != nil {
					rItem := resMap["p2p|"+dNode.Name]
					cell := model.PodMeshCell{
						SrcNode:   sNode.Name,
						SrcPod:    sProbe.Name,
						SrcIP:     sProbe.Status.PodIP,
						DstNode:   dNode.Name,
						DstPod:    dProbe.Name,
						DstIP:     dProbe.Status.PodIP,
						SameNode:  sNode.Name == dNode.Name,
						Reachable: rItem.OK,
						LatencyMs: rItem.LatencyMs,
						Error:     rItem.Error,
					}
					mesh.Cells = append(mesh.Cells, cell)
					mesh.TotalPairs++
					if cell.Reachable {
						mesh.HealthyPairs++
						meshLatSum += cell.LatencyMs
						meshLatCnt++
						if cell.LatencyMs > mesh.MaxLatencyMs {
							mesh.MaxLatencyMs = cell.LatencyMs
						}
					} else {
						mesh.FailedPairs++
					}
				}

				if dstIP := k8s.NodeInternalIP(dNode); dstIP != "" {
					rItem := resMap["p2n|"+dNode.Name]
					pn.Checks = append(pn.Checks, model.PodNodeCheckItem{
						SrcNode:    sNode.Name,
						SrcPod:     sProbe.Name,
						DstNode:    dNode.Name,
						DstNodeIP:  dstIP,
						TargetPort: 9191,
						Reachable:  rItem.OK,
						LatencyMs:  rItem.LatencyMs,
						Error:      rItem.Error,
					})
					pn.TotalChecks++
					if rItem.OK {
						pn.HealthyChecks++
						pnLatSum += rItem.LatencyMs
						pnLatCnt++
					}
				}
			}

			// DNS & core ClusterIP checks
			if dItem, ok := resMap["svc|dns"]; ok {
				ps.Checks = append(ps.Checks, model.PodServiceCheckItem{
					SrcNode:    sNode.Name,
					SrcPod:     sProbe.Name,
					TargetType: "dns",
					TargetName: "CoreDNS Resolution",
					TargetAddr: "kubernetes.default.svc.cluster.local",
					Reachable:  dItem.OK,
					LatencyMs:  dItem.LatencyMs,
					Detail:     dItem.Detail,
					Error:      dItem.Error,
				})
				ps.TotalChecks++
				if dItem.OK {
					ps.HealthyChecks++
					dnsLatSum += dItem.LatencyMs
					dnsLatCnt++
				}
			}
			if aItem, ok := resMap["svc|k8s-api"]; ok {
				ps.Checks = append(ps.Checks, model.PodServiceCheckItem{
					SrcNode:    sNode.Name,
					SrcPod:     sProbe.Name,
					TargetType: "clusterip",
					TargetName: "default/kubernetes",
					TargetAddr: kubeAPIIP + ":443",
					Reachable:  aItem.OK,
					LatencyMs:  aItem.LatencyMs,
					Detail:     aItem.Detail,
					Error:      aItem.Error,
				})
				ps.TotalChecks++
				if aItem.OK {
					ps.HealthyChecks++
				}
			}
			if cItem, ok := resMap["svc|coredns-tcp"]; ok {
				ps.Checks = append(ps.Checks, model.PodServiceCheckItem{
					SrcNode:    sNode.Name,
					SrcPod:     sProbe.Name,
					TargetType: "clusterip",
					TargetName: "kube-system/kube-dns",
					TargetAddr: kubeDNSIP + ":53",
					Reachable:  cItem.OK,
					LatencyMs:  cItem.LatencyMs,
					Detail:     cItem.Detail,
					Error:      cItem.Error,
				})
				ps.TotalChecks++
				if cItem.OK {
					ps.HealthyChecks++
				}
			}
		}(srcNode, srcProbe, tasks)
	}

	wg.Wait()

	if meshLatCnt > 0 {
		mesh.AvgLatencyMs = round2(meshLatSum / float64(meshLatCnt))
	}
	if pnLatCnt > 0 {
		pn.AvgLatencyMs = round2(pnLatSum / float64(pnLatCnt))
	}
	if dnsLatCnt > 0 {
		ps.DNSAvgMs = round2(dnsLatSum / float64(dnsLatCnt))
	}
	return mesh, pn, ps
}

func (r *Runner) callProbePod(ctx context.Context, podIP string, tasks []probeTask) (*probeResp, error) {
	b, _ := json.Marshal(map[string]interface{}{"tasks": tasks})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://%s:8099/probe", podIP), bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out probeResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Runner) auditServiceBackends(ctx context.Context, nodes []*corev1.Node) model.ServiceBackendSummary {
	svcs, epMap := r.k8sStore.ListAllServices()
	sum := model.ServiceBackendSummary{TotalServices: len(svcs)}

	// Pick a healthy worker/node agent to execute parallel TCP backend & ClusterIP checks
	agentNode := ""
	for _, n := range nodes {
		if k8s.IsNodeReady(n) {
			agentNode = n.Name
			if _, ok := n.Labels["node-role.kubernetes.io/control-plane"]; !ok {
				break
			}
		}
	}

	// Build batch TCP targets across ClusterIPs and backend Pod IP:TargetPorts
	var tcpTargets []agentclient.HostTCPTarget
	type svcBuild struct {
		detail        model.ServiceHealthDetail
		cipTargetID   string
		backendTarget []string
	}
	builds := make([]*svcBuild, 0, len(svcs))

	for _, svc := range svcs {
		key := svc.Namespace + "/" + svc.Name
		slices := epMap[key]

		ports := make([]string, 0, len(svc.Spec.Ports))
		var firstTCPPort int32
		for _, p := range svc.Spec.Ports {
			ports = append(ports, fmt.Sprintf("%d/%s", p.Port, p.Protocol))
			if firstTCPPort == 0 && (p.Protocol == "" || p.Protocol == corev1.ProtocolTCP) {
				firstTCPPort = p.Port
			}
		}

		sb := &svcBuild{
			detail: model.ServiceHealthDetail{
				Namespace: svc.Namespace,
				Name:      svc.Name,
				Type:      string(svc.Spec.Type),
				ClusterIP: svc.Spec.ClusterIP,
				Ports:     ports,
				Backends:  []model.BackendEndpointHealth{},
			},
		}

		if svc.Spec.ClusterIP != "" && svc.Spec.ClusterIP != "None" && firstTCPPort > 0 {
			tid := fmt.Sprintf("cip|%s", key)
			sb.cipTargetID = tid
			tcpTargets = append(tcpTargets, agentclient.HostTCPTarget{
				ID:      tid,
				Address: fmt.Sprintf("%s:%d", svc.Spec.ClusterIP, firstTCPPort),
			})
		}

		// Collect backend endpoints from EndpointSlices
		seenEP := make(map[string]bool)
		for _, sl := range slices {
			var epPort int32
			epProto := "TCP"
			for _, sp := range sl.Ports {
				if sp.Port != nil && (sp.Protocol == nil || *sp.Protocol == corev1.ProtocolTCP) {
					epPort = *sp.Port
					break
				}
			}
			if epPort == 0 && len(sl.Ports) > 0 && sl.Ports[0].Port != nil {
				epPort = *sl.Ports[0].Port
				if sl.Ports[0].Protocol != nil {
					epProto = string(*sl.Ports[0].Protocol)
				}
			}
			for _, ep := range sl.Endpoints {
				ready := ep.Conditions.Ready == nil || *ep.Conditions.Ready
				podNS, podName, nodeName := "", "", ""
				if ep.TargetRef != nil && ep.TargetRef.Kind == "Pod" {
					podNS = ep.TargetRef.Namespace
					podName = ep.TargetRef.Name
				}
				if ep.NodeName != nil {
					nodeName = *ep.NodeName
				}
				for _, addr := range ep.Addresses {
					ek := fmt.Sprintf("%s:%d", addr, epPort)
					if seenEP[ek] {
						continue
					}
					seenEP[ek] = true
					be := model.BackendEndpointHealth{
						PodNamespace: podNS,
						PodName:      podName,
						PodIP:        addr,
						NodeName:     nodeName,
						TargetPort:   int(epPort),
						Protocol:     epProto,
						K8sReady:     ready,
					}
					sb.detail.Backends = append(sb.detail.Backends, be)
					tid := ""
					if epProto == "TCP" && epPort > 0 && len(tcpTargets) < 400 {
						tid = fmt.Sprintf("ep|%s|%s", key, ek)
						tcpTargets = append(tcpTargets, agentclient.HostTCPTarget{
							ID:      tid,
							Address: ek,
						})
					}
					sb.backendTarget = append(sb.backendTarget, tid)
				}
			}
		}

		builds = append(builds, sb)
	}

	// Execute all TCP checks in a single fast parallel batch via the chosen Node Agent
	tcpResMap := make(map[string]agentclient.HostTCPResult)
	if agentNode != "" && len(tcpTargets) > 0 {
		if results, err := r.agents.HostTCPCheck(ctx, agentNode, 1000, tcpTargets); err == nil {
			for _, res := range results {
				tcpResMap[res.ID] = res
			}
		}
	}

	// Assemble final Service audit statuses & root-cause diagnoses
	for _, sb := range builds {
		d := &sb.detail
		d.TotalBackends = len(d.Backends)
		for i := range d.Backends {
			if d.Backends[i].K8sReady {
				d.ReadyBackends++
			}
			tid := sb.backendTarget[i]
			if tid == "" {
				// Non-TCP (e.g. UDP-only) service backend: trust K8s Ready state
				d.Backends[i].ProbeOK = d.Backends[i].K8sReady
				if d.Backends[i].ProbeOK {
					d.ReachableBackends++
				}
				continue
			}
			if tr, ok := tcpResMap[tid]; ok {
				d.Backends[i].ProbeOK = tr.OK
				d.Backends[i].LatencyMs = tr.LatencyMs
				d.Backends[i].Error = tr.Error
				if tr.OK {
					d.ReachableBackends++
				}
			}
		}

		if sb.cipTargetID != "" {
			if tr, ok := tcpResMap[sb.cipTargetID]; ok {
				okCopy := tr.OK
				d.ClusterIPReachable = &okCopy
				d.ClusterIPLatencyMs = tr.LatencyMs
				d.ClusterIPError = tr.Error
			}
		}

		switch {
		case d.ClusterIP == "None":
			if d.TotalBackends == 0 {
				d.Status = "no-endpoints"
				d.Diagnosis = "Headless Service 无关联后端 Endpoint"
				sum.NoEndpointSvcs++
			} else if d.ReachableBackends == d.TotalBackends {
				d.Status = "healthy"
				d.Diagnosis = fmt.Sprintf("Headless Service 全部 %d 个后端可达", d.TotalBackends)
				sum.HealthyServices++
			} else {
				d.Status = "degraded"
				d.Diagnosis = fmt.Sprintf("Headless Service 部分后端不可达 (%d/%d)", d.ReachableBackends, d.TotalBackends)
				sum.DegradedServices++
			}
		case d.TotalBackends == 0:
			d.Status = "no-endpoints"
			d.Diagnosis = "Selector 未匹配到任何后端 Pod（或 EndpointSlice 为空）"
			sum.NoEndpointSvcs++
		case d.ClusterIPReachable != nil && !*d.ClusterIPReachable && d.ReachableBackends > 0:
			d.Status = "broken"
			d.Diagnosis = "后端 Pod 直连正常，但 ClusterIP 不可达 — 疑似 kube-proxy (iptables/IPVS) 转发规则异常！"
			sum.BrokenServices++
		case d.ReachableBackends == 0:
			d.Status = "broken"
			d.Diagnosis = fmt.Sprintf("全部 %d 个后端 Pod 端口探测失败（目标应用未监听或策略阻断）", d.TotalBackends)
			sum.BrokenServices++
		case d.ReachableBackends < d.TotalBackends || d.ReadyBackends < d.TotalBackends:
			d.Status = "degraded"
			d.Diagnosis = fmt.Sprintf("部分后端异常：Ready %d/%d，TCP 可达 %d/%d", d.ReadyBackends, d.TotalBackends, d.ReachableBackends, d.TotalBackends)
			sum.DegradedServices++
		default:
			d.Status = "healthy"
			d.Diagnosis = fmt.Sprintf("ClusterIP 与全部 %d 个后端 Pod 均健康可达", d.TotalBackends)
			sum.HealthyServices++
		}

		sum.Services = append(sum.Services, *d)
	}

	// Sort broken/degraded services to the top for instant visibility
	order := map[string]int{"broken": 0, "degraded": 1, "no-endpoints": 2, "healthy": 3}
	sort.Slice(sum.Services, func(i, j int) bool {
		oi, oj := order[sum.Services[i].Status], order[sum.Services[j].Status]
		if oi != oj {
			return oi < oj
		}
		return sum.Services[i].Namespace+"/"+sum.Services[i].Name < sum.Services[j].Namespace+"/"+sum.Services[j].Name
	})

	return sum
}

func (r *Runner) auditInfrastructure(nodes []*corev1.Node) model.InfraHealthSummary {
	infra := model.InfraHealthSummary{MTUConsistent: true}

	// 1. Calico-node pods readiness
	calicoPods := r.k8sStore.ListPods("calico-system")
	for _, p := range calicoPods {
		if strings.HasPrefix(p.Name, "calico-node-") {
			infra.CalicoNodesTotal++
			if p.Ready {
				infra.CalicoNodesReady++
			}
		}
	}
	if infra.CalicoNodesTotal > 0 {
		st := "pass"
		if infra.CalicoNodesReady < infra.CalicoNodesTotal {
			st = "fail"
		}
		infra.Checks = append(infra.Checks, model.InfraCheckItem{
			Category: "Calico",
			Name:     "calico-node DaemonSet",
			Status:   st,
			Detail:   fmt.Sprintf("%d/%d calico-node pods Ready", infra.CalicoNodesReady, infra.CalicoNodesTotal),
		})
	}

	// 2. BIRD BGP peers & MTU consistency across node agent snapshots
	snaps := r.flows.ListNodeSnapshots()
	mtus := make(map[int]bool)
	for _, sn := range snaps {
		if sn.NICMTU > 0 {
			mtus[sn.NICMTU] = true
		}
		for _, bp := range sn.BGPProtocols {
			if strings.EqualFold(bp.Proto, "BGP") {
				infra.BGPTotal++
				if strings.Contains(strings.ToLower(bp.Info), "established") || strings.EqualFold(bp.State, "up") {
					infra.BGPEstablished++
				}
			}
		}
	}
	if infra.BGPTotal > 0 {
		st := "pass"
		if infra.BGPEstablished < infra.BGPTotal {
			st = "fail"
		}
		infra.Checks = append(infra.Checks, model.InfraCheckItem{
			Category: "BGP",
			Name:     "BIRD BGP Peer Sessions",
			Status:   st,
			Detail:   fmt.Sprintf("%d/%d BGP sessions Established", infra.BGPEstablished, infra.BGPTotal),
		})
	}
	if len(mtus) > 1 {
		infra.MTUConsistent = false
		infra.Checks = append(infra.Checks, model.InfraCheckItem{
			Category: "MTU",
			Name:     "Node Primary NIC MTU Consistency",
			Status:   "warn",
			Detail:   fmt.Sprintf("Detected inconsistent node NIC MTUs across cluster (%d distinct values)", len(mtus)),
		})
	} else {
		infra.Checks = append(infra.Checks, model.InfraCheckItem{
			Category: "MTU",
			Name:     "Node Primary NIC MTU Consistency",
			Status:   "pass",
			Detail:   "All reporting nodes share consistent primary NIC MTU",
		})
	}

	// 3. IPAM utilization, orphan & borrowed IPs
	ipam := r.k8sStore.GetIPAMSnapshot()
	for _, p := range ipam.Pools {
		if p.UtilizationPercent > infra.MaxPoolUsagePct {
			infra.MaxPoolUsagePct = p.UtilizationPercent
		}
		infra.OrphanIPCount += p.OrphanIPs
		infra.BorrowedIPCount += p.BorrowedIPs
	}
	poolSt := "pass"
	if infra.MaxPoolUsagePct >= 90 {
		poolSt = "fail"
	} else if infra.MaxPoolUsagePct >= 75 {
		poolSt = "warn"
	}
	infra.Checks = append(infra.Checks, model.InfraCheckItem{
		Category: "IPAM",
		Name:     "IPPool Capacity & Utilization",
		Status:   poolSt,
		Detail:   fmt.Sprintf("Peak pool utilization %.2f%%, Borrowed IPs: %d, Orphan IPs: %d", infra.MaxPoolUsagePct, infra.BorrowedIPCount, infra.OrphanIPCount),
	})

	// 4. Agent telemetry coverage
	onlineAgents := 0
	ebpfCnt := 0
	for _, n := range nodes {
		if sn, ok := snaps[n.Name]; ok && time.Since(sn.LastHeartbeat) < 30*time.Second {
			onlineAgents++
			if sn.Engine == model.TelemetryEBPF {
				ebpfCnt++
			}
		}
	}
	agSt := "pass"
	if onlineAgents < len(nodes) {
		agSt = "warn"
	}
	infra.Checks = append(infra.Checks, model.InfraCheckItem{
		Category: "Telemetry",
		Name:     "Diagnostic Agent Coverage",
		Status:   agSt,
		Detail:   fmt.Sprintf("%d/%d nodes reporting (%d eBPF, %d Legacy)", onlineAgents, len(nodes), ebpfCnt, onlineAgents-ebpfCnt),
	})

	return infra
}

func computeOverallScore(mesh model.PodMeshHealth, pn model.PodNodeHealth, ps model.PodServiceHealth, svc model.ServiceBackendSummary, infra model.InfraHealthSummary) int {
	score := 100.0
	if mesh.TotalPairs > 0 && mesh.FailedPairs > 0 {
		score -= 35.0 * float64(mesh.FailedPairs) / float64(mesh.TotalPairs)
	}
	if pn.TotalChecks > 0 && pn.HealthyChecks < pn.TotalChecks {
		score -= 20.0 * float64(pn.TotalChecks-pn.HealthyChecks) / float64(pn.TotalChecks)
	}
	if ps.TotalChecks > 0 && ps.HealthyChecks < ps.TotalChecks {
		score -= 20.0 * float64(ps.TotalChecks-ps.HealthyChecks) / float64(ps.TotalChecks)
	}
	if svc.TotalServices > 0 {
		bad := svc.BrokenServices*2 + svc.DegradedServices
		pen := 15.0 * float64(bad) / float64(svc.TotalServices*2)
		if pen > 15 {
			pen = 15
		}
		score -= pen
	}
	for _, c := range infra.Checks {
		if c.Status == "fail" {
			score -= 10
		} else if c.Status == "warn" {
			score -= 3
		}
	}
	if score < 0 {
		score = 0
	}
	return int(math.Round(score))
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
