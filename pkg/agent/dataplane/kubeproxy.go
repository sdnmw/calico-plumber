package dataplane

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"calico-netdiag/pkg/model"
)

// KubeProxyServiceRule describes a parsed iptables or IPVS kube-proxy DNAT rule.
type KubeProxyServiceRule struct {
	Mode        model.KubeProxyMode `json:"mode"`
	ClusterIP   string              `json:"clusterIP"`
	Port        int                 `json:"port"`
	Protocol    string              `json:"protocol"`
	ChainOrAlgo string              `json:"chainOrAlgo"` // KUBE-SVC-xxx or ipvs rr/lc
	Backends    []string            `json:"backends"`    // ip:port
	RawSummary  string              `json:"rawSummary"`
}

// LookupKubeProxyService inspects live iptables or IPVS rules on the node for a ClusterIP:Port.
func LookupKubeProxyService(mode model.KubeProxyMode, clusterIP string, port int, proto string) *KubeProxyServiceRule {
	if mode == model.KubeProxyIPVS {
		out, err := exec.Command("ipvsadm", "-Ln").CombinedOutput()
		if err == nil {
			if r := ParseIPVSOutput(string(out), clusterIP, port, proto); r != nil {
				return r
			}
		}
	}
	out, err := exec.Command("iptables-save", "-t", "nat").CombinedOutput()
	if err == nil {
		return ParseIptablesNAT(string(out), clusterIP, port, proto)
	}
	return nil
}

// CheckCalicoNATOutgoing inspects whether cali-nat-outgoing MASQUERADE is configured in iptables nat.
func CheckCalicoNATOutgoing() bool {
	out, err := exec.Command("iptables-save", "-t", "nat").CombinedOutput()
	if err != nil {
		return false
	}
	s := string(out)
	return strings.Contains(s, "cali-nat-outgoing") && strings.Contains(s, "MASQUERADE")
}

// InspectCalicoPolicyChains checks live cali-fw-<veth> and cali-tw-<veth> filter chains for a Pod veth.
func InspectCalicoPolicyChains(veth string) string {
	if veth == "" {
		return ""
	}
	out, err := exec.Command("iptables-save", "-t", "filter").CombinedOutput()
	if err != nil {
		return ""
	}
	fwChain := "cali-fw-" + veth
	twChain := "cali-tw-" + veth
	var fwRules, twRules []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "-A "+fwChain+" ") {
			fwRules = append(fwRules, extractCalicoPolicyRef(line))
		} else if strings.HasPrefix(line, "-A "+twChain+" ") {
			twRules = append(twRules, extractCalicoPolicyRef(line))
		}
	}
	fwDesc := "default-allow"
	if len(fwRules) > 0 {
		fwDesc = strings.Join(dedupNonEmpty(fwRules), ", ")
	}
	twDesc := "default-allow"
	if len(twRules) > 0 {
		twDesc = strings.Join(dedupNonEmpty(twRules), ", ")
	}
	return fmt.Sprintf("Egress(%s): %s | Ingress(%s): %s", fwChain, fwDesc, twChain, twDesc)
}

var (
	reIptablesDest = regexp.MustCompile(`-d\s+([0-9.]+)/32`)
	reIptablesPort = regexp.MustCompile(`--dport\s+(\d+)`)
	reIptablesJump = regexp.MustCompile(`-j\s+(\S+)`)
	reIptablesDNAT = regexp.MustCompile(`--to-destination\s+([0-9.:]+)`)
	reCalicoJump   = regexp.MustCompile(`-j\s+(cali-(?:pi|po|pro)-\S+)`)
)

// ParseIptablesNAT parses `iptables-save -t nat` output to find the kube-proxy DNAT chain and endpoints.
func ParseIptablesNAT(raw, clusterIP string, port int, proto string) *KubeProxyServiceRule {
	protoLower := strings.ToLower(proto)
	if protoLower == "" {
		protoLower = "tcp"
	}

	svcChain := ""
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(line, "-A KUBE-SERVICES ") {
			continue
		}
		if !strings.Contains(line, clusterIP+"/32") {
			continue
		}
		if !strings.Contains(line, "-p "+protoLower) {
			continue
		}
		pm := reIptablesPort.FindStringSubmatch(line)
		if len(pm) < 2 {
			continue
		}
		p, _ := strconv.Atoi(pm[1])
		if p != port {
			continue
		}
		jm := reIptablesJump.FindStringSubmatch(line)
		if len(jm) >= 2 && strings.HasPrefix(jm[1], "KUBE-SVC-") {
			svcChain = jm[1]
			break
		}
	}
	if svcChain == "" {
		return nil
	}

	var sepChains []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "-A "+svcChain+" ") {
			if jm := reIptablesJump.FindStringSubmatch(line); len(jm) >= 2 && strings.HasPrefix(jm[1], "KUBE-SEP-") {
				sepChains = append(sepChains, jm[1])
			}
		}
	}

	var backends []string
	sepSet := make(map[string]bool, len(sepChains))
	for _, sc := range sepChains {
		sepSet[sc] = true
	}
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(line, "-A KUBE-SEP-") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 || !sepSet[parts[1]] {
			continue
		}
		if dm := reIptablesDNAT.FindStringSubmatch(line); len(dm) >= 2 {
			backends = append(backends, dm[1])
		}
	}

	return &KubeProxyServiceRule{
		Mode:        model.KubeProxyIptables,
		ClusterIP:   clusterIP,
		Port:        port,
		Protocol:    strings.ToUpper(protoLower),
		ChainOrAlgo: svcChain,
		Backends:    backends,
		RawSummary:  fmt.Sprintf("iptables KUBE-SERVICES → %s → %d SEP backend(s) [%s]", svcChain, len(backends), strings.Join(backends, ", ")),
	}
}

// ParseIPVSOutput parses `ipvsadm -Ln` output to find a virtual service and its real server backends.
func ParseIPVSOutput(raw, clusterIP string, port int, proto string) *KubeProxyServiceRule {
	protoUpper := strings.ToUpper(proto)
	if protoUpper == "" {
		protoUpper = "TCP"
	}
	target := fmt.Sprintf("%s:%d", clusterIP, port)

	var current *KubeProxyServiceRule
	for _, line := range strings.Split(raw, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "IP Virtual Server") || strings.HasPrefix(trim, "Prot ") || strings.HasPrefix(trim, "-> RemoteAddress") {
			continue
		}
		if strings.HasPrefix(trim, "TCP ") || strings.HasPrefix(trim, "UDP ") {
			if current != nil {
				return current
			}
			fields := strings.Fields(trim)
			if len(fields) >= 3 && strings.EqualFold(fields[0], protoUpper) && fields[1] == target {
				current = &KubeProxyServiceRule{
					Mode:        model.KubeProxyIPVS,
					ClusterIP:   clusterIP,
					Port:        port,
					Protocol:    protoUpper,
					ChainOrAlgo: fields[2],
				}
			}
			continue
		}
		if current != nil && strings.HasPrefix(trim, "->") {
			fields := strings.Fields(trim)
			if len(fields) >= 2 {
				current.Backends = append(current.Backends, fields[1])
			}
		}
	}
	if current != nil {
		current.RawSummary = fmt.Sprintf("IPVS (%s) %s:%d → %d real server(s) [%s]",
			current.ChainOrAlgo, clusterIP, port, len(current.Backends), strings.Join(current.Backends, ", "))
	}
	return current
}

func extractCalicoPolicyRef(line string) string {
	if m := reCalicoJump.FindStringSubmatch(line); len(m) >= 2 {
		return m[1]
	}
	if strings.Contains(line, "DROP") {
		return "DROP"
	}
	return ""
}

func dedupNonEmpty(in []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
