package incident

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"calico-netdiag/pkg/k8s"
	"calico-netdiag/pkg/model"
	"calico-netdiag/pkg/server/agentclient"
	"calico-netdiag/pkg/server/flowstore"
	"calico-netdiag/pkg/server/healthcheck"

	corev1 "k8s.io/api/core/v1"
)

// Store keeps bounded snapshots of intermittent & ongoing network failures and supports identical-condition retests.
type Store struct {
	namespace string
	k8sStore  *k8s.Store
	flows     *flowstore.Store
	agents    *agentclient.Client

	mu        sync.RWMutex
	incidents map[string]*model.IncidentSnapshot // id -> snapshot
	byKey     map[string]string                  // dedup key -> id
	seq       int
}

// NewStore creates an incident snapshot store.
func NewStore(namespace string, k8sStore *k8s.Store, flows *flowstore.Store, agents *agentclient.Client) *Store {
	if namespace == "" {
		namespace = "calico-netdiag"
	}
	return &Store{
		namespace: namespace,
		k8sStore:  k8sStore,
		flows:     flows,
		agents:    agents,
		incidents: make(map[string]*model.IncidentSnapshot),
		byKey:     make(map[string]string),
	}
}

// IngestHealthReport automatically records failing/degraded Service ports and marks recovered ones as intermittent/recovered.
func (s *Store) IngestHealthReport(rep *model.GlobalHealthReport) {
	if rep == nil {
		return
	}
	now := rep.CheckedAt
	if now.IsZero() {
		now = time.Now()
	}

	activeFailKeys := make(map[string]bool)

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, svc := range rep.ServiceBackends.Services {
		for _, pc := range svc.PortChecks {
			if pc.Status != "broken" && pc.Status != "degraded" {
				continue
			}
			key := fmt.Sprintf("svc_port|%s/%s:%d/%s", svc.Namespace, svc.Name, pc.ServicePort, pc.Protocol)
			activeFailKeys[key] = true

			var backendNames []string
			firstPod, firstIP, firstNode := "", "", ""
			for _, be := range pc.Backends {
				label := be.PodIP
				if be.PodName != "" {
					label = fmt.Sprintf("%s/%s (%s)", be.PodNamespace, be.PodName, be.PodIP)
				}
				backendNames = append(backendNames, label)
				if !be.ProbeOK && firstIP == "" {
					firstPod = be.PodName
					firstIP = be.PodIP
					firstNode = be.NodeName
				}
			}
			if firstIP == "" && len(pc.Backends) > 0 {
				firstPod = pc.Backends[0].PodName
				firstIP = pc.Backends[0].PodIP
				firstNode = pc.Backends[0].NodeName
			}

			srcNS, srcPod := splitNSName(pc.ProbeSourcePod)
			cleanFailSummary := normalizeInitialFailureSummary(pc)
			backendState := formatInitialBackendState(pc)

			if id, ok := s.byKey[key]; ok {
				inc := s.incidents[id]
				inc.LastSeenAt = now
				inc.FailureCount++
				inc.Status = "active"
				inc.RecoveredAt = nil
				inc.RecoveryNarrative = ""
				if pc.FailureKind != "" {
					inc.FailureKind = pc.FailureKind
				}
				inc.FailureSummary = cleanFailSummary
				if inc.InitialFailureSummary == "" || inc.InitialFailureSummary == "正常" {
					inc.InitialFailureSummary = cleanFailSummary
				}
				if inc.InitialBackendState == "" {
					inc.InitialBackendState = backendState
				}
				inc.TargetBackendPod = firstPod
				inc.TargetBackendIP = firstIP
				inc.TargetBackendNode = firstNode
				inc.Timeline = s.collectTimeline(inc.FirstSeenAt, now, inc.SrcNamespace, inc.TargetNamespace, inc.TargetService, inc.TargetBackendPod)
			} else {
				s.seq++
				id := fmt.Sprintf("inc-%s-%03d", now.Format("150405"), s.seq)
				statusWord := "失败"
				if pc.Status == "degraded" {
					statusWord = "降级"
				}
				inc := &model.IncidentSnapshot{
					ID:                    id,
					Key:                   key,
					Title:                 fmt.Sprintf("%s/%s:%d/%s · 首次%s（%s）", svc.Namespace, svc.Name, pc.ServicePort, pc.Protocol, statusWord, cleanFailSummary),
					Kind:                  "service_port",
					Status:                "active",
					FirstSeenAt:           now,
					LastSeenAt:            now,
					FailureCount:          1,
					SrcNamespace:          srcNS,
					SrcPod:                srcPod,
					SrcNode:               pc.ProbeSourceNode,
					TargetNamespace:       svc.Namespace,
					TargetService:         svc.Name,
					TargetClusterIP:       svc.ClusterIP,
					TargetBackendPod:      firstPod,
					TargetBackendIP:       firstIP,
					TargetBackendNode:     firstNode,
					ServicePort:           pc.ServicePort,
					BackendPort:           pc.ResolvedTargetPort,
					Protocol:              pc.Protocol,
					InitialResult:         pc.Status,
					InitialStatus:         pc.Status,
					InitialFailureSummary: cleanFailSummary,
					InitialBackendState:   backendState,
					FailureKind:           pc.FailureKind,
					FailureSummary:        cleanFailSummary,
					InitialLatencyMs:      pc.ClusterIPLatencyMs,
					InitialBackends:       backendNames,
				}
				inc.Timeline = s.collectTimeline(now, now, srcNS, svc.Namespace, svc.Name, firstPod)
				s.incidents[id] = inc
				s.byKey[key] = id
			}
		}
	}

	// Mark previously active service_port incidents that are no longer failing as recovered/intermittent
	for key, id := range s.byKey {
		if !strings.HasPrefix(key, "svc_port|") || activeFailKeys[key] {
			continue
		}
		if inc, ok := s.incidents[id]; ok && inc.Status == "active" {
			recTime := now
			inc.RecoveredAt = &recTime
			if inc.FailureCount > 1 {
				inc.Status = "intermittent"
			} else {
				inc.Status = "recovered"
			}
			inc.Timeline = s.collectTimeline(inc.FirstSeenAt, now, inc.SrcNamespace, inc.TargetNamespace, inc.TargetService, inc.TargetBackendPod)
			inc.RecoveryNarrative = BuildRecoveryNarrative(*inc)
		}
	}

	s.pruneLocked()
}

// CreateManual saves a user-triggered failure snapshot (from Health, Topology anomaly, or TraceFlow).
func (s *Store) CreateManual(req model.CreateIncidentRequest) model.IncidentSnapshot {
	now := time.Now()
	proto := req.Protocol
	if proto == "" {
		proto = "TCP"
	}
	kind := req.Kind
	if kind == "" {
		kind = "service_port"
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		target := req.TargetService
		if target == "" {
			target = req.TargetBackendPod
		}
		if target == "" {
			target = req.TargetBackendIP
		}
		title = fmt.Sprintf("%s/%s:%d %s", req.TargetNamespace, target, req.ServicePort, req.FailureSummary)
	}

	var backends []string
	if s.k8sStore != nil && req.TargetNamespace != "" && req.TargetService != "" {
		if _, _, slices, _, ok := s.k8sStore.InspectServiceState(req.TargetNamespace, req.TargetService); ok {
			for _, sl := range slices {
				for _, ep := range sl.Endpoints {
					for _, a := range ep.Addresses {
						if ep.TargetPod != "" {
							backends = append(backends, fmt.Sprintf("%s (%s)", ep.TargetPod, a))
						} else {
							backends = append(backends, a)
						}
					}
				}
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := fmt.Sprintf("inc-%s-%03d", now.Format("150405"), s.seq)
	key := fmt.Sprintf("manual|%s|%s/%s:%d", id, req.TargetNamespace, req.TargetService, req.ServicePort)
	cleanManualSummary := strings.TrimSpace(req.FailureSummary)
	if cleanManualSummary == "" || cleanManualSummary == "正常" {
		cleanManualSummary = "原因待确认"
	}
	inc := &model.IncidentSnapshot{
		ID:                    id,
		Key:                   key,
		Title:                 title,
		Kind:                  kind,
		Status:                "active",
		FirstSeenAt:           now,
		LastSeenAt:            now,
		FailureCount:          1,
		SrcNamespace:          req.SrcNamespace,
		SrcPod:                req.SrcPod,
		TargetNamespace:       req.TargetNamespace,
		TargetService:         req.TargetService,
		TargetClusterIP:       req.TargetClusterIP,
		TargetBackendPod:      req.TargetBackendPod,
		TargetBackendIP:       req.TargetBackendIP,
		ServicePort:           req.ServicePort,
		BackendPort:           req.BackendPort,
		Protocol:              proto,
		InitialResult:         "failed",
		InitialStatus:         "failed",
		InitialFailureSummary: cleanManualSummary,
		InitialBackendState:   fmt.Sprintf("端口 %d/%s · 记录后端 %d 个", req.ServicePort, proto, len(backends)),
		FailureKind:           req.FailureKind,
		FailureSummary:        cleanManualSummary,
		InitialLatencyMs:      req.InitialLatencyMs,
		InitialBackends:       backends,
	}
	inc.Timeline = s.collectTimeline(now, now, req.SrcNamespace, req.TargetNamespace, req.TargetService, req.TargetBackendPod)
	s.incidents[id] = inc
	s.byKey[key] = id
	s.pruneLocked()
	return *inc
}

// List returns all captured incidents (newest LastSeenAt first) with refreshed timeline correlation.
func (s *Store) List(ctx context.Context) []model.IncidentSnapshot {
	s.mu.RLock()
	out := make([]model.IncidentSnapshot, 0, len(s.incidents))
	for _, inc := range s.incidents {
		cp := *inc
		cp.Timeline = s.collectTimelineWithK8sEvents(ctx, inc.FirstSeenAt, inc.LastSeenAt, inc.SrcNamespace, inc.TargetNamespace, inc.TargetService, inc.TargetBackendPod)
		if cp.Status == "recovered" || cp.Status == "intermittent" {
			cp.RecoveryNarrative = BuildRecoveryNarrative(cp)
		}
		out = append(out, cp)
	}
	s.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].Status == "active" && out[j].Status != "active" {
			return true
		}
		if out[i].Status != "active" && out[j].Status == "active" {
			return false
		}
		return out[i].LastSeenAt.After(out[j].LastSeenAt)
	})
	return out
}

// Retest replays the exact same source/target/port probe conditions and produces a Before vs After diff.
func (s *Store) Retest(ctx context.Context, id string) (*model.IncidentSnapshot, error) {
	s.mu.RLock()
	orig, ok := s.incidents[id]
	if !ok {
		s.mu.RUnlock()
		return nil, fmt.Errorf("incident %q not found", id)
	}
	snap := *orig
	s.mu.RUnlock()

	now := time.Now()
	rec := model.IncidentRetestRecord{
		TestedAt: now,
	}

	// Resolve current backends & clusterIP from live K8s store
	clusterIP := snap.TargetClusterIP
	backendIP := snap.TargetBackendIP
	backendPort := snap.BackendPort
	if backendPort <= 0 {
		backendPort = snap.ServicePort
	}
	var currBackends []string
	var firstLiveIP, firstReadyIP string
	origIPStillLive := false
	if s.k8sStore != nil && snap.TargetNamespace != "" && snap.TargetService != "" {
		if svc, _, slices, _, exists := s.k8sStore.InspectServiceState(snap.TargetNamespace, snap.TargetService); exists {
			clusterIP = svc.Spec.ClusterIP
			for _, sl := range slices {
				for _, ep := range sl.Endpoints {
					isReady := ep.Ready == nil || *ep.Ready
					for _, a := range ep.Addresses {
						lbl := a
						if ep.TargetPod != "" {
							lbl = fmt.Sprintf("%s (%s)", ep.TargetPod, a)
						}
						currBackends = append(currBackends, lbl)
						if a == backendIP {
							origIPStillLive = true
						}
						if firstLiveIP == "" {
							firstLiveIP = a
						}
						if isReady && firstReadyIP == "" {
							firstReadyIP = a
						}
					}
				}
			}
		}
	}
	replacedBackendNote := ""
	if !origIPStillLive && (firstReadyIP != "" || firstLiveIP != "") {
		liveIP := firstReadyIP
		if liveIP == "" {
			liveIP = firstLiveIP
		}
		if backendIP != "" && backendIP != liveIP {
			replacedBackendNote = fmt.Sprintf("原后端 IP (%s) 已销毁，本次直连改测当前就绪后端 (%s)", backendIP, liveIP)
		}
		backendIP = liveIP
	}
	rec.CurrentBackends = currBackends

	// Pick identical source Pod (if still Running) or fallback to node probe / host agent
	var srcPodObj *corev1.Pod
	if s.k8sStore != nil && snap.SrcNamespace != "" && snap.SrcPod != "" {
		if p, _, ok := s.k8sStore.GetPod(snap.SrcNamespace, snap.SrcPod); ok && p.Status.Phase == corev1.PodRunning && p.Status.PodIP != "" {
			srcPodObj = p
		}
	}
	if srcPodObj == nil && s.k8sStore != nil {
		probes := s.k8sStore.ListComponentPods(s.namespace, "app.kubernetes.io/component", "probe")
		for _, p := range probes {
			if p.Status.Phase == corev1.PodRunning && p.Status.PodIP != "" {
				srcPodObj = p
				if p.Spec.NodeName == snap.SrcNode {
					break
				}
			}
		}
	}

	// Execute live probes (ClusterIP + direct backend)
	hasCIP := clusterIP != "" && clusterIP != "None" && snap.ServicePort > 0
	hasDirect := backendIP != "" && backendPort > 0

	if srcPodObj != nil && s.agents != nil {
		rec.ProbeSource = fmt.Sprintf("%s/%s (%s)", srcPodObj.Namespace, srcPodObj.Name, srcPodObj.Spec.NodeName)
		if hasCIP {
			hres, err := s.agents.RunTCPHandshake(ctx, srcPodObj.Spec.NodeName, string(srcPodObj.UID), srcPodObj.Status.PodIP, srcPodObj.Namespace+"/"+srcPodObj.Name, model.TCPHandshakeRequest{
				SrcNamespace: srcPodObj.Namespace,
				SrcPod:       srcPodObj.Name,
				TargetHost:   clusterIP,
				TargetPort:   snap.ServicePort,
				Count:        1,
				TimeoutMs:    1500,
			})
			if err == nil && hres != nil {
				rec.ClusterIPOK = hres.SuccessCount > 0
				rec.ClusterIPLatMs = hres.AvgRTTMs
				if !rec.ClusterIPOK && len(hres.Attempts) > 0 {
					_, fl := healthcheck.ClassifyProbeError(hres.Attempts[0].Error + " " + hres.Attempts[0].Result)
					if fl == "" {
						fl = hres.Attempts[0].Result
					}
					rec.ClusterIPFailure = fl
				}
			} else if err != nil {
				_, rec.ClusterIPFailure = healthcheck.ClassifyProbeError(err.Error())
			}
		}
		if hasDirect {
			hres, err := s.agents.RunTCPHandshake(ctx, srcPodObj.Spec.NodeName, string(srcPodObj.UID), srcPodObj.Status.PodIP, srcPodObj.Namespace+"/"+srcPodObj.Name, model.TCPHandshakeRequest{
				SrcNamespace: srcPodObj.Namespace,
				SrcPod:       srcPodObj.Name,
				TargetHost:   backendIP,
				TargetPort:   backendPort,
				Count:        1,
				TimeoutMs:    1500,
			})
			if err == nil && hres != nil {
				rec.DirectBackendOK = hres.SuccessCount > 0
				rec.DirectLatMs = hres.AvgRTTMs
				if !rec.DirectBackendOK && len(hres.Attempts) > 0 {
					_, fl := healthcheck.ClassifyProbeError(hres.Attempts[0].Error + " " + hres.Attempts[0].Result)
					if fl == "" {
						fl = hres.Attempts[0].Result
					}
					rec.DirectFailure = fl
				}
			} else if err != nil {
				_, rec.DirectFailure = healthcheck.ClassifyProbeError(err.Error())
			}
		}
	} else if s.agents != nil && s.k8sStore != nil {
		// Fallback to host TCP check
		nodes := s.k8sStore.ListNodes()
		if len(nodes) > 0 {
			agentNode := nodes[0].Name
			rec.ProbeSource = fmt.Sprintf("节点 Agent (%s)", agentNode)
			var targets []agentclient.HostTCPTarget
			if hasCIP {
				targets = append(targets, agentclient.HostTCPTarget{ID: "cip", Address: fmt.Sprintf("%s:%d", clusterIP, snap.ServicePort)})
			}
			if hasDirect {
				targets = append(targets, agentclient.HostTCPTarget{ID: "direct", Address: fmt.Sprintf("%s:%d", backendIP, backendPort)})
			}
			if results, err := s.agents.HostTCPCheck(ctx, agentNode, 1500, targets); err == nil {
				for _, r := range results {
					if r.ID == "cip" {
						rec.ClusterIPOK = r.OK
						rec.ClusterIPLatMs = r.LatencyMs
						if !r.OK {
							_, rec.ClusterIPFailure = healthcheck.ClassifyProbeError(r.Error)
						}
					} else if r.ID == "direct" {
						rec.DirectBackendOK = r.OK
						rec.DirectLatMs = r.LatencyMs
						if !r.OK {
							_, rec.DirectFailure = healthcheck.ClassifyProbeError(r.Error)
						}
					}
				}
			}
		}
	}

	// Build Before vs After diff
	rec.Diff = BuildIncidentDiff(snap, rec, hasCIP, hasDirect)
	if replacedBackendNote != "" {
		rec.Diff.ChangesSummary = append(rec.Diff.ChangesSummary, replacedBackendNote)
	}
	if rec.Diff.StatusAfter == "recovered" {
		rec.Verdict = "recovered"
		rec.VerdictText = "已恢复连通（与故障现场对比已恢复正常）"
	} else if (hasCIP && rec.ClusterIPOK) || (hasDirect && rec.DirectBackendOK) {
		rec.Verdict = "partial_improved"
		rec.VerdictText = "部分恢复，但仍有路径失败"
	} else {
		rec.Verdict = "still_failing"
		rec.VerdictText = "故障仍在复现：" + rec.Diff.FailureAfter
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if inc, exists := s.incidents[id]; exists {
		inc.Retests = append([]model.IncidentRetestRecord{rec}, inc.Retests...)
		if len(inc.Retests) > 10 {
			inc.Retests = inc.Retests[:10]
		}
		if rec.Verdict == "recovered" {
			inc.Status = "recovered"
			if inc.RecoveredAt == nil {
				rt := now
				inc.RecoveredAt = &rt
			}
		} else {
			inc.Status = "active"
			inc.LastSeenAt = now
		}
		inc.Timeline = s.collectTimelineWithK8sEvents(ctx, inc.FirstSeenAt, now, inc.SrcNamespace, inc.TargetNamespace, inc.TargetService, inc.TargetBackendPod)
		if inc.Status == "recovered" || inc.Status == "intermittent" {
			inc.RecoveryNarrative = BuildRecoveryNarrative(*inc)
		}
		cp := *inc
		return &cp, nil
	}
	return nil, fmt.Errorf("incident %q disappeared", id)
}

// BuildIncidentDiff computes a structured Before-vs-After comparison between the original snapshot and a retest.
func BuildIncidentDiff(snap model.IncidentSnapshot, rec model.IncidentRetestRecord, hasCIP, hasDirect bool) model.IncidentDiff {
	afterOK := (!hasCIP || rec.ClusterIPOK) && (!hasDirect || rec.DirectBackendOK) && (hasCIP || hasDirect)
	statusAfter := "still_failing"
	failAfter := rec.ClusterIPFailure
	if failAfter == "" {
		failAfter = rec.DirectFailure
	}
	if afterOK {
		statusAfter = "recovered"
		failAfter = "正常连通"
	} else if failAfter == "" {
		failAfter = "探测失败"
	}

	latAfter := rec.ClusterIPLatMs
	if latAfter == 0 {
		latAfter = rec.DirectLatMs
	}

	beforeFail := snap.InitialFailureSummary
	if beforeFail == "" || beforeFail == "正常" {
		beforeFail = snap.FailureSummary
	}
	if beforeFail == "" || beforeFail == "正常" {
		beforeFail = "原因待确认"
	}

	beforeSet := make(map[string]string, len(snap.InitialBackends))
	for _, b := range snap.InitialBackends {
		beforeSet[backendDiffKey(b)] = b
	}
	afterSet := make(map[string]string, len(rec.CurrentBackends))
	for _, b := range rec.CurrentBackends {
		afterSet[backendDiffKey(b)] = b
	}
	var added, removed []string
	for k, b := range afterSet {
		if _, ok := beforeSet[k]; !ok {
			added = append(added, b)
		}
	}
	for k, b := range beforeSet {
		if _, ok := afterSet[k]; !ok {
			removed = append(removed, b)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)

	var changes []string
	if afterOK {
		changes = append(changes, fmt.Sprintf("连通性：%s → 已恢复正常 (%.2f ms)", beforeFail, latAfter))
	} else if failAfter != beforeFail {
		changes = append(changes, fmt.Sprintf("失败表现变化：%s → %s", beforeFail, failAfter))
	} else {
		changes = append(changes, fmt.Sprintf("失败表现未变：仍为 %s", failAfter))
	}
	if len(added) > 0 {
		changes = append(changes, fmt.Sprintf("新增后端：%s", strings.Join(added, ", ")))
	}
	if len(removed) > 0 {
		changes = append(changes, fmt.Sprintf("移除后端：%s", strings.Join(removed, ", ")))
	}
	if len(added) == 0 && len(removed) == 0 {
		changes = append(changes, "后端列表无变更")
	}

	return model.IncidentDiff{
		StatusBefore:    snap.InitialResult,
		StatusAfter:     statusAfter,
		FailureBefore:   beforeFail,
		FailureAfter:    failAfter,
		LatencyBefore:   snap.InitialLatencyMs,
		LatencyAfter:    latAfter,
		BackendsAdded:   added,
		BackendsRemoved: removed,
		ChangesSummary:  changes,
	}
}

func (s *Store) collectTimeline(first, last time.Time, srcNS, targetNS, targetSvc, targetPod string) []model.ClusterChangeEvent {
	return s.collectTimelineWithK8sEvents(context.Background(), first, last, srcNS, targetNS, targetSvc, targetPod)
}

func (s *Store) collectTimelineWithK8sEvents(ctx context.Context, first, last time.Time, srcNS, targetNS, targetSvc, targetPod string) []model.ClusterChangeEvent {
	since := first.Add(-10 * time.Minute)
	var out []model.ClusterChangeEvent

	if s.k8sStore != nil {
		out = append(out, s.k8sStore.ListRecentChangeEvents(since, srcNS, targetNS)...)
		if targetNS != "" {
			hints := []string{targetSvc, targetPod}
			out = append(out, s.k8sStore.FetchNamespaceK8sEvents(ctx, targetNS, hints, 12)...)
		}
	}
	if s.flows != nil {
		for _, bev := range s.flows.BGPEvents() {
			if bev.Timestamp.Before(since) {
				continue
			}
			out = append(out, model.ClusterChangeEvent{
				Timestamp: bev.Timestamp,
				Category:  "bgp_change",
				Severity:  bev.Severity,
				Object:    fmt.Sprintf("BGP/%s/%s", bev.NodeName, bev.PeerName),
				Summary:   bev.Message,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	if len(out) > 25 {
		out = out[:25]
	}
	return out
}

func (s *Store) pruneLocked() {
	const maxIncidents = 80
	if len(s.incidents) <= maxIncidents {
		return
	}
	type pair struct {
		id  string
		key string
		ts  time.Time
	}
	all := make([]pair, 0, len(s.incidents))
	for id, inc := range s.incidents {
		all = append(all, pair{id: id, key: inc.Key, ts: inc.LastSeenAt})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ts.Before(all[j].ts) })
	for i := 0; i < len(all)-maxIncidents; i++ {
		delete(s.incidents, all[i].id)
		delete(s.byKey, all[i].key)
	}
}

func splitNSName(s string) (string, string) {
	if idx := strings.Index(s, "/"); idx > 0 {
		return s[:idx], s[idx+1:]
	}
	return "", s
}

// backendDiffKey extracts the canonical IP inside "name (ip)" so bare IP and labeled node/pod IP match cleanly.
func backendDiffKey(label string) string {
	s := strings.TrimSpace(label)
	if l := strings.LastIndex(s, "("); l >= 0 {
		if r := strings.LastIndex(s, ")"); r > l {
			ip := strings.TrimSpace(s[l+1 : r])
			if ip != "" {
				return ip
			}
		}
	}
	return s
}

// normalizeInitialFailureSummary ensures a degraded/broken incident never records "正常" or empty text.
func normalizeInitialFailureSummary(pc model.ServicePortHealth) string {
	s := strings.TrimSpace(pc.FailureSummary)
	if s != "" && s != "正常" {
		return s
	}
	if pc.ReadyBackends < pc.TotalBackends {
		return fmt.Sprintf("部分后端未就绪（Ready %d/%d，已测 %d/%d 可达）", pc.ReadyBackends, pc.TotalBackends, pc.ReachableBackends, pc.TestedBackends)
	}
	if pc.TestedBackends > 0 && pc.ReachableBackends < pc.TestedBackends {
		return fmt.Sprintf("部分后端不可达（%d/%d 可达，原因待确认）", pc.ReachableBackends, pc.TestedBackends)
	}
	return "原因待确认"
}

func formatInitialBackendState(pc model.ServicePortHealth) string {
	proto := pc.Protocol
	if proto == "" {
		proto = "TCP"
	}
	if pc.TotalBackends == 0 {
		return fmt.Sprintf("端口 %d/%s · 无后端", pc.ServicePort, proto)
	}
	return fmt.Sprintf("端口 %d/%s · 后端 Ready %d/%d，实测可达 %d/%d",
		pc.ServicePort, proto, pc.ReadyBackends, pc.TotalBackends, pc.ReachableBackends, pc.TestedBackends)
}

// BuildRecoveryNarrative summarizes what was initially observed, what cluster change occurred, and the current recovered state.
func BuildRecoveryNarrative(inc model.IncidentSnapshot) string {
	initStatusWord := "探测失败"
	if inc.InitialStatus == "degraded" || inc.InitialResult == "degraded" {
		initStatusWord = "降级"
	}
	reason := strings.TrimSpace(inc.InitialFailureSummary)
	if reason == "" || reason == "正常" {
		reason = strings.TrimSpace(inc.FailureSummary)
	}
	if reason == "" || reason == "正常" {
		reason = "原因待确认"
	}

	firstPart := fmt.Sprintf("首次巡检记录为%s（%s）", initStatusWord, reason)

	// Scan timeline for concrete recovery signals (Ready=false -> Ready=true, Endpoint change, Pod rebuild, etc.)
	midPart := "随后巡检探测恢复连通"
	hasReadyRecover := false
	hasEndpointChange := false
	hasPodRebuild := false
	hasPolicyChange := false
	hasBGPChange := false
	for _, ev := range inc.Timeline {
		low := strings.ToLower(ev.Summary)
		if strings.Contains(ev.Summary, "Ready") || strings.Contains(low, "ready=true") || strings.Contains(ev.Summary, "就绪") {
			hasReadyRecover = true
		}
		switch ev.Category {
		case "endpoint_change":
			hasEndpointChange = true
		case "pod_rebuild":
			hasPodRebuild = true
		case "policy_change":
			hasPolicyChange = true
		case "bgp_change":
			hasBGPChange = true
		}
	}
	switch {
	case hasReadyRecover:
		midPart = "随后观察到后端恢复 Ready"
	case hasEndpointChange:
		midPart = "随后观察到后端 Endpoint 更替"
	case hasPodRebuild:
		midPart = "随后观察到关联 Pod 重建完成"
	case hasPolicyChange:
		midPart = "随后观察到网络策略变更"
	case hasBGPChange:
		midPart = "随后观察到 BGP 会话状态变化"
	}

	currPart := "当前已恢复"
	if inc.Status == "intermittent" {
		currPart = fmt.Sprintf("当前已恢复（此前累计异常 %d 次）", inc.FailureCount)
	}
	return fmt.Sprintf("%s；%s；%s。", firstPart, midPart, currPart)
}
