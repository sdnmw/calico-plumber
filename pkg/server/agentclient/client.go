package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"calico-netdiag/pkg/k8s"
	"calico-netdiag/pkg/model"
	"calico-netdiag/pkg/server/flowstore"

	"github.com/gorilla/websocket"
)

// Client talks to per-node calico-netdiag-agent DaemonSet instances.
type Client struct {
	k8sStore   *k8s.Store
	flows      *flowstore.Store
	agentPort  int
	httpClient *http.Client
}

// NewClient constructs a Node Agent client.
func NewClient(k8sStore *k8s.Store, flows *flowstore.Store, agentPort int) *Client {
	if agentPort <= 0 {
		agentPort = 9191
	}
	return &Client{
		k8sStore:  k8sStore,
		flows:     flows,
		agentPort: agentPort,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// AgentAddress resolves http://<ip>:<port> for a Kubernetes Node.
func (c *Client) AgentAddress(nodeName string) (string, error) {
	if snap, ok := c.flows.GetNodeSnapshot(nodeName); ok && snap.AgentPodIP != "" {
		return fmt.Sprintf("http://%s:%d", snap.AgentPodIP, c.agentPort), nil
	}
	if node, ok := c.k8sStore.GetNode(nodeName); ok {
		if ip := k8s.NodeInternalIP(node); ip != "" {
			return fmt.Sprintf("http://%s:%d", ip, c.agentPort), nil
		}
	}
	return "", fmt.Errorf("agent endpoint not found for node %q", nodeName)
}

// PodDataplaneResp is the JSON payload returned by an Agent's /api/dataplane/pod endpoint.
type PodDataplaneResp struct {
	HostVeth       string             `json:"hostVeth"`
	PodIface       string             `json:"podIface"`
	PodMAC         string             `json:"podMAC"`
	PodMTU         int                `json:"podMTU"`
	PodRoutes      []model.RouteEntry `json:"podRoutes"`
	HostRouteToPod string             `json:"hostRouteToPod"`
	PolicyChains   string             `json:"policyChains"`
	Error          string             `json:"error"`
}

func (c *Client) GetPodDataplane(ctx context.Context, nodeName, podUID, podIP string) (*PodDataplaneResp, error) {
	base, err := c.AgentAddress(nodeName)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/api/dataplane/pod?uid=%s&ip=%s", base, url.QueryEscape(podUID), url.QueryEscape(podIP))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out PodDataplaneResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) RunTraceCapture(ctx context.Context, nodeName string, treq model.AgentTraceCaptureRequest) (*model.AgentTraceCaptureResponse, error) {
	base, err := c.AgentAddress(nodeName)
	if err != nil {
		return nil, err
	}
	var out model.AgentTraceCaptureResponse
	if err := c.postJSON(ctx, base+"/api/diag/traceflow", treq, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) RunTCPHandshake(ctx context.Context, nodeName, podUID, podIP, podDisplay string, hreq model.TCPHandshakeRequest) (*model.TCPHandshakeResponse, error) {
	base, err := c.AgentAddress(nodeName)
	if err != nil {
		return nil, err
	}
	body := map[string]interface{}{
		"podUid":     podUID,
		"podIP":      podIP,
		"podDisplay": podDisplay,
		"request":    hreq,
	}
	var out model.TCPHandshakeResponse
	if err := c.postJSON(ctx, base+"/api/diag/tcp-handshake", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) RunHTTPProbe(ctx context.Context, nodeName, podUID, podIP, podDisplay string, preq model.HTTPProbeRequest) (*model.HTTPProbeResponse, error) {
	base, err := c.AgentAddress(nodeName)
	if err != nil {
		return nil, err
	}
	body := map[string]interface{}{
		"podUid":     podUID,
		"podIP":      podIP,
		"podDisplay": podDisplay,
		"request":    preq,
	}
	var out model.HTTPProbeResponse
	if err := c.postJSON(ctx, base+"/api/diag/http-probe", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// HostTCPTarget & HostTCPResult are used for fast parallel Service/Backend health auditing.
type HostTCPTarget struct {
	ID      string `json:"id"`
	Address string `json:"address"`
}

type HostTCPResult struct {
	ID        string  `json:"id"`
	Address   string  `json:"address"`
	OK        bool    `json:"ok"`
	LatencyMs float64 `json:"latencyMs"`
	Error     string  `json:"error,omitempty"`
}

func (c *Client) HostTCPCheck(ctx context.Context, nodeName string, timeoutMs int, targets []HostTCPTarget) ([]HostTCPResult, error) {
	base, err := c.AgentAddress(nodeName)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Results []HostTCPResult `json:"results"`
	}
	err = c.postJSON(ctx, base+"/api/diag/tcp-check", map[string]interface{}{
		"timeoutMs": timeoutMs,
		"targets":   targets,
	}, &resp)
	return resp.Results, err
}

// ProxyTCPDumpWS bridges the browser WebSocket directly to the target node Agent's TCPdump WebSocket.
func (c *Client) ProxyTCPDumpWS(browserConn *websocket.Conn, nodeName, podUID, podIP string, req model.TCPDumpRequest) error {
	base, err := c.AgentAddress(nodeName)
	if err != nil {
		return err
	}
	wsURL := "ws" + base[4:] + "/api/diag/tcpdump"
	agentConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("connect agent tcpdump ws (%s): %w", nodeName, err)
	}
	defer agentConn.Close()

	// Send start payload to agent
	if err := agentConn.WriteJSON(map[string]interface{}{
		"podUid":  podUID,
		"podIP":   podIP,
		"request": req,
	}); err != nil {
		return err
	}

	// Forward stop/control messages from browser -> agent
	go func() {
		for {
			mt, msg, err := browserConn.ReadMessage()
			if err != nil {
				_ = agentConn.WriteMessage(websocket.TextMessage, []byte("stop"))
				return
			}
			_ = agentConn.WriteMessage(mt, msg)
		}
	}()

	// Stream packet frames from agent -> browser
	for {
		mt, msg, err := agentConn.ReadMessage()
		if err != nil {
			return nil
		}
		if err := browserConn.WriteMessage(mt, msg); err != nil {
			return nil
		}
	}
}

// ProxyPcapDownload streams a saved .pcap file from a node Agent to the HTTP response writer.
func (c *Client) ProxyPcapDownload(w http.ResponseWriter, r *http.Request, nodeName, captureID string) error {
	base, err := c.AgentAddress(nodeName)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, base+"/api/diag/pcap/"+captureID, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent returned status %d", resp.StatusCode)
	}
	w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"capture-%s.pcap\"", captureID))
	_, err = io.Copy(w, resp.Body)
	return err
}

func (c *Client) postJSON(ctx context.Context, targetURL string, body interface{}, out interface{}) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("agent HTTP %d: %s", resp.StatusCode, string(raw))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
