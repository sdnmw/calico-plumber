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
	droppedCount := 0
	for _, h := range res.ForwardHops {
		if h.Status == "DROPPED" {
			droppedCount++
			if h.Component != "ingress_policy" {
				t.Fatalf("expected only ingress_policy to be DROPPED, got %s (%s)", h.Component, h.Title)
			}
		}
		if (h.Component == "host_veth" && h.Lane == "dest-node") || h.Component == "dst_pod" {
			if h.Status != "SKIPPED" {
				t.Fatalf("expected downstream hop %s to be SKIPPED after ingress policy drop, got %s", h.Title, h.Status)
			}
		}
	}
	if droppedCount != 1 {
		t.Fatalf("expected exactly 1 DROPPED hop, got %d", droppedCount)
	}
}

func TestStitchTraceHostNetworkServiceNoFalseDrop(t *testing.T) {
	now := time.Now()
	t0 := now.UnixNano()
	in := StitchInput{
		ID:                "tf-apiserver",
		StartedAt:         now,
		CompletedAt:       now.Add(1 * time.Millisecond),
		Protocol:          "TCP",
		SrcPort:           49743,
		DstPort:           443,
		EncapMode:         model.EncapBGPDirect,
		CrossNode:         false,
		IsService:         true,
		TargetHostNetwork: true,
		Source:            model.EndpointIdentity{IP: "172.31.226.40", Kind: "Pod", Namespace: "sks-system", PodName: "cluster-quota-enforcer", NodeName: "master-1", DisplayName: "sks-system/cluster-quota-enforcer"},
		Destination:       model.EndpointIdentity{IP: "10.250.252.1", Kind: "Service", Namespace: "default", ServiceName: "kubernetes", DisplayName: "svc/default/kubernetes"},
		SrcNode:           "master-1",
		DstNode:           "master-1",
		SrcResp: &model.AgentTraceCaptureResponse{
			NodeName:        "master-1",
			KubeProxyMode:   model.KubeProxyIptables,
			HostVeth:        "cali9316adc81a5",
			EgressInterface: "lo",
			EncapDetected:   "Host Local (lo)",
			RouteSummary:    "local 192.168.118.206 dev lo table local",
			NATRuleMatched:  "iptables KUBE-SERVICES → KUBE-SVC-NPX46M4PTMTKRN6Y → 192.168.118.206:6443",
			TranslatedDstIP: "192.168.118.206",
			TranslatedPort:  6443,
			InjectResult:    "CONNECTED",
			CapturedPackets: []model.CapturedTracePacket{
				{Stage: "src_pod_eth0", Direction: "forward", Interface: "eth0", TimestampNs: t0, SrcIP: "172.31.226.40", DstIP: "10.250.252.1", Summary: "SYN"},
				{Stage: "src_cali_veth", Direction: "forward", Interface: "cali9316adc81a5", TimestampNs: t0 + 40000, Summary: "SYN"},
				{Stage: "dst_host_lo", Direction: "forward", Interface: "lo", TimestampNs: t0 + 80000, SrcIP: "172.31.226.40", DstIP: "192.168.118.206", DstPort: 6443, Summary: "SYN lo"},
				{Stage: "src_pod_eth0", Direction: "return", Interface: "eth0", TimestampNs: t0 + 130000, TCPFlags: "SYN,ACK", Summary: "SYN-ACK"},
			},
		},
		ResolveIP: func(ip string, port uint16) model.EndpointIdentity {
			return model.EndpointIdentity{IP: ip, Port: port, Kind: "Node", NodeName: "master-1", DisplayName: "node/master-1"}
		},
	}

	res := StitchTraceResult(in)
	if res.Status != "REACHABLE" {
		t.Fatalf("expected REACHABLE, got %s", res.Status)
	}
	foundHostStack := false
	for _, h := range res.ForwardHops {
		if h.Status == "DROPPED" {
			t.Fatalf("unexpected false-positive DROPPED on hop %q (%s)", h.Title, h.Component)
		}
		if h.Title == "Dest Calico veth" {
			t.Fatalf("hostNetwork target must not render Dest Calico veth card")
		}
		if strings.Contains(h.Title, "Host Local Stack") && h.Status == "DELIVERED" {
			foundHostStack = true
		}
	}
	if !foundHostStack {
		t.Fatalf("expected Host Local Stack (lo / hostNetwork) DELIVERED hop")
	}
}

func TestStitchTraceExternalSNATNoFalseDrop(t *testing.T) {
	now := time.Now()
	t0 := now.UnixNano()
	in := StitchInput{
		ID:          "tf-ext-snat",
		StartedAt:   now,
		CompletedAt: now.Add(3 * time.Millisecond),
		Protocol:    "TCP",
		SrcPort:     54321,
		DstPort:     80,
		EncapMode:   model.EncapBGPDirect,
		IsExternal:  true,
		Source:      model.EndpointIdentity{IP: "172.31.226.50", Kind: "Pod", Namespace: "lab", PodName: "client", NodeName: "worker-1", DisplayName: "lab/client"},
		Destination: model.EndpointIdentity{IP: "192.168.112.51", Port: 80, Kind: "External", DisplayName: "192.168.112.51"},
		SrcNode:     "worker-1",
		SrcResp: &model.AgentTraceCaptureResponse{
			NodeName:        "worker-1",
			HostVeth:        "caliabc",
			EgressInterface: "ens4",
			EncapDetected:   "Direct BGP",
			MasqueradeNAT:   true,
			InjectResult:    "CONNECTED",
			CapturedPackets: []model.CapturedTracePacket{
				{Stage: "src_pod_eth0", Direction: "forward", Interface: "eth0", TimestampNs: t0},
				{Stage: "src_cali_veth", Direction: "forward", Interface: "caliabc", TimestampNs: t0 + 50000},
				// src_phy_nic intentionally omitted (e.g. MASQUERADE randomized port not captured)
				{Stage: "src_pod_eth0", Direction: "return", Interface: "eth0", TimestampNs: t0 + 1500000, TCPFlags: "SYN,ACK"},
			},
		},
	}

	res := StitchTraceResult(in)
	if res.Status != "REACHABLE" {
		t.Fatalf("expected REACHABLE, got %s", res.Status)
	}
	nicHopOK := false
	for _, h := range res.ForwardHops {
		if h.Status == "DROPPED" {
			t.Fatalf("unexpected false-positive DROPPED on hop %q", h.Title)
		}
		if h.Component == "host_nic" && h.Status == "NAT" {
			nicHopOK = true
		}
	}
	if !nicHopOK {
		t.Fatalf("expected Source Host NIC Egress to be NAT under Calico SNAT")
	}
}

func TestStitchTraceMultiReplicaServiceUncapturedVeth(t *testing.T) {
	now := time.Now()
	t0 := now.UnixNano()
	in := StitchInput{
		ID:          "tf-svc-multi",
		StartedAt:   now,
		CompletedAt: now.Add(2 * time.Millisecond),
		Protocol:    "TCP",
		SrcPort:     55000,
		DstPort:     53,
		EncapMode:   model.EncapBGPDirect,
		CrossNode:   true,
		IsService:   true,
		Source:      model.EndpointIdentity{IP: "172.31.226.50", Kind: "Pod", DisplayName: "lab/client"},
		Destination: model.EndpointIdentity{IP: "10.250.252.10", Kind: "Service", DisplayName: "svc/kube-system/kube-dns"},
		SrcNode:     "worker-1",
		DstNode:     "master-1",
		SrcResp: &model.AgentTraceCaptureResponse{
			NodeName:        "worker-1",
			KubeProxyMode:   model.KubeProxyIptables,
			HostVeth:        "caliabc",
			EgressInterface: "ens4",
			InjectResult:    "CONNECTED",
			CapturedPackets: []model.CapturedTracePacket{
				{Stage: "src_pod_eth0", Direction: "forward", Interface: "eth0", TimestampNs: t0},
				{Stage: "src_cali_veth", Direction: "forward", Interface: "caliabc", TimestampNs: t0 + 50000},
				{Stage: "src_phy_nic", Direction: "forward", Interface: "ens4", TimestampNs: t0 + 100000},
				{Stage: "src_pod_eth0", Direction: "return", Interface: "eth0", TimestampNs: t0 + 800000, TCPFlags: "SYN,ACK"},
			},
		},
		DstResp: &model.AgentTraceCaptureResponse{
			NodeName: "master-1",
			CapturedPackets: []model.CapturedTracePacket{
				{Stage: "dst_phy_nic", Direction: "forward", Interface: "ens4", TimestampNs: t0 + 250000},
				// dst_cali_veth and dst_pod_eth0 omitted because kube-proxy selected another replica
			},
		},
	}

	res := StitchTraceResult(in)
	if res.Status != "REACHABLE" {
		t.Fatalf("expected REACHABLE, got %s", res.Status)
	}
	for _, h := range res.ForwardHops {
		if h.Status == "DROPPED" {
			t.Fatalf("unexpected false-positive DROPPED on hop %q when end-to-end CONNECTED", h.Title)
		}
		if h.Title == "Dest Calico veth" && h.Status != "PASSED" {
			t.Fatalf("expected Dest Calico veth to be PASSED, got %s", h.Status)
		}
		if h.Component == "dst_pod" && h.Status != "DELIVERED" {
			t.Fatalf("expected Destination Pod eth0 to be DELIVERED, got %s", h.Status)
		}
	}
}

func TestStitchTraceSameNodeIngressPolicyDrop(t *testing.T) {
	now := time.Now()
	t0 := now.UnixNano()
	in := StitchInput{
		ID:          "tf-same-node-drop",
		StartedAt:   now,
		CompletedAt: now.Add(2500 * time.Millisecond),
		Protocol:    "TCP",
		SrcPort:     56000,
		DstPort:     8080,
		EncapMode:   model.EncapBGPDirect,
		CrossNode:   false,
		Source:      model.EndpointIdentity{IP: "172.31.226.10", Kind: "Pod", DisplayName: "lab/client"},
		Destination: model.EndpointIdentity{IP: "172.31.226.20", Kind: "Pod", DisplayName: "lab/backend"},
		SrcNode:     "worker-1",
		DstNode:     "worker-1",
		DstPolicies: []model.MatchedPolicySummary{{Name: "deny-backend-ingress"}},
		SrcResp: &model.AgentTraceCaptureResponse{
			NodeName:        "worker-1",
			HostVeth:        "cali111",
			EgressInterface: "cali222",
			InjectResult:    "TIMEOUT_OR_ERROR",
			CapturedPackets: []model.CapturedTracePacket{
				{Stage: "src_pod_eth0", Direction: "forward", Interface: "eth0", TimestampNs: t0},
				{Stage: "src_cali_veth", Direction: "forward", Interface: "cali111", TimestampNs: t0 + 50000},
			},
		},
	}

	res := StitchTraceResult(in)
	if res.Status != "DROPPED" {
		t.Fatalf("expected DROPPED, got %s", res.Status)
	}
	if !strings.Contains(res.RootCauseHint, "Ingress NetworkPolicy") {
		t.Fatalf("expected same-node Ingress NetworkPolicy hint, got %q", res.RootCauseHint)
	}
	for _, h := range res.ForwardHops {
		if h.Component == "egress_policy" && h.Status != "PASSED" {
			t.Fatalf("expected egress_policy PASSED when src has no policy and dst has ingress policy, got %s", h.Status)
		}
		if h.Component == "ingress_policy" && h.Status != "DROPPED" {
			t.Fatalf("expected ingress_policy DROPPED, got %s", h.Status)
		}
		if h.Title == "Dest Calico veth" && h.Status != "SKIPPED" {
			t.Fatalf("expected Dest Calico veth SKIPPED, got %s", h.Status)
		}
	}
}
