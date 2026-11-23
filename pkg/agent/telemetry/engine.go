package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"calico-netdiag/pkg/agent/dataplane"
	"calico-netdiag/pkg/model"
)

// Collector abstracts Mode 1 (eBPF CO-RE) and Mode 2 (Conntrack + INET_DIAG).
type Collector interface {
	Mode() model.TelemetryEngineMode
	Poll() ([]model.AgentFlowSample, error)
	Close() error
}

// Engine manages adaptive telemetry collection and periodic batch reporting to the Server.
type Engine struct {
	nodeName      string
	agentPodIP    string
	serverURL     string
	requestedMode string // auto, ebpf, conntrack

	mu         sync.RWMutex
	collector  Collector
	activeMode model.TelemetryEngineMode
	reason     string
	lastFlows  int
	httpClient *http.Client
}

// NewEngine initializes the telemetry engine with automatic eBPF -> Conntrack fallback.
func NewEngine(nodeName, agentPodIP, serverURL, requestedMode string) *Engine {
	req := strings.ToLower(strings.TrimSpace(requestedMode))
	if req == "" {
		req = "auto"
	}

	e := &Engine{
		nodeName:      nodeName,
		agentPodIP:    agentPodIP,
		serverURL:     strings.TrimRight(serverURL, "/"),
		requestedMode: req,
		httpClient:    &http.Client{Timeout: 5 * time.Second},
	}

	switch req {
	case "conntrack", "legacy":
		e.collector = newConntrackCollector(nodeName)
		e.activeMode = model.TelemetryConntrack
		e.reason = "Explicitly configured via TELEMETRY_MODE=legacy"
	case "ebpf":
		col, err := newEBPFCollector(nodeName)
		if err != nil {
			log.Printf("[telemetry] requested eBPF failed (%v); falling back to Legacy mode", err)
			e.collector = newConntrackCollector(nodeName)
			e.activeMode = model.TelemetryConntrack
			e.reason = fmt.Sprintf("Fallback to Legacy: %v", err)
		} else {
			e.collector = col
			e.activeMode = model.TelemetryEBPF
			e.reason = "eBPF active (BTF verified)"
		}
	default: // auto
		col, err := newEBPFCollector(nodeName)
		if err != nil {
			log.Printf("[telemetry] auto-detect: eBPF unavailable (%v) → activating Legacy mode (Conntrack + INET_DIAG)", err)
			e.collector = newConntrackCollector(nodeName)
			e.activeMode = model.TelemetryConntrack
			e.reason = fmt.Sprintf("Auto-fallback to Legacy (%v)", err)
		} else {
			log.Printf("[telemetry] auto-detect: eBPF activated on node %s", nodeName)
			e.collector = col
			e.activeMode = model.TelemetryEBPF
			e.reason = "Auto-selected eBPF (kernel BTF supported)"
		}
	}

	return e
}

// Status returns the active telemetry engine mode and diagnostic reason.
func (e *Engine) Status() (model.TelemetryEngineMode, string, int) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeMode, e.reason, e.lastFlows
}

// Run starts the poll-and-push telemetry loop until ctx is canceled.
func (e *Engine) Run(ctx context.Context, interval time.Duration) {
	if interval < time.Second {
		interval = 2 * time.Second
	}
	// Initial immediate push so Server sees this node right away.
	e.pushOnce(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = e.collector.Close()
			return
		case <-ticker.C:
			e.pushOnce(ctx)
		}
	}
}

func (e *Engine) pushOnce(ctx context.Context) {
	flows, err := e.collector.Poll()
	if err != nil {
		log.Printf("[telemetry] poll error: %v", err)
	}

	hostInfo := dataplane.InspectHost(e.nodeName)
	e.mu.Lock()
	e.lastFlows = len(flows)
	mode := e.activeMode
	reason := e.reason
	e.mu.Unlock()

	batch := model.AgentFlowBatch{
		NodeName:      e.nodeName,
		AgentPodIP:    e.agentPodIP,
		Engine:        mode,
		EngineReason:  reason,
		BTFAvailable:  hostInfo.BTFAvailable,
		ConntrackAcct: hostInfo.ConntrackAcct,
		KubeProxyMode: hostInfo.KubeProxyMode,
		KernelVersion: hostInfo.KernelVersion,
		PrimaryNIC:    hostInfo.PrimaryNIC,
		NICMTU:        hostInfo.NICMTU,
		TunnelIfaces:  hostInfo.TunnelInterfaces,
		BGPProtocols:  hostInfo.BGPProtocols,
		Timestamp:     time.Now(),
		Flows:         flows,
	}

	if e.serverURL == "" {
		return
	}
	payload, err := json.Marshal(batch)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.serverURL+"/api/internal/agent/report", bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.httpClient.Do(req)
	if err != nil {
		log.Printf("[telemetry] report push failed: %v", err)
		return
	}
	_ = resp.Body.Close()
}
