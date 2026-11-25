package audit

import (
	"testing"

	"calico-netdiag/pkg/model"
)

func TestAggregateKernelAudit_CrossNodeDrift(t *testing.T) {
	nodes := []model.NodeKernelAudit{
		{
			NodeName: "node-a",
			Score:    100,
			Status:   "healthy",
			MTU:      model.MTUCheck{PrimaryNIC: "ens4", PrimaryMTU: 1500, Status: "pass"},
			Sysctls: []model.SysctlCheck{
				{Key: "net.ipv4.conf.all.rp_filter", Value: "0", Status: "pass"},
			},
		},
		{
			NodeName: "node-b",
			Score:    90,
			Status:   "warning",
			MTU:      model.MTUCheck{PrimaryNIC: "ens4", PrimaryMTU: 9000, Status: "pass"},
			Sysctls: []model.SysctlCheck{
				{Key: "net.ipv4.conf.all.rp_filter", Value: "1", Status: "critical"},
			},
			Findings: []model.AuditFinding{
				{ID: "rp-strict", Severity: "critical", Category: "sysctl", Title: "rp_filter=1"},
			},
		},
	}

	rep := AggregateKernelAudit(model.EncapBGPDirect, nodes)
	if rep.OverallStatus != "critical" {
		t.Fatalf("expected critical overall status, got %s", rep.OverallStatus)
	}
	if rep.AverageScore != 95 {
		t.Fatalf("expected average score 95, got %d", rep.AverageScore)
	}
	foundMTUDrift := false
	foundRPDrift := false
	for _, f := range rep.Findings {
		if f.ID == "cluster-primary-mtu-drift" {
			foundMTUDrift = true
		}
		if f.ID == "cluster-rpfilter-drift" {
			foundRPDrift = true
		}
	}
	if !foundMTUDrift {
		t.Fatalf("expected cluster-primary-mtu-drift finding")
	}
	if !foundRPDrift {
		t.Fatalf("expected cluster-rpfilter-drift finding")
	}
}

func TestCompareClusterRoutes_HealthyBaseline(t *testing.T) {
	ipam := model.IPAMSnapshot{
		Pools: []model.IPPoolSummary{
			{Name: "default-ipv4-ippool", CIDR: "172.31.0.0/16", EncapMode: model.EncapBGPDirect},
		},
		Blocks: []model.IPAMBlockDetail{
			{Name: "blk-a", CIDR: "172.31.225.0/26", PoolName: "default-ipv4-ippool", AffinityNode: "node-a", AffinityState: "confirmed", AllocatedSlots: 1},
			{Name: "blk-b", CIDR: "172.31.226.0/26", PoolName: "default-ipv4-ippool", AffinityNode: "node-b", AffinityState: "confirmed", AllocatedSlots: 1},
		},
	}
	pods := []model.PodBrief{
		{Namespace: "default", Name: "app-a", IP: "172.31.225.10", NodeName: "node-a", Phase: "Running"},
		{Namespace: "default", Name: "app-b", IP: "172.31.226.20", NodeName: "node-b", Phase: "Running"},
	}
	inputs := []NodeRouteInput{
		{
			NodeName:   "node-a",
			InternalIP: "192.168.1.10",
			Ready:      true,
			Routes: []model.HostRouteEntry{
				{Dst: "172.31.225.0/26", Type: "blackhole", Protocol: "bird"},
				{Dst: "172.31.226.0/26", Gateway: "192.168.1.11", Dev: "ens4", Type: "unicast", Protocol: "bird"},
				{Dst: "172.31.225.10/32", Dev: "cali1111", Type: "unicast", Protocol: "calico-felix"},
			},
		},
		{
			NodeName:   "node-b",
			InternalIP: "192.168.1.11",
			Ready:      true,
			Routes: []model.HostRouteEntry{
				{Dst: "172.31.226.0/26", Type: "blackhole", Protocol: "bird"},
				{Dst: "172.31.225.0/26", Gateway: "192.168.1.10", Dev: "ens4", Type: "unicast", Protocol: "bird"},
				{Dst: "172.31.226.20/32", Dev: "cali2222", Type: "unicast", Protocol: "calico-felix"},
			},
		},
	}

	rep := CompareClusterRoutes(model.EncapBGPDirect, inputs, pods, ipam, nil)
	if rep.OverallStatus != "healthy" || rep.TotalDrifts != 0 {
		t.Fatalf("expected healthy with 0 drifts, got status=%s drifts=%d (%+v)", rep.OverallStatus, rep.TotalDrifts, rep.Drifts)
	}
}

func TestCompareClusterRoutes_DetectsDriftsAndAsymmetry(t *testing.T) {
	ipam := model.IPAMSnapshot{
		Pools: []model.IPPoolSummary{
			{Name: "default-ipv4-ippool", CIDR: "172.31.0.0/16", EncapMode: model.EncapBGPDirect},
		},
		Blocks: []model.IPAMBlockDetail{
			{Name: "blk-a", CIDR: "172.31.225.0/26", PoolName: "default-ipv4-ippool", AffinityNode: "node-a", AffinityState: "confirmed", AllocatedSlots: 1},
			{Name: "blk-b", CIDR: "172.31.226.0/26", PoolName: "default-ipv4-ippool", AffinityNode: "node-b", AffinityState: "confirmed", AllocatedSlots: 1},
		},
	}
	pods := []model.PodBrief{
		{Namespace: "default", Name: "app-a", IP: "172.31.225.10", NodeName: "node-a", Phase: "Running"},
		{Namespace: "default", Name: "app-b", IP: "172.31.226.20", NodeName: "node-b", Phase: "Running"},
	}
	inputs := []NodeRouteInput{
		{
			NodeName:   "node-a",
			InternalIP: "192.168.1.10",
			Ready:      true,
			Routes: []model.HostRouteEntry{
				{Dst: "172.31.225.0/26", Type: "blackhole", Protocol: "bird"},
				{Dst: "172.31.299.0/26", Type: "blackhole", Protocol: "bird"},                                         // invalid CIDR ignored or unexpected
				{Dst: "172.31.200.0/26", Type: "blackhole", Protocol: "bird"},                                         // unexpected_blackhole
				{Dst: "172.31.226.0/26", Gateway: "192.168.1.11", Dev: "ens4", Type: "unicast", Protocol: "bird"},     // direct
				{Dst: "172.31.225.10/32", Dev: "cali1111", Type: "unicast", Protocol: "calico-felix"},
				{Dst: "172.31.225.99/32", Dev: "calidead", Type: "unicast", Protocol: "calico-felix"},                 // ghost_pod_route
			},
		},
		{
			NodeName:   "node-b",
			InternalIP: "192.168.1.11",
			Ready:      true,
			Routes: []model.HostRouteEntry{
				// missing blackhole for 172.31.226.0/26!
				// asymmetric path: node-b routes to node-a via tunl0 (ipip) while node-a routes via ens4 (direct)!
				{Dst: "172.31.225.0/26", Gateway: "192.168.1.10", Dev: "tunl0", Type: "unicast", Protocol: "bird"},
				// missing local Pod /32 route for 172.31.226.20!
			},
		},
	}

	rep := CompareClusterRoutes(model.EncapBGPDirect, inputs, pods, ipam, nil)
	if rep.OverallStatus != "critical" {
		t.Fatalf("expected critical status, got %s", rep.OverallStatus)
	}

	kinds := make(map[string]bool)
	for _, d := range rep.Drifts {
		kinds[d.Kind] = true
	}
	for _, want := range []string{"unexpected_blackhole", "ghost_pod_route", "missing_blackhole", "missing_pod", "asymmetric_path"} {
		if !kinds[want] {
			t.Errorf("expected drift kind %q not found in %+v", want, rep.Drifts)
		}
	}
	if rep.AsymmetricCount != 1 {
		t.Errorf("expected AsymmetricCount=1, got %d", rep.AsymmetricCount)
	}
	if rep.GhostPodCount != 1 {
		t.Errorf("expected GhostPodCount=1, got %d", rep.GhostPodCount)
	}
}
