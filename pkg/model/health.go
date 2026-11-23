package model

import "time"

// GlobalHealthReport is the complete cluster network health assessment.
type GlobalHealthReport struct {
	CheckedAt          time.Time              `json:"checkedAt"`
	DurationMs         int64                  `json:"durationMs"`
	OverallScore       int                    `json:"overallScore"` // 0..100
	OverallStatus      string                 `json:"overallStatus"` // healthy, degraded, critical
	PodToPod           PodMeshHealth          `json:"podToPod"`
	PodToNode          PodNodeHealth          `json:"podToNode"`
	PodToService       PodServiceHealth       `json:"podToService"`
	ServiceBackends    ServiceBackendSummary  `json:"serviceBackends"`
	Infrastructure     InfraHealthSummary     `json:"infrastructure"`
}

// PodMeshHealth holds the N×N probe Pod connectivity & latency matrix.
type PodMeshHealth struct {
	TotalPairs     int                 `json:"totalPairs"`
	HealthyPairs   int                 `json:"healthyPairs"`
	FailedPairs    int                 `json:"failedPairs"`
	AvgLatencyMs   float64             `json:"avgLatencyMs"`
	MaxLatencyMs   float64             `json:"maxLatencyMs"`
	Nodes          []string            `json:"nodes"`
	Cells          []PodMeshCell       `json:"cells"`
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

// ServiceHealthDetail reports ClusterIP health vs direct backend Pod health.
type ServiceHealthDetail struct {
	Namespace          string                  `json:"namespace"`
	Name               string                  `json:"name"`
	Type               string                  `json:"type"`
	ClusterIP          string                  `json:"clusterIP"`
	Ports              []string                `json:"ports"`
	Status             string                  `json:"status"` // healthy, degraded, broken, no-endpoints, headless
	TotalBackends      int                     `json:"totalBackends"`
	ReadyBackends      int                     `json:"readyBackends"`
	ReachableBackends  int                     `json:"reachableBackends"`
	ClusterIPReachable *bool                   `json:"clusterIPReachable,omitempty"`
	ClusterIPLatencyMs float64                 `json:"clusterIPLatencyMs,omitempty"`
	ClusterIPError     string                  `json:"clusterIPError,omitempty"`
	Diagnosis          string                  `json:"diagnosis"`
	Backends           []BackendEndpointHealth `json:"backends"`
}

// BackendEndpointHealth is a single Pod endpoint behind a Service.
type BackendEndpointHealth struct {
	PodNamespace string  `json:"podNamespace,omitempty"`
	PodName      string  `json:"podName,omitempty"`
	PodIP        string  `json:"podIP"`
	NodeName     string  `json:"nodeName,omitempty"`
	TargetPort   int     `json:"targetPort"`
	Protocol     string  `json:"protocol"`
	K8sReady     bool    `json:"k8sReady"`
	ProbeOK      bool    `json:"probeOk"`
	LatencyMs    float64 `json:"latencyMs"`
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
