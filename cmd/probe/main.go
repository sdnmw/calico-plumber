package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

// calico-netdiag-probe is an ultra-lightweight Pod-network canary daemon (1 pod per node, ~8MiB RAM)
// that acts as both a Pod-IP echo target and an active prober for Pod-Pod, Pod-Node, Pod-SVC, and DNS health checks.
func main() {
	listenAddr := flag.String("listen", ":8099", "Probe HTTP listen address")
	flag.Parse()

	nodeName := os.Getenv("NODE_NAME")
	podName := os.Getenv("POD_NAME")
	podIP := os.Getenv("POD_IP")

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":   "pong",
			"nodeName": nodeName,
			"podName":  podName,
			"podIP":    podIP,
		})
	})

	// POST /probe executes concurrent TCP, HTTP, and DNS probes from inside this Pod's Calico network.
	mux.HandleFunc("/probe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		type probeTask struct {
			ID        string `json:"id"`
			Kind      string `json:"kind"` // tcp, http, dns
			Target    string `json:"target"`
			TimeoutMs int    `json:"timeoutMs"`
		}
		var req struct {
			Tasks []probeTask `json:"tasks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		type probeResult struct {
			ID        string  `json:"id"`
			OK        bool    `json:"ok"`
			LatencyMs float64 `json:"latencyMs"`
			Detail    string  `json:"detail,omitempty"`
			Error     string  `json:"error,omitempty"`
		}
		results := make([]probeResult, len(req.Tasks))
		done := make(chan struct{}, len(req.Tasks))
		httpCli := &http.Client{Timeout: 2500 * time.Millisecond}

		for i, task := range req.Tasks {
			go func(idx int, t probeTask) {
				defer func() { done <- struct{}{} }()
				timeout := time.Duration(t.TimeoutMs) * time.Millisecond
				if timeout <= 0 || timeout > 5*time.Second {
					timeout = 2 * time.Second
				}
				start := time.Now()
				switch t.Kind {
				case "http":
					hctx, cancel := context.WithTimeout(r.Context(), timeout)
					defer cancel()
					hreq, err := http.NewRequestWithContext(hctx, http.MethodGet, t.Target, nil)
					if err != nil {
						results[idx] = probeResult{ID: t.ID, Error: err.Error()}
						return
					}
					resp, err := httpCli.Do(hreq)
					lat := round2(float64(time.Since(start).Microseconds()) / 1000.0)
					if err != nil {
						results[idx] = probeResult{ID: t.ID, LatencyMs: lat, Error: err.Error()}
						return
					}
					_ = resp.Body.Close()
					results[idx] = probeResult{
						ID:        t.ID,
						OK:        resp.StatusCode < 500,
						LatencyMs: lat,
						Detail:    fmt.Sprintf("HTTP %d", resp.StatusCode),
					}
				case "dns":
					dctx, cancel := context.WithTimeout(r.Context(), timeout)
					defer cancel()
					ips, err := net.DefaultResolver.LookupHost(dctx, t.Target)
					lat := round2(float64(time.Since(start).Microseconds()) / 1000.0)
					if err != nil {
						results[idx] = probeResult{ID: t.ID, LatencyMs: lat, Error: err.Error()}
						return
					}
					detail := ""
					if len(ips) > 0 {
						detail = ips[0]
					}
					results[idx] = probeResult{ID: t.ID, OK: true, LatencyMs: lat, Detail: detail}
				default: // tcp
					conn, err := net.DialTimeout("tcp4", t.Target, timeout)
					lat := round2(float64(time.Since(start).Microseconds()) / 1000.0)
					if err != nil {
						results[idx] = probeResult{ID: t.ID, LatencyMs: lat, Error: err.Error()}
						return
					}
					_ = conn.Close()
					results[idx] = probeResult{ID: t.ID, OK: true, LatencyMs: lat, Detail: "TCP connected"}
				}
			}(i, task)
		}
		for range req.Tasks {
			<-done
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"nodeName": nodeName,
			"podName":  podName,
			"podIP":    podIP,
			"results":  results,
		})
	})

	log.Printf("[calico-netdiag-probe] listening on %s (node=%s pod=%s ip=%s)", *listenAddr, nodeName, podName, podIP)
	srv := &http.Server{Addr: *listenAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100.0
}
