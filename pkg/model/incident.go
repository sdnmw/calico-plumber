package model

import "time"

// ClusterChangeEvent represents a surrounding Pod, EndpointSlice, NetworkPolicy, or BGP change event.
type ClusterChangeEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Category  string    `json:"category"` // pod_rebuild, endpoint_change, policy_change, bgp_change, k8s_event
	Severity  string    `json:"severity"` // info, warning, critical
	Namespace string    `json:"namespace,omitempty"`
	Object    string    `json:"object"`
	Summary   string    `json:"summary"`
}

// IncidentSnapshot captures a specific intermittent or ongoing network failure for replay & diff.
type IncidentSnapshot struct {
	ID                string                 `json:"id"`
	Key               string                 `json:"key"` // deduplication key
	Title             string                 `json:"title"`
	Kind              string                 `json:"kind"` // service_port, flow_anomaly, traceflow
	Status            string                 `json:"status"` // active, recovered, intermittent
	FirstSeenAt       time.Time              `json:"firstSeenAt"`
	LastSeenAt        time.Time              `json:"lastSeenAt"`
	FailureCount      int                    `json:"failureCount"`
	SrcNamespace      string                 `json:"srcNamespace,omitempty"`
	SrcPod            string                 `json:"srcPod,omitempty"`
	SrcNode           string                 `json:"srcNode,omitempty"`
	TargetNamespace   string                 `json:"targetNamespace,omitempty"`
	TargetService     string                 `json:"targetService,omitempty"`
	TargetClusterIP   string                 `json:"targetClusterIP,omitempty"`
	TargetBackendPod  string                 `json:"targetBackendPod,omitempty"`
	TargetBackendIP   string                 `json:"targetBackendIP,omitempty"`
	TargetBackendNode string                 `json:"targetBackendNode,omitempty"`
	ServicePort       int                    `json:"servicePort,omitempty"`
	BackendPort       int                    `json:"backendPort,omitempty"`
	Protocol          string                 `json:"protocol"`
	InitialResult         string                 `json:"initialResult"`                   // broken, degraded, failed
	InitialStatus         string                 `json:"initialStatus,omitempty"`         // immutable initial status
	InitialFailureSummary string                 `json:"initialFailureSummary,omitempty"` // immutable initial failure summary (never overwritten by recovery; "原因待确认" if no evidence)
	InitialBackendState   string                 `json:"initialBackendState,omitempty"`   // e.g. "端口 8080/TCP · 后端 Ready 1/2，实测可达 1/1"
	FailureKind           string                 `json:"failureKind,omitempty"`
	FailureSummary        string                 `json:"failureSummary"`
	InitialLatencyMs      float64                `json:"initialLatencyMs,omitempty"`
	InitialBackends       []string               `json:"initialBackends,omitempty"`
	RecoveredAt           *time.Time             `json:"recoveredAt,omitempty"`
	RecoveryNarrative     string                 `json:"recoveryNarrative,omitempty"`
	Timeline              []ClusterChangeEvent   `json:"timeline"`
	Retests               []IncidentRetestRecord `json:"retests,omitempty"`
}

// IncidentRetestRecord stores one "retest with identical conditions" execution and before/after diff.
type IncidentRetestRecord struct {
	TestedAt         time.Time    `json:"testedAt"`
	ProbeSource      string       `json:"probeSource"`
	ClusterIPOK      bool         `json:"clusterIPOk"`
	ClusterIPLatMs   float64      `json:"clusterIPLatencyMs,omitempty"`
	ClusterIPFailure string       `json:"clusterIPFailure,omitempty"`
	DirectBackendOK  bool         `json:"directBackendOk"`
	DirectLatMs      float64      `json:"directLatencyMs,omitempty"`
	DirectFailure    string       `json:"directFailure,omitempty"`
	CurrentBackends  []string     `json:"currentBackends,omitempty"`
	Verdict          string       `json:"verdict"` // recovered, still_failing, partial_improved
	VerdictText      string       `json:"verdictText"`
	Diff             IncidentDiff `json:"diff"`
}

// IncidentDiff highlights exact changes between the original failure snapshot and the retest.
type IncidentDiff struct {
	StatusBefore   string   `json:"statusBefore"`
	StatusAfter    string   `json:"statusAfter"`
	FailureBefore  string   `json:"failureBefore"`
	FailureAfter   string   `json:"failureAfter"`
	LatencyBefore  float64  `json:"latencyBefore"`
	LatencyAfter   float64  `json:"latencyAfter"`
	BackendsAdded  []string `json:"backendsAdded,omitempty"`
	BackendsRemoved []string `json:"backendsRemoved,omitempty"`
	ChangesSummary []string `json:"changesSummary"`
}

// CreateIncidentRequest allows manual saving of a specific failure现场 from UI.
type CreateIncidentRequest struct {
	Title             string `json:"title"`
	Kind              string `json:"kind"` // service_port, flow_anomaly, traceflow
	SrcNamespace      string `json:"srcNamespace,omitempty"`
	SrcPod            string `json:"srcPod,omitempty"`
	TargetNamespace   string `json:"targetNamespace,omitempty"`
	TargetService     string `json:"targetService,omitempty"`
	TargetClusterIP   string `json:"targetClusterIP,omitempty"`
	TargetBackendPod  string `json:"targetBackendPod,omitempty"`
	TargetBackendIP   string `json:"targetBackendIP,omitempty"`
	ServicePort       int    `json:"servicePort,omitempty"`
	BackendPort       int    `json:"backendPort,omitempty"`
	Protocol          string `json:"protocol,omitempty"`
	FailureKind       string `json:"failureKind,omitempty"`
	FailureSummary    string `json:"failureSummary"`
	InitialLatencyMs  float64 `json:"initialLatencyMs,omitempty"`
}
