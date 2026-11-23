package dataplane

import (
	"testing"

	"calico-netdiag/pkg/model"
)

func TestParseBIRDProtocolsOutput(t *testing.T) {
	raw := `
1002-name     proto    table    state  since       info
1002-static1  Static   master   up     2026-09-19
1002-kernel1  Kernel   master   up     2026-09-19
1002-Mesh_192_168_118_206 BGP      master   up     2026-09-19  Established
`
	protos := ParseBIRDProtocolsOutput(raw, "mw-workergroup1")
	if len(protos) != 3 {
		t.Fatalf("expected 3 protocols, got %d", len(protos))
	}
	bgp := protos[2]
	if bgp.Proto != "BGP" || bgp.PeerIP != "192.168.118.206" || bgp.Info != "Established" {
		t.Fatalf("unexpected BGP parse: %+v", bgp)
	}
}

func TestParseIptablesNAT(t *testing.T) {
	raw := `
-A KUBE-SERVICES -d 10.250.252.92/32 -p tcp -m tcp --dport 8080 -j KUBE-SVC-ABC123
-A KUBE-SVC-ABC123 -m statistic --mode random --probability 0.50000 -j KUBE-SEP-111
-A KUBE-SVC-ABC123 -j KUBE-SEP-222
-A KUBE-SEP-111 -p tcp -m tcp -j DNAT --to-destination 172.31.226.68:8080
-A KUBE-SEP-222 -p tcp -m tcp -j DNAT --to-destination 172.31.225.24:8080
`
	rule := ParseIptablesNAT(raw, "10.250.252.92", 8080, "TCP")
	if rule == nil {
		t.Fatal("expected parsed iptables rule")
	}
	if rule.Mode != model.KubeProxyIptables || rule.ChainOrAlgo != "KUBE-SVC-ABC123" || len(rule.Backends) != 2 {
		t.Fatalf("unexpected rule: %+v", rule)
	}
}

func TestParseIPVSOutput(t *testing.T) {
	raw := `
IP Virtual Server version 1.2.1 (size=4096)
Prot LocalAddress:Port Scheduler Flags
  -> RemoteAddress:Port           Forward Weight ActiveConn InActConn
TCP  10.250.252.10:53 rr
  -> 172.31.225.23:53             Masq    1      0          0
  -> 172.31.226.13:53             Masq    1      0          0
`
	rule := ParseIPVSOutput(raw, "10.250.252.10", 53, "TCP")
	if rule == nil {
		t.Fatal("expected parsed IPVS rule")
	}
	if rule.Mode != model.KubeProxyIPVS || rule.ChainOrAlgo != "rr" || len(rule.Backends) != 2 {
		t.Fatalf("unexpected IPVS rule: %+v", rule)
	}
}
