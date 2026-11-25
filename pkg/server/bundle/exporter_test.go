package bundle

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"calico-netdiag/pkg/model"
)

func TestBuildDiagnosticBundleZip(t *testing.T) {
	in := BundleInput{
		GeneratedAt: time.Date(2026, 11, 25, 20, 30, 0, 0, time.UTC),
		Overview: model.ClusterNetworkOverview{
			K8sVersion:       "v1.31.2",
			CalicoVersion:    "v3.28.0",
			PrimaryEncapMode: model.EncapBGPDirect,
			LinuxDataplane:   "Iptables",
			KubeProxyMode:    model.KubeProxyIptables,
			PodCount:         42,
			Nodes: []model.NodeNetworkStatus{
				{Name: "node-1", InternalIP: "192.168.1.10", PrimaryNIC: "ens4", NICMTU: 1500, TelemetryEngine: model.TelemetryEBPF},
			},
		},
		KernelAudit: &model.ClusterKernelAuditReport{
			OverallStatus: "warning",
			AverageScore:  88,
			WarningCount:  1,
			Nodes: []model.NodeKernelAudit{
				{
					NodeName: "node-1",
					Score:    88,
					Status:   "warning",
					Conntrack: model.ConntrackStats{
						Count: 1200, Max: 131072, UsagePct: 0.92,
					},
					MTU: model.MTUCheck{PrimaryNIC: "ens4", PrimaryMTU: 1500, Status: "pass"},
				},
			},
			Findings: []model.AuditFinding{
				{ID: "f1", NodeName: "node-1", Severity: "warning", Category: "sysctl", Title: "Sample Warning <script>alert(1)</script>"},
			},
		},
		RouteAudit: &model.ClusterRouteAuditReport{
			OverallStatus: "healthy",
			Nodes:         []model.NodeRouteAudit{{NodeName: "node-1", Status: "healthy", TotalRoutes: 15}},
		},
		FocusTrace: &model.TraceFlowResult{
			ID:             "tf-demo123",
			Status:         "REACHABLE",
			TotalLatencyMs: 0.42,
			Protocol:       "TCP",
			DstPort:        8099,
			EncapMode:      model.EncapBGPDirect,
			Source:         model.EndpointIdentity{Namespace: "default", PodName: "src-pod", DisplayName: "default/src-pod", IP: "172.31.1.10"},
			Destination:    model.EndpointIdentity{Namespace: "default", PodName: "dst-pod", DisplayName: "default/dst-pod", IP: "172.31.2.20"},
			VerdictSummary: "端到端路径可达",
		},
	}

	raw, filename, err := BuildDiagnosticBundleZip(in)
	if err != nil {
		t.Fatalf("BuildDiagnosticBundleZip failed: %v", err)
	}
	if !strings.HasPrefix(filename, "calico-plumber-diag-20261125-203000") || !strings.HasSuffix(filename, ".zip") {
		t.Fatalf("unexpected filename: %s", filename)
	}

	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("invalid zip archive: %v", err)
	}

	entries := make(map[string]string)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		entries[f.Name] = string(b)
	}

	required := []string{
		"report.html",
		"README.txt",
		"01-cluster-overview.json",
		"02-kernel-audit.json",
		"03-route-bgp-audit.json",
		"04-ipam-snapshot.json",
		"06-traceflow-history.json",
		"06b-traceflow-focus.json",
	}
	for _, name := range required {
		if _, ok := entries[name]; !ok {
			t.Errorf("missing expected zip entry %s", name)
		}
	}

	// Verify HTML escaping of untrusted strings
	if strings.Contains(entries["report.html"], "<script>alert(1)</script>") {
		t.Fatalf("expected HTML report to escape script tags")
	}
	if !strings.Contains(entries["report.html"], "tf-demo123") {
		t.Fatalf("expected HTML report to contain focus trace id")
	}

	// Verify valid JSON inside 01-cluster-overview.json
	var ov model.ClusterNetworkOverview
	if err := json.Unmarshal([]byte(entries["01-cluster-overview.json"]), &ov); err != nil {
		t.Fatalf("01-cluster-overview.json invalid json: %v", err)
	}
	if ov.K8sVersion != "v1.31.2" {
		t.Fatalf("unexpected overview K8sVersion: %s", ov.K8sVersion)
	}
}
