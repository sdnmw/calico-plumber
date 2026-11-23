package model

import "time"

// CalicoEncapMode represents the active encapsulation / routing mode of an IPPool or cluster.
type CalicoEncapMode string

const (
	EncapBGPDirect        CalicoEncapMode = "BGP"
	EncapIPIPAlways       CalicoEncapMode = "IPIP (Always)"
	EncapIPIPCrossSubnet  CalicoEncapMode = "IPIP (CrossSubnet)"
	EncapVXLANAlways      CalicoEncapMode = "VXLAN (Always)"
	EncapVXLANCrossSubnet CalicoEncapMode = "VXLAN (CrossSubnet)"
	EncapUnknown          CalicoEncapMode = "Unknown"
)

// KubeProxyMode represents the dataplane mode of kube-proxy.
type KubeProxyMode string

const (
	KubeProxyIptables KubeProxyMode = "iptables"
	KubeProxyIPVS     KubeProxyMode = "ipvs"
	KubeProxyNFTables KubeProxyMode = "nftables"
	KubeProxyEBPF     KubeProxyMode = "calico-ebpf"
	KubeProxyUnknown  KubeProxyMode = "unknown"
)

// TelemetryEngineMode indicates which flow telemetry backend is active on a node.
type TelemetryEngineMode string

const (
	TelemetryEBPF      TelemetryEngineMode = "ebpf"
	TelemetryConntrack TelemetryEngineMode = "conntrack"
)

// ClusterNetworkOverview aggregates Calico, kube-proxy, node, BGP, and telemetry status.
type ClusterNetworkOverview struct {
	UpdatedAt         time.Time             `json:"updatedAt"`
	K8sVersion        string                `json:"k8sVersion"`
	CalicoVersion     string                `json:"calicoVersion"`
	CalicoVariant     string                `json:"calicoVariant"`
	PrimaryEncapMode  CalicoEncapMode       `json:"primaryEncapMode"`
	LinuxDataplane    string                `json:"linuxDataplane"` // Iptables, NFTables, BPF
	BGPEnabled        bool                  `json:"bgpEnabled"`
	BGPASNumber       int                   `json:"bgpAsNumber,omitempty"`
	NodeToNodeMesh    bool                  `json:"nodeToNodeMesh"`
	KubeProxyMode     KubeProxyMode         `json:"kubeProxyMode"`
	ClusterCIDR       string                `json:"clusterCIDR,omitempty"`
	ServiceCIDR       string                `json:"serviceCIDR,omitempty"`
	MTU               int                   `json:"mtu"`
	VXLANVNI          int                   `json:"vxlanVni,omitempty"`
	VXLANPort         int                   `json:"vxlanPort,omitempty"`
	PodCount          int                   `json:"podCount"`
	ServiceCount      int                   `json:"serviceCount"`
	NamespaceCount    int                   `json:"namespaceCount"`
	NetworkPolicyCnt  int                   `json:"networkPolicyCount"`
	IPPools           []IPPoolSummary       `json:"ipPools"`
	Nodes             []NodeNetworkStatus   `json:"nodes"`
	BGPPeers          []BGPPeerSummary      `json:"bgpPeers"`
	GlobalTraffic     TrafficSummaryStats   `json:"globalTraffic"`
}

// NodeNetworkStatus combines K8s node metadata with live Agent dataplane observations.
type NodeNetworkStatus struct {
	Name              string              `json:"name"`
	Roles             []string            `json:"roles"`
	Ready             bool                `json:"ready"`
	InternalIP        string              `json:"internalIP"`
	OSImage           string              `json:"osImage"`
	KernelVersion     string              `json:"kernelVersion"`
	ContainerRuntime  string              `json:"containerRuntime"`
	Architecture      string              `json:"architecture"`
	PodCIDR           string              `json:"podCIDR,omitempty"`
	IPIPTunnelIP      string              `json:"ipipTunnelIP,omitempty"`
	VXLANTunnelIP     string              `json:"vxlanTunnelIP,omitempty"`
	VXLANTunnelMAC    string              `json:"vxlanTunnelMAC,omitempty"`
	PrimaryNIC        string              `json:"primaryNIC,omitempty"`
	NICMTU            int                 `json:"nicMTU,omitempty"`
	TunnelInterfaces  []TunnelIfaceInfo   `json:"tunnelInterfaces,omitempty"`
	PodCount          int                 `json:"podCount"`
	BlockCount        int                 `json:"blockCount"`
	AllocatedIPs      int                 `json:"allocatedIPs"`
	AgentReady        bool                `json:"agentReady"`
	AgentPodIP        string              `json:"agentPodIP,omitempty"`
	ProbePodIP        string              `json:"probePodIP,omitempty"`
	KubeProxyMode     KubeProxyMode       `json:"kubeProxyMode"`
	TelemetryEngine   TelemetryEngineMode `json:"telemetryEngine"`
	TelemetryReason   string              `json:"telemetryReason,omitempty"`
	BTFAvailable      bool                `json:"btfAvailable"`
	ConntrackAcct     bool                `json:"conntrackAcct"`
	BGPProtocols      []BIRDProtocol      `json:"bgpProtocols,omitempty"`
	ActiveFlowsCount  int                 `json:"activeFlowsCount"`
	LastHeartbeat     time.Time           `json:"lastHeartbeat,omitempty"`
}

// TunnelIfaceInfo describes a Calico tunnel or primary interface on a node.
type TunnelIfaceInfo struct {
	Name  string `json:"name"`
	Type  string `json:"type"` // physical, ipip, vxlan, ipvs
	IP    string `json:"ip,omitempty"`
	MAC   string `json:"mac,omitempty"`
	MTU   int    `json:"mtu"`
	State string `json:"state"`
}

// BIRDProtocol represents a live BIRD BGP session on a node.
type BIRDProtocol struct {
	Name    string `json:"name"`
	Proto   string `json:"proto"`
	Table   string `json:"table"`
	State   string `json:"state"`
	Since   string `json:"since"`
	Info    string `json:"info"`
	PeerIP  string `json:"peerIP,omitempty"`
	Node    string `json:"node,omitempty"`
}

// BGPPeerSummary represents configured BGPPeer CRDs or live mesh peers.
type BGPPeerSummary struct {
	Name         string `json:"name"`
	SourceNode   string `json:"sourceNode"`
	PeerIP       string `json:"peerIP"`
	PeerNode     string `json:"peerNode,omitempty"`
	ASNumber     int    `json:"asNumber,omitempty"`
	Scope        string `json:"scope"` // Mesh, Global, Node
	State        string `json:"state"` // Established, Idle, Connect, Active
	Since        string `json:"since,omitempty"`
	RouteCount   int    `json:"routeCount,omitempty"`
}

// TrafficSummaryStats is a high-level real-time traffic snapshot.
type TrafficSummaryStats struct {
	ActiveFlows      int     `json:"activeFlows"`
	TotalBytesPerSec float64 `json:"totalBytesPerSec"`
	TotalPktsPerSec  float64 `json:"totalPktsPerSec"`
	AvgLatencyMs     float64 `json:"avgLatencyMs"`
	RetransmitsPerMin uint64 `json:"retransmitsPerMin"`
	EBPFNodes        int     `json:"ebpfNodes"`
	ConntrackNodes   int     `json:"conntrackNodes"`
}
