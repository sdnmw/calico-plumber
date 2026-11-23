package k8s

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Clients bundles typed and dynamic Kubernetes clients.
type Clients struct {
	Config  *rest.Config
	Typed   kubernetes.Interface
	Dynamic dynamic.Interface
}

// NewClients creates Kubernetes clients from in-cluster config or kubeconfig path.
func NewClients(kubeconfigPath string) (*Clients, error) {
	cfg, err := buildConfig(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	cfg.QPS = 50
	cfg.Burst = 100

	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create typed k8s client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create dynamic k8s client: %w", err)
	}
	return &Clients{
		Config:  cfg,
		Typed:   typed,
		Dynamic: dyn,
	}, nil
}

func buildConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	}
	if envPath := os.Getenv("KUBECONFIG"); envPath != "" {
		return clientcmd.BuildConfigFromFlags("", envPath)
	}
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	home, _ := os.UserHomeDir()
	defaultPath := filepath.Join(home, ".kube", "config")
	if _, err := os.Stat(defaultPath); err == nil {
		return clientcmd.BuildConfigFromFlags("", defaultPath)
	}
	return nil, fmt.Errorf("unable to locate in-cluster config or kubeconfig")
}
