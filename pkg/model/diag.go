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
