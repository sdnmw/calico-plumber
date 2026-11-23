package k8s

import (
	"testing"

	"calico-netdiag/pkg/model"
)

func TestClassifyAndEffectiveEncapModes(t *testing.T) {
	cases := []struct {
		name       string
		ipip       string
		vxlan      string
		bgp        bool
		wantPool   model.CalicoEncapMode
		srcCIDR    string
		dstIP      string
		wantActual model.CalicoEncapMode
	}{
		{"BGP direct", "Never", "Never", true, model.EncapBGPDirect, "192.168.112.0/20", "192.168.118.206", model.EncapBGPDirect},
		{"IPIP Always", "Always", "Never", true, model.EncapIPIPAlways, "192.168.112.0/20", "192.168.118.206", model.EncapIPIPAlways},
		{"IPIP CrossSubnet same subnet", "CrossSubnet", "Never", true, model.EncapIPIPCrossSubnet, "192.168.112.0/20", "192.168.118.206", model.EncapBGPDirect},
		{"IPIP CrossSubnet different subnet", "CrossSubnet", "Never", true, model.EncapIPIPCrossSubnet, "192.168.112.0/20", "10.10.20.5", model.EncapIPIPAlways},
		{"VXLAN Always", "Never", "Always", false, model.EncapVXLANAlways, "192.168.112.0/20", "192.168.118.206", model.EncapVXLANAlways},
		{"VXLAN CrossSubnet same subnet", "Never", "CrossSubnet", false, model.EncapVXLANCrossSubnet, "192.168.112.0/20", "192.168.118.206", model.EncapBGPDirect},
		{"VXLAN CrossSubnet different subnet", "Never", "CrossSubnet", false, model.EncapVXLANCrossSubnet, "192.168.112.0/20", "10.20.30.40", model.EncapVXLANAlways},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPool := ClassifyEncapMode(tc.ipip, tc.vxlan, tc.bgp)
			if gotPool != tc.wantPool {
				t.Fatalf("ClassifyEncapMode=%q want %q", gotPool, tc.wantPool)
			}
			gotActual := EffectiveCrossNodeEncap(gotPool, tc.srcCIDR, tc.dstIP)
			if gotActual != tc.wantActual {
				t.Fatalf("EffectiveCrossNodeEncap=%q want %q", gotActual, tc.wantActual)
			}
		})
	}
}

func TestBuildIPAMSnapshotClassification(t *testing.T) {
	i0, i1, i2, i3 := 0, 1, 2, 3
	allocs := make([]*int, 64)
	allocs[0] = &i0 // tunnel
	allocs[1] = &i1 // local active pod
	allocs[2] = &i2 // borrowed pod on node-2
	allocs[3] = &i3 // orphan allocation (pod deleted)

	pools := []RawIPPool{{
		Name: "default-ipv4-ippool", CIDR: "172.31.224.0/20", BlockSize: 26,
		IPIPMode: "Never", VXLANMode: "Never", NATOutgoing: true,
	}}
	blocks := []RawIPAMBlock{{
		Name: "172-31-225-0-26", CIDR: "172.31.225.0/26", Affinity: "host:node-1",
		Allocations: allocs,
		Attributes: []IPAMAttr{
			{HandleID: "ipip-tunnel-addr-node-1"},
			{HandleID: "k8s-pod-network.aaa", Namespace: "demo", Pod: "web-1", Node: "node-1"},
			{HandleID: "k8s-pod-network.bbb", Namespace: "demo", Pod: "api-2", Node: "node-2"},
			{HandleID: "k8s-pod-network.ccc", Namespace: "demo", Pod: "ghost-3", Node: "node-1"},
		},
	}}
	affs := []RawBlockAffinity{{Name: "node-1-block", CIDR: "172.31.225.0/26", Node: "node-1", State: "confirmed"}}

	podsByIP := map[string]model.PodBrief{
		"172.31.225.1": {Namespace: "demo", Name: "web-1", IP: "172.31.225.1", NodeName: "node-1", Phase: "Running", Ready: true},
		"172.31.225.2": {Namespace: "demo", Name: "api-2", IP: "172.31.225.2", NodeName: "node-2", Phase: "Running", Ready: true},
	}
	podsByKey := map[string]model.PodBrief{
		"demo/web-1": podsByIP["172.31.225.1"],
		"demo/api-2": podsByIP["172.31.225.2"],
	}

	snap, lookup := BuildIPAMSnapshot(pools, blocks, affs, podsByIP, podsByKey, true)
	if len(snap.Pools) != 1 || len(snap.Blocks) != 1 {
		t.Fatalf("unexpected counts pools=%d blocks=%d", len(snap.Pools), len(snap.Blocks))
	}
	p := snap.Pools[0]
	if p.AllocatedIPs != 4 || p.TunnelIPs != 1 || p.ActivePodIPs != 1 || p.BorrowedIPs != 1 || p.OrphanIPs != 1 {
		t.Fatalf("unexpected pool counts: %+v", p)
	}
	b := snap.Blocks[0]
	if b.Slots[0].Status != model.IPStatusTunnel ||
		b.Slots[1].Status != model.IPStatusPod ||
		b.Slots[2].Status != model.IPStatusBorrowed ||
		b.Slots[3].Status != model.IPStatusOrphan ||
		b.Slots[4].Status != model.IPStatusFree {
		t.Fatalf("unexpected slot statuses: 0=%s 1=%s 2=%s 3=%s 4=%s",
			b.Slots[0].Status, b.Slots[1].Status, b.Slots[2].Status, b.Slots[3].Status, b.Slots[4].Status)
	}
	if !lookup["172.31.225.2"].Borrowed {
		t.Fatalf("expected 172.31.225.2 to be marked borrowed in lookup")
	}
}
