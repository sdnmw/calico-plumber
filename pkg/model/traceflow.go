package model

import "time"

// TraceFlowRequest initiates a multi-hop live packet trace.
type TraceFlowRequest struct {
	SrcNamespace string `json:"srcNamespace"`
	SrcPod       string `json:"srcPod"`
	TargetType   string `json:"targetType"` // pod, service, node, ip
	DstNamespace string `json:"dstNamespace,omitempty"`
	DstName      string `json:"dstName,omitempty"`
	DstIP        string `json:"dstIP,omitempty"`
	Protocol     string `json:"protocol"` // TCP, UDP, ICMP
	DstPort      int    `json:"dstPort,omitempty"`
	TimeoutMs    int    `json:"timeoutMs,omitempty"`
}

// TraceFlowResult contains the complete hop-by-hop dataplane pipeline & observations.
type TraceFlowResult struct {
	ID              string              `json:"id"`
	StartedAt       time.Time           `json:"startedAt"`
	CompletedAt     time.Time           `json:"completedAt"`
	TotalLatencyMs  float64             `json:"totalLatencyMs"`
	Status          string              `json:"status"` // REACHABLE, DROPPED, RESET, TIMEOUT, ERROR
	VerdictSummary  string              `json:"verdictSummary"`
	RootCauseHint   string              `json:"rootCauseHint,omitempty"`
	EncapMode       CalicoEncapMode     `json:"encapMode"`
	CrossNode       bool                `json:"crossNode"`
	KubeProxyMode   KubeProxyMode       `json:"kubeProxyMode"`
	Source          EndpointIdentity    `json:"source"`
	Destination     EndpointIdentity    `json:"destination"`
	ResolvedBackend *EndpointIdentity   `json:"resolvedBackend,omitempty"`
	Protocol        string              `json:"protocol"`
	SrcPort         int                 `json:"srcPort,omitempty"`
	DstPort         int                 `json:"dstPort,omitempty"`
	TraceSignature  string              `json:"traceSignature"`
	ForwardHops     []TraceHop          `json:"forwardHops"`
	ReturnHops      []TraceHop          `json:"returnHops,omitempty"`
	Observations    []TraceObservation  `json:"observations"`
}

// TraceHop is a visual node in the TraceFlow swimlane pipeline.
type TraceHop struct {
	Index         int               `json:"index"`
	Lane          string            `json:"lane"` // source-node, fabric, dest-node, external
	NodeName      string            `json:"nodeName,omitempty"`
	Component     string            `json:"component"` // pod_nic, host_veth, egress_policy, kube_proxy_nat, route_lookup, tunnel_encap, host_nic, underlay_fabric, tunnel_decap, ingress_policy, dst_pod
	Title         string            `json:"title"`
	Subtitle      string            `json:"subtitle,omitempty"`
	Interface     string            `json:"interface,omitempty"`
	Status        string            `json:"status"` // OBSERVED, PASSED, NAT, ENCAPSULATED, DECAPSULATED, DELIVERED, DROPPED, RESET, SKIPPED, TIMEOUT
	Observed      bool              `json:"observed"`
	TimestampUs   int64             `json:"timestampUs,omitempty"`
	DeltaMs       float64           `json:"deltaMs,omitempty"`
	PacketSummary string            `json:"packetSummary,omitempty"`
	OuterHeader   string            `json:"outerHeader,omitempty"` // e.g. "IPIP 192.168.118.202 -> 192.168.118.206" or "VXLAN VNI=4096 UDP:4789"
	RuleDetail    string            `json:"ruleDetail,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// TraceObservation is a row in the Observations log table.
type TraceObservation struct {
	Seq           int     `json:"seq"`
	Direction     string  `json:"direction"` // Forward, Return
	Type          string  `json:"type"`      // Originated, Forwarded, Translated, Encapsulated, Decapsulated, Delivered, Replied, Dropped
	NodeName      string  `json:"nodeName"`
	Component     string  `json:"component"`
	Interface     string  `json:"interface,omitempty"`
	Detail        string  `json:"detail"`
	TimestampMs   float64 `json:"timestampMs"`
}

// AgentTraceCaptureRequest instructs an Agent to arm packet sniffers for a TraceFlow.
type AgentTraceCaptureRequest struct {
	TraceID       string   `json:"traceId"`
	Role          string   `json:"role"` // source, dest, both
	PodUID        string   `json:"podUid,omitempty"`
	PodIP         string   `json:"podIP,omitempty"`
	PodVeth       string   `json:"podVeth,omitempty"`
	PeerPodUID    string   `json:"peerPodUid,omitempty"`
	PeerPodIP     string   `json:"peerPodIP,omitempty"`
	PeerPodVeth   string   `json:"peerPodVeth,omitempty"`
	SrcIP         string   `json:"srcIP"`
	DstIP         string   `json:"dstIP"`
	BackendIPs    []string `json:"backendIPs,omitempty"`
	Protocol      string   `json:"protocol"`
	SrcPort       int      `json:"srcPort"`
	DstPort       int      `json:"dstPort"`
	BackendPort   int      `json:"backendPort,omitempty"`
	ICMPID        uint16   `json:"icmpId,omitempty"`
	TimeoutMs     int      `json:"timeoutMs"`
	Inject        bool     `json:"inject"`
	EncapMode     string   `json:"encapMode,omitempty"`
}

// AgentTraceCaptureResponse returns all observed packets + host route/NAT/policy metadata.
type AgentTraceCaptureResponse struct {
	NodeName         string                 `json:"nodeName"`
	KubeProxyMode    KubeProxyMode          `json:"kubeProxyMode"`
	HostVeth         string                 `json:"hostVeth,omitempty"`
	PeerHostVeth     string                 `json:"peerHostVeth,omitempty"`
	EgressInterface  string                 `json:"egressInterface,omitempty"`
	RouteSummary     string                 `json:"routeSummary,omitempty"`
	RouteProtocol    string                 `json:"routeProtocol,omitempty"`
	NextHop          string                 `json:"nextHop,omitempty"`
	EncapDetected    string                 `json:"encapDetected,omitempty"`
	NATRuleMatched   string                 `json:"natRuleMatched,omitempty"`
	TranslatedDstIP  string                 `json:"translatedDstIP,omitempty"`
	TranslatedPort   int                    `json:"translatedPort,omitempty"`
	MasqueradeNAT    bool                   `json:"masqueradeNat,omitempty"`
	PolicyChainInfo  string                 `json:"policyChainInfo,omitempty"`
	InjectResult     string                 `json:"injectResult,omitempty"`
	InjectError      string                 `json:"injectError,omitempty"`
	CapturedPackets  []CapturedTracePacket  `json:"capturedPackets"`
}

// CapturedTracePacket represents a single real packet observed on a specific interface.
type CapturedTracePacket struct {
	Interface     string `json:"interface"`
	Stage         string `json:"stage"` // src_pod_eth0, src_cali_veth, src_tunnel, src_phy_nic, dst_phy_nic, dst_tunnel, dst_cali_veth, dst_pod_eth0
	Direction     string `json:"direction"` // forward, return
	TimestampNs   int64  `json:"timestampNs"`
	SrcIP         string `json:"srcIP"`
	DstIP         string `json:"dstIP"`
	SrcPort       uint16 `json:"srcPort,omitempty"`
	DstPort       uint16 `json:"dstPort,omitempty"`
	Protocol      string `json:"protocol"`
	TCPFlags      string `json:"tcpFlags,omitempty"`
	ICMPType      string `json:"icmpType,omitempty"`
	OuterHeader   string `json:"outerHeader,omitempty"`
	Length        int    `json:"length"`
	Summary       string `json:"summary"`
}
