//go:build !(linux && amd64 && ebpf)

package telemetry

import (
	"fmt"
	"runtime"
)

func newEBPFCollector(nodeName string) (Collector, error) {
	return nil, fmt.Errorf("eBPF CO-RE collector not enabled in this build (%s/%s)", runtime.GOOS, runtime.GOARCH)
}
