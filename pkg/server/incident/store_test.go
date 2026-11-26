package incident

import (
	"context"
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
