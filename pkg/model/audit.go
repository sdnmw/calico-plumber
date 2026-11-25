package model

import "time"

// ConntrackStats holds live Linux netfilter conntrack capacity and error counters.
type ConntrackStats struct {
	Count               int64   `json:"count"`
	Max                 int64   `json:"max"`
	Buckets             int64   `json:"buckets"`
	UsagePct            float64 `json:"usagePct"`
	Searched            uint64  `json:"searched"`
	Found               uint64  `json:"found"`
	New                 uint64  `json:"new"`
	Invalid             uint64  `json:"invalid"`
	Ignore              uint64  `json:"ignore"`
	Insert              uint64  `json:"insert"`
	InsertFailed        uint64  `json:"insertFailed"`
	Drop                uint64  `json:"drop"`
	EarlyDrop           uint64  `json:"earlyDrop"`
	ICMPError           uint64  `json:"icmpError"`
	SearchRestart       uint64  `json:"searchRestart"`
	NATOutgoingEnabled  bool    `json:"natOutgoingEnabled"`
	SNATRandomFully     bool    `json:"snatRandomFully"`
	AcctEnabled         bool    `json:"acctEnabled"`
}

// SysctlCheck represents a single audited Linux kernel network parameter.
type SysctlCheck struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Expected    string `json:"expected"`
	Category    string `json:"category"` // routing, conntrack, tcp, bridge, neighbor
	Status      string `json:"status"`   // pass, warn, critical, info
	Explanation string `json:"explanation"`
	Remediation string `json:"remediation,omitempty"`
}

// MTUCheck validates MTU consistency across host NIC, Calico tunnel, and Pod veths.
type MTUCheck struct {
	PrimaryNIC        string   `json:"primaryNIC"`
	PrimaryMTU        int      `json:"primaryMTU"`
	TunnelIface       string   `json:"tunnelIface,omitempty"`
	TunnelType        string   `json:"tunnelType,omitempty"`
	TunnelMTU         int      `json:"tunnelMTU,omitempty"`
	ExpectedTunnelMTU int      `json:"expectedTunnelMTU,omitempty"`
	SampledVethCount  int      `json:"sampledVethCount"`
	MinVethMTU        int      `json:"minVethMTU,omitempty"`
	MaxVethMTU        int      `json:"maxVethMTU,omitempty"`
	MismatchedVeths   []string `json:"mismatchedVeths,omitempty"`
	Status            string   `json:"status"` // pass, warn, critical
	Detail            string   `json:"detail"`
}

// AuditFinding describes a concrete actionable risk discovered during kernel or route auditing.
type AuditFinding struct {
	ID          string `json:"id"`
	NodeName    string `json:"nodeName,omitempty"`
	Severity    string `json:"severity"` // critical, warning, info
	Category    string `json:"category"` // conntrack, sysctl, mtu, route, bgp
	Title       string `json:"title"`
	Detail      string `json:"detail"`
	Remediation string `json:"remediation,omitempty"`
}

// NodeKernelAudit summarizes one node's live kernel, conntrack, NAT, and MTU health.
type NodeKernelAudit struct {
	NodeName      string         `json:"nodeName"`
	InternalIP    string         `json:"internalIP,omitempty"`
	KernelVersion string         `json:"kernelVersion,omitempty"`
	Score         int            `json:"score"`  // 0..100
	Status        string         `json:"status"` // healthy, warning, critical, unreachable
	Error         string         `json:"error,omitempty"`
	Conntrack     ConntrackStats `json:"conntrack"`
	Sysctls       []SysctlCheck  `json:"sysctls"`
	MTU           MTUCheck       `json:"mtu"`
	Findings      []AuditFinding `json:"findings,omitempty"`
	CollectedAt   time.Time      `json:"collectedAt"`
}

// ClusterKernelAuditReport aggregates kernel/NAT/MTU audits across all cluster nodes.
type ClusterKernelAuditReport struct {
	GeneratedAt   time.Time         `json:"generatedAt"`
	OverallStatus string            `json:"overallStatus"` // healthy, warning, critical
	AverageScore  int               `json:"averageScore"`
	EncapMode     CalicoEncapMode   `json:"encapMode"`
	CriticalCount int               `json:"criticalCount"`
	WarningCount  int               `json:"warningCount"`
	Nodes         []NodeKernelAudit `json:"nodes"`
	Findings      []AuditFinding    `json:"findings"`
}

// PMTUDProbeRequest triggers an ICMP Don't Fragment (DF) Path MTU Discovery probe from inside a Pod's netns.
type PMTUDProbeRequest struct {
	SrcNamespace string `json:"srcNamespace"`
	SrcPod       string `json:"srcPod"`
	TargetIP     string `json:"targetIP"`
	MaxMTU       int    `json:"maxMTU,omitempty"` // default 1500
	TimeoutMs    int    `json:"timeoutMs,omitempty"`
}

// PMTUDAttempt records one DF packet size test during Path MTU Discovery.
type PMTUDAttempt struct {
	PacketSize int     `json:"packetSize"` // IPv4 total bytes (IP header 20 + ICMP header 8 + payload)
	Success    bool    `json:"success"`
	Result     string  `json:"result"` // ECHO_REPLY, FRAG_NEEDED, TIMEOUT, ERROR
	LatencyMs  float64 `json:"latencyMs"`
	Detail     string  `json:"detail,omitempty"`
}

// PMTUDProbeResult summarizes discovered Path MTU and detects MTU blackholes.
type PMTUDProbeResult struct {
	SrcPod        string         `json:"srcPod"`
	SrcIP         string         `json:"srcIP"`
	TargetIP      string         `json:"targetIP"`
	PodMTU        int            `json:"podMTU"`
	DiscoveredMTU int            `json:"discoveredMTU"`
	Blackhole     bool           `json:"blackhole"`
	Status        string         `json:"status"` // pass, warn, critical
	Diagnosis     string         `json:"diagnosis"`
	Attempts      []PMTUDAttempt `json:"attempts"`
}

// HostRouteEntry represents a real Linux IPv4 route from a node's main routing table.
type HostRouteEntry struct {
	Dst      string `json:"dst"`
	Gateway  string `json:"gateway,omitempty"`
	Dev      string `json:"dev,omitempty"`
	Protocol string `json:"protocol"` // bird, kernel, boot, static, zebra
	Type     string `json:"type"`     // unicast, blackhole, local, unreachable
	Scope    string `json:"scope,omitempty"`
	Metric   int    `json:"metric,omitempty"`
	Flags    string `json:"flags,omitempty"`
}

// AgentHostRoutesResponse is returned by each Agent's /api/dataplane/routes endpoint.
type AgentHostRoutesResponse struct {
	NodeName    string           `json:"nodeName"`
	PrimaryNIC  string           `json:"primaryNIC"`
	PrimaryIP   string           `json:"primaryIP"`
	Routes      []HostRouteEntry `json:"routes"`
	LocalVeths  []string         `json:"localVeths"`
	CollectedAt time.Time        `json:"collectedAt"`
}

// RouteDriftItem describes a single discrepancy between expected Calico routes and actual kernel routes.
type RouteDriftItem struct {
	NodeName     string `json:"nodeName"`
	Kind         string `json:"kind"` // missing_block, missing_pod, ghost_pod_route, unexpected_blackhole, missing_blackhole, encap_mismatch, asymmetric_path
	Severity     string `json:"severity"` // critical, warning, info
	CIDR         string `json:"cidr"`
	Expected     string `json:"expected"`
	Actual       string `json:"actual"`
	Detail       string `json:"detail"`
	Remediation  string `json:"remediation,omitempty"`
}

// BGPEvent records a detected BGP peer state transition (flapping / session down / recovery).
type BGPEvent struct {
	Timestamp time.Time `json:"timestamp"`
	NodeName  string    `json:"nodeName"`
	PeerName  string    `json:"peerName"`
	PeerIP    string    `json:"peerIP"`
	PrevState string    `json:"prevState"`
	NewState  string    `json:"newState"`
	Severity  string    `json:"severity"` // critical, warning, info
	Message   string    `json:"message"`
}

// NodeRouteAudit summarizes route table inspection and drift counts for one node.
type NodeRouteAudit struct {
	NodeName         string           `json:"nodeName"`
	InternalIP       string           `json:"internalIP"`
	Status           string           `json:"status"` // healthy, warning, critical, unreachable
	Error            string           `json:"error,omitempty"`
	TotalRoutes      int              `json:"totalRoutes"`
	BIRDRoutes       int              `json:"birdRoutes"`
	LocalPodRoutes   int              `json:"localPodRoutes"`
	BlackholeRoutes  int              `json:"blackholeRoutes"`
	ExpectedBlocks   int              `json:"expectedBlocks"`
	ExpectedLocalPod int              `json:"expectedLocalPods"`
	DriftCount       int              `json:"driftCount"`
	BGPPeersUp       int              `json:"bgpPeersUp"`
	BGPPeersTotal    int              `json:"bgpPeersTotal"`
	Routes           []HostRouteEntry `json:"routes"`
	Drifts           []RouteDriftItem `json:"drifts,omitempty"`
}

// AsymmetricPair records a node pair whose forward and reverse Calico routes use inconsistent encapsulation/paths.
type AsymmetricPair struct {
	NodeA       string `json:"nodeA"`
	NodeB       string `json:"nodeB"`
	AToBCIDR    string `json:"aToBCIDR"`
	AToBPath    string `json:"aToBPath"`
	BToACIDR    string `json:"bToACIDR"`
	BToAPath    string `json:"bToAPath"`
	Detail      string `json:"detail"`
}

// ClusterRouteAuditReport is the full cluster-wide Expected vs Actual Route & BGP consistency report.
type ClusterRouteAuditReport struct {
	GeneratedAt     time.Time        `json:"generatedAt"`
	OverallStatus   string           `json:"overallStatus"` // healthy, warning, critical
	EncapMode       CalicoEncapMode  `json:"encapMode"`
	TotalDrifts     int              `json:"totalDrifts"`
	MissingCount    int              `json:"missingCount"`
	GhostPodCount   int              `json:"ghostPodCount"`
	AsymmetricCount int              `json:"asymmetricCount"`
	Nodes           []NodeRouteAudit `json:"nodes"`
	Drifts          []RouteDriftItem `json:"drifts"`
	AsymmetricPairs []AsymmetricPair `json:"asymmetricPairs,omitempty"`
	BGPEvents       []BGPEvent       `json:"bgpEvents,omitempty"`
}
