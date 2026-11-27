package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"calico-netdiag/pkg/model"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func (s *Store) appendChangeEventLocked(ev model.ClusterChangeEvent) {
	s.changeEvents = append([]model.ClusterChangeEvent{ev}, s.changeEvents...)
	if len(s.changeEvents) > 500 {
		s.changeEvents = s.changeEvents[:500]
	}
}

func (s *Store) recordClusterDiffsLocked(
	now time.Time,
	nextPods map[string]*corev1.Pod,
	nextSlices map[string][]*discoveryv1.EndpointSlice,
	nextK8sPols []networkingv1.NetworkPolicy,
	nextCalicoPols []RawCalicoPolicy,
) {
	// 1. Pod creations / restarts / deletions
	for key, np := range nextPods {
		prev, existed := s.pods[key]
		if !existed {
			s.appendChangeEventLocked(model.ClusterChangeEvent{
				Timestamp: now,
				Category:  "pod_rebuild",
				Severity:  "info",
				Namespace: np.Namespace,
				Object:    key,
				Summary:   fmt.Sprintf("Pod 新建/重建 (%s @ %s, IP=%s)", np.Name, np.Spec.NodeName, np.Status.PodIP),
			})
			continue
		}
		if prev.Status.PodIP != np.Status.PodIP && np.Status.PodIP != "" {
			s.appendChangeEventLocked(model.ClusterChangeEvent{
				Timestamp: now,
				Category:  "pod_rebuild",
				Severity:  "warning",
				Namespace: np.Namespace,
				Object:    key,
				Summary:   fmt.Sprintf("Pod IP 变更: %s → %s", prev.Status.PodIP, np.Status.PodIP),
			})
		}
		if totalRestarts(np) > totalRestarts(prev) {
			s.appendChangeEventLocked(model.ClusterChangeEvent{
				Timestamp: now,
				Category:  "pod_rebuild",
				Severity:  "warning",
				Namespace: np.Namespace,
				Object:    key,
				Summary:   fmt.Sprintf("Pod 容器发生重启 (累计重启 %d → %d 次)", totalRestarts(prev), totalRestarts(np)),
			})
		}
		if IsPodReady(prev) != IsPodReady(np) {
			sev := "info"
			msg := fmt.Sprintf("Pod 恢复 Ready (%s)", np.Name)
			if !IsPodReady(np) {
				sev = "warning"
				msg = fmt.Sprintf("Pod 变为 NotReady (%s: %s)", np.Name, podNotReadyReason(np))
			}
			s.appendChangeEventLocked(model.ClusterChangeEvent{
				Timestamp: now,
				Category:  "pod_rebuild",
				Severity:  sev,
				Namespace: np.Namespace,
				Object:    key,
				Summary:   msg,
			})
		}
	}
	for key, prev := range s.pods {
		if _, exists := nextPods[key]; !exists {
			s.appendChangeEventLocked(model.ClusterChangeEvent{
				Timestamp: now,
				Category:  "pod_rebuild",
				Severity:  "info",
				Namespace: prev.Namespace,
				Object:    key,
				Summary:   fmt.Sprintf("Pod 被删除/终止 (%s, 原 IP=%s)", prev.Name, prev.Status.PodIP),
			})
		}
	}

	// 2. EndpointSlice backend changes per Service
	sliceSignature := func(list []*discoveryv1.EndpointSlice) string {
		var addrs []string
		for _, sl := range list {
			for _, ep := range sl.Endpoints {
				ready := ep.Conditions.Ready == nil || *ep.Conditions.Ready
				for _, a := range ep.Addresses {
					addrs = append(addrs, fmt.Sprintf("%s(ready=%v)", a, ready))
				}
			}
		}
		sort.Strings(addrs)
		return strings.Join(addrs, ",")
	}
	for svcKey, slList := range nextSlices {
		prevSig := sliceSignature(s.endpointSlices[svcKey])
		currSig := sliceSignature(slList)
		if prevSig != currSig {
			ns := ""
			if idx := strings.Index(svcKey, "/"); idx > 0 {
				ns = svcKey[:idx]
			}
			s.appendChangeEventLocked(model.ClusterChangeEvent{
				Timestamp: now,
				Category:  "endpoint_change",
				Severity:  "warning",
				Namespace: ns,
				Object:    svcKey,
				Summary:   fmt.Sprintf("Service 后端更替: [%s] → [%s]", emptyDash(prevSig), emptyDash(currSig)),
			})
		}
	}

	// 3. K8s NetworkPolicy & Calico Policy diffs
	prevK8sNP := make(map[string]string, len(s.k8sNetPols))
	for _, p := range s.k8sNetPols {
		prevK8sNP[p.Namespace+"/"+p.Name] = p.ResourceVersion
	}
	currK8sNP := make(map[string]string, len(nextK8sPols))
	for _, p := range nextK8sPols {
		k := p.Namespace + "/" + p.Name
		currK8sNP[k] = p.ResourceVersion
		if prevRV, ok := prevK8sNP[k]; !ok {
			s.appendChangeEventLocked(model.ClusterChangeEvent{
				Timestamp: now,
				Category:  "policy_change",
				Severity:  "warning",
				Namespace: p.Namespace,
				Object:    "NetworkPolicy/" + k,
				Summary:   fmt.Sprintf("新增 K8s NetworkPolicy %s", k),
			})
		} else if prevRV != p.ResourceVersion {
			s.appendChangeEventLocked(model.ClusterChangeEvent{
				Timestamp: now,
				Category:  "policy_change",
				Severity:  "warning",
				Namespace: p.Namespace,
				Object:    "NetworkPolicy/" + k,
				Summary:   fmt.Sprintf("K8s NetworkPolicy %s 规则已更新", k),
			})
		}
	}
	for k := range prevK8sNP {
		if _, ok := currK8sNP[k]; !ok {
			ns := ""
			if idx := strings.Index(k, "/"); idx > 0 {
				ns = k[:idx]
			}
			s.appendChangeEventLocked(model.ClusterChangeEvent{
				Timestamp: now,
				Category:  "policy_change",
				Severity:  "info",
				Namespace: ns,
				Object:    "NetworkPolicy/" + k,
				Summary:   fmt.Sprintf("删除 K8s NetworkPolicy %s", k),
			})
		}
	}

	prevCal := make(map[string]string, len(s.calicoNetPols))
	for _, p := range s.calicoNetPols {
		prevCal[calicoPolicyKey(p)] = p.ResourceVersion
	}
	for _, p := range nextCalicoPols {
		k := calicoPolicyKey(p)
		if prevRV, ok := prevCal[k]; !ok {
			s.appendChangeEventLocked(model.ClusterChangeEvent{
				Timestamp: now,
				Category:  "policy_change",
				Severity:  "warning",
				Namespace: p.Namespace,
				Object:    k,
				Summary:   fmt.Sprintf("新增 Calico 策略 %s (Tier=%s)", k, p.Tier),
			})
		} else if prevRV != p.ResourceVersion {
			s.appendChangeEventLocked(model.ClusterChangeEvent{
				Timestamp: now,
				Category:  "policy_change",
				Severity:  "warning",
				Namespace: p.Namespace,
				Object:    k,
				Summary:   fmt.Sprintf("Calico 策略 %s 已变更", k),
			})
		}
	}
}

func calicoPolicyKey(p RawCalicoPolicy) string {
	if p.Global {
		return "GlobalNetworkPolicy/" + p.Name
	}
	return "CalicoPolicy/" + p.Namespace + "/" + p.Name
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "空"
	}
	return s
}

// ListRecentChangeEvents returns recent Pod/EndpointSlice/Policy changes optionally scoped by namespaces and lookback window.
func (s *Store) ListRecentChangeEvents(since time.Time, namespaces ...string) []model.ClusterChangeEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nsSet := make(map[string]bool)
	for _, ns := range namespaces {
		if strings.TrimSpace(ns) != "" {
			nsSet[ns] = true
		}
	}

	out := make([]model.ClusterChangeEvent, 0, 32)
	for _, ev := range s.changeEvents {
		if !since.IsZero() && ev.Timestamp.Before(since) {
			continue
		}
		if len(nsSet) > 0 && ev.Namespace != "" && !nsSet[ev.Namespace] {
			continue
		}
		out = append(out, ev)
		if len(out) >= 40 {
			break
		}
	}
	return out
}

// FetchNamespaceK8sEvents performs a best-effort query of recent Kubernetes Warning/Normal events for a namespace.
func (s *Store) FetchNamespaceK8sEvents(ctx context.Context, namespace string, objectHints []string, limit int) []model.ClusterChangeEvent {
	if s.clients == nil || s.clients.Typed == nil || namespace == "" {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	evList, err := s.clients.Typed.CoreV1().Events(namespace).List(cctx, metav1.ListOptions{Limit: 80})
	if err != nil || evList == nil {
		return nil
	}
	hints := make([]string, 0, len(objectHints))
	for _, h := range objectHints {
		if strings.TrimSpace(h) != "" {
			hints = append(hints, strings.ToLower(h))
		}
	}

	out := make([]model.ClusterChangeEvent, 0, limit)
	for _, e := range evList.Items {
		ts := e.LastTimestamp.Time
		if ts.IsZero() {
			ts = e.EventTime.Time
		}
		if ts.IsZero() {
			ts = e.CreationTimestamp.Time
		}
		obj := fmt.Sprintf("%s/%s", e.InvolvedObject.Kind, e.InvolvedObject.Name)
		if len(hints) > 0 {
			matched := false
			lo := strings.ToLower(e.InvolvedObject.Name + " " + e.Message)
			for _, h := range hints {
				if strings.Contains(lo, h) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		sev := "info"
		if strings.EqualFold(e.Type, "Warning") {
			sev = "warning"
		}
		out = append(out, model.ClusterChangeEvent{
			Timestamp: ts,
			Category:  "k8s_event",
			Severity:  sev,
			Namespace: namespace,
			Object:    obj,
			Summary:   fmt.Sprintf("[%s] %s", e.Reason, e.Message),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// InspectServiceState inspects a Service's selector, matching Pods (including non-Ready ones), and EndpointSlices.
func (s *Store) InspectServiceState(namespace, name string) (*corev1.Service, []model.ServiceMatchingPodInfo, []model.EndpointSliceBrief, []*corev1.Pod, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := namespace + "/" + name
	svc, ok := s.services[key]
	if !ok {
		return nil, nil, nil, nil, false
	}

	var matchedPods []*corev1.Pod
	var podInfos []model.ServiceMatchingPodInfo
	if len(svc.Spec.Selector) > 0 {
		sel := labels.SelectorFromSet(svc.Spec.Selector)
		for _, p := range s.pods {
			if p.Namespace != namespace {
				continue
			}
			if sel.Matches(labels.Set(p.Labels)) {
				matchedPods = append(matchedPods, p)
			}
		}
		sort.Slice(matchedPods, func(i, j int) bool { return matchedPods[i].Name < matchedPods[j].Name })
		for _, p := range matchedPods {
			var cPorts []string
			for _, c := range p.Spec.Containers {
				for _, cp := range c.Ports {
					proto := string(cp.Protocol)
					if proto == "" {
						proto = "TCP"
					}
					if cp.Name != "" {
						cPorts = append(cPorts, fmt.Sprintf("%d/%s (%s)", cp.ContainerPort, proto, cp.Name))
					} else {
						cPorts = append(cPorts, fmt.Sprintf("%d/%s", cp.ContainerPort, proto))
					}
				}
			}
			ready := IsPodReady(p)
			reason := ""
			if !ready {
				reason = podNotReadyReason(p)
			}
			podInfos = append(podInfos, model.ServiceMatchingPodInfo{
				Namespace:      p.Namespace,
				Name:           p.Name,
				IP:             p.Status.PodIP,
				NodeName:       p.Spec.NodeName,
				Phase:          string(p.Status.Phase),
				Ready:          ready,
				NotReadyReason: reason,
				RestartCount:   totalRestarts(p),
				ContainerPorts: cPorts,
			})
		}
	}

	// EndpointSlices
	var slicesOut []model.EndpointSliceBrief
	for _, sl := range s.endpointSlices[key] {
		brief := model.EndpointSliceBrief{
			Name:        sl.Name,
			AddressType: string(sl.AddressType),
		}
		for _, sp := range sl.Ports {
			pn := ""
			if sp.Name != nil && *sp.Name != "" {
				pn = *sp.Name + ":"
			}
			portNum := int32(0)
			if sp.Port != nil {
				portNum = *sp.Port
			}
			proto := "TCP"
			if sp.Protocol != nil {
				proto = string(*sp.Protocol)
			}
			brief.Ports = append(brief.Ports, fmt.Sprintf("%s%d/%s", pn, portNum, proto))
		}
		for _, ep := range sl.Endpoints {
			var item struct {
				Addresses   []string `json:"addresses"`
				Ready       *bool    `json:"ready,omitempty"`
				Serving     *bool    `json:"serving,omitempty"`
				Terminating *bool    `json:"terminating,omitempty"`
				TargetPod   string   `json:"targetPod,omitempty"`
				NodeName    string   `json:"nodeName,omitempty"`
			}
			item.Addresses = ep.Addresses
			item.Ready = ep.Conditions.Ready
			item.Serving = ep.Conditions.Serving
			item.Terminating = ep.Conditions.Terminating
			if ep.TargetRef != nil {
				if ep.TargetRef.Namespace != "" {
					item.TargetPod = ep.TargetRef.Namespace + "/" + ep.TargetRef.Name
				} else {
					item.TargetPod = ep.TargetRef.Name
				}
			}
			if ep.NodeName != nil {
				item.NodeName = *ep.NodeName
			}
			brief.Endpoints = append(brief.Endpoints, item)
		}
		slicesOut = append(slicesOut, brief)
	}

	return svc, podInfos, slicesOut, matchedPods, true
}

func podNotReadyReason(p *corev1.Pod) string {
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return fmt.Sprintf("容器 %s 等待中: %s", cs.Name, cs.State.Waiting.Reason)
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
			return fmt.Sprintf("容器 %s 已退出: %s (code %d)", cs.Name, cs.State.Terminated.Reason, cs.State.Terminated.ExitCode)
		}
		if !cs.Ready {
			return fmt.Sprintf("容器 %s 就绪探针未通过 (ReadinessProbe)", cs.Name)
		}
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status != corev1.ConditionTrue {
			if c.Message != "" {
				return fmt.Sprintf("%s: %s", c.Reason, c.Message)
			}
			if c.Reason != "" {
				return c.Reason
			}
		}
	}
	return string(p.Status.Phase)
}

// EvaluatePoliciesForDirection returns K8s & Calico NetworkPolicies selecting the given Pod for "Egress" or "Ingress",
// explicitly tagged as config-inferred ("按配置推算").
func (s *Store) EvaluatePoliciesForDirection(pod *corev1.Pod, direction string) []model.PolicyAttributionItem {
	if pod == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	podLabels := labels.Set(pod.Labels)
	var out []model.PolicyAttributionItem

	// 1. Kubernetes NetworkPolicy
	for _, np := range s.k8sNetPols {
		if np.Namespace != pod.Namespace {
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(&np.Spec.PodSelector)
		if err != nil || !sel.Matches(podLabels) {
			continue
		}
		types := np.Spec.PolicyTypes
		if len(types) == 0 {
			types = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
			if len(np.Spec.Egress) > 0 {
				types = append(types, networkingv1.PolicyTypeEgress)
			}
		}
		applies := false
		for _, pt := range types {
			if strings.EqualFold(string(pt), direction) {
				applies = true
				break
			}
		}
		if !applies {
			continue
		}

		selStr := sel.String()
		if selStr == "" {
			selStr = "<all pods in namespace>"
		}
		var summaries []string
		effect := "restrict_possible"
		if strings.EqualFold(direction, "Ingress") {
			if len(np.Spec.Ingress) == 0 {
				summaries = []string{"无允许规则 → 默认拒绝所有入站流量 (Default Deny Ingress)"}
				effect = "default_deny"
			} else {
				for _, r := range np.Spec.Ingress {
					summaries = append(summaries, summarizeK8sIngressRule(r))
				}
			}
		} else {
			if len(np.Spec.Egress) == 0 {
				summaries = []string{"无允许规则 → 默认拒绝所有出站流量 (Default Deny Egress)"}
				effect = "default_deny"
			} else {
				for _, r := range np.Spec.Egress {
					summaries = append(summaries, summarizeK8sEgressRule(r))
				}
			}
		}

		out = append(out, model.PolicyAttributionItem{
			Source:        "K8s NetworkPolicy",
			Namespace:     np.Namespace,
			Name:          np.Name,
			Direction:     direction,
			Selector:      selStr,
			RuleSummaries: summaries,
			EffectHint:    effect,
			EvidenceKind:  "按配置推算",
		})
	}

	// 2. Calico NetworkPolicy & GlobalNetworkPolicy
	for _, cp := range s.calicoNetPols {
		if !cp.Global && cp.Namespace != pod.Namespace {
			continue
		}
		if !calicoSelectorMatches(cp.Selector, pod.Labels) {
			continue
		}
		applies := false
		for _, pt := range cp.PolicyTypes {
			if strings.EqualFold(pt, direction) {
				applies = true
				break
			}
		}
		if !applies {
			continue
		}
		src := "Calico NetworkPolicy"
		if cp.Global {
			src = "Calico GlobalNetworkPolicy"
		}
		var summaries []string
		effect := "restrict_possible"
		if strings.EqualFold(direction, "Ingress") {
			summaries = cp.IngressSummaries
			if len(summaries) == 0 {
				summaries = []string{"无 Ingress 允许规则 → 默认拒绝入站"}
				effect = "default_deny"
			}
		} else {
			summaries = cp.EgressSummaries
			if len(summaries) == 0 {
				summaries = []string{"无 Egress 允许规则 → 默认拒绝出站"}
				effect = "default_deny"
			}
		}
		selStr := cp.Selector
		if selStr == "" {
			selStr = "all()"
		}
		out = append(out, model.PolicyAttributionItem{
			Source:        src,
			Namespace:     cp.Namespace,
			Name:          cp.Name,
			Tier:          cp.Tier,
			Order:         cp.Order,
			Direction:     direction,
			Selector:      selStr,
			RuleSummaries: summaries,
			EffectHint:    effect,
			EvidenceKind:  "按配置推算",
		})
	}

	return out
}

func summarizeK8sIngressRule(r networkingv1.NetworkPolicyIngressRule) string {
	var fromParts []string
	if len(r.From) == 0 {
		fromParts = append(fromParts, "任意来源")
	} else {
		for _, peer := range r.From {
			fromParts = append(fromParts, summarizeK8sPeer(peer))
		}
	}
	portStr := summarizeK8sPorts(r.Ports)
	return fmt.Sprintf("Allow from [%s]%s", strings.Join(fromParts, " | "), portStr)
}

func summarizeK8sEgressRule(r networkingv1.NetworkPolicyEgressRule) string {
	var toParts []string
	if len(r.To) == 0 {
		toParts = append(toParts, "任意目标")
	} else {
		for _, peer := range r.To {
			toParts = append(toParts, summarizeK8sPeer(peer))
		}
	}
	portStr := summarizeK8sPorts(r.Ports)
	return fmt.Sprintf("Allow to [%s]%s", strings.Join(toParts, " | "), portStr)
}

func summarizeK8sPeer(p networkingv1.NetworkPolicyPeer) string {
	var parts []string
	if p.NamespaceSelector != nil {
		if sel, err := metav1.LabelSelectorAsSelector(p.NamespaceSelector); err == nil {
			s := sel.String()
			if s == "" {
				s = "*"
			}
			parts = append(parts, "ns("+s+")")
		}
	}
	if p.PodSelector != nil {
		if sel, err := metav1.LabelSelectorAsSelector(p.PodSelector); err == nil {
			s := sel.String()
			if s == "" {
				s = "*"
			}
			parts = append(parts, "pod("+s+")")
		}
	}
	if p.IPBlock != nil {
		parts = append(parts, "cidr("+p.IPBlock.CIDR+")")
	}
	if len(parts) == 0 {
		return "*"
	}
	return strings.Join(parts, " & ")
}

func summarizeK8sPorts(ports []networkingv1.NetworkPolicyPort) string {
	if len(ports) == 0 {
		return " (全部端口)"
	}
	var out []string
	for _, p := range ports {
		proto := "TCP"
		if p.Protocol != nil {
			proto = string(*p.Protocol)
		}
		if p.Port != nil {
			out = append(out, fmt.Sprintf("%s:%s", proto, p.Port.String()))
		} else {
			out = append(out, proto+":*")
		}
	}
	return " ports(" + strings.Join(out, ",") + ")"
}
