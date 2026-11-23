package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"calico-netdiag/pkg/agent/dataplane"
	"calico-netdiag/pkg/agent/diag"
	"calico-netdiag/pkg/agent/telemetry"
	"calico-netdiag/pkg/model"

	"github.com/gorilla/websocket"
)

// Server exposes internal HTTP & WebSocket endpoints on the Node Agent for the Control Plane Server.
type Server struct {
	nodeName  string
	telemetry *telemetry.Engine
	upgrader  websocket.Upgrader
}

// NewServer constructs an Agent HTTP API handler.
func NewServer(nodeName string, eng *telemetry.Engine) *Server {
	return &Server{
		nodeName:  nodeName,
		telemetry: eng,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// Routes registers all internal Agent diagnostic & dataplane endpoints.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/api/dataplane/host", s.handleHostDataplane)
	mux.HandleFunc("/api/dataplane/pod", s.handlePodDataplane)
	mux.HandleFunc("/api/dataplane/kubeproxy", s.handleKubeProxyLookup)
	mux.HandleFunc("/api/diag/traceflow", s.handleTraceFlow)
	mux.HandleFunc("/api/diag/tcp-handshake", s.handleTCPHandshake)
	mux.HandleFunc("/api/diag/http-probe", s.handleHTTPProbe)
	mux.HandleFunc("/api/diag/tcpdump", s.handleTCPDumpWS)
	mux.HandleFunc("/api/diag/pcap/", s.handlePcapDownload)
	mux.HandleFunc("/api/diag/tcp-check", s.handleHostTCPCheck)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	mode, reason, flows := s.telemetry.Status()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":          "ok",
		"nodeName":        s.nodeName,
		"telemetryEngine": mode,
		"telemetryReason": reason,
		"lastFlowsCount":  flows,
	})
}

func (s *Server) handleHostDataplane(w http.ResponseWriter, _ *http.Request) {
	info := dataplane.InspectHost(s.nodeName)
	mode, reason, flows := s.telemetry.Status()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"nodeName":        s.nodeName,
		"hostInfo":        info,
		"telemetryEngine": mode,
		"telemetryReason": reason,
		"lastFlowsCount":  flows,
	})
}

func (s *Server) handlePodDataplane(w http.ResponseWriter, r *http.Request) {
	podUID := r.URL.Query().Get("uid")
	podIP := r.URL.Query().Get("ip")
	veth, pIface, mac, mtu, routes, hostRoute, err := dataplane.InspectPodNetns(podUID, podIP)
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"hostVeth":       veth,
		"podIface":       pIface,
		"podMAC":         mac,
		"podMTU":         mtu,
		"podRoutes":      routes,
		"hostRouteToPod": hostRoute,
		"policyChains":   dataplane.InspectCalicoPolicyChains(veth),
		"error":          errStr,
	})
}

func (s *Server) handleKubeProxyLookup(w http.ResponseWriter, r *http.Request) {
	clusterIP := r.URL.Query().Get("ip")
	proto := r.URL.Query().Get("proto")
	port := 0
	_, _ = fmt.Sscanf(r.URL.Query().Get("port"), "%d", &port)
	info := dataplane.InspectHost(s.nodeName)
	rule := dataplane.LookupKubeProxyService(info.KubeProxyMode, clusterIP, port, proto)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"mode": info.KubeProxyMode,
		"rule": rule,
	})
}

func (s *Server) handleTraceFlow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req model.AgentTraceCaptureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	res, err := diag.ExecuteTraceCapture(r.Context(), s.nodeName, req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleTCPHandshake(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload struct {
		PodUID     string                    `json:"podUid"`
		PodIP      string                    `json:"podIP"`
		PodDisplay string                    `json:"podDisplay"`
		Request    model.TCPHandshakeRequest `json:"request"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	res, err := diag.RunTCPHandshake(r.Context(), payload.PodUID, payload.PodIP, payload.PodDisplay, payload.Request)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleHTTPProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload struct {
		PodUID     string                 `json:"podUid"`
		PodIP      string                 `json:"podIP"`
		PodDisplay string                 `json:"podDisplay"`
		Request    model.HTTPProbeRequest `json:"request"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	res, err := diag.RunHTTPProbe(r.Context(), payload.PodUID, payload.PodIP, payload.PodDisplay, payload.Request)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleTCPDumpWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	var startMsg struct {
		PodUID  string               `json:"podUid"`
		PodIP   string               `json:"podIP"`
		Request model.TCPDumpRequest `json:"request"`
	}
	if err := conn.ReadJSON(&startMsg); err != nil {
		_ = conn.WriteJSON(model.TCPDumpControlMessage{Type: "error", Message: "invalid start payload: " + err.Error()})
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Listen for client-side stop signal or disconnect
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				cancel()
				return
			}
			if strings.Contains(string(msg), "stop") {
				cancel()
				return
			}
		}
	}()

	err = diag.RunTCPDump(ctx, startMsg.PodUID, startMsg.PodIP, startMsg.Request, func(m model.TCPDumpControlMessage) error {
		return conn.WriteJSON(m)
	})
	if err != nil && ctx.Err() == nil {
		_ = conn.WriteJSON(model.TCPDumpControlMessage{Type: "error", Message: err.Error()})
	}
}

func (s *Server) handlePcapDownload(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/diag/pcap/")
	id = strings.TrimSuffix(id, ".pcap")
	p, err := diag.GetPcapFilePath(id)
	if err != nil {
		http.Error(w, "pcap not found or expired", http.StatusNotFound)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.Error(w, "open pcap error", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"capture-%s.pcap\"", id))
	http.ServeContent(w, r, id+".pcap", time.Now(), f)
}

// handleHostTCPCheck performs fast parallel TCP reachability checks from the node host netns
// (used by Global Health Check to audit Service ClusterIPs and backend Pod IP:TargetPorts).
func (s *Server) handleHostTCPCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	type target struct {
		ID      string `json:"id"`
		Address string `json:"address"`
	}
	var req struct {
		TimeoutMs int      `json:"timeoutMs"`
		Targets   []target `json:"targets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	if timeout <= 0 || timeout > 5*time.Second {
		timeout = 1200 * time.Millisecond
	}

	type result struct {
		ID        string  `json:"id"`
		Address   string  `json:"address"`
		OK        bool    `json:"ok"`
		LatencyMs float64 `json:"latencyMs"`
		Error     string  `json:"error,omitempty"`
	}
	out := make([]result, len(req.Targets))
	sem := make(chan struct{}, 24)
	done := make(chan struct{}, len(req.Targets))

	for i, t := range req.Targets {
		go func(idx int, tgt target) {
			sem <- struct{}{}
			defer func() {
				<-sem
				done <- struct{}{}
			}()
			st := time.Now()
			c, err := net.DialTimeout("tcp4", tgt.Address, timeout)
			lat := float64(time.Since(st).Microseconds()) / 1000.0
			lat = float64(int(lat*100)) / 100
			if err != nil {
				out[idx] = result{ID: tgt.ID, Address: tgt.Address, OK: false, LatencyMs: lat, Error: err.Error()}
				return
			}
			_ = c.Close()
			out[idx] = result{ID: tgt.ID, Address: tgt.Address, OK: true, LatencyMs: lat}
		}(i, t)
	}
	for range req.Targets {
		<-done
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"results": out})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
