package k8s

import (
	"net"
	"strings"

	"calico-netdiag/pkg/model"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	gvrIPPools = schema.GroupVersionResource{
		Group: "crd.projectcalico.org", Version: "v1", Resource: "ippools",
	}
	gvrIPAMBlocks = schema.GroupVersionResource{
		Group: "crd.projectcalico.org", Version: "v1", Resource: "ipamblocks",
	}
	gvrBlockAffinities = schema.GroupVersionResource{
		Group: "crd.projectcalico.org", Version: "v1", Resource: "blockaffinities",
	}
	gvrFelixConfigs = schema.GroupVersionResource{
		Group: "crd.projectcalico.org", Version: "v1", Resource: "felixconfigurations",
	}
	gvrBGPConfigs = schema.GroupVersionResource{
		Group: "crd.projectcalico.org", Version: "v1", Resource: "bgpconfigurations",
	}
	gvrBGPPeers = schema.GroupVersionResource{
		Group: "crd.projectcalico.org", Version: "v1", Resource: "bgppeers",
	}
	gvrCalicoNetPols = schema.GroupVersionResource{
		Group: "crd.projectcalico.org", Version: "v1", Resource: "networkpolicies",
	}
	gvrCalicoGlobalNetPols = schema.GroupVersionResource{
		Group: "crd.projectcalico.org", Version: "v1", Resource: "globalnetworkpolicies",
	}
	gvrInstallations = schema.GroupVersionResource{
		Group: "operator.tigera.io", Version: "v1", Resource: "installations",
	}
)

// RawIPPool holds parsed Calico IPPool CRD fields.
type RawIPPool struct {
	Name             string
	CIDR             string
	BlockSize        int
	IPIPMode         string
	VXLANMode        string
	NATOutgoing      bool
	Disabled         bool
	DisableBGPExport bool
	NodeSelector     string
	AllowedUses      []string
}

// RawIPAMBlock holds parsed Calico IPAMBlock CRD fields.
type RawIPAMBlock struct {
	Name           string
	CIDR           string
	Affinity       string // e.g. "host:node-1"
	StrictAffinity bool
	Deleted        bool
	Allocations    []*int
	Attributes     []IPAMAttr
}

// IPAMAttr represents one entry in IPAMBlock.spec.attributes.
type IPAMAttr struct {
	HandleID  string
	Namespace string
	Pod       string
	Node      string
	Timestamp string
	Type      string
}

// RawBlockAffinity holds parsed Calico BlockAffinity CRD fields.
type RawBlockAffinity struct {
	Name    string
	CIDR    string
	Node    string
	State   string
	Deleted bool
}

// CalicoInstallInfo holds Tigera Installation fields.
type CalicoInstallInfo struct {
	Version        string
	Variant        string
	LinuxDataplane string
	BGPEnabled     bool
	MTU            int
}

// FelixConfigInfo holds FelixConfiguration fields.
type FelixConfigInfo struct {
	BPFEnabled    bool
	NFTablesMode  string
	VXLANVNI      int
	VXLANPort     int
	IPIPMTU       int
	VXLANMTU      int
}

// BGPConfigInfo holds BGPConfiguration fields.
type BGPConfigInfo struct {
	ASNumber       int
	NodeToNodeMesh bool
}

// RawBGPPeer holds BGPPeer CRD fields.
type RawBGPPeer struct {
	Name         string
	PeerIP       string
	ASNumber     int
	Node         string
	NodeSelector string
}

// RawCalicoPolicy holds Calico NetworkPolicy or GlobalNetworkPolicy fields.
type RawCalicoPolicy struct {
	Global      bool
	Namespace   string
	Name        string
	Tier        string
	Order       *float64
	Selector    string
	PolicyTypes []string
	IngressCnt  int
	EgressCnt   int
}

// ClassifyEncapMode resolves a human-readable Calico encapsulation mode from IPPool fields.
func ClassifyEncapMode(ipipMode, vxlanMode string, bgpEnabled bool) model.CalicoEncapMode {
	ipip := strings.ToLower(strings.TrimSpace(ipipMode))
	vxlan := strings.ToLower(strings.TrimSpace(vxlanMode))

	switch vxlan {
	case "always":
		return model.EncapVXLANAlways
	case "crosssubnet":
		return model.EncapVXLANCrossSubnet
	}
	switch ipip {
	case "always":
		return model.EncapIPIPAlways
	case "crosssubnet":
		return model.EncapIPIPCrossSubnet
	}
	if bgpEnabled || (ipip == "never" || ipip == "") && (vxlan == "never" || vxlan == "") {
		return model.EncapBGPDirect
	}
	return model.EncapUnknown
}

// EffectiveCrossNodeEncap determines whether traffic between two node IPs uses
// encapsulation or direct routing, accurately handling CrossSubnet modes.
func EffectiveCrossNodeEncap(poolMode model.CalicoEncapMode, srcNodeCIDR, dstNodeIP string) model.CalicoEncapMode {
	sameSubnet := false
	if srcNodeCIDR != "" && dstNodeIP != "" {
		if _, netCIDR, err := net.ParseCIDR(srcNodeCIDR); err == nil {
			if ip := net.ParseIP(dstNodeIP); ip != nil {
				sameSubnet = netCIDR.Contains(ip)
			}
		}
	}
	switch poolMode {
	case model.EncapIPIPCrossSubnet:
		if sameSubnet {
			return model.EncapBGPDirect
		}
		return model.EncapIPIPAlways
	case model.EncapVXLANCrossSubnet:
		if sameSubnet {
			return model.EncapBGPDirect
		}
		return model.EncapVXLANAlways
	default:
		return poolMode
	}
}

func ParseIPPool(u *unstructured.Unstructured) RawIPPool {
	spec, _ := u.Object["spec"].(map[string]interface{})
	pool := RawIPPool{
		Name:             u.GetName(),
		CIDR:             getString(spec, "cidr"),
		BlockSize:        getInt(spec, "blockSize", 26),
		IPIPMode:         getString(spec, "ipipMode"),
		VXLANMode:        getString(spec, "vxlanMode"),
		NATOutgoing:      getBool(spec, "natOutgoing", false),
		Disabled:         getBool(spec, "disabled", false),
		DisableBGPExport: getBool(spec, "disableBGPExport", false),
		NodeSelector:     getString(spec, "nodeSelector"),
		AllowedUses:      getStringSlice(spec, "allowedUses"),
	}
	if pool.IPIPMode == "" {
		pool.IPIPMode = "Never"
	}
	if pool.VXLANMode == "" {
		pool.VXLANMode = "Never"
	}
	if pool.NodeSelector == "" {
		pool.NodeSelector = "all()"
	}
	return pool
}

func ParseIPAMBlock(u *unstructured.Unstructured) RawIPAMBlock {
	spec, _ := u.Object["spec"].(map[string]interface{})
	b := RawIPAMBlock{
		Name:           u.GetName(),
		CIDR:           getString(spec, "cidr"),
		Affinity:       getString(spec, "affinity"),
		StrictAffinity: getBool(spec, "strictAffinity", false),
		Deleted:        getBool(spec, "deleted", false),
	}
	if rawAllocs, ok := spec["allocations"].([]interface{}); ok {
		b.Allocations = make([]*int, len(rawAllocs))
		for i, v := range rawAllocs {
			if v == nil {
				continue
			}
			switch n := v.(type) {
			case int64:
				iv := int(n)
				b.Allocations[i] = &iv
			case float64:
				iv := int(n)
				b.Allocations[i] = &iv
			case int:
				iv := n
				b.Allocations[i] = &iv
			}
		}
	}
	if rawAttrs, ok := spec["attributes"].([]interface{}); ok {
		b.Attributes = make([]IPAMAttr, len(rawAttrs))
		for i, item := range rawAttrs {
			m, _ := item.(map[string]interface{})
			if m == nil {
				continue
			}
			attr := IPAMAttr{
				HandleID: getString(m, "handle_id"),
			}
			if sec, ok := m["secondary"].(map[string]interface{}); ok {
				attr.Namespace = getString(sec, "namespace")
				attr.Pod = getString(sec, "pod")
				attr.Node = getString(sec, "node")
				attr.Timestamp = getString(sec, "timestamp")
				attr.Type = getString(sec, "type")
			}
			b.Attributes[i] = attr
		}
	}
	return b
}

func ParseBlockAffinity(u *unstructured.Unstructured) RawBlockAffinity {
	spec, _ := u.Object["spec"].(map[string]interface{})
	delStr := getString(spec, "deleted")
	delBool := getBool(spec, "deleted", false) || strings.EqualFold(delStr, "true")
	return RawBlockAffinity{
		Name:    u.GetName(),
		CIDR:    getString(spec, "cidr"),
		Node:    getString(spec, "node"),
		State:   getString(spec, "state"),
		Deleted: delBool,
	}
}

func ParseInstallation(u *unstructured.Unstructured) CalicoInstallInfo {
	info := CalicoInstallInfo{
		Variant:        "Calico",
		LinuxDataplane: "Iptables",
		BGPEnabled:     true,
		MTU:            1500,
	}
	if status, ok := u.Object["status"].(map[string]interface{}); ok {
		if v := getString(status, "calicoVersion"); v != "" {
			info.Version = v
		}
		if v := getString(status, "variant"); v != "" {
			info.Variant = v
		}
		if mtu := getInt(status, "mtu", 0); mtu > 0 {
			info.MTU = mtu
		}
	}
	if spec, ok := u.Object["spec"].(map[string]interface{}); ok {
		if cn, ok := spec["calicoNetwork"].(map[string]interface{}); ok {
			if dp := getString(cn, "linuxDataplane"); dp != "" {
				info.LinuxDataplane = dp
			}
			if bgp := getString(cn, "bgp"); bgp != "" {
				info.BGPEnabled = !strings.EqualFold(bgp, "Disabled")
			}
		}
	}
	return info
}

func ParseFelixConfig(u *unstructured.Unstructured) FelixConfigInfo {
	spec, _ := u.Object["spec"].(map[string]interface{})
	return FelixConfigInfo{
		BPFEnabled:   getBool(spec, "bpfEnabled", false),
		NFTablesMode: getString(spec, "nftablesMode"),
		VXLANVNI:     getInt(spec, "vxlanVNI", 4096),
		VXLANPort:    getInt(spec, "vxlanPort", 4789),
		IPIPMTU:      getInt(spec, "ipipMTU", 0),
		VXLANMTU:     getInt(spec, "vxlanMTU", 0),
	}
}

func ParseBGPConfig(u *unstructured.Unstructured) BGPConfigInfo {
	spec, _ := u.Object["spec"].(map[string]interface{})
	return BGPConfigInfo{
		ASNumber:       getInt(spec, "asNumber", 64512),
		NodeToNodeMesh: getBool(spec, "nodeToNodeMeshEnabled", true),
	}
}

func ParseBGPPeer(u *unstructured.Unstructured) RawBGPPeer {
	spec, _ := u.Object["spec"].(map[string]interface{})
	return RawBGPPeer{
		Name:         u.GetName(),
		PeerIP:       getString(spec, "peerIP"),
		ASNumber:     getInt(spec, "asNumber", 0),
		Node:         getString(spec, "node"),
		NodeSelector: getString(spec, "nodeSelector"),
	}
}

func ParseCalicoPolicy(u *unstructured.Unstructured, global bool) RawCalicoPolicy {
	spec, _ := u.Object["spec"].(map[string]interface{})
	p := RawCalicoPolicy{
		Global:      global,
		Namespace:   u.GetNamespace(),
		Name:        u.GetName(),
		Tier:        getString(spec, "tier"),
		Selector:    getString(spec, "selector"),
		PolicyTypes: getStringSlice(spec, "types"),
	}
	if p.Tier == "" {
		p.Tier = "default"
	}
	if rawOrder, ok := spec["order"]; ok && rawOrder != nil {
		switch v := rawOrder.(type) {
		case float64:
			p.Order = &v
		case int64:
			fv := float64(v)
			p.Order = &fv
		}
	}
	if ing, ok := spec["ingress"].([]interface{}); ok {
		p.IngressCnt = len(ing)
	}
	if eg, ok := spec["egress"].([]interface{}); ok {
		p.EgressCnt = len(eg)
	}
	return p
}

func getString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getInt(m map[string]interface{}, key string, def int) int {
	if m == nil {
		return def
	}
	switch v := m[key].(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	case int:
		return v
	default:
		return def
	}
}

func getBool(m map[string]interface{}, key string, def bool) bool {
	if m == nil {
		return def
	}
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

func getStringSlice(m map[string]interface{}, key string) []string {
	if m == nil {
		return nil
	}
	raw, ok := m[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
