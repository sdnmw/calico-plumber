package dataplane

import (
	"bufio"
	"net"
	"strings"
	"time"

	"calico-netdiag/pkg/model"
)

const defaultBirdSocket = "/var/run/calico/bird.ctl"

// QueryBIRDProtocols queries BIRD's Unix control socket directly (no birdcl binary needed).
func QueryBIRDProtocols(socketPath, nodeName string) []model.BIRDProtocol {
	if socketPath == "" {
		socketPath = defaultBirdSocket
	}
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	reader := bufio.NewReader(conn)
	// Read greeting line (e.g., "0001 BIRD v0.3.3+birdv1.6.8 ready.")
	if _, err := reader.ReadString('\n'); err != nil {
		return nil
	}
	if _, err := conn.Write([]byte("show protocols\n")); err != nil {
		return nil
	}

	var lines []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(trimmed, "0000") {
			break
		}
		lines = append(lines, trimmed)
	}
	return ParseBIRDProtocolsOutput(strings.Join(lines, "\n"), nodeName)
}

// ParseBIRDProtocolsOutput parses raw BIRD control socket or birdcl `show protocols` output.
func ParseBIRDProtocolsOutput(raw, nodeName string) []model.BIRDProtocol {
	var out []model.BIRDProtocol
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Strip BIRD numeric response codes like "1002-" or "2002-"
		if len(line) >= 5 && isDigits(line[:4]) && (line[4] == '-' || line[4] == ' ') {
			line = strings.TrimSpace(line[5:])
		}
		if strings.HasPrefix(line, "name ") || strings.HasPrefix(line, "BIRD ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		proto := model.BIRDProtocol{
			Name:  fields[0],
			Proto: fields[1],
			Table: fields[2],
			State: fields[3],
			Since: fields[4],
			Node:  nodeName,
		}
		if len(fields) >= 6 {
			// Sometimes Since includes a date + time (e.g. "2026-09-19 10:00:00") or just date + info ("Established")
			if strings.Contains(fields[5], ":") && len(fields) >= 7 {
				proto.Since = fields[4] + " " + fields[5]
				proto.Info = strings.Join(fields[6:], " ")
			} else {
				proto.Info = strings.Join(fields[5:], " ")
			}
		}
		// Extract peer IP from Calico mesh / peer protocol name (e.g. Mesh_192_168_118_206 or Node_192_168_118_206)
		if strings.EqualFold(proto.Proto, "BGP") {
			proto.PeerIP = extractPeerIPFromName(proto.Name)
		}
		out = append(out, proto)
	}
	return out
}

func extractPeerIPFromName(name string) string {
	for _, prefix := range []string{"Mesh_", "Node_", "Global_"} {
		if strings.HasPrefix(name, prefix) {
			candidate := strings.ReplaceAll(strings.TrimPrefix(name, prefix), "_", ".")
			if ip := net.ParseIP(candidate); ip != nil {
				return ip.String()
			}
		}
	}
	return ""
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
