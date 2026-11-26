package model

import "time"

// TCPDumpRequest configures a live packet capture session.
type TCPDumpRequest struct {
	Namespace   string `json:"namespace"`
	PodName     string `json:"podName"`
	NodeName    string `json:"nodeName,omitempty"`
	TargetIface string `json:"targetIface"` // pod:eth0, host:veth, host:tunl0, host:vxlan.calico, host:primary
	BPFFilter   string `json:"bpfFilter"`
	MaxPackets  int    `json:"maxPackets"`
	DurationSec int    `json:"durationSec"`
	SnapLen     int    `json:"snapLen"`
}

// PacketFrame is streamed over WebSocket to the UI during TCPdump.
type PacketFrame struct {
	Number     int       `json:"number"`
	Timestamp  time.Time `json:"timestamp"`
	RelTimeMs  float64   `json:"relTimeMs"`
	Interface  string    `json:"interface"`
	SrcIP      string    `json:"srcIP"`
	SrcPort    uint16    `json:"srcPort,omitempty"`
	DstIP      string    `json:"dstIP"`
	DstPort    uint16    `json:"dstPort,omitempty"`
	Protocol   string    `json:"protocol"`
	Length     int       `json:"length"`
	TCPFlags   string    `json:"tcpFlags,omitempty"`
	Info       string    `json:"info"`
	HexDump    string    `json:"hexDump,omitempty"`
	CaptureID  string    `json:"captureId,omitempty"`
}

// TCPDumpControlMessage wraps frames or lifecycle events on the WebSocket.
type TCPDumpControlMessage struct {
	Type      string       `json:"type"` // started, packet, stats, done, error
	CaptureID string       `json:"captureId,omitempty"`
	Interface string       `json:"interface,omitempty"`
	Message   string       `json:"message,omitempty"`
	PacketCnt int          `json:"packetCount,omitempty"`
	PcapReady bool         `json:"pcapReady,omitempty"`
	Frame     *PacketFrame `json:"frame,omitempty"`
}

// TCPHandshakeRequest tests TCP 3-way handshake from inside a Pod's netns.
type TCPHandshakeRequest struct {
	SrcNamespace string `json:"srcNamespace"`
	SrcPod       string `json:"srcPod"`
	TargetHost   string `json:"targetHost"` // IP, PodIP, Service DNS, or domain
	TargetPort   int    `json:"targetPort"`
	Count        int    `json:"count"`
	TimeoutMs    int    `json:"timeoutMs"`
}

// TCPHandshakeStep is one observed packet in the 3-way handshake ladder.
type TCPHandshakeStep struct {
	Step        string  `json:"step"` // DNS, SYN, SYN-ACK, ACK, RST, FIN, TIMEOUT
	Direction   string  `json:"direction"` // outbound, inbound, local
	TimestampMs float64 `json:"timestampMs"`
	DeltaMs     float64 `json:"deltaMs"`
	Seq         uint32  `json:"seq,omitempty"`
	Ack         uint32  `json:"ack,omitempty"`
	Window      uint16  `json:"window,omitempty"`
	Detail      string  `json:"detail"`
}

// TCPHandshakeAttempt is one round of 3-way handshake probing.
type TCPHandshakeAttempt struct {
	Round          int                `json:"round"`
	Success        bool               `json:"success"`
	Result         string             `json:"result"` // ESTABLISHED, RST_REFUSED, SYN_TIMEOUT, DNS_FAILED, ERROR
	ResolvedIP     string             `json:"resolvedIP,omitempty"`
	DNSLookupMs    float64            `json:"dnsLookupMs"`
	SynAckRTTMs    float64            `json:"synAckRttMs"`
	TotalMs        float64            `json:"totalMs"`
	Error          string             `json:"error,omitempty"`
	Steps          []TCPHandshakeStep `json:"steps"`
}

// TCPHandshakeResponse aggregates all rounds and root-cause analysis.
type TCPHandshakeResponse struct {
	SrcPod         string                `json:"srcPod"`
	SrcIP          string                `json:"srcIP"`
	TargetHost     string                `json:"targetHost"`
	ResolvedIP     string                `json:"resolvedIP"`
	TargetPort     int                   `json:"targetPort"`
	SuccessCount   int                   `json:"successCount"`
	TotalCount     int                   `json:"totalCount"`
	MinRTTMs       float64               `json:"minRttMs"`
	AvgRTTMs       float64               `json:"avgRttMs"`
	MaxRTTMs       float64               `json:"maxRttMs"`
	Diagnosis      string                `json:"diagnosis"`
	Attempts       []TCPHandshakeAttempt `json:"attempts"`
}

// HTTPProbeRequest runs an HTTP/HTTPS timing probe from inside a Pod's netns.
type HTTPProbeRequest struct {
	SrcNamespace    string            `json:"srcNamespace"`
	SrcPod          string            `json:"srcPod"`
	URL             string            `json:"url"`
	Method          string            `json:"method"`
	Headers         map[string]string `json:"headers,omitempty"`
	HostHeader      string            `json:"hostHeader,omitempty"`
	Body            string            `json:"body,omitempty"`
	InsecureSkipTLS bool              `json:"insecureSkipTLS"`
	FollowRedirects bool              `json:"followRedirects"`
	TimeoutMs       int               `json:"timeoutMs"`
}

// HTTPProbeResponse includes the full Chrome/curl-style waterfall timing breakdown.
type HTTPProbeResponse struct {
	SrcPod           string              `json:"srcPod"`
	SrcIP            string              `json:"srcIP"`
	URL              string              `json:"url"`
	Method           string              `json:"method"`
	Success          bool                `json:"success"`
	StatusCode       int                 `json:"statusCode"`
	StatusText       string              `json:"statusText,omitempty"`
	Protocol         string              `json:"protocol,omitempty"`
	ResolvedIP       string              `json:"resolvedIP,omitempty"`
	RemoteAddr       string              `json:"remoteAddr,omitempty"`
	DNSLookupMs      float64             `json:"dnsLookupMs"`
	TCPConnectMs     float64             `json:"tcpConnectMs"`
	TLSHandshakeMs   float64             `json:"tlsHandshakeMs"`
	ServerWaitTTFBMs float64             `json:"serverWaitTtfbMs"`
	ContentXferMs    float64             `json:"contentTransferMs"`
	TotalMs          float64             `json:"totalMs"`
	TLSVersion       string              `json:"tlsVersion,omitempty"`
	TLSCipher        string              `json:"tlsCipher,omitempty"`
	TLSCertSubject   string              `json:"tlsCertSubject,omitempty"`
	TLSCertExpiry    string              `json:"tlsCertExpiry,omitempty"`
	ResponseHeaders  map[string][]string `json:"responseHeaders,omitempty"`
	BodyPreview      string              `json:"bodyPreview,omitempty"`
	BodyBytes        int                 `json:"bodyBytes"`
	Error            string              `json:"error,omitempty"`
	Diagnosis        string              `json:"diagnosis"`
}

// ListeningSocket is an active LISTEN socket observed directly inside a Pod netns (/proc/net/tcp|tcp6|udp).
type ListeningSocket struct {
	Protocol  string `json:"protocol"`  // TCP, TCP6, UDP, UDP6
	LocalAddr string `json:"localAddr"` // 0.0.0.0, 127.0.0.1, ::, ::1
	Port      int    `json:"port"`
	Loopback  bool   `json:"loopback"` // true if bound only to 127.0.0.1 / ::1
}

// ServiceMatchingPodInfo describes a Pod matched by a Service's selector (including unready pods & real listening sockets).
type ServiceMatchingPodInfo struct {
	Namespace        string            `json:"namespace"`
	Name             string            `json:"name"`
	IP               string            `json:"ip"`
	NodeName         string            `json:"nodeName"`
	Phase            string            `json:"phase"`
	Ready            bool              `json:"ready"`
	NotReadyReason   string            `json:"notReadyReason,omitempty"`
	RestartCount     int32             `json:"restartCount"`
	ContainerPorts   []string          `json:"containerPorts,omitempty"`
	ListeningSockets []ListeningSocket `json:"listeningSockets,omitempty"`
	SocketInspectErr string            `json:"socketInspectErr,omitempty"`
	TargetPortMatch  string            `json:"targetPortMatch,omitempty"` // listening_all, listening_loopback_only, not_listening, unknown
}

// EndpointSliceBrief summarizes an EndpointSlice's addresses and conditions for empty/degraded backend diagnosis.
type EndpointSliceBrief struct {
	Name        string   `json:"name"`
	AddressType string   `json:"addressType"`
	Ports       []string `json:"ports"`
	Endpoints   []struct {
		Addresses   []string `json:"addresses"`
		Ready       *bool    `json:"ready,omitempty"`
		Serving     *bool    `json:"serving,omitempty"`
		Terminating *bool    `json:"terminating,omitempty"`
		TargetPod   string   `json:"targetPod,omitempty"`
		NodeName    string   `json:"nodeName,omitempty"`
	} `json:"endpoints"`
}

// ServiceInspectResponse answers "why are backends empty?" and "is the target Pod actually listening on targetPort?".
type ServiceInspectResponse struct {
	Namespace         string                   `json:"namespace"`
	Name              string                   `json:"name"`
	Type              string                   `json:"type"`
	ClusterIP         string                   `json:"clusterIP"`
	Selector          map[string]string        `json:"selector"`
	Ports             []ServicePortMapping     `json:"ports"`
	FocusPort         int                      `json:"focusPort,omitempty"`
	MatchingPods      []ServiceMatchingPodInfo `json:"matchingPods"`
	EndpointSlices    []EndpointSliceBrief     `json:"endpointSlices"`
	Conclusion        string                   `json:"conclusion"`
	EvidenceSource    string                   `json:"evidenceSource"` // "实际观测到"
	NextStepHint      string                   `json:"nextStepHint"`
}

// ServicePortMapping maps a declared Service port to its targetPort.
type ServicePortMapping struct {
	Name       string `json:"name,omitempty"`
	Port       int    `json:"port"`
	TargetPort string `json:"targetPort"`
	Protocol   string `json:"protocol"`
}

// CompareSourcesRequest compares reachability and applicable Calico/K8s policies between a probe Pod and a business Pod.
type CompareSourcesRequest struct {
	SrcNamespace    string `json:"srcNamespace"`
	SrcPod          string `json:"srcPod"`
	TargetNamespace string `json:"targetNamespace"`
	TargetService   string `json:"targetService"`
	TargetPort      int    `json:"targetPort"`
	Protocol        string `json:"protocol"` // TCP
}

// PolicyAttributionItem describes one K8s or Calico NetworkPolicy affecting a source or destination Pod.
type PolicyAttributionItem struct {
	Source         string   `json:"source"`         // K8s NetworkPolicy, Calico NetworkPolicy, Calico GlobalNetworkPolicy
	Namespace      string   `json:"namespace,omitempty"`
	Name           string   `json:"name"`
	Tier           string   `json:"tier,omitempty"`
	Order          *float64 `json:"order,omitempty"`
	Direction      string   `json:"direction"`      // Egress, Ingress
	Selector       string   `json:"selector"`       // podSelector / Calico selector
	RuleSummaries  []string `json:"ruleSummaries"`  // human-readable match conditions
	EffectHint     string   `json:"effectHint"`     // allow_likely, restrict_possible, default_deny
	EvidenceKind   string   `json:"evidenceKind"`   // "按配置推算"
}

// SourceProbeOutcome records live observed reachability from one source Pod to the target Service/backend.
type SourceProbeOutcome struct {
	Role           string                  `json:"role"` // probe, business
	PodNamespace   string                  `json:"podNamespace"`
	PodName        string                  `json:"podName"`
	PodIP          string                  `json:"podIP"`
	NodeName       string                  `json:"nodeName"`
	PodLabels      map[string]string       `json:"podLabels,omitempty"`
	ClusterIPOK    bool                    `json:"clusterIPOk"`
	ClusterIPLatMs float64                 `json:"clusterIPLatencyMs"`
	ClusterIPFail  string                  `json:"clusterIPFailure,omitempty"`
	ClusterIPErr   string                  `json:"clusterIPError,omitempty"`
	DirectPodOK    bool                    `json:"directPodOk"`
	DirectPodLatMs float64                 `json:"directPodLatencyMs"`
	DirectPodFail  string                  `json:"directPodFailure,omitempty"`
	DirectPodErr   string                  `json:"directPodError,omitempty"`
	EvidenceKind   string                  `json:"evidenceKind"` // "实际观测到"
	EgressPolicies []PolicyAttributionItem `json:"egressPolicies"`
}

// CompareSourcesResponse presents side-by-side observed probes and config-inferred policies.
type CompareSourcesResponse struct {
	CheckedAt          time.Time               `json:"checkedAt"`
	TargetService      string                  `json:"targetService"`
	TargetClusterIP    string                  `json:"targetClusterIP"`
	TargetPort         int                     `json:"targetPort"`
	BackendPod         string                  `json:"backendPod,omitempty"`
	BackendIP          string                  `json:"backendIP,omitempty"`
	BackendNode        string                  `json:"backendNode,omitempty"`
	BackendPort        int                     `json:"backendPort,omitempty"`
	BackendLabels      map[string]string       `json:"backendLabels,omitempty"`
	ProbeOutcome       SourceProbeOutcome      `json:"probeOutcome"`
	BusinessOutcome    SourceProbeOutcome      `json:"businessOutcome"`
	TargetIngressPols  []PolicyAttributionItem `json:"targetIngressPolicies"`
	PolicyDiffSummary  string                  `json:"policyDiffSummary"`
	Conclusion         string                  `json:"conclusion"`
	RecommendedNext    string                  `json:"recommendedNext"`
}
