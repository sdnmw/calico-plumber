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
	discoveryv1 "k8s.io/api/discovery/v1"
)

// Runner orchestrates periodic and on-demand cluster-wide network health checks.
type Runner struct {
	namespace  string
	k8sStore   *k8s.Store
	flows      *flowstore.Store
	agents     *agentclient.Client
	httpClient *http.Client

	mu       sync.RWMutex
	latest   *model.GlobalHealthReport
	onReport func(*model.GlobalHealthReport)
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

// SetOnReport registers a callback invoked whenever a new GlobalHealthReport completes.
func (r *Runner) SetOnReport(fn func(*model.GlobalHealthReport)) {
	r.mu.Lock()
	r.onReport = fn
	r.mu.Unlock()
}

// Start runs an initial check shortly after boot and schedules periodic checks.
func (r *Runner) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	go func() {
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
		podMesh  model.PodMeshHealth
		podNode  model.PodNodeHealth
		podSvc   model.PodServiceHealth
		svcAudit model.ServiceBackendSummary
		infraSum model.InfraHealthSummary
		wg       sync.WaitGroup
	)

	// 1. Run Pod-Pod, Pod-Node, and Pod-SVC/DNS probes concurrently via each node's Probe Pod
	wg.Add(1)
	go func() {
		defer wg.Done()
		podMesh, podNode, podSvc = r.runProbeMatrix(cctx, nodes, probeByNode)
	}()

	// 2. Run Full Cluster Per-Port Service & Backend Endpoint Audit (preferring Probe Pod source)
	wg.Add(1)
	go func() {
		defer wg.Done()
		svcAudit = r.auditServiceBackends(cctx, nodes, probeByNode)
	}()

	// 3. Run Calico Infrastructure Health Checks
	wg.Add(1)
	go func() {
		defer wg.Done()
		infraSum = r.auditInfrastructure(nodes)
	}()

	wg.Wait()

	score, status := computeOverallScoreAndStatus(podMesh, podNode, podSvc, svcAudit, infraSum)
	headline := buildSummaryHeadline(len(nodes), podMesh, svcAudit)

	report := &model.GlobalHealthReport{
		CheckedAt:       time.Now(),
		DurationMs:      time.Since(start).Milliseconds(),
		OverallScore:    score,
		OverallStatus:   status,
		SummaryHeadline: headline,
		PodToPod:        podMesh,
		PodToNode:       podNode,
		PodToService:    podSvc,
		ServiceBackends: svcAudit,
		Infrastructure:  infraSum,
	}

	r.mu.Lock()
	r.latest = report
	cb := r.onReport
	r.mu.Unlock()

	if cb != nil {
		cb(report)
	}
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
	var crossLatSum, allLatSum, pnLatSum, dnsLatSum float64
	var crossLatCnt, allLatCnt, pnLatCnt, dnsLatCnt int

	for _, srcNode := range nodes {
		srcProbe := probeByNode[srcNode.Name]
		if srcProbe == nil {
			continue
		}

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
					sameNode := sNode.Name == dNode.Name
					selfCheck := sameNode && sProbe.Status.PodIP == dProbe.Status.PodIP
					cell := model.PodMeshCell{
						SrcNode:   sNode.Name,
						SrcPod:    sProbe.Name,
						SrcIP:     sProbe.Status.PodIP,
						DstNode:   dNode.Name,
						DstPod:    dProbe.Name,
						DstIP:     dProbe.Status.PodIP,
						SameNode:  sameNode,
						SelfCheck: selfCheck,
						Reachable: rItem.OK,
						LatencyMs: rItem.LatencyMs,
						Error:     rItem.Error,
					}
					mesh.Cells = append(mesh.Cells, cell)
					mesh.TotalPairs++
					if cell.Reachable {
						mesh.HealthyPairs++
						allLatSum += cell.LatencyMs
						allLatCnt++
						if cell.LatencyMs > mesh.MaxLatencyMs {
							mesh.MaxLatencyMs = cell.LatencyMs
						}
					} else {
						mesh.FailedPairs++
					}
					if selfCheck {
						mesh.SelfCheckTotal++
						if cell.Reachable {
							mesh.SelfCheckHealthy++
						}
					} else {
						mesh.CrossNodeTotal++
						if cell.Reachable {
							mesh.CrossNodeHealthy++
							crossLatSum += cell.LatencyMs
							crossLatCnt++
						} else {
							mesh.CrossNodeFailed++
						}
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

	if crossLatCnt > 0 {
		mesh.AvgLatencyMs = round2(crossLatSum / float64(crossLatCnt))
	} else if allLatCnt > 0 {
		mesh.AvgLatencyMs = round2(allLatSum / float64(allLatCnt))
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

// callProbePodChunked runs large TCP target batches against a Probe Pod in safe chunks of 32 concurrent probes.
func (r *Runner) callProbePodChunked(ctx context.Context, podIP string, targets []agentclient.HostTCPTarget, timeoutMs int) (map[string]agentclient.HostTCPResult, error) {
	out := make(map[string]agentclient.HostTCPResult, len(targets))
	const chunkSize = 32
	for i := 0; i < len(targets); i += chunkSize {
		end := i + chunkSize
		if end > len(targets) {
			end = len(targets)
		}
		chunk := make([]probeTask, 0, end-i)
		for _, t := range targets[i:end] {
			chunk = append(chunk, probeTask{
				ID:        t.ID,
				Kind:      "tcp",
				Target:    t.Address,
				TimeoutMs: timeoutMs,
			})
		}
		resp, err := r.callProbePod(ctx, podIP, chunk)
		if err != nil {
			return out, err
		}
		for _, item := range resp.Results {
			out[item.ID] = agentclient.HostTCPResult{
				ID:        item.ID,
				OK:        item.OK,
				LatencyMs: item.LatencyMs,
				Error:     item.Error,
			}
		}
	}
	return out, nil
}

// ClassifyProbeError maps raw Go/Linux socket error strings into crisp operator-readable failure classes.
func ClassifyProbeError(errStr string) (kind string, label string) {
	lo := strings.ToLower(strings.TrimSpace(errStr))
	switch {
	case lo == "":
		return "", ""
	case strings.Contains(lo, "connection refused"):
		return "connection_refused", "连接被拒绝 (Connection Refused)"
	case strings.Contains(lo, "i/o timeout") || strings.Contains(lo, "deadline exceeded") || strings.Contains(lo, "timed out"):
		return "timeout", "等待超时 (Timeout)"
	case strings.Contains(lo, "no route to host") || strings.Contains(lo, "network is unreachable"):
		return "no_route", "路由/网络不可达"
	case strings.Contains(lo, "connection reset"):
		return "connection_reset", "连接被重置 (TCP RST)"
	case strings.Contains(lo, "no such host") || strings.Contains(lo, "dns"):
		return "dns_error", "DNS 解析失败"
	default:
		return "other", errStr
	}
}

func resolveEndpointSlicePort(sp corev1.ServicePort, svcPortCount int, sl *discoveryv1.EndpointSlice) (int, string) {
	proto := string(sp.Protocol)
	if proto == "" {
		proto = "TCP"
	}
	// 1. Exact match by port name + protocol
	if sp.Name != "" {
		for _, epPort := range sl.Ports {
			if epPort.Port == nil {
				continue
			}
			epProto := "TCP"
			if epPort.Protocol != nil {
				epProto = string(*epPort.Protocol)
			}
			if epPort.Name != nil && *epPort.Name == sp.Name && strings.EqualFold(epProto, proto) {
				return int(*epPort.Port), epProto
			}
		}
	}
	// 2. Single-port service matching single-port EndpointSlice
	if svcPortCount == 1 && len(sl.Ports) == 1 && sl.Ports[0].Port != nil {
		epProto := proto
		if sl.Ports[0].Protocol != nil {
			epProto = string(*sl.Ports[0].Protocol)
		}
		return int(*sl.Ports[0].Port), epProto
	}
	// 3. Match by numeric targetPort or servicePort
	targetNum := int(sp.TargetPort.IntVal)
	if targetNum <= 0 {
		targetNum = int(sp.Port)
	}
	for _, epPort := range sl.Ports {
		if epPort.Port == nil {
			continue
		}
		epProto := "TCP"
		if epPort.Protocol != nil {
			epProto = string(*epPort.Protocol)
		}
		if int(*epPort.Port) == targetNum && strings.EqualFold(epProto, proto) {
			return int(*epPort.Port), epProto
		}
	}
	// 4. Fallback to declared numeric targetPort or service port
	if targetNum > 0 {
		return targetNum, proto
	}
	return int(sp.Port), proto
}

func (r *Runner) auditServiceBackends(ctx context.Context, nodes []*corev1.Node, probeByNode map[string]*corev1.Pod) model.ServiceBackendSummary {
	svcs, epMap := r.k8sStore.ListAllServices()

	// Pick preferred Probe Pod (prefer worker node so Service traffic tests realistic worker path)
	var chosenProbe *corev1.Pod
	var fallbackNode string
	for _, n := range nodes {
		if !k8s.IsNodeReady(n) {
			continue
		}
		isCP := false
		if _, ok := n.Labels["node-role.kubernetes.io/control-plane"]; ok {
			isCP = true
		}
		if p := probeByNode[n.Name]; p != nil {
			if chosenProbe == nil || !isCP {
				chosenProbe = p
			}
		}
		if fallbackNode == "" || !isCP {
			fallbackNode = n.Name
		}
	}

	probeSourceLabel := ""
	probeSourcePod := ""
	probeSourceNode := ""
	if chosenProbe != nil {
		probeSourcePod = chosenProbe.Namespace + "/" + chosenProbe.Name
		probeSourceNode = chosenProbe.Spec.NodeName
		probeSourceLabel = fmt.Sprintf("探针 Pod %s (%s)", chosenProbe.Name, chosenProbe.Spec.NodeName)
	} else if fallbackNode != "" {
		probeSourceNode = fallbackNode
		probeSourceLabel = fmt.Sprintf("节点 Agent (%s hostNetns)", fallbackNode)
	} else {
		probeSourceLabel = "无可用探针来源"
	}

	return r.evaluateServicesPerPort(ctx, svcs, epMap, probeSourceLabel, probeSourcePod, probeSourceNode, func(targets []agentclient.HostTCPTarget) map[string]agentclient.HostTCPResult {
		if len(targets) == 0 {
			return map[string]agentclient.HostTCPResult{}
		}
		if chosenProbe != nil {
			res, err := r.callProbePodChunked(ctx, chosenProbe.Status.PodIP, targets, 1000)
			if err == nil && len(res) > 0 {
				return res
			}
		}
		out := make(map[string]agentclient.HostTCPResult, len(targets))
		if fallbackNode != "" {
			if results, err := r.agents.HostTCPCheck(ctx, fallbackNode, 1000, targets); err == nil {
				for _, res := range results {
					out[res.ID] = res
				}
			}
		}
		return out
	})
}

// evaluateServicesPerPort is separated from network transport for deterministic unit testing.
func (r *Runner) evaluateServicesPerPort(
	_ context.Context,
	svcs []*corev1.Service,
	epMap map[string][]*discoveryv1.EndpointSlice,
	probeSourceLabel, probeSourcePod, probeSourceNode string,
	runTCPBatch func([]agentclient.HostTCPTarget) map[string]agentclient.HostTCPResult,
) model.ServiceBackendSummary {
	const maxTCPTargetsBudget = 600
	sum := model.ServiceBackendSummary{TotalServices: len(svcs)}

	type portBuild struct {
		ph            model.ServicePortHealth
		cipTargetID   string
		backendTarget []string
		budgetCapped  bool
	}
	type svcBuild struct {
		detail model.ServiceHealthDetail
		ports  []*portBuild
	}

	var tcpTargets []agentclient.HostTCPTarget
	builds := make([]*svcBuild, 0, len(svcs))

	for _, svc := range svcs {
		key := svc.Namespace + "/" + svc.Name
		slices := epMap[key]
		portLabels := make([]string, 0, len(svc.Spec.Ports))
		for _, p := range svc.Spec.Ports {
			proto := string(p.Protocol)
			if proto == "" {
				proto = "TCP"
			}
			portLabels = append(portLabels, fmt.Sprintf("%d/%s", p.Port, proto))
		}

		sb := &svcBuild{
			detail: model.ServiceHealthDetail{
				Namespace:       svc.Namespace,
				Name:            svc.Name,
				Type:            string(svc.Spec.Type),
				ClusterIP:       svc.Spec.ClusterIP,
				Ports:           portLabels,
				Selector:        svc.Spec.Selector,
				ProbeSource:     probeSourceLabel,
				ProbeSourcePod:  probeSourcePod,
				ProbeSourceNode: probeSourceNode,
				PortChecks:      []model.ServicePortHealth{},
				Backends:        []model.BackendEndpointHealth{},
			},
		}

		// Ensure even a 0-port Service (e.g., ExternalName or special headless) gets represented
		specPorts := svc.Spec.Ports
		if len(specPorts) == 0 {
			specPorts = []corev1.ServicePort{{Port: 0, Protocol: corev1.ProtocolTCP}}
		}

		for pIdx, sp := range specPorts {
			proto := string(sp.Protocol)
			if proto == "" {
				proto = "TCP"
			}
			targetPortDecl := sp.TargetPort.String()
			if targetPortDecl == "" || targetPortDecl == "0" {
				if sp.Port > 0 {
					targetPortDecl = fmt.Sprintf("%d", sp.Port)
				} else {
					targetPortDecl = "—"
				}
			}

			pb := &portBuild{
				ph: model.ServicePortHealth{
					PortName:        sp.Name,
					ServicePort:     int(sp.Port),
					Protocol:        proto,
					TargetPort:      targetPortDecl,
					ProbeSource:     probeSourceLabel,
					ProbeSourcePod:  probeSourcePod,
					ProbeSourceNode: probeSourceNode,
					Backends:        []model.BackendEndpointHealth{},
				},
			}

			isTCP := strings.EqualFold(proto, "TCP") && sp.Port > 0
			isHeadless := svc.Spec.ClusterIP == "" || svc.Spec.ClusterIP == "None"

			// Collect backend endpoints for this specific Service port
			seenEP := make(map[string]bool)
			resolvedTargetPort := int(sp.TargetPort.IntVal)
			if resolvedTargetPort <= 0 {
				resolvedTargetPort = int(sp.Port)
			}
			for _, sl := range slices {
				epPort, epProto := resolveEndpointSlicePort(sp, len(svc.Spec.Ports), sl)
				if epPort > 0 {
					resolvedTargetPort = epPort
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
							ServicePort:  int(sp.Port),
							TargetPort:   epPort,
							Protocol:     epProto,
							K8sReady:     ready,
							ProbeSource:  probeSourceLabel,
							ProbeState:   "untested",
						}
						pb.ph.Backends = append(pb.ph.Backends, be)
						tid := ""
						if isTCP && epPort > 0 {
							if len(tcpTargets) < maxTCPTargetsBudget {
								tid = fmt.Sprintf("ep|%s|%d|%s", key, pIdx, ek)
								tcpTargets = append(tcpTargets, agentclient.HostTCPTarget{
									ID:      tid,
									Address: ek,
								})
							} else {
								pb.budgetCapped = true
							}
						}
						pb.backendTarget = append(pb.backendTarget, tid)
					}
				}
			}
			pb.ph.ResolvedTargetPort = resolvedTargetPort

			// Enqueue ClusterIP:ServicePort TCP check for every TCP port
			if isHeadless {
				pb.ph.ClusterIPState = "headless"
			} else if !isTCP {
				pb.ph.ClusterIPState = "untested"
			} else if len(pb.ph.Backends) == 0 {
				pb.ph.ClusterIPState = "untested"
			} else if len(tcpTargets) < maxTCPTargetsBudget {
				tid := fmt.Sprintf("cip|%s|%d|%d", key, pIdx, sp.Port)
				pb.cipTargetID = tid
				tcpTargets = append(tcpTargets, agentclient.HostTCPTarget{
					ID:      tid,
					Address: fmt.Sprintf("%s:%d", svc.Spec.ClusterIP, sp.Port),
				})
			} else {
				pb.ph.ClusterIPState = "untested"
				pb.budgetCapped = true
			}

			sb.ports = append(sb.ports, pb)
		}

		builds = append(builds, sb)
	}

	// Execute all TCP checks in parallel batch
	tcpResMap := runTCPBatch(tcpTargets)

	// Evaluate per-port and per-Service verdicts
	for _, sb := range builds {
		d := &sb.detail
		uniqueBackendSeen := make(map[string]bool)

		var portVerdicts []string
		hasBrokenPort := false
		hasDegradedPort := false
		hasHealthyPort := false
		allNoEndpoints := true
		allUntested := true
		firstCIPSet := false

		for _, pb := range sb.ports {
			ph := &pb.ph
			ph.TotalBackends = len(ph.Backends)
			failReasons := make([]string, 0)
			failReasonSet := make(map[string]bool)
			firstFailKind := ""

			for i := range ph.Backends {
				be := &ph.Backends[i]
				if be.K8sReady {
					ph.ReadyBackends++
				}
				tid := pb.backendTarget[i]
				if tid != "" {
					if tr, ok := tcpResMap[tid]; ok {
						be.ProbeState = "tested"
						be.ProbeOK = tr.OK
						be.LatencyMs = tr.LatencyMs
						be.Error = tr.Error
						ph.TestedBackends++
						if tr.OK {
							ph.ReachableBackends++
						} else {
							fk, fl := ClassifyProbeError(tr.Error)
							be.FailureKind = fk
							be.FailureLabel = fl
							if firstFailKind == "" {
								firstFailKind = fk
							}
							if fl != "" && !failReasonSet[fl] {
								failReasonSet[fl] = true
								failReasons = append(failReasons, fl)
							}
						}
					}
				}

				// Aggregate flat backends list (deduplicated by podIP:targetPort)
				bk := fmt.Sprintf("%s:%d/%s", be.PodIP, be.TargetPort, be.Protocol)
				if !uniqueBackendSeen[bk] {
					uniqueBackendSeen[bk] = true
					d.Backends = append(d.Backends, *be)
					d.TotalBackends++
					if be.K8sReady {
						d.ReadyBackends++
					}
					if be.ProbeOK {
						d.ReachableBackends++
					}
				}
			}

			// Evaluate ClusterIP result for this port
			if pb.cipTargetID != "" {
				if tr, ok := tcpResMap[pb.cipTargetID]; ok {
					ph.ClusterIPLatencyMs = tr.LatencyMs
					ph.ClusterIPError = tr.Error
					if tr.OK {
						ph.ClusterIPState = "ok"
					} else {
						ph.ClusterIPState = "failed"
						fk, fl := ClassifyProbeError(tr.Error)
						ph.ClusterIPFailure = fk
						if firstFailKind == "" {
							firstFailKind = fk
						}
						if fl != "" && !failReasonSet[fl] {
							failReasonSet[fl] = true
							failReasons = append(failReasons, fl)
						}
					}
					if !firstCIPSet {
						firstCIPSet = true
						okCopy := tr.OK
						d.ClusterIPReachable = &okCopy
						d.ClusterIPLatencyMs = tr.LatencyMs
						d.ClusterIPError = tr.Error
					} else if !tr.OK && d.ClusterIPReachable != nil && *d.ClusterIPReachable {
						// Surface failure if any port's ClusterIP failed
						okCopy := false
						d.ClusterIPReachable = &okCopy
						d.ClusterIPError = tr.Error
					}
				} else {
					ph.ClusterIPState = "untested"
				}
			}

			// Determine ProbeState & port-level status/diagnosis
			isTCP := strings.EqualFold(ph.Protocol, "TCP") && ph.ServicePort > 0
			if ph.TotalBackends == 0 {
				ph.ProbeState = "untested"
				ph.UntestedReason = "无后端 Endpoint，未发起连接探测"
				ph.Status = "no-endpoints"
				ph.FailureKind = "untested"
				ph.FailureSummary = "无后端（未测）"
				ph.Diagnosis = fmt.Sprintf("端口 %d/%s 无后端 Endpoint（Selector 未匹配或 Pod 未 Ready）", ph.ServicePort, ph.Protocol)
				ph.RecommendedAction = "inspect_empty_backends"
			} else if !isTCP {
				allNoEndpoints = false
				ph.ProbeState = "untested"
				ph.UntestedReason = fmt.Sprintf("%s 协议暂未执行主动拨测", ph.Protocol)
				ph.Status = "untested"
				ph.FailureKind = "untested"
				ph.FailureSummary = fmt.Sprintf("未测（%s 协议）", ph.Protocol)
				ph.Diagnosis = fmt.Sprintf("端口 %d/%s 存在 %d 个后端（%s 端口未测）", ph.ServicePort, ph.Protocol, ph.TotalBackends, ph.Protocol)
				ph.RecommendedAction = "trace_normal"
			} else if pb.budgetCapped && ph.TestedBackends == 0 && ph.ClusterIPState == "untested" {
				allNoEndpoints = false
				ph.ProbeState = "untested"
				ph.UntestedReason = "超出单次巡检配额，本端口未测"
				ph.Status = "untested"
				ph.FailureKind = "untested"
				ph.FailureSummary = "未测（超出配额）"
				ph.Diagnosis = fmt.Sprintf("端口 %d/%s 超出单次巡检配额，状态未知", ph.ServicePort, ph.Protocol)
				ph.RecommendedAction = "trace_normal"
			} else {
				allNoEndpoints = false
				allUntested = false
				ph.ProbeState = "tested"
				failText := strings.Join(failReasons, " / ")
				if failText == "" {
					failText = "正常"
				}

				switch {
				case ph.ClusterIPState == "failed" && ph.ReachableBackends > 0:
					ph.Status = "broken"
					ph.FailureKind = firstFailKind
					ph.FailureSummary = failText
					ph.Diagnosis = fmt.Sprintf("直连后端成功（%d/%d），但访问 ClusterIP:%d 失败（%s）— 疑似 kube-proxy 转发异常", ph.ReachableBackends, ph.TestedBackends, ph.ServicePort, failText)
					ph.RecommendedAction = "trace_service_dnat"
					hasBrokenPort = true
				case ph.TestedBackends > 0 && ph.ReachableBackends == 0:
					ph.Status = "broken"
					ph.FailureKind = firstFailKind
					ph.FailureSummary = failText
					ph.Diagnosis = fmt.Sprintf("本次探测的 %d 个后端（端口 %d/%s）均未连通：%s", ph.TestedBackends, ph.ResolvedTargetPort, ph.Protocol, failText)
					ph.RecommendedAction = "check_port_listening"
					hasBrokenPort = true
				case ph.ReachableBackends < ph.TestedBackends || ph.ReadyBackends < ph.TotalBackends:
					ph.Status = "degraded"
					ph.FailureKind = firstFailKind
					ph.FailureSummary = failText
					ph.Diagnosis = fmt.Sprintf("部分后端未连通：Ready %d/%d，实测可达 %d/%d（%s）", ph.ReadyBackends, ph.TotalBackends, ph.ReachableBackends, ph.TestedBackends, failText)
					ph.RecommendedAction = "check_port_listening"
					hasDegradedPort = true
				default:
					ph.Status = "healthy"
					ph.FailureSummary = "正常"
					if ph.ClusterIPState == "headless" {
						ph.Diagnosis = fmt.Sprintf("Headless 端口 %d/%s 全部 %d 个后端实测可达", ph.ServicePort, ph.Protocol, ph.ReachableBackends)
					} else {
						ph.Diagnosis = fmt.Sprintf("端口 %d/%s 的 ClusterIP 与全部 %d 个后端实测可达", ph.ServicePort, ph.Protocol, ph.ReachableBackends)
					}
					ph.RecommendedAction = "compare_policy_sources"
					hasHealthyPort = true
				}
			}

			d.PortChecks = append(d.PortChecks, *ph)
			portTag := fmt.Sprintf("%d/%s", ph.ServicePort, ph.Protocol)
			if ph.ServicePort == 0 {
				portTag = "无端口"
			}
			switch ph.Status {
			case "broken":
				portVerdicts = append(portVerdicts, fmt.Sprintf("%s 失败（%s）", portTag, ph.FailureSummary))
			case "degraded":
				portVerdicts = append(portVerdicts, fmt.Sprintf("%s 降级（%d/%d 可达）", portTag, ph.ReachableBackends, ph.TestedBackends))
			case "untested":
				portVerdicts = append(portVerdicts, fmt.Sprintf("%s 未测", portTag))
			case "healthy":
				portVerdicts = append(portVerdicts, fmt.Sprintf("%s 正常", portTag))
			}
		}

		// Service-level rollup
		switch {
		case allNoEndpoints || d.TotalBackends == 0:
			d.Status = "no-endpoints"
			d.Diagnosis = "Selector 未匹配到任何后端 Pod 或 EndpointSlice 为空"
			d.RecommendedAction = "inspect_empty_backends"
			sum.NoEndpointSvcs++
		case hasBrokenPort:
			d.Status = "broken"
			// Pick the highest-priority recommended action from broken port
			d.RecommendedAction = "check_port_listening"
			for _, pc := range d.PortChecks {
				if pc.Status == "broken" {
					d.RecommendedAction = pc.RecommendedAction
					break
				}
			}
			if len(d.PortChecks) == 1 {
				d.Diagnosis = d.PortChecks[0].Diagnosis
			} else {
				d.Diagnosis = strings.Join(portVerdicts, "；")
			}
			sum.BrokenServices++
		case hasDegradedPort:
			d.Status = "degraded"
			d.RecommendedAction = "check_port_listening"
			if len(d.PortChecks) == 1 {
				d.Diagnosis = d.PortChecks[0].Diagnosis
			} else {
				d.Diagnosis = strings.Join(portVerdicts, "；")
			}
			sum.DegradedServices++
		case allUntested && !hasHealthyPort:
			d.Status = "healthy"
			d.RecommendedAction = "trace_normal"
			d.Diagnosis = strings.Join(portVerdicts, "；")
			sum.HealthyServices++
		default:
			d.Status = "healthy"
			d.RecommendedAction = "compare_policy_sources"
			if len(d.PortChecks) == 1 {
				d.Diagnosis = d.PortChecks[0].Diagnosis
			} else {
				d.Diagnosis = strings.Join(portVerdicts, "；")
			}
			sum.HealthyServices++
		}

		sum.Services = append(sum.Services, *d)
	}

	// Sort broken/degraded services to the top
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

func buildSummaryHeadline(nodeCount int, mesh model.PodMeshHealth, svc model.ServiceBackendSummary) model.HealthSummaryHeadline {
	crossOK := mesh.CrossNodeTotal == 0 || mesh.CrossNodeFailed == 0
	meshPart := ""
	if mesh.CrossNodeTotal > 0 {
		if crossOK {
			meshPart = fmt.Sprintf("%d 个节点的跨节点探针双向连通", nodeCount)
		} else {
			meshPart = fmt.Sprintf("%d 个节点的跨节点探针存在异常（%d/%d 可达）", nodeCount, mesh.CrossNodeHealthy, mesh.CrossNodeTotal)
		}
	} else {
		meshPart = fmt.Sprintf("%d 个节点探针在线", nodeCount)
	}

	svcPart := fmt.Sprintf("%d 个 Service 全部正常", svc.TotalServices)
	if svc.BrokenServices > 0 || svc.DegradedServices > 0 || svc.NoEndpointSvcs > 0 {
		svcPart = fmt.Sprintf("%d 个 Service 中，%d 个探测失败、%d 个降级、%d 个无后端",
			svc.TotalServices, svc.BrokenServices, svc.DegradedServices, svc.NoEndpointSvcs)
	}

	return model.HealthSummaryHeadline{
		NodeCount:          nodeCount,
		CrossNodeHealthy:   mesh.CrossNodeHealthy,
		CrossNodeTotal:     mesh.CrossNodeTotal,
		CrossNodeConnected: crossOK,
		TotalServices:      svc.TotalServices,
		BrokenServices:     svc.BrokenServices,
		DegradedServices:   svc.DegradedServices,
		NoEndpointServices: svc.NoEndpointSvcs,
		NeedsAttention:     !crossOK || svc.BrokenServices > 0 || svc.DegradedServices > 0,
		HeadlineText:       meshPart + "。" + svcPart + "。",
	}
}

func computeOverallScoreAndStatus(mesh model.PodMeshHealth, pn model.PodNodeHealth, ps model.PodServiceHealth, svc model.ServiceBackendSummary, infra model.InfraHealthSummary) (int, string) {
	score := 100.0
	if mesh.CrossNodeTotal > 0 && mesh.CrossNodeFailed > 0 {
		score -= 35.0 * float64(mesh.CrossNodeFailed) / float64(mesh.CrossNodeTotal)
	} else if mesh.TotalPairs > 0 && mesh.FailedPairs > 0 {
		score -= 35.0 * float64(mesh.FailedPairs) / float64(mesh.TotalPairs)
	}
	if pn.TotalChecks > 0 && pn.HealthyChecks < pn.TotalChecks {
		score -= 20.0 * float64(pn.TotalChecks-pn.HealthyChecks) / float64(pn.TotalChecks)
	}
	if ps.TotalChecks > 0 && ps.HealthyChecks < ps.TotalChecks {
		score -= 20.0 * float64(ps.TotalChecks-ps.HealthyChecks) / float64(ps.TotalChecks)
	}
	// Meaningful per-Service deductions so broken/degraded Services never hide behind "100 / healthy"
	brokenPen := float64(svc.BrokenServices) * 6.0
	if brokenPen > 18.0 {
		brokenPen = 18.0
	}
	degradedPen := float64(svc.DegradedServices) * 3.0
	if degradedPen > 9.0 {
		degradedPen = 9.0
	}
	score -= (brokenPen + degradedPen)

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
	intScore := int(math.Round(score))

	// Enforce hard status & score ceiling whenever real probe failures exist
	if svc.BrokenServices > 0 && intScore > 89 {
		intScore = 89
	}

	status := "healthy"
	if intScore < 70 || mesh.CrossNodeFailed > 0 {
		status = "critical"
	} else if intScore < 90 || svc.BrokenServices > 0 || svc.DegradedServices > 0 {
		status = "degraded"
	}
	return intScore, status
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
