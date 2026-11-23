package traceflow

import (
	"strings"
	"testing"
	"time"

	"calico-netdiag/pkg/model"
)

func TestStitchTraceCrossNodeVXLANSuccess(t *testing.T) {
	now := time.Now()
	t0 := now.UnixNano()
	in := StitchInput{
		ID:          "tf-test1",
		StartedAt:   now,
		CompletedAt: now.Add(5 * time.Millisecond),
		Protocol:    "TCP",
		SrcPort:     51234,
		DstPort:     8080,
		EncapMode:   model.EncapVXLANAlways,
		CrossNode:   true,
		IsService:   true,
		Source:      model.EndpointIdentity{IP: "172.31.226.68", Kind: "Pod", Namespace: "demo", PodName: "client", NodeName: "node-a", DisplayName: "demo/client"},
		Destination: model.EndpointIdentity{IP: "10.250.252.92", Kind: "Service", Namespace: "demo", ServiceName: "api", DisplayName: "svc/demo/api"},
		SrcNode:     "node-a",
		DstNode:     "node-b",
		SrcResp: &model.AgentTraceCaptureResponse{
			NodeName:        "node-a",
			KubeProxyMode:   model.KubeProxyIptables,
			HostVeth:        "cali111",
			EgressInterface: "vxlan.calico",
			EncapDetected:   "VXLAN (vxlan.calico)",
			RouteSummary:    "172.31.225.24 via 172.31.225.0 dev vxlan.calico onlink",
			NATRuleMatched:  "iptables KUBE-SERVICES → KUBE-SVC-X → 172.31.225.24:8080",
			TranslatedDstIP: "172.31.225.24",
			TranslatedPort:  8080,
			InjectResult:    "CONNECTED",
			CapturedPackets: []model.CapturedTracePacket{
				{Stage: "src_pod_eth0", Direction: "forward", Interface: "eth0", TimestampNs: t0, SrcIP: "172.31.226.68", DstIP: "10.250.252.92", Summary: "SYN"},
				{Stage: "src_cali_veth", Direction: "forward", Interface: "cali111", TimestampNs: t0 + 100000, Summary: "SYN"},
				{Stage: "src_tunnel", Direction: "forward", Interface: "vxlan.calico", TimestampNs: t0 + 200000, Summary: "SYN inner"},
				{Stage: "src_phy_nic", Direction: "forward", Interface: "ens4", TimestampNs: t0 + 300000, OuterHeader: "VXLAN VNI=4096 192.168.118.202:50000 → 192.168.118.206:4789", Summary: "SYN outer"},
				{Stage: "src_pod_eth0", Direction: "return", Interface: "eth0", TimestampNs: t0 + 1200000, TCPFlags: "SYN,ACK", Summary: "SYN-ACK"},
			},
		},
		DstResp: &model.AgentTraceCaptureResponse{
			NodeName: "node-b",
			CapturedPackets: []model.CapturedTracePacket{
				{Stage: "dst_phy_nic", Direction: "forward", Interface: "ens4", TimestampNs: t0 + 500000, OuterHeader: "VXLAN VNI=4096", Summary: "SYN arrive"},
				{Stage: "dst_tunnel", Direction: "forward", Interface: "vxlan.calico", TimestampNs: t0 + 600000, Summary: "SYN decap"},
				{Stage: "dst_cali_veth", Direction: "forward", Interface: "cali222", TimestampNs: t0 + 700000, Summary: "SYN veth"},
				{Stage: "dst_pod_eth0", Direction: "forward", Interface: "eth0", TimestampNs: t0 + 800000, Summary: "SYN pod"},
			},
		},
		ResolveIP: func(ip string, port uint16) model.EndpointIdentity {
			return model.EndpointIdentity{IP: ip, Port: port, Kind: "Pod", Namespace: "demo", PodName: "api-1", DisplayName: "demo/api-1"}
		},
	}

	res := StitchTraceResult(in)
	if res.Status != "REACHABLE" {
		t.Fatalf("expected REACHABLE, got %s (%s)", res.Status, res.VerdictSummary)
	}
	if res.ResolvedBackend == nil || res.ResolvedBackend.IP != "172.31.225.24" {
		t.Fatalf("expected resolved DNAT backend 172.31.225.24")
	}
	hasVXLAN := false
	for _, h := range res.ForwardHops {
		if strings.Contains(h.OuterHeader, "VXLAN VNI=4096") {
			hasVXLAN = true
		}
	}
	if !hasVXLAN {
		t.Fatalf("expected VXLAN outer header preserved in forward hops")
	}
}

func TestStitchTraceDropAtDestIngressPolicy(t *testing.T) {
	now := time.Now()
	t0 := now.UnixNano()
	in := StitchInput{
		ID:          "tf-drop",
		StartedAt:   now,
		CompletedAt: now.Add(2500 * time.Millisecond),
		Protocol:    "TCP",
		SrcPort:     52000,
		DstPort:     80,
		EncapMode:   model.EncapBGPDirect,
		CrossNode:   true,
		Source:      model.EndpointIdentity{IP: "172.31.226.10", Kind: "Pod", DisplayName: "demo/a"},
		Destination: model.EndpointIdentity{IP: "172.31.225.10", Kind: "Pod", DisplayName: "demo/b"},
		SrcNode:     "node-a",
		DstNode:     "node-b",
		DstPolicies: []model.MatchedPolicySummary{{Name: "deny-all-ingress"}},
		SrcResp: &model.AgentTraceCaptureResponse{
			NodeName:        "node-a",
			HostVeth:        "cali111",
			EgressInterface: "ens4",
			InjectResult:    "TIMEOUT_OR_ERROR",
			CapturedPackets: []model.CapturedTracePacket{
				{Stage: "src_pod_eth0", Direction: "forward", Interface: "eth0", TimestampNs: t0},
				{Stage: "src_cali_veth", Direction: "forward", Interface: "cali111", TimestampNs: t0 + 100000},
				{Stage: "src_phy_nic", Direction: "forward", Interface: "ens4", TimestampNs: t0 + 200000},
			},
		},
		DstResp: &model.AgentTraceCaptureResponse{
			NodeName: "node-b",
			CapturedPackets: []model.CapturedTracePacket{
				{Stage: "dst_phy_nic", Direction: "forward", Interface: "ens4", TimestampNs: t0 + 400000},
			},
		},
	}

	res := StitchTraceResult(in)
	if res.Status != "DROPPED" {
		t.Fatalf("expected DROPPED, got %s", res.Status)
	}
	if !strings.Contains(res.RootCauseHint, "Ingress NetworkPolicy") {
		t.Fatalf("expected Ingress NetworkPolicy drop localization, got %q", res.RootCauseHint)
	}
}
