package dataplane

import (
	"testing"

	"calico-netdiag/pkg/model"
)

func TestParseConntrackStat(t *testing.T) {
	sample := `entries  searched found new invalid ignore delete delete_list insert insert_failed drop early_drop icmp_error expect_new expect_create expect_delete search_restart
00000120 00000010 000000a0 00000000 00000002 00000001 00000000 00000000 00000005 00000003 00000001 00000000 00000000 00000000 00000000 00000000 0000000a
00000120 00000005 00000020 00000000 00000001 00000000 00000000 00000000 00000005 00000002 00000004 00000001 00000000 00000000 00000000 00000000 00000006`
	var ct model.ConntrackStats
	ParseConntrackStat(sample, &ct)
	if ct.Searched != 0x15 {
		t.Fatalf("expected searched=21, got %d", ct.Searched)
	}
	if ct.InsertFailed != 5 {
		t.Fatalf("expected insert_failed=5, got %d", ct.InsertFailed)
	}
	if ct.Drop != 5 {
		t.Fatalf("expected drop=5, got %d", ct.Drop)
	}
	if ct.EarlyDrop != 1 {
		t.Fatalf("expected early_drop=1, got %d", ct.EarlyDrop)
	}
	if ct.SearchRestart != 0x10 {
		t.Fatalf("expected search_restart=16, got %d", ct.SearchRestart)
	}
}

func TestEvaluateKernelAuditFindings(t *testing.T) {
	ct := model.ConntrackStats{
		Count:              115000,
		Max:                131072,
		Buckets:            32768,
		UsagePct:           87.74,
		Drop:               3,
		InsertFailed:       12,
		NATOutgoingEnabled: true,
		SNATRandomFully:    false,
	}
	sysctls := []model.SysctlCheck{
		{Key: "net.ipv4.ip_forward", Value: "1", Expected: "1", Status: "pass"},
		{Key: "net.ipv4.tcp_tw_recycle", Value: "1", Expected: "0", Status: "critical"},
	}
	mtu := model.MTUCheck{
		PrimaryNIC: "ens4", PrimaryMTU: 1500, TunnelIface: "vxlan.calico", TunnelMTU: 1500, ExpectedTunnelMTU: 1450,
		Status: "critical", Detail: "mismatch",
	}

	findings, score, status := evaluateKernelAuditFindings("node-1", ct, sysctls, mtu)
	if status != "critical" {
		t.Fatalf("expected critical status, got %s", status)
	}
	if score >= 50 {
		t.Fatalf("expected severely degraded score, got %d", score)
	}
	if len(findings) < 5 {
		t.Fatalf("expected >= 5 findings (ct high, ct drop, insert_failed, random-fully, sysctl, mtu), got %d", len(findings))
	}
}
