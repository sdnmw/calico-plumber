package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"calico-netdiag/pkg/agent"
	"calico-netdiag/pkg/agent/telemetry"
)

func main() {
	var (
		listenAddr    = flag.String("listen", envOr("AGENT_LISTEN", ":9191"), "Agent HTTP listen address")
		nodeName      = flag.String("node-name", envOr("NODE_NAME", hostname()), "Kubernetes Node name")
		podIP         = flag.String("pod-ip", envOr("POD_IP", ""), "Agent Pod/Host IP")
		serverURL     = flag.String("server-url", envOr("SERVER_URL", "http://calico-netdiag-server.calico-netdiag.svc:8080"), "Control plane Server URL")
		telemetryMode = flag.String("telemetry-mode", envOr("TELEMETRY_MODE", "auto"), "Telemetry mode: auto | ebpf | conntrack")
		pollInterval  = flag.Duration("poll-interval", 2*time.Second, "Telemetry poll/report interval")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("[calico-netdiag-agent] starting on node=%s listen=%s telemetry-mode=%s server=%s",
		*nodeName, *listenAddr, *telemetryMode, *serverURL)

	eng := telemetry.NewEngine(*nodeName, *podIP, *serverURL, *telemetryMode)
	mode, reason, _ := eng.Status()
	log.Printf("[calico-netdiag-agent] telemetry active=%s reason=%q", mode, reason)

	go eng.Run(ctx, *pollInterval)

	srv := &http.Server{
		Addr:              *listenAddr,
		Handler:           agent.NewServer(*nodeName, eng).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[calico-netdiag-agent] listen error: %v", err)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}
