package healthcheck

import (
	"context"
	"strings"
	"testing"

	"calico-netdiag/pkg/model"
	"calico-netdiag/pkg/server/agentclient"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func boolPtr(b bool) *bool         { return &b }
func int32Ptr(v int32) *int32      { return &v }
func strPtr(s string) *string      { return &s }
func protoPtr(p corev1.Protocol) *corev1.Protocol { return &p }

func TestEvaluateServicesPerPort_MultiPortAndHeadlessZeroReachable(t *testing.T) {
	r := &Runner{}

	// 1. Dual-port Service: 80/TCP refused, 443/TCP OK
	dualSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "everoute-lb-adapter", Name: "lb-adapter-service"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.250.252.80",
			Selector:  map[string]string{"app": "lb-adapter"},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP},
				{Name: "https", Port: 443, TargetPort: intstr.FromInt(443), Protocol: corev1.ProtocolTCP},
			},
		},
	}
	dualSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "everoute-lb-adapter", Name: "lb-adapter-service-abc"},
		Ports: []discoveryv1.EndpointPort{
			{Name: strPtr("http"), Port: int32Ptr(80), Protocol: protoPtr(corev1.ProtocolTCP)},
			{Name: strPtr("https"), Port: int32Ptr(443), Protocol: protoPtr(corev1.ProtocolTCP)},
		},
		Endpoints: []discoveryv1.Endpoint{
			{
				Addresses:  []string{"172.31.226.10"},
				Conditions: discoveryv1.EndpointConditions{Ready: boolPtr(true)},
				TargetRef:  &corev1.ObjectReference{Kind: "Pod", Namespace: "everoute-lb-adapter", Name: "lb-adapter-0"},
			},
		},
	}

	// 2. Headless Service with 0/2 reachable -> must say "本次探测的 2 个后端" and status "broken"
	headlessSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "kubelet"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "None",
			Ports: []corev1.ServicePort{
				{Name: "https-metrics", Port: 10250, TargetPort: intstr.FromInt(10250), Protocol: corev1.ProtocolTCP},
			},
		},
	}
	headlessSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "kubelet-xyz"},
		Ports: []discoveryv1.EndpointPort{
			{Name: strPtr("https-metrics"), Port: int32Ptr(10250), Protocol: protoPtr(corev1.ProtocolTCP)},
		},
		Endpoints: []discoveryv1.Endpoint{
			{Addresses: []string{"192.168.118.206"}, Conditions: discoveryv1.EndpointConditions{Ready: boolPtr(true)}},
			{Addresses: []string{"192.168.118.202"}, Conditions: discoveryv1.EndpointConditions{Ready: boolPtr(true)}},
		},
	}

	// 3. No-endpoints Service -> recommended action must be inspect_empty_backends
	emptySvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "demo", Name: "empty-svc"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.250.252.99",
			Selector:  map[string]string{"app": "missing"},
			Ports:     []corev1.ServicePort{{Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP}},
		},
	}

	epMap := map[string][]*discoveryv1.EndpointSlice{
		"everoute-lb-adapter/lb-adapter-service": {dualSlice},
		"kube-system/kubelet":                    {headlessSlice},
	}

	sum := r.evaluateServicesPerPort(
		context.Background(),
		[]*corev1.Service{dualSvc, headlessSvc, emptySvc},
		epMap,
		"探针 Pod calico-netdiag-probe-zs7qc (mw-worker)",
		"calico-netdiag/calico-netdiag-probe-zs7qc",
		"mw-worker",
		func(targets []agentclient.HostTCPTarget) map[string]agentclient.HostTCPResult {
			res := make(map[string]agentclient.HostTCPResult)
			for _, tgt := range targets {
				switch tgt.Address {
				case "10.250.252.80:80", "172.31.226.10:80":
					res[tgt.ID] = agentclient.HostTCPResult{ID: tgt.ID, OK: false, LatencyMs: 0.4, Error: "dial tcp4: connect: connection refused"}
				case "10.250.252.80:443", "172.31.226.10:443":
					res[tgt.ID] = agentclient.HostTCPResult{ID: tgt.ID, OK: true, LatencyMs: 0.9}
				case "192.168.118.206:10250", "192.168.118.202:10250":
					res[tgt.ID] = agentclient.HostTCPResult{ID: tgt.ID, OK: false, LatencyMs: 1000, Error: "dial tcp4: i/o timeout"}
				}
			}
			return res
		},
	)

	if sum.BrokenServices != 2 || sum.NoEndpointSvcs != 1 {
		t.Fatalf("expected 2 broken + 1 no-endpoints, got broken=%d degraded=%d no-ep=%d", sum.BrokenServices, sum.DegradedServices, sum.NoEndpointSvcs)
	}

	byKey := make(map[string]model.ServiceHealthDetail)
	for _, s := range sum.Services {
		byKey[s.Namespace+"/"+s.Name] = s
	}

	dual := byKey["everoute-lb-adapter/lb-adapter-service"]
	if len(dual.PortChecks) != 2 {
		t.Fatalf("expected 2 port checks for dual-port service, got %d", len(dual.PortChecks))
	}
	if dual.PortChecks[0].ServicePort != 80 || dual.PortChecks[0].Status != "broken" || dual.PortChecks[0].FailureKind != "connection_refused" {
		t.Fatalf("unexpected port 80 verdict: %+v", dual.PortChecks[0])
	}
	if dual.PortChecks[1].ServicePort != 443 || dual.PortChecks[1].Status != "healthy" || dual.PortChecks[1].ClusterIPState != "ok" {
		t.Fatalf("unexpected port 443 verdict: %+v", dual.PortChecks[1])
	}
	if dual.RecommendedAction != "check_port_listening" {
		t.Fatalf("expected check_port_listening, got %s", dual.RecommendedAction)
	}

	hl := byKey["kube-system/kubelet"]
	if hl.Status != "broken" {
		t.Fatalf("expected kubelet 0/2 status=broken, got %s", hl.Status)
	}
	if !strings.Contains(hl.Diagnosis, "本次探测的 2 个后端") || strings.Contains(hl.Diagnosis, "部分后端") {
		t.Fatalf("expected accurate 0/2 wording, got %q", hl.Diagnosis)
	}
	if hl.PortChecks[0].FailureKind != "timeout" {
		t.Fatalf("expected timeout failure kind, got %s", hl.PortChecks[0].FailureKind)
	}

	em := byKey["demo/empty-svc"]
	if em.RecommendedAction != "inspect_empty_backends" {
		t.Fatalf("expected inspect_empty_backends for empty svc, got %s", em.RecommendedAction)
	}

	// Verify overall score/status never reports 100/healthy when broken services exist
	mesh := model.PodMeshHealth{CrossNodeTotal: 2, CrossNodeHealthy: 2, SelfCheckTotal: 2, SelfCheckHealthy: 2, TotalPairs: 4, HealthyPairs: 4}
	pn := model.PodNodeHealth{TotalChecks: 4, HealthyChecks: 4}
	ps := model.PodServiceHealth{TotalChecks: 6, HealthyChecks: 6}
	score, status := computeOverallScoreAndStatus(mesh, pn, ps, sum, model.InfraHealthSummary{})
	if status == "healthy" || score >= 90 {
		t.Fatalf("expected degraded and score <= 89 when broken services exist, got score=%d status=%s", score, status)
	}
}
