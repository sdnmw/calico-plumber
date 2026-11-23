package model

import "time"

// IPAllocationStatus classifies each IP slot within a Calico IPAMBlock.
type IPAllocationStatus string

const (
	IPStatusFree     IPAllocationStatus = "free"
	IPStatusPod      IPAllocationStatus = "pod"
	IPStatusTunnel   IPAllocationStatus = "tunnel"
	IPStatusBorrowed IPAllocationStatus = "borrowed"
	IPStatusOrphan   IPAllocationStatus = "orphan"
)

// IPPoolSummary describes a Calico IPPool and its utilization metrics.
type IPPoolSummary struct {
	Name                  string          `json:"name"`
	CIDR                  string          `json:"cidr"`
	BlockSize             int             `json:"blockSize"`
	IPIPMode              string          `json:"ipipMode"`
	VXLANMode             string          `json:"vxlanMode"`
	EncapMode             CalicoEncapMode `json:"encapMode"`
	NATOutgoing           bool            `json:"natOutgoing"`
	Disabled              bool            `json:"disabled"`
	DisableBGPExport      bool            `json:"disableBGPExport"`
	NodeSelector          string          `json:"nodeSelector"`
	AllowedUses           []string        `json:"allowedUses,omitempty"`
	TotalIPs              int             `json:"totalIPs"`
	TotalBlocks           int             `json:"totalBlocks"`
	AllocatedBlocks       int             `json:"allocatedBlocks"`
	AllocatedIPs          int             `json:"allocatedIPs"`
	ActivePodIPs          int             `json:"activePodIPs"`
	TunnelIPs             int             `json:"tunnelIPs"`
	BorrowedIPs           int             `json:"borrowedIPs"`
	OrphanIPs             int             `json:"orphanIPs"`
	UtilizationPercent    float64         `json:"utilizationPercent"`
}

// IPAMBlockDetail represents a Calico IPAMBlock with per-slot allocation info.
type IPAMBlockDetail struct {
	Name               string         `json:"name"`
	CIDR               string         `json:"cidr"`
	PoolName           string         `json:"poolName"`
	AffinityNode       string         `json:"affinityNode"`
	AffinityState      string         `json:"affinityState"` // confirmed, pending, none
	StrictAffinity     bool           `json:"strictAffinity"`
	TotalSlots         int            `json:"totalSlots"`
	AllocatedSlots     int            `json:"allocatedSlots"`
	BorrowedSlots      int            `json:"borrowedSlots"`
	OrphanSlots        int            `json:"orphanSlots"`
	UtilizationPercent float64        `json:"utilizationPercent"`
	Slots              []IPSlotDetail `json:"slots"`
}

// IPSlotDetail represents a single IP address inside an IPAMBlock.
type IPSlotDetail struct {
	Index        int                `json:"index"`
	IP           string             `json:"ip"`
	Status       IPAllocationStatus `json:"status"`
	HandleID     string             `json:"handleId,omitempty"`
	Namespace    string             `json:"namespace,omitempty"`
	PodName      string             `json:"podName,omitempty"`
	PodNode      string             `json:"podNode,omitempty"`
	WorkloadKind string             `json:"workloadKind,omitempty"`
	WorkloadName string             `json:"workloadName,omitempty"`
	PodPhase     string             `json:"podPhase,omitempty"`
	AllocatedAt  string             `json:"allocatedAt,omitempty"`
}

// IPAMSnapshot is returned by /api/v1/ipam.
type IPAMSnapshot struct {
	UpdatedAt time.Time         `json:"updatedAt"`
	Pools     []IPPoolSummary   `json:"pools"`
	Blocks    []IPAMBlockDetail `json:"blocks"`
}

// SearchResultItem is a unified entity returned by Omni-Search.
type SearchResultItem struct {
	Kind         string            `json:"kind"` // Pod, Deployment, StatefulSet, DaemonSet, Service, Node, IP
	Name         string            `json:"name"`
	Namespace    string            `json:"namespace,omitempty"`
	IP           string            `json:"ip,omitempty"`
	ClusterIP    string            `json:"clusterIP,omitempty"`
	NodeName     string            `json:"nodeName,omitempty"`
	HostIP       string            `json:"hostIP,omitempty"`
	IPPool       string            `json:"ipPool,omitempty"`
	IPAMBlock    string            `json:"ipamBlock,omitempty"`
	Borrowed     bool              `json:"borrowed,omitempty"`
	Status       string            `json:"status,omitempty"`
	WorkloadKind string            `json:"workloadKind,omitempty"`
	WorkloadName string            `json:"workloadName,omitempty"`
	Ports        []string          `json:"ports,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	PodCount     int               `json:"podCount,omitempty"`
	ReadyPods    int               `json:"readyPods,omitempty"`
	Pods         []PodBrief        `json:"pods,omitempty"`
}

// PodBrief is a compact Pod reference used in search results and workload drill-downs.
type PodBrief struct {
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	IP           string `json:"ip"`
	NodeName     string `json:"nodeName"`
	HostIP       string `json:"hostIP"`
	Phase        string `json:"phase"`
	Ready        bool   `json:"ready"`
	HostNetwork  bool   `json:"hostNetwork"`
	IPPool       string `json:"ipPool,omitempty"`
	IPAMBlock    string `json:"ipamBlock,omitempty"`
	Borrowed     bool   `json:"borrowed,omitempty"`
	WorkloadKind string `json:"workloadKind,omitempty"`
	WorkloadName string `json:"workloadName,omitempty"`
	RestartCount int32  `json:"restartCount,omitempty"`
}

// PodNetworkDetail is the full dataplane + K8s inspector payload for a selected Pod.
type PodNetworkDetail struct {
	Pod             PodBrief              `json:"pod"`
	UID             string                `json:"uid"`
	CreatedAt       time.Time             `json:"createdAt"`
	Labels          map[string]string     `json:"labels,omitempty"`
	Containers      []string              `json:"containers,omitempty"`
	EncapMode       CalicoEncapMode       `json:"encapMode"`
	HostVeth        string                `json:"hostVeth,omitempty"`
	PodIface        string                `json:"podIface,omitempty"`
	PodMAC          string                `json:"podMAC,omitempty"`
	PodMTU          int                   `json:"podMTU,omitempty"`
	PodRoutes       []RouteEntry          `json:"podRoutes,omitempty"`
	HostRouteToPod  string                `json:"hostRouteToPod,omitempty"`
	Services        []PodServiceRef       `json:"services,omitempty"`
	NetworkPolicies []MatchedPolicySummary `json:"networkPolicies,omitempty"`
	DataplaneError  string                `json:"dataplaneError,omitempty"`
}

// RouteEntry is a Linux kernel route entry.
type RouteEntry struct {
	Dst      string `json:"dst"`
	Gateway  string `json:"gateway,omitempty"`
	Dev      string `json:"dev"`
	Protocol string `json:"protocol,omitempty"`
	Src      string `json:"src,omitempty"`
	Flags    string `json:"flags,omitempty"`
}

// PodServiceRef describes a Service selecting a Pod.
type PodServiceRef struct {
	Namespace  string   `json:"namespace"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	ClusterIP  string   `json:"clusterIP"`
	Ports      []string `json:"ports"`
	Ready      bool     `json:"ready"`
}

// MatchedPolicySummary summarizes a K8s or Calico NetworkPolicy applying to a Pod.
type MatchedPolicySummary struct {
	Source      string   `json:"source"` // K8s NetworkPolicy, Calico NetworkPolicy, Calico GlobalNetworkPolicy
	Namespace   string   `json:"namespace,omitempty"`
	Name        string   `json:"name"`
	Tier        string   `json:"tier,omitempty"`
	Order       *float64 `json:"order,omitempty"`
	PolicyTypes []string `json:"policyTypes"` // Ingress, Egress
	IngressCnt  int      `json:"ingressRuleCount"`
	EgressCnt   int      `json:"egressRuleCount"`
	Summary     string   `json:"summary"`
}
