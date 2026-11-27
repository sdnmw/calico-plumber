package incident

import (
	"context"
	"strings"
	"testing"
	"time"

	"calico-netdiag/pkg/model"
)

func TestIncidentAutoIngestAndRecoveryDiff(t *testing.T) {
	st := NewStore("calico-netdiag", nil, nil, nil)
	t1 := time.Now().Add(-2 * time.Minute)
	repFail := &model.GlobalHealthReport{
		CheckedAt: t1,
		ServiceBackends: model.ServiceBackendSummary{
			Services: []model.ServiceHealthDetail{
				{
					Namespace: "everoute-lb-adapter",
					Name:      "lb-adapter-service",
					ClusterIP: "10.250.252.80",
					Status:    "broken",
					PortChecks: []model.ServicePortHealth{
						{
							ServicePort:        80,
							Protocol:           "TCP",
							ResolvedTargetPort: 80,
							Status:             "broken",
							FailureKind:        "connection_refused",
							FailureSummary:     "连接被拒绝 (Connection Refused)",
							ProbeSourcePod:     "calico-netdiag/calico-netdiag-probe-zs7qc",
							ProbeSourceNode:    "mw-worker",
							Backends: []model.BackendEndpointHealth{
								{PodNamespace: "everoute-lb-adapter", PodName: "lb-0", PodIP: "172.31.226.10", ProbeOK: false},
							},
						},
					},
				},
			},
		},
	}

	st.IngestHealthReport(repFail)
	list := st.List(context.Background())
	if len(list) != 1 || list[0].Status != "active" || list[0].FailureCount != 1 {
		t.Fatalf("expected 1 active incident, got %+v", list)
	}

	// Second failure increments count
	repFail.CheckedAt = time.Now().Add(-1 * time.Minute)
	st.IngestHealthReport(repFail)
	list = st.List(context.Background())
	if list[0].FailureCount != 2 {
		t.Fatalf("expected FailureCount=2, got %d", list[0].FailureCount)
	}

	// Recovery marks intermittent
	repOK := &model.GlobalHealthReport{
		CheckedAt: time.Now(),
		ServiceBackends: model.ServiceBackendSummary{
			Services: []model.ServiceHealthDetail{
				{
					Namespace: "everoute-lb-adapter",
					Name:      "lb-adapter-service",
					Status:    "healthy",
					PortChecks: []model.ServicePortHealth{
						{ServicePort: 80, Protocol: "TCP", Status: "healthy"},
					},
				},
			},
		},
	}
	st.IngestHealthReport(repOK)
	list = st.List(context.Background())
	if list[0].Status != "intermittent" {
		t.Fatalf("expected intermittent status after multi-fail recovery, got %s", list[0].Status)
	}

	// Verify Diff builder
	diff := BuildIncidentDiff(list[0], model.IncidentRetestRecord{
		ClusterIPOK:     true,
		ClusterIPLatMs:  0.82,
		DirectBackendOK: true,
		DirectLatMs:     0.65,
		CurrentBackends: []string{"everoute-lb-adapter/lb-1 (172.31.226.11)"},
	}, true, true)
	if diff.StatusAfter != "recovered" || len(diff.BackendsAdded) != 1 || len(diff.BackendsRemoved) != 1 {
		t.Fatalf("unexpected diff: %+v", diff)
	}
}

func TestDegradedIncidentNeverSaysNormalAndBuildsRecoveryNarrative(t *testing.T) {
	st := NewStore("calico-netdiag", nil, nil, nil)
	t1 := time.Now().Add(-3 * time.Minute)
	// Even if upstream mistakenly passes FailureSummary="正常" on a degraded port, Store must sanitize it.
	st.IngestHealthReport(&model.GlobalHealthReport{
		CheckedAt: t1,
		ServiceBackends: model.ServiceBackendSummary{
			Services: []model.ServiceHealthDetail{
				{
					Namespace: "calico-netdiag",
					Name:      "calico-netdiag-server",
					ClusterIP: "10.250.253.88",
					Status:    "degraded",
					PortChecks: []model.ServicePortHealth{
						{
							ServicePort:        8080,
							Protocol:           "TCP",
							ResolvedTargetPort: 8080,
							Status:             "degraded",
							FailureSummary:     "正常",
							TotalBackends:      2,
							ReadyBackends:      1,
							TestedBackends:     1,
							ReachableBackends:  1,
							Backends: []model.BackendEndpointHealth{
								{PodNamespace: "calico-netdiag", PodName: "server-new", PodIP: "172.31.226.40", K8sReady: false},
							},
						},
					},
				},
			},
		},
	})

	list := st.List(context.Background())
	if len(list) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(list))
	}
	if list[0].InitialFailureSummary == "正常" || strings.Contains(list[0].Title, "正常") {
		t.Fatalf("incident title/summary must never say 正常: title=%q initSummary=%q", list[0].Title, list[0].InitialFailureSummary)
	}
	if !strings.Contains(list[0].InitialBackendState, "Ready 1/2") {
		t.Fatalf("expected InitialBackendState to capture Ready 1/2, got %q", list[0].InitialBackendState)
	}

	// Simulate recovery + Ready=false -> Ready=true timeline event
	st.IngestHealthReport(&model.GlobalHealthReport{
		CheckedAt: time.Now(),
		ServiceBackends: model.ServiceBackendSummary{
			Services: []model.ServiceHealthDetail{
				{
					Namespace: "calico-netdiag",
					Name:      "calico-netdiag-server",
					Status:    "healthy",
					PortChecks: []model.ServicePortHealth{
						{ServicePort: 8080, Protocol: "TCP", Status: "healthy"},
					},
				},
			},
		},
	})
	inc := st.List(context.Background())[0]
	inc.Timeline = []model.ClusterChangeEvent{
		{Timestamp: time.Now(), Category: "pod_rebuild", Summary: "Pod 状态变化：Ready=false → Ready=true"},
	}
	narrative := BuildRecoveryNarrative(inc)
	if !strings.Contains(narrative, "首次巡检记录为降级") || !strings.Contains(narrative, "恢复 Ready") || !strings.Contains(narrative, "当前已恢复") {
		t.Fatalf("unexpected recovery narrative: %q", narrative)
	}

	// Missing evidence fallback -> "原因待确认"
	noEv := model.IncidentSnapshot{InitialStatus: "degraded", Status: "recovered"}
	noEvNarr := BuildRecoveryNarrative(noEv)
	if !strings.Contains(noEvNarr, "原因待确认") {
		t.Fatalf("expected 原因待确认 fallback, got %q", noEvNarr)
	}
}
