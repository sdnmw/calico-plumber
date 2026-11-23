package k8s

import (
	"fmt"
	"sort"
	"strings"

	"calico-netdiag/pkg/model"

	"k8s.io/apimachinery/pkg/labels"
)

// Search performs a fast cross-resource Omni-Search across Pods, Deployments,
// StatefulSets, DaemonSets, Services, Nodes, and IP addresses.
func (s *Store) Search(query, namespaceFilter, kindFilter string, limit int) []model.SearchResultItem {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > 200 {
		limit = 60
	}
	q := strings.ToLower(strings.TrimSpace(query))
	kf := strings.ToLower(strings.TrimSpace(kindFilter))

	type scored struct {
		score int
		item  model.SearchResultItem
	}
	var results []scored

	matchStr := func(val string, exactBoost int) int {
		if q == "" {
			return 1
		}
		lv := strings.ToLower(val)
		if lv == q {
			return 100 + exactBoost
		}
		if strings.HasPrefix(lv, q) {
			return 70 + exactBoost
		}
		if strings.Contains(lv, q) {
			return 40 + exactBoost
		}
		return 0
	}

	matchLabels := func(lbls map[string]string) int {
		if q == "" || !strings.Contains(q, "=") {
			return 0
		}
		kv := strings.SplitN(q, "=", 2)
		for k, v := range lbls {
			if strings.EqualFold(k, kv[0]) && strings.Contains(strings.ToLower(v), kv[1]) {
				return 65
			}
		}
		return 0
	}

	// 1. Pods
	if kf == "" || kf == "pod" {
		for key, p := range s.pods {
			if namespaceFilter != "" && p.Namespace != namespaceFilter {
				continue
			}
			b := s.podBriefs[key]
			sc := maxInt(
				matchStr(b.IP, 20),
				matchStr(b.Name, 10),
				matchStr(b.Namespace+"/"+b.Name, 10),
				matchStr(b.WorkloadName, 5),
				matchStr(b.NodeName, 0),
				matchStr(b.IPAMBlock, 0),
				matchLabels(p.Labels),
			)
			if sc == 0 {
				continue
			}
			results = append(results, scored{
				score: sc,
				item: model.SearchResultItem{
					Kind:         "Pod",
					Name:         b.Name,
					Namespace:    b.Namespace,
					IP:           b.IP,
					NodeName:     b.NodeName,
					HostIP:       b.HostIP,
					IPPool:       b.IPPool,
					IPAMBlock:    b.IPAMBlock,
					Borrowed:     b.Borrowed,
					Status:       b.Phase,
					WorkloadKind: b.WorkloadKind,
					WorkloadName: b.WorkloadName,
					Labels:       p.Labels,
				},
			})
		}
	}

	// Helper to collect pods owned by a workload
	podsForWorkload := func(ns, kind, name string) ([]model.PodBrief, int) {
		var list []model.PodBrief
		ready := 0
		for _, b := range s.podBriefs {
			if b.Namespace == ns && b.WorkloadKind == kind && b.WorkloadName == name {
				list = append(list, b)
				if b.Ready {
					ready++
				}
			}
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		return list, ready
	}

	// 2. Deployments
	if kf == "" || kf == "deployment" || kf == "deploy" || kf == "workload" {
		for _, d := range s.deployments {
			if namespaceFilter != "" && d.Namespace != namespaceFilter {
				continue
			}
			pods, ready := podsForWorkload(d.Namespace, "Deployment", d.Name)
			sc := maxInt(
				matchStr(d.Name, 15),
				matchStr(d.Namespace+"/"+d.Name, 12),
				matchLabels(d.Labels),
			)
			if sc == 0 && q != "" {
				for _, pb := range pods {
					if strings.Contains(strings.ToLower(pb.IP), q) {
						sc = 50
						break
					}
				}
			}
			if sc == 0 {
				continue
			}
			results = append(results, scored{
				score: sc,
				item: model.SearchResultItem{
					Kind:      "Deployment",
					Name:      d.Name,
					Namespace: d.Namespace,
					Status:    fmt.Sprintf("%d/%d Ready", ready, len(pods)),
					Labels:    d.Labels,
					PodCount:  len(pods),
					ReadyPods: ready,
					Pods:      pods,
				},
			})
		}
	}

	// 3. StatefulSets
	if kf == "" || kf == "statefulset" || kf == "sts" || kf == "workload" {
		for _, sts := range s.statefulSets {
			if namespaceFilter != "" && sts.Namespace != namespaceFilter {
				continue
			}
			pods, ready := podsForWorkload(sts.Namespace, "StatefulSet", sts.Name)
			sc := maxInt(matchStr(sts.Name, 14), matchStr(sts.Namespace+"/"+sts.Name, 12), matchLabels(sts.Labels))
			if sc == 0 {
				continue
			}
			results = append(results, scored{
				score: sc,
				item: model.SearchResultItem{
					Kind:      "StatefulSet",
					Name:      sts.Name,
					Namespace: sts.Namespace,
					Status:    fmt.Sprintf("%d/%d Ready", ready, len(pods)),
					Labels:    sts.Labels,
					PodCount:  len(pods),
					ReadyPods: ready,
					Pods:      pods,
				},
			})
		}
	}

	// 4. DaemonSets
	if kf == "" || kf == "daemonset" || kf == "ds" || kf == "workload" {
		for _, ds := range s.daemonSets {
			if namespaceFilter != "" && ds.Namespace != namespaceFilter {
				continue
			}
			pods, ready := podsForWorkload(ds.Namespace, "DaemonSet", ds.Name)
			sc := maxInt(matchStr(ds.Name, 14), matchStr(ds.Namespace+"/"+ds.Name, 12), matchLabels(ds.Labels))
			if sc == 0 {
				continue
			}
			results = append(results, scored{
				score: sc,
				item: model.SearchResultItem{
					Kind:      "DaemonSet",
					Name:      ds.Name,
					Namespace: ds.Namespace,
					Status:    fmt.Sprintf("%d/%d Ready", ready, len(pods)),
					Labels:    ds.Labels,
					PodCount:  len(pods),
					ReadyPods: ready,
					Pods:      pods,
				},
			})
		}
	}

	// 5. Services
	if kf == "" || kf == "service" || kf == "svc" {
		for _, svc := range s.services {
			if namespaceFilter != "" && svc.Namespace != namespaceFilter {
				continue
			}
			ports := make([]string, 0, len(svc.Spec.Ports))
			portMatch := 0
			for _, sp := range svc.Spec.Ports {
				pStr := fmt.Sprintf("%d/%s", sp.Port, sp.Protocol)
				if sp.NodePort > 0 {
					pStr = fmt.Sprintf("%d:%d/%s", sp.Port, sp.NodePort, sp.Protocol)
					portMatch = maxInt(portMatch, matchStr(fmt.Sprintf("%d", sp.NodePort), 10))
				}
				ports = append(ports, pStr)
			}
			sc := maxInt(
				matchStr(svc.Spec.ClusterIP, 20),
				matchStr(svc.Name, 12),
				matchStr(svc.Namespace+"/"+svc.Name, 10),
				portMatch,
			)
			if sc == 0 {
				continue
			}
			// Find backing pods
			var backingPods []model.PodBrief
			readyCnt := 0
			if len(svc.Spec.Selector) > 0 {
				sel := labels.SelectorFromSet(svc.Spec.Selector)
				for k, p := range s.pods {
					if p.Namespace == svc.Namespace && sel.Matches(labels.Set(p.Labels)) {
						b := s.podBriefs[k]
						backingPods = append(backingPods, b)
						if b.Ready {
							readyCnt++
						}
					}
				}
			}
			results = append(results, scored{
				score: sc,
				item: model.SearchResultItem{
					Kind:      "Service",
					Name:      svc.Name,
					Namespace: svc.Namespace,
					IP:        svc.Spec.ClusterIP,
					ClusterIP: svc.Spec.ClusterIP,
					Status:    string(svc.Spec.Type),
					Ports:     ports,
					PodCount:  len(backingPods),
					ReadyPods: readyCnt,
					Pods:      backingPods,
				},
			})
		}
	}

	// 6. Nodes
	if (kf == "" || kf == "node") && namespaceFilter == "" {
		for _, n := range s.nodes {
			ip := NodeInternalIP(n)
			sc := maxInt(matchStr(ip, 18), matchStr(n.Name, 14))
			if sc == 0 {
				continue
			}
			status := "NotReady"
			if IsNodeReady(n) {
				status = "Ready"
			}
			results = append(results, scored{
				score: sc,
				item: model.SearchResultItem{
					Kind:     "Node",
					Name:     n.Name,
					IP:       ip,
					NodeName: n.Name,
					Status:   status,
				},
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		if results[i].item.Kind != results[j].item.Kind {
			return results[i].item.Kind < results[j].item.Kind
		}
		if results[i].item.Namespace != results[j].item.Namespace {
			return results[i].item.Namespace < results[j].item.Namespace
		}
		return results[i].item.Name < results[j].item.Name
	})

	if len(results) > limit {
		results = results[:limit]
	}
	out := make([]model.SearchResultItem, len(results))
	for i := range results {
		out[i] = results[i].item
	}
	return out
}

func maxInt(vals ...int) int {
	m := 0
	for _, v := range vals {
		if v > m {
			m = v
		}
	}
	return m
}
