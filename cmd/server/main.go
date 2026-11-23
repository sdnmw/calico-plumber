package main

import (
	"context"
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"calico-netdiag/pkg/k8s"
	"calico-netdiag/pkg/server/agentclient"
	"calico-netdiag/pkg/server/api"
	"calico-netdiag/pkg/server/flowstore"
	"calico-netdiag/pkg/server/healthcheck"
	"calico-netdiag/pkg/server/traceflow"
)

//go:embed all:ui
var embeddedUI embed.FS

//go:embed all:docs
var embeddedDocs embed.FS

func main() {
	var (
		listenAddr   = flag.String("listen", envOr("SERVER_LISTEN", ":8080"), "HTTP listen address")
		kubeconfig   = flag.String("kubeconfig", os.Getenv("KUBECONFIG"), "Optional kubeconfig path")
		namespace    = flag.String("namespace", envOr("POD_NAMESPACE", "calico-netdiag"), "Deployment namespace")
		agentPort    = flag.Int("agent-port", 9191, "Node Agent HTTP port")
		syncInterval = flag.Duration("k8s-sync-interval", 5*time.Second, "K8s & Calico CRD sync interval")
		healthEvery  = flag.Duration("health-interval", 60*time.Second, "Global health check interval")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	clients, err := k8s.NewClients(*kubeconfig)
	if err != nil {
		log.Fatalf("[calico-netdiag-server] init k8s clients: %v", err)
	}

	store := k8s.NewStore(clients)
	if err := store.Start(ctx, *syncInterval); err != nil {
		log.Fatalf("[calico-netdiag-server] start k8s store: %v", err)
	}

	flows := flowstore.NewStore(store, 1*time.Hour)
	agents := agentclient.NewClient(store, flows, *agentPort)
	tracer := traceflow.NewOrchestrator(store, agents)
	health := healthcheck.NewRunner(*namespace, store, flows, agents)
	health.Start(ctx, *healthEvery)

	var staticFS fs.FS
	if sub, err := fs.Sub(embeddedUI, "ui"); err == nil {
		if _, statErr := fs.Stat(sub, "index.html"); statErr == nil {
			staticFS = sub
		}
	}
	var docsFS fs.FS
	if sub, err := fs.Sub(embeddedDocs, "docs"); err == nil {
		if _, statErr := fs.Stat(sub, "index.html"); statErr == nil {
			docsFS = sub
		}
	}

	router := api.NewRouter(*namespace, store, flows, agents, tracer, health, staticFS, docsFS)
	srv := &http.Server{
		Addr:              *listenAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Printf("[calico-netdiag-server] listening on %s (namespace=%s)", *listenAddr, *namespace)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[calico-netdiag-server] listen error: %v", err)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
