package flowstore

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"calico-netdiag/pkg/k8s"
	"calico-netdiag/pkg/model"
)

// NodeAgentSnapshot stores the latest heartbeat & dataplane metadata from a Node Agent.
type NodeAgentSnapshot struct {
	NodeName      string
	AgentPodIP    string
	Engine        model.TelemetryEngineMode
	EngineReason  string
	BTFAvailable  bool
	ConntrackAcct bool
	KubeProxyMode model.KubeProxyMode
	KernelVersion string
	PrimaryNIC    string
	NICMTU        int
	TunnelIfaces  []model.TunnelIfaceInfo
	BGPProtocols  []model.BIRDProtocol
	ActiveFlows   int
	LastHeartbeat time.Time
}

type rawBucketEntry struct {
	timestamp   time.Time
	nodeName    string
	engine      model.TelemetryEngineMode
	srcIP       string
	dstIP       string
	srcPort     uint16
	dstPort     uint16
	protocol    string
	serviceIP   string
	servicePort uint16
	txBytes     uint64
	rxBytes     uint64
	txPackets   uint64
	rxPackets   uint64
	rttAvgUs    uint32
	rttMaxUs    uint32
	retransmits uint32
	resets      uint32
	state       string
}

// Store aggregates real-time flow samples across all nodes in a bounded in-memory ring.
type Store struct {
	k8sStore  *k8s.Store
	mu        sync.RWMutex
	nodes     map[string]*NodeAgentSnapshot
	entries   []rawBucketEntry
	bgpEvents []model.BGPEvent
	maxAge    time.Duration
}

// NewStore creates a flow aggregation store retaining up to maxAge of samples.
func NewStore(k8sStore *k8s.Store, maxAge time.Duration) *Store {
	if maxAge <= 0 {
		maxAge = 1 * time.Hour
	}
	return &Store{
		k8sStore:  k8sStore,
		nodes:     make(map[string]*NodeAgentSnapshot),
		entries:   make([]rawBucketEntry, 0, 4096),
		bgpEvents: make([]model.BGPEvent, 0, 64),
		maxAge:    maxAge,
	}
}

// Ingest processes a periodic AgentFlowBatch from a Node Agent.
func (s *Store) Ingest(batch model.AgentFlowBatch) {
	now := batch.Timestamp
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Detect BGP session state transitions (flapping / session down / recovery) vs previous heartbeat
	if prev, ok := s.nodes[batch.NodeName]; ok {
		s.recordBGPTransitionsLocked(batch.NodeName, prev.BGPProtocols, batch.BGPProtocols, time.Now())
	} else {
		// Record initial non-Established BGP peers so immediate faults appear in timeline
		for _, bp := range batch.BGPProtocols {
			if !strings.EqualFold(bp.Proto, "BGP") {
				continue
			}
			st := bgpStateLabel(bp)
			if !strings.Contains(strings.ToLower(st), "established") {
				s.appendBGPEventLocked(model.BGPEvent{
					Timestamp: time.Now(),
					NodeName:  batch.NodeName,
					PeerName:  bp.Name,
					PeerIP:    bp.PeerIP,
					PrevState: "Unknown",
					NewState:  st,
					Severity:  "warning",
					Message:   fmt.Sprintf("节点 %s 首次上报时 BGP 邻居 %s (%s) 状态异常: %s", batch.NodeName, bp.Name, bp.PeerIP, st),
				})
			}
		}
	}

	s.nodes[batch.NodeName] = &NodeAgentSnapshot{
		NodeName:      batch.NodeName,
		AgentPodIP:    batch.AgentPodIP,
		Engine:        batch.Engine,
		EngineReason:  batch.EngineReason,
		BTFAvailable:  batch.BTFAvailable,
		ConntrackAcct: batch.ConntrackAcct,
		KubeProxyMode: batch.KubeProxyMode,
		KernelVersion: batch.KernelVersion,
		PrimaryNIC:    batch.PrimaryNIC,
		NICMTU:        batch.NICMTU,
		TunnelIfaces:  batch.TunnelIfaces,
		BGPProtocols:  batch.BGPProtocols,
		ActiveFlows:   len(batch.Flows),
		LastHeartbeat: time.Now(),
	}

	for _, f := range batch.Flows {
		ts := f.Timestamp
		if ts.IsZero() {
			ts = now
		}
		// Normalize direction so client (ephemeral port) -> server (service/listening port)
		srcIP, dstIP := f.SrcIP, f.DstIP
		srcPort, dstPort := f.SrcPort, f.DstPort
		txB, rxB := f.TxBytes, f.RxBytes
		txP, rxP := f.TxPackets, f.RxPackets

		if shouldFlipDirection(srcPort, dstPort, f.ServiceIP) {
			srcIP, dstIP = dstIP, srcIP
			srcPort, dstPort = dstPort, srcPort
			txB, rxB = rxB, txB
			txP, rxP = rxP, txP
		}

		s.entries = append(s.entries, rawBucketEntry{
			timestamp:   ts,
			nodeName:    batch.NodeName,
			engine:      f.Engine,
			srcIP:       srcIP,
			dstIP:       dstIP,
			srcPort:     srcPort,
			dstPort:     dstPort,
			protocol:    f.Protocol,
			serviceIP:   f.ServiceIP,
			servicePort: f.ServicePort,
			txBytes:     txB,
			rxBytes:     rxB,
			txPackets:   txP,
			rxPackets:   rxP,
			rttAvgUs:    f.RTTAvgUs,
			rttMaxUs:    f.RTTMaxUs,
			retransmits: f.Retransmits,
			resets:      f.Resets,
			state:       f.State,
		})
	}

	// Prune expired or oversized buffer
	s.pruneLocked(time.Now().Add(-s.maxAge))
}

// BGPEvents returns recent BGP peer state transition events (newest first).
func (s *Store) BGPEvents() []model.BGPEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.BGPEvent, len(s.bgpEvents))
	copy(out, s.bgpEvents)
	return out
}

func (s *Store) recordBGPTransitionsLocked(nodeName string, prevList, currList []model.BIRDProtocol, now time.Time) {
	prevMap := make(map[string]model.BIRDProtocol)
	for _, p := range prevList {
		if strings.EqualFold(p.Proto, "BGP") {
			prevMap[p.Name] = p
		}
	}
	currMap := make(map[string]model.BIRDProtocol)
	for _, c := range currList {
		if strings.EqualFold(c.Proto, "BGP") {
			currMap[c.Name] = c
		}
	}

	for name, curr := range currMap {
		currSt := bgpStateLabel(curr)
		if prev, existed := prevMap[name]; existed {
			prevSt := bgpStateLabel(prev)
			if prevSt != currSt {
				sev := "warning"
				if strings.Contains(strings.ToLower(currSt), "established") {
					sev = "info"
				} else if strings.Contains(strings.ToLower(prevSt), "established") {
					sev = "critical"
				}
				s.appendBGPEventLocked(model.BGPEvent{
					Timestamp: now,
					NodeName:  nodeName,
					PeerName:  name,
					PeerIP:    curr.PeerIP,
					PrevState: prevSt,
					NewState:  currSt,
					Severity:  sev,
					Message:   fmt.Sprintf("节点 %s BGP 邻居 %s (%s) 状态变更: %s → %s", nodeName, name, curr.PeerIP, prevSt, currSt),
				})
			}
		} else {
			s.appendBGPEventLocked(model.BGPEvent{
				Timestamp: now,
				NodeName:  nodeName,
				PeerName:  name,
				PeerIP:    curr.PeerIP,
				PrevState: "None",
				NewState:  currSt,
				Severity:  "info",
				Message:   fmt.Sprintf("节点 %s 新增 BGP 邻居 %s (%s)，当前状态 %s", nodeName, name, curr.PeerIP, currSt),
			})
		}
	}

	for name, prev := range prevMap {
		if _, exists := currMap[name]; !exists {
			s.appendBGPEventLocked(model.BGPEvent{
				Timestamp: now,
				NodeName:  nodeName,
				PeerName:  name,
				PeerIP:    prev.PeerIP,
				PrevState: bgpStateLabel(prev),
				NewState:  "Removed",
				Severity:  "warning",
				Message:   fmt.Sprintf("节点 %s BGP 邻居 %s (%s) 已从 BIRD 协议列表消失", nodeName, name, prev.PeerIP),
			})
		}
	}
}

func (s *Store) appendBGPEventLocked(ev model.BGPEvent) {
	s.bgpEvents = append([]model.BGPEvent{ev}, s.bgpEvents...)
	if len(s.bgpEvents) > 200 {
		s.bgpEvents = s.bgpEvents[:200]
	}
}

func bgpStateLabel(bp model.BIRDProtocol) string {
	if strings.TrimSpace(bp.Info) != "" {
		return strings.TrimSpace(bp.Info)
	}
	if strings.TrimSpace(bp.State) != "" {
		return strings.TrimSpace(bp.State)
	}
	return "Unknown"
}

func (s *Store) pruneLocked(cutoff time.Time) {
	const maxEntries = 200000
	idx := 0
	for idx < len(s.entries) && s.entries[idx].timestamp.Before(cutoff) {
		idx++
	}
	if idx > 0 {
		s.entries = append(s.entries[:0], s.entries[idx:]...)
	}
	if len(s.entries) > maxEntries {
		drop := len(s.entries) - maxEntries
		s.entries = append(s.entries[:0], s.entries[drop:]...)
	}
}

// GetNodeSnapshot returns the latest Agent snapshot for a node.
func (s *Store) GetNodeSnapshot(nodeName string) (*NodeAgentSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.nodes[nodeName]
	if !ok {
		return nil, false
	}
	cp := *snap
	return &cp, true
}

// ListNodeSnapshots returns all known node agent snapshots.
func (s *Store) ListNodeSnapshots() map[string]*NodeAgentSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*NodeAgentSnapshot, len(s.nodes))
	for k, v := range s.nodes {
		cp := *v
		out[k] = &cp
	}
	return out
}

// GlobalTrafficSummary returns high-level real-time traffic metrics over the last 60s.
func (s *Store) GlobalTrafficSummary() model.TrafficSummaryStats {
	flows := s.QueryFlows(60, "", "", "", 2000)
	var stats model.TrafficSummaryStats
	stats.ActiveFlows = len(flows)

	var latSum float64
	var latCnt int
	for _, f := range flows {
		stats.TotalBytesPerSec += f.BytesPerSec
		stats.TotalPktsPerSec += f.PacketsPerSec
		stats.RetransmitsPerMin += f.Retransmits
		if f.LatencyAvgMs > 0 {
			latSum += f.LatencyAvgMs
			latCnt++
		}
	}
	stats.TotalBytesPerSec = round2(stats.TotalBytesPerSec)
	stats.TotalPktsPerSec = round2(stats.TotalPktsPerSec)
	if latCnt > 0 {
		stats.AvgLatencyMs = round2(latSum / float64(latCnt))
	}

	snaps := s.ListNodeSnapshots()
	for _, sn := range snaps {
		if time.Since(sn.LastHeartbeat) > 30*time.Second {
			continue
		}
		if sn.Engine == model.TelemetryEBPF {
			stats.EBPFNodes++
		} else {
			stats.ConntrackNodes++
		}
	}
	return stats
}

type aggFlow struct {
	srcIP       string
	dstIP       string
	srcPort     uint16
	dstPort     uint16
	protocol    string
	serviceIP   string
	servicePort uint16
	txBytes     uint64
	rxBytes     uint64
	txPkts      uint64
	rxPkts      uint64
	rttsMs      []float64
	maxRttMs    float64
	retransmits uint64
	resets      uint64
	state       string
	engine      model.TelemetryEngineMode
	lastSeen    time.Time
	nodesSeen   map[string]bool
	firstNode   string
}

// QueryFlows aggregates and enriches 5-tuple flows within the specified lookback window.
func (s *Store) QueryFlows(windowSec int, namespaceFilter, search, protocolFilter string, limit int) []model.EnrichedFlow {
	if windowSec <= 0 {
		windowSec = 60
	}
	if limit <= 0 || limit > 2000 {
		limit = 300
	}
	cutoff := time.Now().Add(-time.Duration(windowSec) * time.Second)
	prevStart := cutoff.Add(-time.Duration(windowSec) * time.Second)

	s.mu.RLock()
	// Snapshot relevant entries for current window [cutoff, now] and previous equal window [prevStart, cutoff)
	relevant := make([]rawBucketEntry, 0, 1024)
	prevRelevant := make([]rawBucketEntry, 0, 512)
	hasOlderBaseline := len(s.entries) > 0 && s.entries[0].timestamp.Before(cutoff.Add(-15*time.Second))
	for i := len(s.entries) - 1; i >= 0; i-- {
		ts := s.entries[i].timestamp
		if ts.Before(prevStart) {
			break
		}
		if !ts.Before(cutoff) {
			relevant = append(relevant, s.entries[i])
		} else {
			prevRelevant = append(prevRelevant, s.entries[i])
		}
	}
	s.mu.RUnlock()

	// Aggregate previous window retransmits by 4-tuple (proto|srcIP|dstIP|dstPort) so ephemeral srcPort changes still compare cleanly
	prevFlowRetrans := make(map[string]uint64)
	prevFlowSeen := make(map[string]string) // key -> firstNode for cross-node dedup
	for _, e := range prevRelevant {
		if protocolFilter != "" && !strings.EqualFold(e.protocol, protocolFilter) {
			continue
		}
		pk := fmt.Sprintf("%s|%s|%s:%d", e.protocol, e.srcIP, e.dstIP, e.dstPort)
		if fn, ok := prevFlowSeen[pk]; !ok {
			prevFlowSeen[pk] = e.nodeName
			prevFlowRetrans[pk] += uint64(e.retransmits)
		} else if fn == e.nodeName {
			prevFlowRetrans[pk] += uint64(e.retransmits)
		}
	}

	// Aggregate by 5-tuple (with deduplication across src & dst nodes: pick one node's byte counters if both report the exact same 5-tuple)
	grouped := make(map[string]*aggFlow)
	for _, e := range relevant {
		if protocolFilter != "" && !strings.EqualFold(e.protocol, protocolFilter) {
			continue
		}
		key := fmt.Sprintf("%s|%s:%d|%s:%d", e.protocol, e.srcIP, e.srcPort, e.dstIP, e.dstPort)
		af, ok := grouped[key]
		if !ok {
			af = &aggFlow{
				srcIP:       e.srcIP,
				dstIP:       e.dstIP,
				srcPort:     e.srcPort,
				dstPort:     e.dstPort,
				protocol:    e.protocol,
				serviceIP:   e.serviceIP,
				servicePort: e.servicePort,
				engine:      e.engine,
				state:       e.state,
				lastSeen:    e.timestamp,
				nodesSeen:   map[string]bool{e.nodeName: true},
				firstNode:   e.nodeName,
			}
			grouped[key] = af
		}
		if e.serviceIP != "" && af.serviceIP == "" {
			af.serviceIP = e.serviceIP
			af.servicePort = e.servicePort
		}
		if e.timestamp.After(af.lastSeen) {
			af.lastSeen = e.timestamp
			if e.state != "" {
				af.state = e.state
			}
		}
		if e.engine == model.TelemetryEBPF {
			af.engine = model.TelemetryEBPF
		}
		// Deduplicate cross-node double-counting: accumulate bytes/pkts from the primary reporting node of this 5-tuple
		af.nodesSeen[e.nodeName] = true
		if e.nodeName == af.firstNode {
			af.txBytes += e.txBytes
			af.rxBytes += e.rxBytes
			af.txPkts += e.txPackets
			af.rxPkts += e.rxPackets
			af.retransmits += uint64(e.retransmits)
			af.resets += uint64(e.resets)
		}
		if e.rttAvgUs > 0 {
			ms := float64(e.rttAvgUs) / 1000.0
			af.rttsMs = append(af.rttsMs, ms)
			if maxMs := float64(e.rttMaxUs) / 1000.0; maxMs > af.maxRttMs {
				af.maxRttMs = maxMs
			} else if ms > af.maxRttMs {
				af.maxRttMs = ms
			}
		}
	}

	q := strings.ToLower(strings.TrimSpace(search))
	nsFilter := strings.TrimSpace(namespaceFilter)
	out := make([]model.EnrichedFlow, 0, len(grouped))

	for key, af := range grouped {
		srcID := s.k8sStore.ResolveIP(af.srcIP, af.srcPort)
		dstID := s.k8sStore.ResolveIP(af.dstIP, af.dstPort)
		var viaSvc *model.EndpointIdentity
		if af.serviceIP != "" {
			svcID := s.k8sStore.ResolveIP(af.serviceIP, af.servicePort)
			viaSvc = &svcID
		}

		if nsFilter != "" && nsFilter != "all" {
			matchesNS := srcID.Namespace == nsFilter || dstID.Namespace == nsFilter || (viaSvc != nil && viaSvc.Namespace == nsFilter)
			if !matchesNS {
				continue
			}
		}
		if q != "" {
			haystack := strings.ToLower(strings.Join([]string{
				srcID.DisplayName, srcID.IP, srcID.WorkloadName, srcID.NodeName,
				dstID.DisplayName, dstID.IP, dstID.WorkloadName, dstID.NodeName,
				af.protocol, fmt.Sprintf("%d", af.dstPort),
			}, " "))
			if viaSvc != nil {
				haystack += " " + strings.ToLower(viaSvc.DisplayName+" "+viaSvc.IP)
			}
			if !strings.Contains(haystack, q) {
				continue
			}
		}

		avgMs, p95Ms := calcLatencyStats(af.rttsMs)
		maxMs := round2(af.maxRttMs)
		if maxMs < avgMs {
			maxMs = avgMs
		}

		totalBytes := af.txBytes + af.rxBytes
		totalPkts := af.txPkts + af.rxPkts
		bps := round2(float64(totalBytes) / float64(windowSec))
		pps := round2(float64(totalPkts) / float64(windowSec))

		crossNode := srcID.NodeName != "" && dstID.NodeName != "" && srcID.NodeName != dstID.NodeName
		health := classifyFlowHealth(avgMs, af.retransmits, af.resets)
		pk := fmt.Sprintf("%s|%s|%s:%d", af.protocol, af.srcIP, af.dstIP, af.dstPort)
		prevRet := prevFlowRetrans[pk]
		retPerMB, estRate, rateNote, deltaPct, anomScore, anomReasons := computeAnomalyMetrics(
			af.retransmits, af.resets, af.txBytes, af.rxBytes, af.txPkts, af.rxPkts, avgMs, af.engine, windowSec, prevRet, hasOlderBaseline,
		)

		out = append(out, model.EnrichedFlow{
			ID:                   key,
			Src:                  srcID,
			Dst:                  dstID,
			ViaService:           viaSvc,
			Protocol:             af.protocol,
			SrcPort:              af.srcPort,
			DstPort:              af.dstPort,
			BytesTotal:           totalBytes,
			TxBytes:              af.txBytes,
			RxBytes:              af.rxBytes,
			PacketsTotal:         totalPkts,
			BytesPerSec:          bps,
			PacketsPerSec:        pps,
			LatencyAvgMs:         avgMs,
			LatencyP95Ms:         p95Ms,
			LatencyMaxMs:         maxMs,
			Retransmits:          af.retransmits,
			Resets:               af.resets,
			Connections:          1,
			State:                af.state,
			CrossNode:            crossNode,
			Engine:               af.engine,
			Health:               health,
			LastSeen:             af.lastSeen,
			WindowSec:            windowSec,
			PrevRetransmits:      prevRet,
			RetransmitDeltaPct:   deltaPct,
			BaselineAvailable:    hasOlderBaseline,
			RetransmitPerMB:      retPerMB,
			EstRetransmitRatePct: estRate,
			RateNote:             rateNote,
			AnomalyScore:         anomScore,
			AnomalyReasons:       anomReasons,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].BytesTotal != out[j].BytesTotal {
			return out[i].BytesTotal > out[j].BytesTotal
		}
		return out[i].LastSeen.After(out[j].LastSeen)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// BuildTopology constructs a multi-granularity traffic graph from live 5-tuples.
func (s *Store) BuildTopology(granularity string, windowSec int, namespaces []string, search, protocol string) model.TopologyGraph {
	if granularity == "" {
		granularity = "workload"
	}
	if windowSec <= 0 {
		windowSec = 300
	}

	// Query flows (if single namespace selected, filter directly; if multiple, filter below)
	nsSingle := ""
	nsSet := make(map[string]bool)
	for _, n := range namespaces {
		n = strings.TrimSpace(n)
		if n != "" && n != "all" {
			nsSet[n] = true
		}
	}
	if len(nsSet) == 1 {
		for k := range nsSet {
			nsSingle = k
		}
	}
	flows := s.QueryFlows(windowSec, nsSingle, search, protocol, 1500)

	nodeMap := make(map[string]*model.TopologyNode)
	nodePorts := make(map[string]map[string]bool)
	nodePods := make(map[string]map[string]bool)
	nodeLatSum := make(map[string]float64)
	nodeLatCnt := make(map[string]int)

	type edgeAcc struct {
		edge          model.TopologyEdge
		protoSet      map[string]bool
		portSet       map[uint16]bool
		rtts          []float64
		txBytes       uint64
		rxBytes       uint64
		txPkts        uint64
		rxPkts        uint64
		prevRetrans   uint64
		baselineAvail bool
		engine        model.TelemetryEngineMode
	}
	edgeMap := make(map[string]*edgeAcc)

	ensureNode := func(ep model.EndpointIdentity) string {
		id, label, kind, ns, nodeName, ip := topoIdentity(ep, granularity)
		if _, ok := nodeMap[id]; !ok {
			nodeMap[id] = &model.TopologyNode{
				ID:        id,
				Label:     label,
				Kind:      kind,
				Namespace: ns,
				NodeName:  nodeName,
				IP:        ip,
				Health:    "healthy",
			}
			nodePorts[id] = make(map[string]bool)
			nodePods[id] = make(map[string]bool)
		}
		if ep.PodName != "" {
			nodePods[id][ep.Namespace+"/"+ep.PodName] = true
		}
		return id
	}

	var totalBps, totalLatSum float64
	var totalLatCnt, matchedFlowCnt int

	for _, f := range flows {
		if len(nsSet) > 1 {
			if !nsSet[f.Src.Namespace] && !nsSet[f.Dst.Namespace] && (f.ViaService == nil || !nsSet[f.ViaService.Namespace]) {
				continue
			}
		}
		srcNodeID := ensureNode(f.Src)
		dstNodeID := ensureNode(f.Dst)
		if srcNodeID == dstNodeID {
			continue
		}
		matchedFlowCnt++

		// Record exposed destination port on destination node card
		portTag := fmt.Sprintf("%s:%d", f.Protocol, f.DstPort)
		nodePorts[dstNodeID][portTag] = true

		nodeMap[srcNodeID].OutboundBps += f.BytesPerSec
		nodeMap[dstNodeID].InboundBps += f.BytesPerSec
		nodeMap[dstNodeID].Retransmits += f.Retransmits
		if f.LatencyAvgMs > 0 {
			nodeLatSum[dstNodeID] += f.LatencyAvgMs
			nodeLatCnt[dstNodeID]++
			totalLatSum += f.LatencyAvgMs
			totalLatCnt++
		}
		totalBps += f.BytesPerSec

		edgeKey := srcNodeID + "->" + dstNodeID
		ea, ok := edgeMap[edgeKey]
		if !ok {
			ea = &edgeAcc{
				edge: model.TopologyEdge{
					ID:     edgeKey,
					Source: srcNodeID,
					Target: dstNodeID,
					Health: "healthy",
				},
				protoSet: make(map[string]bool),
				portSet:  make(map[uint16]bool),
			}
			edgeMap[edgeKey] = ea
		}
		ea.protoSet[f.Protocol] = true
		ea.portSet[f.DstPort] = true
		ea.edge.BytesTotal += f.BytesTotal
		ea.edge.BytesPerSec += f.BytesPerSec
		ea.edge.PacketsPerSec += f.PacketsPerSec
		ea.edge.Retransmits += f.Retransmits
		ea.edge.Resets += f.Resets
		ea.edge.FlowCount++
		ea.txBytes += f.TxBytes
		ea.rxBytes += f.RxBytes
		ea.txPkts += f.PacketsTotal
		ea.prevRetrans += f.PrevRetransmits
		if f.BaselineAvailable {
			ea.baselineAvail = true
		}
		if f.Engine != "" {
			ea.engine = f.Engine
		}
		if f.CrossNode {
			ea.edge.CrossNode = true
		}
		if f.LatencyAvgMs > 0 {
			ea.rtts = append(ea.rtts, f.LatencyAvgMs)
			if f.LatencyMaxMs > ea.edge.LatencyMaxMs {
				ea.edge.LatencyMaxMs = f.LatencyMaxMs
			}
		}
		if len(ea.edge.FiveTuples) < 50 {
			ea.edge.FiveTuples = append(ea.edge.FiveTuples, f)
		}
	}

	nodesOut := make([]model.TopologyNode, 0, len(nodeMap))
	for id, n := range nodeMap {
		n.InboundBps = round2(n.InboundBps)
		n.OutboundBps = round2(n.OutboundBps)
		n.PodCount = len(nodePods[id])
		if nodeLatCnt[id] > 0 {
			n.AvgLatencyMs = round2(nodeLatSum[id] / float64(nodeLatCnt[id]))
		}
		for pt := range nodePorts[id] {
			n.Ports = append(n.Ports, pt)
		}
		sort.Strings(n.Ports)
		if len(n.Ports) > 6 {
			n.Ports = n.Ports[:6]
		}
		n.Health = classifyFlowHealth(n.AvgLatencyMs, n.Retransmits, 0)
		nodesOut = append(nodesOut, *n)
	}
	sort.Slice(nodesOut, func(i, j int) bool {
		bi := nodesOut[i].InboundBps + nodesOut[i].OutboundBps
		bj := nodesOut[j].InboundBps + nodesOut[j].OutboundBps
		if bi != bj {
			return bi > bj
		}
		return nodesOut[i].ID < nodesOut[j].ID
	})

	// Cap overly dense topology graphs to top 80 most active nodes for crisp UI rendering
	allowedNodes := make(map[string]bool, len(nodesOut))
	if len(nodesOut) > 80 {
		nodesOut = nodesOut[:80]
	}
	for _, n := range nodesOut {
		allowedNodes[n.ID] = true
	}

	edgesOut := make([]model.TopologyEdge, 0, len(edgeMap))
	warnEdges, critEdges := 0, 0
	for _, ea := range edgeMap {
		if !allowedNodes[ea.edge.Source] || !allowedNodes[ea.edge.Target] {
			continue
		}
		for p := range ea.protoSet {
			ea.edge.Protocols = append(ea.edge.Protocols, p)
		}
		sort.Strings(ea.edge.Protocols)
		for pt := range ea.portSet {
			ea.edge.DstPorts = append(ea.edge.DstPorts, pt)
		}
		sort.Slice(ea.edge.DstPorts, func(i, j int) bool { return ea.edge.DstPorts[i] < ea.edge.DstPorts[j] })

		ea.edge.BytesPerSec = round2(ea.edge.BytesPerSec)
		ea.edge.PacketsPerSec = round2(ea.edge.PacketsPerSec)
		ea.edge.LatencyAvgMs, ea.edge.LatencyP95Ms = calcLatencyStats(ea.rtts)
		ea.edge.LatencyMaxMs = round2(ea.edge.LatencyMaxMs)
		ea.edge.Health = classifyFlowHealth(ea.edge.LatencyAvgMs, ea.edge.Retransmits, ea.edge.Resets)
		ea.edge.WindowSec = windowSec
		ea.edge.PrevRetransmits = ea.prevRetrans
		ea.edge.BaselineAvailable = ea.baselineAvail
		ea.edge.RetransmitPerMB, ea.edge.EstRetransmitRatePct, ea.edge.RateNote, ea.edge.RetransmitDeltaPct, ea.edge.AnomalyScore, ea.edge.AnomalyReasons = computeAnomalyMetrics(
			ea.edge.Retransmits, ea.edge.Resets, ea.txBytes, ea.rxBytes, ea.txPkts, ea.rxPkts, ea.edge.LatencyAvgMs, ea.engine, windowSec, ea.prevRetrans, ea.baselineAvail,
		)
		if ea.edge.Health == "warning" {
			warnEdges++
		} else if ea.edge.Health == "critical" {
			critEdges++
		}

		// Format primary edge label e.g. "TCP:8080" or "TCP:80,+2"
		primaryProto := "TCP"
		if len(ea.edge.Protocols) > 0 {
			primaryProto = ea.edge.Protocols[0]
		}
		if len(ea.edge.DstPorts) == 1 {
			ea.edge.PrimaryLabel = fmt.Sprintf("%s:%d", primaryProto, ea.edge.DstPorts[0])
		} else if len(ea.edge.DstPorts) > 1 {
			ea.edge.PrimaryLabel = fmt.Sprintf("%s:%d +%d", primaryProto, ea.edge.DstPorts[0], len(ea.edge.DstPorts)-1)
		} else {
			ea.edge.PrimaryLabel = primaryProto
		}

		edgesOut = append(edgesOut, ea.edge)
	}
	sort.Slice(edgesOut, func(i, j int) bool { return edgesOut[i].BytesTotal > edgesOut[j].BytesTotal })

	avgLat := 0.0
	if totalLatCnt > 0 {
		avgLat = round2(totalLatSum / float64(totalLatCnt))
	}

	return model.TopologyGraph{
		GeneratedAt: time.Now(),
		Granularity: granularity,
		WindowSec:   windowSec,
		Namespaces:  s.k8sStore.GetNamespaces(),
		Nodes:       nodesOut,
		Edges:       edgesOut,
		Summary: model.TopologySummary{
			NodeCount:        len(nodesOut),
			EdgeCount:        len(edgesOut),
			TotalFlows:       matchedFlowCnt,
			TotalBytesPerSec: round2(totalBps),
			AvgLatencyMs:     avgLat,
			WarningEdges:     warnEdges,
			CriticalEdges:    critEdges,
		},
	}
}

func topoIdentity(ep model.EndpointIdentity, granularity string) (id, label, kind, ns, nodeName, ip string) {
	switch granularity {
	case "namespace":
		if ep.Namespace != "" {
			return "ns:" + ep.Namespace, ep.Namespace, "Namespace", ep.Namespace, "", ""
		}
		if ep.Kind == "Node" {
			return "node:" + ep.NodeName, ep.NodeName, "Node", "", ep.NodeName, ep.IP
		}
		return "ext:" + ep.IP, ep.IP, "External", "", "", ep.IP

	case "node":
		if ep.NodeName != "" {
			return "node:" + ep.NodeName, ep.NodeName, "Node", "", ep.NodeName, ep.IP
		}
		return "ext:" + ep.IP, ep.IP, "External", "", "", ep.IP

	case "pod":
		if ep.Kind == "Pod" {
			return "pod:" + ep.Namespace + "/" + ep.PodName, ep.PodName, "Pod", ep.Namespace, ep.NodeName, ep.IP
		}
		if ep.Kind == "Service" {
			return "svc:" + ep.Namespace + "/" + ep.ServiceName, ep.ServiceName, "Service", ep.Namespace, "", ep.IP
		}
		if ep.Kind == "Node" {
			return "node:" + ep.NodeName, ep.NodeName, "Node", "", ep.NodeName, ep.IP
		}
		return "ext:" + ep.IP, ep.IP, "External", "", "", ep.IP

	default: // workload
		if ep.Kind == "Pod" {
			wkName := ep.WorkloadName
			wkKind := ep.WorkloadKind
			if wkName == "" {
				wkName = ep.PodName
				wkKind = "Pod"
			}
			return fmt.Sprintf("wk:%s/%s/%s", ep.Namespace, wkKind, wkName), wkName, wkKind, ep.Namespace, ep.NodeName, ep.IP
		}
		if ep.Kind == "Service" {
			return "svc:" + ep.Namespace + "/" + ep.ServiceName, ep.ServiceName, "Service", ep.Namespace, "", ep.IP
		}
		if ep.Kind == "Node" {
			return "node:" + ep.NodeName, ep.NodeName, "Node", "", ep.NodeName, ep.IP
		}
		return "ext:" + ep.IP, ep.IP, "External", "", "", ep.IP
	}
}

func shouldFlipDirection(srcPort, dstPort uint16, serviceIP string) bool {
	if serviceIP != "" {
		// Service calls already point toward destination
		return false
	}
	// If srcPort is a well-known / service port (<32768) and dstPort is ephemeral (>=32768),
	// the sample was observed from the server socket side; flip so flow points Client -> Server.
	if srcPort < 32768 && dstPort >= 32768 {
		return true
	}
	return false
}

func calcLatencyStats(samples []float64) (avgMs, p95Ms float64) {
	if len(samples) == 0 {
		return 0, 0
	}
	cp := make([]float64, len(samples))
	copy(cp, samples)
	sort.Float64s(cp)
	sum := 0.0
	for _, v := range cp {
		sum += v
	}
	avgMs = round2(sum / float64(len(cp)))
	idx := int(math.Ceil(0.95*float64(len(cp)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	p95Ms = round2(cp[idx])
	return avgMs, p95Ms
}

func classifyFlowHealth(avgLatMs float64, retrans, resets uint64) string {
	if resets > 3 || retrans > 10 || avgLatMs > 100 {
		return "critical"
	}
	if resets > 0 || retrans > 0 || avgLatMs > 25 {
		return "warning"
	}
	return "healthy"
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// computeAnomalyMetrics computes honest retransmit density, optional estimated ratio, previous-window delta,
// and an anomaly ranking score so the UI can distinguish "high-volume baseline traffic" from "newly degraded links".
func computeAnomalyMetrics(
	retransmits, resets, txBytes, rxBytes, txPkts, rxPkts uint64,
	avgLatMs float64,
	engine model.TelemetryEngineMode,
	windowSec int,
	prevRetrans uint64,
	baselineAvail bool,
) (retPerMB float64, estRatePct *float64, rateNote string, deltaPct *float64, anomalyScore float64, reasons []string) {
	totalBytes := txBytes + rxBytes
	if totalBytes >= 1024 && retransmits > 0 {
		mb := float64(totalBytes) / (1024.0 * 1024.0)
		if mb > 0 {
			retPerMB = round2(float64(retransmits) / mb)
		}
	}

	// Only compute a retransmit ratio when the denominator is meaningful, and clearly label its basis
	if retransmits > 0 {
		if engine != model.TelemetryEBPF && txPkts >= 20 {
			pct := round2(float64(retransmits) * 100.0 / float64(txPkts+retransmits))
			estRatePct = &pct
			rateNote = "基于 tcp_info 发送段统计"
		} else {
			estSegs := txPkts
			if bySegs := (txBytes + 1459) / 1460; bySegs > estSegs {
				estSegs = bySegs
			}
			if totalSegs := (totalBytes + 1459) / 1460; estSegs < 20 && totalSegs >= 20 {
				estSegs = totalSegs
			}
			if estSegs >= 20 {
				pct := round2(float64(retransmits) * 100.0 / float64(estSegs+retransmits))
				estRatePct = &pct
				rateNote = "按发送字节/1460B 估算"
			} else {
				rateNote = "样本包量较小，仅参考次数与基线"
			}
		}
	}

	winLabel := fmt.Sprintf("%dm", windowSec/60)
	if windowSec < 60 {
		winLabel = fmt.Sprintf("%ds", windowSec)
	}

	if baselineAvail && retransmits > 0 {
		if prevRetrans > 0 {
			d := round2((float64(retransmits) - float64(prevRetrans)) * 100.0 / float64(prevRetrans))
			deltaPct = &d
			if d >= 50 {
				reasons = append(reasons, fmt.Sprintf("近 %s 重传 %d 次（较前 %s 恶化 +%.0f%%）", winLabel, retransmits, winLabel, d))
				anomalyScore += 40 + math.Min(d/5.0, 40)
			} else if d <= -30 {
				reasons = append(reasons, fmt.Sprintf("近 %s 重传 %d 次（较前 %s 回落 %.0f%%）", winLabel, retransmits, winLabel, d))
				anomalyScore += 10
			} else {
				reasons = append(reasons, fmt.Sprintf("近 %s 重传 %d 次（与前 %s 持平，前窗 %d 次）", winLabel, retransmits, winLabel, prevRetrans))
				anomalyScore += 18
			}
		} else {
			reasons = append(reasons, fmt.Sprintf("近 %s 新增重传 %d 次（前 %s 为 0）", winLabel, retransmits, winLabel))
			anomalyScore += 45 + math.Min(float64(retransmits)*1.5, 35)
		}
	} else if retransmits > 0 {
		reasons = append(reasons, fmt.Sprintf("近 %s 重传 %d 次（首窗无前段基线）", winLabel, retransmits))
		anomalyScore += 20 + math.Min(math.Log2(float64(retransmits)+1)*8, 35)
	}

	if resets > 0 {
		reasons = append(reasons, fmt.Sprintf("出现 %d 次 TCP RST 重置", resets))
		anomalyScore += 30 + math.Min(float64(resets)*5, 30)
	}
	if avgLatMs > 100 {
		reasons = append(reasons, fmt.Sprintf("平均 RTT 显著偏高 (%.1f ms)", avgLatMs))
		anomalyScore += 30
	} else if avgLatMs > 25 {
		reasons = append(reasons, fmt.Sprintf("平均 RTT 偏高 (%.1f ms)", avgLatMs))
		anomalyScore += 14
	}
	if estRatePct != nil && *estRatePct >= 2.0 {
		anomalyScore += math.Min(*estRatePct*4, 25)
	}

	anomalyScore = round2(anomalyScore)
	return
}
