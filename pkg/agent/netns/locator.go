package netns

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/vishvananda/netlink"
)

// PodNetInfo describes the host-side veth and network namespace path for a local Pod.
type PodNetInfo struct {
	PodUID     string
	PodIP      string
	PID        int
	NetnsPath  string
	NetnsInode uint64
	HostVeth   string
	HostNet    bool
}

// LocatePod finds the network namespace (/proc/<pid>/ns/net) and host Calico veth (cali*)
// for a Pod running on the local node. Works across containerd, CRI-O, and dockerd,
// and supports systemd (underscored UUID) and cgroupfs (dashed UUID) cgroup layouts.
func LocatePod(podUID, podIP string) (*PodNetInfo, error) {
	info := &PodNetInfo{
		PodUID: podUID,
		PodIP:  podIP,
	}

	// 1. Discover host-side cali* veth via /32 kernel route if podIP is provided.
	if podIP != "" {
		info.HostVeth = FindHostVethByIP(podIP)
	}

	// 2. Discover Pod PID and /proc/<pid>/ns/net by scanning /proc/*/cgroup for podUID.
	if podUID != "" {
		uidDashed := strings.ToLower(strings.TrimSpace(podUID))
		uidUnderscored := strings.ReplaceAll(uidDashed, "-", "_")
		hostInode := getNetnsInode("/proc/1/ns/net")

		entries, err := os.ReadDir("/proc")
		if err != nil {
			return nil, fmt.Errorf("read /proc: %w", err)
		}

		var hostNetCandidatePID int
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			pid, err := strconv.Atoi(e.Name())
			if err != nil || pid <= 1 {
				continue
			}
			cgBytes, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cgroup"))
			if err != nil {
				continue
			}
			cg := strings.ToLower(string(cgBytes))
			if !strings.Contains(cg, uidDashed) && !strings.Contains(cg, uidUnderscored) {
				continue
			}
			nsPath := filepath.Join("/proc", e.Name(), "ns", "net")
			inode := getNetnsInode(nsPath)
			if inode == 0 {
				continue
			}
			if inode != hostInode {
				info.PID = pid
				info.NetnsPath = nsPath
				info.NetnsInode = inode
				info.HostNet = false
				return info, nil
			}
			// Keep fallback candidate if pod is hostNetwork: true
			if hostNetCandidatePID == 0 {
				hostNetCandidatePID = pid
			}
		}

		if hostNetCandidatePID > 0 {
			nsPath := filepath.Join("/proc", strconv.Itoa(hostNetCandidatePID), "ns", "net")
			info.PID = hostNetCandidatePID
			info.NetnsPath = nsPath
			info.NetnsInode = getNetnsInode(nsPath)
			info.HostNet = true
			return info, nil
		}
	}

	if info.HostVeth != "" {
		// Even if container exited or PID wasn't found, return partial host veth info with error.
		return info, fmt.Errorf("pod netns not found in /proc for uid=%s (host veth=%s)", podUID, info.HostVeth)
	}
	return nil, fmt.Errorf("pod uid=%s ip=%s not located on local node", podUID, podIP)
}

// FindHostVethByIP resolves the host-side cali* veth interface name from the local routing table.
func FindHostVethByIP(podIP string) string {
	ip := net.ParseIP(podIP)
	if ip == nil {
		return ""
	}
	routes, err := netlink.RouteGet(ip)
	if err != nil {
		return ""
	}
	for _, r := range routes {
		if r.LinkIndex <= 0 {
			continue
		}
		link, err := netlink.LinkByIndex(r.LinkIndex)
		if err != nil {
			continue
		}
		name := link.Attrs().Name
		if strings.HasPrefix(name, "cali") || strings.HasPrefix(name, "veth") {
			return name
		}
	}
	return ""
}

// ListUniqueNetnsPaths returns deduplicated /proc/<pid>/ns/net paths (including host netns)
// across all running processes on the node, used by Mode 2 INET_DIAG socket telemetry.
func ListUniqueNetnsPaths() []string {
	seen := make(map[uint64]string)
	if hostInode := getNetnsInode("/proc/1/ns/net"); hostInode > 0 {
		seen[hostInode] = "/proc/1/ns/net"
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		var out []string
		for _, p := range seen {
			out = append(out, p)
		}
		return out
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		nsPath := filepath.Join("/proc", e.Name(), "ns", "net")
		inode := getNetnsInode(nsPath)
		if inode == 0 {
			continue
		}
		if _, exists := seen[inode]; !exists {
			seen[inode] = nsPath
		}
	}

	out := make([]string, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	return out
}

func getNetnsInode(path string) uint64 {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0
	}
	return st.Ino
}
