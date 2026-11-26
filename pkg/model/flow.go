package model

import "time"

// AgentFlowSample is a single 5-tuple observation reported by a Node Agent.
type AgentFlowSample struct {
	NodeName       string              `json:"nodeName"`
	Engine         TelemetryEngineMode `json:"engine"`
	SrcIP          string              `json:"srcIP"`
	DstIP          string              `json:"dstIP"`
	SrcPort        uint16              `json:"srcPort"`
	DstPort        uint16              `json:"dstPort"`
	Protocol       string              `json:"protocol"` // TCP, UDP, ICMP
	ServiceIP      string              `json:"serviceIP,omitempty"`
	ServicePort    uint16              `json:"servicePort,omitempty"`
	TxBytes        uint64              `json:"txBytes"`
	RxBytes        uint64              `json:"rxBytes"`
	TxPackets      uint64              `json:"txPackets"`
	RxPackets      uint64              `json:"rxPackets"`
	RTTAvgUs       uint32              `json:"rttAvgUs"`
	RTTMaxUs       uint32              `json:"rttMaxUs"`
	RTTSamples     uint32              `json:"rttSamples"`
	Retransmits    uint32              `json:"retransmits"`
	Resets         uint32              `json:"resets"`
	State          string              `json:"state,omitempty"`
	IntervalSec    float64             `json:"intervalSec"`
	Timestamp      time.Time           `json:"timestamp"`
}

// AgentFlowBatch is pushed periodically from each Agent to the Server.
type AgentFlowBatch struct {
	NodeName        string              `json:"nodeName"`
	AgentPodIP      string              `json:"agentPodIP,omitempty"`
	Engine          TelemetryEngineMode `json:"engine"`
	EngineReason    string              `json:"engineReason,omitempty"`
	BTFAvailable    bool                `json:"btfAvailable"`
	ConntrackAcct   bool                `json:"conntrackAcct"`
	KubeProxyMode   KubeProxyMode       `json:"kubeProxyMode"`
	KernelVersion   string              `json:"kernelVersion,omitempty"`
	PrimaryNIC      string              `json:"primaryNIC,omitempty"`
	NICMTU          int                 `json:"nicMTU,omitempty"`
	TunnelIfaces    []TunnelIfaceInfo   `json:"tunnelInterfaces,omitempty"`
	BGPProtocols    []BIRDProtocol      `json:"bgpProtocols,omitempty"`
	Timestamp       time.Time           `json:"timestamp"`
	Flows           []AgentFlowSample   `json:"flows"`
}

// EndpointIdentity is the K8s-enriched identity for a flow endpoint.
type EndpointIdentity struct {
	IP           string `json:"ip"`
	Port         uint16 `json:"port,omitempty"`
	Kind         string `json:"kind"` // Pod, Service, Node, External
	Namespace    string `json:"namespace,omitempty"`
	PodName      string `json:"podName,omitempty"`
	WorkloadKind string `json:"workloadKind,omitempty"`
	WorkloadName string `json:"workloadName,omitempty"`
	ServiceName  string `json:"serviceName,omitempty"`
	NodeName     string `json:"nodeName,omitempty"`
	DisplayName  string `json:"displayName"`
}

// EnrichedFlow is a K8s-enriched 5-tuple flow record displayed in the live flow table and edge drawer.
type EnrichedFlow struct {
	ID            string              `json:"id"`
	Src           EndpointIdentity    `json:"src"`
	Dst           EndpointIdentity    `json:"dst"`
	ViaService    *EndpointIdentity   `json:"viaService,omitempty"`
	Protocol      string              `json:"protocol"`
	DstPort       uint16              `json:"dstPort"`
	SrcPort       uint16              `json:"srcPort"`
	BytesTotal    uint64              `json:"bytesTotal"`
	TxBytes       uint64              `json:"txBytes"`
	RxBytes       uint64              `json:"rxBytes"`
	PacketsTotal  uint64              `json:"packetsTotal"`
	BytesPerSec   float64             `json:"bytesPerSec"`
	PacketsPerSec float64             `json:"packetsPerSec"`
	LatencyAvgMs  float64             `json:"latencyAvgMs"`
	LatencyP95Ms  float64             `json:"latencyP95Ms"`
	LatencyMaxMs  float64             `json:"latencyMaxMs"`
	Retransmits   uint64              `json:"retransmits"`
	Resets        uint64              `json:"resets"`
	Connections   int                 `json:"connections"`
	State         string              `json:"state,omitempty"`
	CrossNode     bool                `json:"crossNode"`
	Engine        TelemetryEngineMode `json:"engine"`
	Health        string              `json:"health"` // healthy, warning, critical
	LastSeen      time.Time           `json:"lastSeen"`
	WindowSec     int                 `json:"windowSec,omitempty"`
	PrevRetransmits      uint64       `json:"prevRetransmits,omitempty"`
	RetransmitDeltaPct   *float64     `json:"retransmitDeltaPct,omitempty"`
	BaselineAvailable    bool         `json:"baselineAvailable"`
	RetransmitPerMB      float64      `json:"retransmitPerMB,omitempty"`
	EstRetransmitRatePct *float64     `json:"estRetransmitRatePct,omitempty"`
	RateNote             string       `json:"rateNote,omitempty"`
	AnomalyScore         float64      `json:"anomalyScore,omitempty"`
	AnomalyReasons       []string     `json:"anomalyReasons,omitempty"`
}

// TopologyGraph is returned by /api/v1/topology for React Flow rendering.
type TopologyGraph struct {
	GeneratedAt time.Time        `json:"generatedAt"`
	Granularity string           `json:"granularity"` // namespace, workload, pod, node
	WindowSec   int              `json:"windowSec"`
	Namespaces  []string         `json:"namespaces"`
	Nodes       []TopologyNode   `json:"nodes"`
	Edges       []TopologyEdge   `json:"edges"`
	Summary     TopologySummary  `json:"summary"`
}

// TopologySummary summarizes the filtered topology view.
type TopologySummary struct {
	NodeCount        int     `json:"nodeCount"`
	EdgeCount        int     `json:"edgeCount"`
	TotalFlows       int     `json:"totalFlows"`
	TotalBytesPerSec float64 `json:"totalBytesPerSec"`
	AvgLatencyMs     float64 `json:"avgLatencyMs"`
	WarningEdges     int     `json:"warningEdges"`
	CriticalEdges    int     `json:"criticalEdges"`
}

// TopologyNode is a service/workload/pod/node card in the graph.
type TopologyNode struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Kind         string   `json:"kind"` // Namespace, Deployment, StatefulSet, DaemonSet, Service, Pod, Node, External
	Namespace    string   `json:"namespace,omitempty"`
	NodeName     string   `json:"nodeName,omitempty"`
	IP           string   `json:"ip,omitempty"`
	PodCount     int      `json:"podCount,omitempty"`
	Ports        []string `json:"ports,omitempty"`
	InboundBps   float64  `json:"inboundBps"`
	OutboundBps  float64  `json:"outboundBps"`
	AvgLatencyMs float64  `json:"avgLatencyMs"`
	Retransmits  uint64   `json:"retransmits"`
	Health       string   `json:"health"` // healthy, warning, critical
}

// TopologyEdge is an aggregated directional traffic link carrying 5-tuple details.
type TopologyEdge struct {
	ID             string         `json:"id"`
	Source         string         `json:"source"`
	Target         string         `json:"target"`
	Protocols      []string       `json:"protocols"`
	DstPorts       []uint16       `json:"dstPorts"`
	PrimaryLabel   string         `json:"primaryLabel"` // e.g. "TCP:8080"
	BytesTotal     uint64         `json:"bytesTotal"`
	BytesPerSec    float64        `json:"bytesPerSec"`
	PacketsPerSec  float64        `json:"packetsPerSec"`
	LatencyAvgMs   float64        `json:"latencyAvgMs"`
	LatencyP95Ms   float64        `json:"latencyP95Ms"`
	LatencyMaxMs   float64        `json:"latencyMaxMs"`
	Retransmits    uint64         `json:"retransmits"`
	Resets         uint64         `json:"resets"`
	FlowCount      int            `json:"flowCount"`
	CrossNode      bool           `json:"crossNode"`
	Health         string         `json:"health"` // healthy, warning, critical
	WindowSec      int            `json:"windowSec,omitempty"`
	PrevRetransmits      uint64   `json:"prevRetransmits,omitempty"`
	RetransmitDeltaPct   *float64 `json:"retransmitDeltaPct,omitempty"`
	BaselineAvailable    bool     `json:"baselineAvailable"`
	RetransmitPerMB      float64  `json:"retransmitPerMB,omitempty"`
	EstRetransmitRatePct *float64 `json:"estRetransmitRatePct,omitempty"`
	RateNote             string   `json:"rateNote,omitempty"`
	AnomalyScore         float64  `json:"anomalyScore,omitempty"`
	AnomalyReasons       []string `json:"anomalyReasons,omitempty"`
	FiveTuples     []EnrichedFlow `json:"fiveTuples"`
}
