package model

import "time"

// GlobalHealthReport is the complete cluster network health assessment.
type GlobalHealthReport struct {
	CheckedAt          time.Time              `json:"checkedAt"`
	DurationMs         int64                  `json:"durationMs"`
	OverallScore       int                    `json:"overallScore"` // 0..100
	OverallStatus      string                 `json:"overallStatus"` // healthy, degraded, critical
	SummaryHeadline    HealthSummaryHeadline  `json:"summaryHeadline"`
	PodToPod           PodMeshHealth          `json:"podToPod"`
	PodToNode          PodNodeHealth          `json:"podToNode"`
	PodToService       PodServiceHealth       `json:"podToService"`
	ServiceBackends    ServiceBackendSummary  `json:"serviceBackends"`
	Infrastructure     InfraHealthSummary     `json:"infrastructure"`
}

// HealthSummaryHeadline gives an at-a-glance, plain-language operator verdict.
type HealthSummaryHeadline struct {
	NodeCount           int    `json:"nodeCount"`
	CrossNodeHealthy    int    `json:"crossNodeHealthy"`
	CrossNodeTotal      int    `json:"crossNodeTotal"`
	CrossNodeConnected  bool   `json:"crossNodeConnected"`
	TotalServices       int    `json:"totalServices"`
	BrokenServices      int    `json:"brokenServices"`
	DegradedServices    int    `json:"degradedServices"`
	NoEndpointServices  int    `json:"noEndpointServices"`
	NeedsAttention      bool   `json:"needsAttention"`
	HeadlineText        string `json:"headlineText"`
}

// PodMeshHealth holds the N×N probe Pod connectivity & latency matrix.
type PodMeshHealth struct {
	TotalPairs       int                 `json:"totalPairs"`
	HealthyPairs     int                 `json:"healthyPairs"`
	FailedPairs      int                 `json:"failedPairs"`
	CrossNodeTotal   int                 `json:"crossNodeTotal"`
	CrossNodeHealthy int                 `json:"crossNodeHealthy"`
	CrossNodeFailed  int                 `json:"crossNodeFailed"`
	SelfCheckTotal   int                 `json:"selfCheckTotal"`
	SelfCheckHealthy int                 `json:"selfCheckHealthy"`
	AvgLatencyMs     float64             `json:"avgLatencyMs"`
	MaxLatencyMs     float64             `json:"maxLatencyMs"`
	Nodes            []string            `json:"nodes"`
	Cells            []PodMeshCell       `json:"cells"`
}

// PodMeshCell is a single directional probe from srcNode's probe pod to dstNode's probe pod.
type PodMeshCell struct {
	SrcNode      string  `json:"srcNode"`
	SrcPod       string  `json:"srcPod"`
	SrcIP        string  `json:"srcIP"`
	DstNode      string  `json:"dstNode"`
	DstPod       string  `json:"dstPod"`
	DstIP        string  `json:"dstIP"`
	SameNode     bool    `json:"sameNode"`
	SelfCheck    bool    `json:"selfCheck"` // true when srcIP == dstIP (probe checking itself)
	Reachable    bool    `json:"reachable"`
	LatencyMs    float64 `json:"latencyMs"`
	Error        string  `json:"error,omitempty"`
}

// PodNodeHealth holds Pod-to-Node reachability checks.
type PodNodeHealth struct {
	TotalChecks   int                 `json:"totalChecks"`
	HealthyChecks int                 `json:"healthyChecks"`
	AvgLatencyMs  float64             `json:"avgLatencyMs"`
	Checks        []PodNodeCheckItem  `json:"checks"`
}

// PodNodeCheckItem is a single Pod-to-Node probe.
type PodNodeCheckItem struct {
	SrcNode    string  `json:"srcNode"`
	SrcPod     string  `json:"srcPod"`
	DstNode    string  `json:"dstNode"`
	DstNodeIP  string  `json:"dstNodeIP"`
	TargetPort int     `json:"targetPort"`
	Reachable  bool    `json:"reachable"`
	LatencyMs  float64 `json:"latencyMs"`
	Error      string  `json:"error,omitempty"`
}

// PodServiceHealth holds core cluster Service & CoreDNS checks from every node's probe pod.
type PodServiceHealth struct {
	TotalChecks   int                    `json:"totalChecks"`
	HealthyChecks int                    `json:"healthyChecks"`
	DNSAvgMs      float64                `json:"dnsAvgMs"`
	Checks        []PodServiceCheckItem  `json:"checks"`
}

// PodServiceCheckItem tests CoreDNS resolution or ClusterIP reachability from a probe pod.
type PodServiceCheckItem struct {
	SrcNode     string  `json:"srcNode"`
	SrcPod      string  `json:"srcPod"`
	TargetType  string  `json:"targetType"` // dns, clusterip
	TargetName  string  `json:"targetName"`
	TargetAddr  string  `json:"targetAddr"`
	Reachable   bool    `json:"reachable"`
	LatencyMs   float64 `json:"latencyMs"`
	Detail      string  `json:"detail,omitempty"`
	Error       string  `json:"error,omitempty"`
}

// ServiceBackendSummary audits all cluster Services and their backing Endpoint Pods.
type ServiceBackendSummary struct {
	TotalServices    int                   `json:"totalServices"`
	HealthyServices  int                   `json:"healthyServices"`
	DegradedServices int                   `json:"degradedServices"`
	BrokenServices   int                   `json:"brokenServices"`
	NoEndpointSvcs   int                   `json:"noEndpointServices"`
	Services         []ServiceHealthDetail `json:"services"`
}

// ServiceHealthDetail reports ClusterIP health vs direct backend Pod health per declared port.
type ServiceHealthDetail struct {
	Namespace          string                  `json:"namespace"`
	Name               string                  `json:"name"`
	Type               string                  `json:"type"`
	ClusterIP          string                  `json:"clusterIP"`
	Ports              []string                `json:"ports"`
	Selector           map[string]string       `json:"selector,omitempty"`
	ProbeSource        string                  `json:"probeSource,omitempty"`
	ProbeSourcePod     string                  `json:"probeSourcePod,omitempty"`
	ProbeSourceNode    string                  `json:"probeSourceNode,omitempty"`
	Status             string                  `json:"status"` // healthy, degraded, broken, no-endpoints, untested
	TotalBackends      int                     `json:"totalBackends"`
	ReadyBackends      int                     `json:"readyBackends"`
	ReachableBackends  int                     `json:"reachableBackends"`
	ClusterIPReachable *bool                   `json:"clusterIPReachable,omitempty"`
	ClusterIPLatencyMs float64                 `json:"clusterIPLatencyMs,omitempty"`
	ClusterIPError     string                  `json:"clusterIPError,omitempty"`
	Diagnosis          string                  `json:"diagnosis"`
	RecommendedAction  string                  `json:"recommendedAction"` // inspect_empty_backends, check_port_listening, trace_service_dnat, compare_policy_sources, trace_normal
	PortChecks         []ServicePortHealth     `json:"portChecks"`
	Backends           []BackendEndpointHealth `json:"backends"`
}

// ServicePortHealth gives exact per-port diagnosis: who probed which Service port, mapped backend port, ClusterIP result, direct backend result, and failure classification.
type ServicePortHealth struct {
	PortName           string                  `json:"portName,omitempty"`
	ServicePort        int                     `json:"servicePort"`
	Protocol           string                  `json:"protocol"` // TCP, UDP, SCTP
	TargetPort         string                  `json:"targetPort"`
	ResolvedTargetPort int                     `json:"resolvedTargetPort"`
	ProbeSource        string                  `json:"probeSource"`
	ProbeSourcePod     string                  `json:"probeSourcePod,omitempty"`
	ProbeSourceNode    string                  `json:"probeSourceNode,omitempty"`
	ProbeState         string                  `json:"probeState"` // tested, untested
	UntestedReason     string                  `json:"untestedReason,omitempty"`
	ClusterIPState     string                  `json:"clusterIPState"` // ok, failed, untested, headless
	ClusterIPLatencyMs float64                 `json:"clusterIPLatencyMs,omitempty"`
	ClusterIPFailure   string                  `json:"clusterIPFailure,omitempty"` // connection_refused, timeout, no_route, other
	ClusterIPError     string                  `json:"clusterIPError,omitempty"`
	TotalBackends      int                     `json:"totalBackends"`
	ReadyBackends      int                     `json:"readyBackends"`
	TestedBackends     int                     `json:"testedBackends"`
	ReachableBackends  int                     `json:"reachableBackends"`
	FailureKind        string                  `json:"failureKind,omitempty"` // connection_refused, timeout, no_route, untested, other
	FailureSummary     string                  `json:"failureSummary"`
	Status             string                  `json:"status"` // healthy, degraded, broken, no-endpoints, untested
	Diagnosis          string                  `json:"diagnosis"`
	RecommendedAction  string                  `json:"recommendedAction"`
	Backends           []BackendEndpointHealth `json:"backends"`
}

// BackendEndpointHealth is a single Pod endpoint behind a Service port.
type BackendEndpointHealth struct {
	PodNamespace string  `json:"podNamespace,omitempty"`
	PodName      string  `json:"podName,omitempty"`
	PodIP        string  `json:"podIP"`
	NodeName     string  `json:"nodeName,omitempty"`
	ServicePort  int     `json:"servicePort,omitempty"`
	TargetPort   int     `json:"targetPort"`
	Protocol     string  `json:"protocol"`
	K8sReady     bool    `json:"k8sReady"`
	ProbeState   string  `json:"probeState,omitempty"` // tested, untested
	ProbeSource  string  `json:"probeSource,omitempty"`
	ProbeOK      bool    `json:"probeOk"`
	LatencyMs    float64 `json:"latencyMs"`
	FailureKind  string  `json:"failureKind,omitempty"` // connection_refused, timeout, no_route, other
	FailureLabel string  `json:"failureLabel,omitempty"`
	Error        string  `json:"error,omitempty"`
}

// InfraHealthSummary summarizes Calico control plane, BGP, IPAM, and MTU checks.
type InfraHealthSummary struct {
	CalicoNodesReady int                `json:"calicoNodesReady"`
	CalicoNodesTotal int                `json:"calicoNodesTotal"`
	BGPEstablished   int                `json:"bgpEstablished"`
	BGPTotal         int                `json:"bgpTotal"`
	MTUConsistent    bool               `json:"mtuConsistent"`
	MaxPoolUsagePct  float64            `json:"maxPoolUsagePct"`
	OrphanIPCount    int                `json:"orphanIPCount"`
	BorrowedIPCount  int                `json:"borrowedIPCount"`
	Checks           []InfraCheckItem   `json:"checks"`
}

// InfraCheckItem is a discrete infrastructure health check finding.
type InfraCheckItem struct {
	Category string `json:"category"` // Calico, BGP, IPAM, MTU, Telemetry
	Name     string `json:"name"`
	Status   string `json:"status"` // pass, warn, fail
	Detail   string `json:"detail"`
}
