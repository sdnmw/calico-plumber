package traceflow

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"calico-netdiag/pkg/k8s"
	"calico-netdiag/pkg/model"
	"calico-netdiag/pkg/server/agentclient"
)

// Orchestrator coordinates multi-node live packet TraceFlows and stores recent history.
type Orchestrator struct {
	k8sStore *k8s.Store
	agents   *agentclient.Client

	mu      sync.RWMutex
	history []model.TraceFlowResult
}

// NewOrchestrator constructs a TraceFlow orchestrator.
func NewOrchestrator(k8sStore *k8s.Store, agents *agentclient.Client) *Orchestrator {
	return &Orchestrator{
		k8sStore: k8sStore,
		agents:   agents,
		history:  make([]model.TraceFlowResult, 0, 32),
	}
}

// History returns the most recent TraceFlow runs (newest first).
func (o *Orchestrator) History() []model.TraceFlowResult {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]model.TraceFlowResult, len(o.history))
	copy(out, o.history)
	return out
}

// Run executes an end-to-end live TraceFlow from a source Pod to a target Pod, Service, Node, or External IP.
func (o *Orchestrator) Run(ctx context.Context, req model.TraceFlowRequest) (*model.TraceFlowResult, error) {
	started := time.Now()
	if req.Protocol == "" {
		req.Protocol = "TCP"
	}
	req.Protocol = strings.ToUpper(req.Protocol)
	if req.TimeoutMs <= 0 || req.TimeoutMs > 8000 {
		req.TimeoutMs = 2500
	}

	// 1. Resolve Source Pod
	srcPodObj, srcBrief, ok := o.k8sStore.GetPod(req.SrcNamespace, req.SrcPod)
	if !ok {
		return nil, fmt.Errorf("source pod %s/%s not found", req.SrcNamespace, req.SrcPod)
	}
	if srcBrief.IP == "" || srcBrief.NodeName == "" {
		return nil, fmt.Errorf("source pod %s/%s has no IP or scheduled node", req.SrcNamespace, req.SrcPod)
	}
	srcIdentity := o.k8sStore.ResolveIP(srcBrief.IP, 0)
	srcIdentity.Namespace = srcBrief.Namespace
	srcIdentity.PodName = srcBrief.Name
	srcIdentity.NodeName = srcBrief.NodeName
	srcIdentity.DisplayName = srcBrief.Namespace + "/" + srcBrief.Name

	// 2. Resolve Destination Target
	var (
		dstIP        string
		dstNode      string
		dstPodUID    string
		dstIdentity  model.EndpointIdentity
		backendIPs   []string
		backendPort  int
		backendNodes = make(map[string]string) // backendIP -> nodeName
		backendUIDs  = make(map[string]string) // backendIP -> podUID
		isService    bool
		isExternal   bool
	)

	switch strings.ToLower(req.TargetType) {
	case "pod":
		dp, db, found := o.k8sStore.GetPod(req.DstNamespace, req.DstName)
		if !found {
			return nil, fmt.Errorf("destination pod %s/%s not found", req.DstNamespace, req.DstName)
		}
		dstIP = db.IP
		dstNode = db.NodeName
		dstPodUID = string(dp.UID)
		dstIdentity = o.k8sStore.ResolveIP(dstIP, uint16(req.DstPort))
		dstIdentity.Namespace = db.Namespace
		dstIdentity.PodName = db.Name
		dstIdentity.NodeName = db.NodeName
		dstIdentity.DisplayName = db.Namespace + "/" + db.Name

	case "service", "svc":
		isService = true
		svc, slices, found := o.k8sStore.GetService(req.DstNamespace, req.DstName)
		if !found {
			return nil, fmt.Errorf("destination service %s/%s not found", req.DstNamespace, req.DstName)
		}
		dstIP = svc.Spec.ClusterIP
		if req.DstPort == 0 && len(svc.Spec.Ports) > 0 {
			req.DstPort = int(svc.Spec.Ports[0].Port)
		}
		// Find targetPort for matched service port
		for _, sp := range svc.Spec.Ports {
			if int(sp.Port) == req.DstPort {
				if sp.TargetPort.IntVal > 0 {
					backendPort = int(sp.TargetPort.IntVal)
				}
				break
			}
		}
		// Collect ready backend endpoint Pods
		for _, sl := range slices {
			if backendPort == 0 && len(sl.Ports) > 0 && sl.Ports[0].Port != nil {
				backendPort = int(*sl.Ports[0].Port)
			}
			for _, ep := range sl.Endpoints {
				if ep.Conditions.Ready != nil && !*ep.Conditions.Ready {
					continue
				}
				for _, addr := range ep.Addresses {
					backendIPs = append(backendIPs, addr)
					if bp, bb, ok := o.k8sStore.GetPodByIP(addr); ok {
						backendNodes[addr] = bb.NodeName
						backendUIDs[addr] = string(bp.UID)
					}
				}
			}
		}
		if len(backendIPs) > 0 {
			// Pick representative backend node for cross-node arming if single or non-local
			for _, bip := range backendIPs {
				if bn := backendNodes[bip]; bn != "" && bn != srcBrief.NodeName {
					dstNode = bn
					dstPodUID = backendUIDs[bip]
					break
				}
			}
			if dstNode == "" {
				dstNode = backendNodes[backendIPs[0]]
				dstPodUID = backendUIDs[backendIPs[0]]
			}
		}
		dstIdentity = model.EndpointIdentity{
			IP:          dstIP,
			Port:        uint16(req.DstPort),
			Kind:        "Service",
			Namespace:   svc.Namespace,
			ServiceName: svc.Name,
			DisplayName: fmt.Sprintf("svc/%s/%s", svc.Namespace, svc.Name),
		}

	case "node":
		nodeObj, found := o.k8sStore.GetNode(req.DstName)
		if !found {
			return nil, fmt.Errorf("destination node %s not found", req.DstName)
		}
		dstIP = k8s.NodeInternalIP(nodeObj)
		dstNode = nodeObj.Name
		dstIdentity = model.EndpointIdentity{
			IP:          dstIP,
			Port:        uint16(req.DstPort),
			Kind:        "Node",
			NodeName:    nodeObj.Name,
			DisplayName: "node/" + nodeObj.Name,
		}

	default: // ip
		dstIP = strings.TrimSpace(req.DstIP)
		if net.ParseIP(dstIP) == nil {
			// Try resolving domain name
			if ips, err := net.LookupIP(dstIP); err == nil {
				for _, ip := range ips {
					if v4 := ip.To4(); v4 != nil {
						dstIP = v4.String()
						break
					}
				}
			}
		}
		if net.ParseIP(dstIP) == nil {
			return nil, fmt.Errorf("invalid destination IP/host %q", req.DstIP)
		}
		dstIdentity = o.k8sStore.ResolveIP(dstIP, uint16(req.DstPort))
		if dstIdentity.Kind == "Pod" {
			if dp, db, ok := o.k8sStore.GetPodByIP(dstIP); ok {
				dstNode = db.NodeName
				dstPodUID = string(dp.UID)
			}
		} else if dstIdentity.Kind == "Node" {
			dstNode = dstIdentity.NodeName
		} else if dstIdentity.Kind == "Service" {
			isService = true
			if svc, slices, ok := o.k8sStore.GetServiceByIP(dstIP); ok {
				for _, sl := range slices {
					for _, ep := range sl.Endpoints {
						for _, a := range ep.Addresses {
							backendIPs = append(backendIPs, a)
							if bp, bb, ok := o.k8sStore.GetPodByIP(a); ok {
								backendNodes[a] = bb.NodeName
								backendUIDs[a] = string(bp.UID)
							}
						}
					}
				}
				_ = svc
			}
		} else {
			isExternal = true
		}
	}

	if req.Protocol == "TCP" && req.DstPort == 0 {
		req.DstPort = 80
	}
	if req.Protocol == "UDP" && req.DstPort == 0 {
		req.DstPort = 53
	}

	// Generate unique TraceFlow packet signature
	traceID := genID()
	srcPort := randomEphemeralPort()
	icmpID := randomICMPID()
	traceSig := fmt.Sprintf("proto=%s sport=%d dport=%d", req.Protocol, srcPort, req.DstPort)
	if req.Protocol == "ICMP" {
		traceSig = fmt.Sprintf("proto=ICMP id=%d", icmpID)
	}

	crossNode := dstNode != "" && dstNode != srcBrief.NodeName
	encapMode := o.k8sStore.PrimaryEncapMode()
	if meta, ok := o.k8sStore.GetIPMeta(srcBrief.IP); ok {
		encapMode = meta.EncapMode
	}

	// 3. Coordinate Source & Destination Node Agents concurrently
	var (
		srcResp *model.AgentTraceCaptureResponse
		dstResp *model.AgentTraceCaptureResponse
		srcErr  error
		dstErr  error
		wg      sync.WaitGroup
	)

	if !crossNode && dstNode == srcBrief.NodeName && !isExternal {
		// Same-node trace: single agent handles both source and destination observation!
		peerIP := dstIP
		if isService && len(backendIPs) > 0 {
			peerIP = backendIPs[0]
		}
		srcResp, srcErr = o.agents.RunTraceCapture(ctx, srcBrief.NodeName, model.AgentTraceCaptureRequest{
			TraceID:     traceID,
			Role:        "both",
			PodUID:      string(srcPodObj.UID),
			PodIP:       srcBrief.IP,
			PeerPodUID:  dstPodUID,
			PeerPodIP:   peerIP,
			SrcIP:       srcBrief.IP,
			DstIP:       dstIP,
			BackendIPs:  backendIPs,
			Protocol:    req.Protocol,
			SrcPort:     srcPort,
			DstPort:     req.DstPort,
			BackendPort: backendPort,
			ICMPID:      icmpID,
			TimeoutMs:   req.TimeoutMs,
			Inject:      true,
			EncapMode:   string(encapMode),
		})
	} else {
		// Cross-node or External trace
		if crossNode {
			peerIP := dstIP
			if isService {
				for _, bip := range backendIPs {
					if backendNodes[bip] == dstNode {
						peerIP = bip
						break
					}
				}
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				dstResp, dstErr = o.agents.RunTraceCapture(ctx, dstNode, model.AgentTraceCaptureRequest{
					TraceID:     traceID,
					Role:        "dest",
					PodUID:      dstPodUID,
					PodIP:       peerIP,
					SrcIP:       srcBrief.IP,
					DstIP:       dstIP,
					BackendIPs:  backendIPs,
					Protocol:    req.Protocol,
					SrcPort:     srcPort,
					DstPort:     req.DstPort,
					BackendPort: backendPort,
					ICMPID:      icmpID,
					TimeoutMs:   req.TimeoutMs + 400,
					Inject:      false,
					EncapMode:   string(encapMode),
				})
			}()
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			srcResp, srcErr = o.agents.RunTraceCapture(ctx, srcBrief.NodeName, model.AgentTraceCaptureRequest{
				TraceID:     traceID,
				Role:        "source",
				PodUID:      string(srcPodObj.UID),
				PodIP:       srcBrief.IP,
				SrcIP:       srcBrief.IP,
				DstIP:       dstIP,
				BackendIPs:  backendIPs,
				Protocol:    req.Protocol,
				SrcPort:     srcPort,
				DstPort:     req.DstPort,
				BackendPort: backendPort,
				ICMPID:      icmpID,
				TimeoutMs:   req.TimeoutMs,
				Inject:      true,
				EncapMode:   string(encapMode),
			})
		}()
		wg.Wait()
	}

	if srcErr != nil {
		return nil, fmt.Errorf("source node trace failed (%s): %w", srcBrief.NodeName, srcErr)
	}
	_ = dstErr

	// 4. Stitch Hops, Observations, Verdict & Root Cause
	_, srcPolicies := o.k8sStore.GetPodServicesAndPolicies(srcBrief.Namespace, srcBrief.Name)
	var dstPolicies []model.MatchedPolicySummary
	if dstIdentity.Kind == "Pod" {
		_, dstPolicies = o.k8sStore.GetPodServicesAndPolicies(dstIdentity.Namespace, dstIdentity.PodName)
	}

	res := StitchTraceResult(StitchInput{
		ID:           traceID,
		StartedAt:    started,
		CompletedAt:  time.Now(),
		Protocol:     req.Protocol,
		SrcPort:      srcPort,
		DstPort:      req.DstPort,
		TraceSig:     traceSig,
		EncapMode:    encapMode,
		CrossNode:    crossNode,
		IsService:    isService,
		IsExternal:   isExternal,
		Source:       srcIdentity,
		Destination:  dstIdentity,
		SrcNode:      srcBrief.NodeName,
		DstNode:      dstNode,
		SrcResp:      srcResp,
		DstResp:      dstResp,
		SrcPolicies:  srcPolicies,
		DstPolicies:  dstPolicies,
		ResolveIP:    o.k8sStore.ResolveIP,
	})

	// Save to history (keep last 30)
	o.mu.Lock()
	o.history = append([]model.TraceFlowResult{*res}, o.history...)
	if len(o.history) > 30 {
		o.history = o.history[:30]
	}
	o.mu.Unlock()

	return res, nil
}

// StitchInput encapsulates all observed agent data to build the TraceFlow pipeline.
// Exported so unit tests can thoroughly verify BGP, IPIP, VXLAN, DNAT, and DROP localization!
type StitchInput struct {
	ID          string
	StartedAt   time.Time
	CompletedAt time.Time
	Protocol    string
	SrcPort     int
	DstPort     int
	TraceSig    string
	EncapMode   model.CalicoEncapMode
	CrossNode   bool
	IsService   bool
	IsExternal  bool
	Source      model.EndpointIdentity
	Destination model.EndpointIdentity
	SrcNode     string
	DstNode     string
	SrcResp     *model.AgentTraceCaptureResponse
	DstResp     *model.AgentTraceCaptureResponse
	SrcPolicies []model.MatchedPolicySummary
	DstPolicies []model.MatchedPolicySummary
	ResolveIP   func(ip string, port uint16) model.EndpointIdentity
}

// StitchTraceResult builds the multi-hop pipeline, observations table, and root-cause verdict.
func StitchTraceResult(in StitchInput) *model.TraceFlowResult {
	res := &model.TraceFlowResult{
		ID:             in.ID,
		StartedAt:      in.StartedAt,
		CompletedAt:    in.CompletedAt,
		EncapMode:      in.EncapMode,
		CrossNode:      in.CrossNode,
		KubeProxyMode:  in.SrcResp.KubeProxyMode,
		Source:         in.Source,
		Destination:    in.Destination,
		Protocol:       in.Protocol,
		SrcPort:        in.SrcPort,
		DstPort:        in.DstPort,
		TraceSignature: in.TraceSig,
	}

	// Index captured packets by stage + direction
	fwd := make(map[string]model.CapturedTracePacket)
	ret := make(map[string]model.CapturedTracePacket)
	for _, p := range in.SrcResp.CapturedPackets {
		if p.Direction == "forward" {
			fwd[p.Stage] = p
		} else {
			ret[p.Stage] = p
		}
	}
	if in.DstResp != nil {
		for _, p := range in.DstResp.CapturedPackets {
			if p.Direction == "forward" {
				fwd[p.Stage] = p
			} else {
				ret[p.Stage] = p
			}
		}
	}

	// Resolve DNAT backend identity if applicable
	if in.SrcResp.TranslatedDstIP != "" && in.ResolveIP != nil {
		be := in.ResolveIP(in.SrcResp.TranslatedDstIP, uint16(in.SrcResp.TranslatedPort))
		res.ResolvedBackend = &be
	}

	// Baseline timestamp (earliest forward packet)
	var baseNs int64
	for _, stage := range []string{"src_pod_eth0", "src_cali_veth", "src_tunnel", "src_phy_nic"} {
		if p, ok := fwd[stage]; ok && p.TimestampNs > 0 {
			baseNs = p.TimestampNs
			break
		}
	}
	deltaMsFromBase := func(tsNs int64) float64 {
		if baseNs == 0 || tsNs == 0 || tsNs < baseNs {
			return 0
		}
		return round2(float64(tsNs-baseNs) / 1e6)
	}

	var hops []model.TraceHop
	var obs []model.TraceObservation
	hopIdx := 0
	obsSeq := 0
	addHop := func(h model.TraceHop, obsType, obsDetail string) {
		hopIdx++
		h.Index = hopIdx
		hops = append(hops, h)
		obsSeq++
		obs = append(obs, model.TraceObservation{
			Seq:         obsSeq,
			Direction:   "Forward",
			Type:        obsType,
			NodeName:    h.NodeName,
			Component:   h.Title,
			Interface:   h.Interface,
			Detail:      obsDetail,
			TimestampMs: h.DeltaMs,
		})
	}

	// --- HOP 1: Source Pod eth0 ---
	p1, ok1 := fwd["src_pod_eth0"]
	addHop(model.TraceHop{
		Lane:          "source-node",
		NodeName:      in.SrcNode,
		Component:     "pod_nic",
		Title:         "Source Pod eth0",
		Subtitle:      in.Source.DisplayName,
		Interface:     "eth0",
		Status:        boolStatus(ok1 || in.SrcResp.InjectError == "", "OBSERVED", "DROPPED"),
		Observed:      ok1,
		TimestampUs:   p1.TimestampNs / 1000,
		DeltaMs:       deltaMsFromBase(p1.TimestampNs),
		PacketSummary: nonEmpty(p1.Summary, fmt.Sprintf("Originated %s probe to %s:%d", in.Protocol, in.Destination.IP, in.DstPort)),
	}, "Originated", nonEmpty(p1.Summary, "Probe packet originated in Pod netns"))

	// --- HOP 2: Source Host cali* veth ---
	p2, ok2 := fwd["src_cali_veth"]
	addHop(model.TraceHop{
		Lane:          "source-node",
		NodeName:      in.SrcNode,
		Component:     "host_veth",
		Title:         "Calico Host veth",
		Subtitle:      nonEmpty(in.SrcResp.HostVeth, "cali*"),
		Interface:     in.SrcResp.HostVeth,
		Status:        boolStatus(ok2, "OBSERVED", "DROPPED"),
		Observed:      ok2,
		TimestampUs:   p2.TimestampNs / 1000,
		DeltaMs:       deltaMsFromBase(p2.TimestampNs),
		PacketSummary: p2.Summary,
	}, boolStatus(ok2, "Forwarded", "Dropped"), nonEmpty(p2.Summary, "Packet entering host netns via Calico veth"))

	// --- HOP 3: Calico Egress Policy Evaluation ---
	egressPolicyStr := "Default Allow (no restricting Egress NetworkPolicy)"
	if len(in.SrcPolicies) > 0 {
		names := make([]string, 0, len(in.SrcPolicies))
		for _, p := range in.SrcPolicies {
			names = append(names, p.Name)
		}
		egressPolicyStr = fmt.Sprintf("Matched %d policy(s): %s", len(names), strings.Join(names, ", "))
	}
	if in.SrcResp.PolicyChainInfo != "" {
		egressPolicyStr += " | " + in.SrcResp.PolicyChainInfo
	}
	passedSrcHost := hasAny(fwd, "src_tunnel", "src_phy_nic", "dst_cali_veth", "dst_pod_eth0") || in.SrcResp.InjectResult == "CONNECTED" || in.SrcResp.InjectResult == "RST_REFUSED" || in.SrcResp.InjectResult == "ECHO_REPLY"
	addHop(model.TraceHop{
		Lane:       "source-node",
		NodeName:   in.SrcNode,
		Component:  "egress_policy",
		Title:      "Calico Egress Policy",
		Subtitle:   "cali-fw-" + in.SrcResp.HostVeth,
		Status:     boolStatus(passedSrcHost, "PASSED", "DROPPED"),
		Observed:   passedSrcHost,
		RuleDetail: egressPolicyStr,
	}, boolStatus(passedSrcHost, "Forwarded", "Dropped"), egressPolicyStr)

	// --- HOP 4 (Conditional): kube-proxy Service DNAT ---
	if in.IsService {
		natDetail := nonEmpty(in.SrcResp.NATRuleMatched, fmt.Sprintf("kube-proxy (%s) DNAT for %s:%d", in.SrcResp.KubeProxyMode, in.Destination.IP, in.DstPort))
		natSub := fmt.Sprintf("%s DNAT", in.SrcResp.KubeProxyMode)
		if in.SrcResp.TranslatedDstIP != "" {
			natSub = fmt.Sprintf("%s:%d → %s:%d", in.Destination.IP, in.DstPort, in.SrcResp.TranslatedDstIP, in.SrcResp.TranslatedPort)
		}
		addHop(model.TraceHop{
			Lane:       "source-node",
			NodeName:   in.SrcNode,
			Component:  "kube_proxy_nat",
			Title:      fmt.Sprintf("kube-proxy (%s)", in.SrcResp.KubeProxyMode),
			Subtitle:   natSub,
			Status:     "NAT",
			Observed:   in.SrcResp.TranslatedDstIP != "" || in.SrcResp.NATRuleMatched != "",
			RuleDetail: natDetail,
		}, "Translated", natDetail)
	}

	// --- HOP 5: Host Kernel Route & Encap Decision ---
	routeStatus := "PASSED"
	if strings.Contains(in.SrcResp.RouteSummary, "no route") {
		routeStatus = "DROPPED"
	}
	routeSub := nonEmpty(in.SrcResp.EncapDetected, string(in.EncapMode))
	if in.IsExternal && in.SrcResp.MasqueradeNAT {
		routeSub += " + cali-nat-outgoing SNAT"
	}
	addHop(model.TraceHop{
		Lane:       "source-node",
		NodeName:   in.SrcNode,
		Component:  "route_lookup",
		Title:      "Kernel Route Lookup",
		Subtitle:   routeSub,
		Interface:  in.SrcResp.EgressInterface,
		Status:     routeStatus,
		Observed:   routeStatus == "PASSED",
		RuleDetail: nonEmpty(in.SrcResp.RouteSummary, "Kernel FIB lookup"),
	}, "Forwarded", nonEmpty(in.SrcResp.RouteSummary, routeSub))

	// --- Cross-Node / External Egress & Fabric Hops ---
	if in.CrossNode || in.IsExternal {
		// Tunnel encapsulation hop (IPIP / VXLAN)
		if pt, okT := fwd["src_tunnel"]; okT || strings.Contains(in.SrcResp.EncapDetected, "IPIP") || strings.Contains(in.SrcResp.EncapDetected, "VXLAN") {
			addHop(model.TraceHop{
				Lane:          "source-node",
				NodeName:      in.SrcNode,
				Component:     "tunnel_encap",
				Title:         "Calico Tunnel Encap",
				Subtitle:      in.SrcResp.EncapDetected,
				Interface:     pt.Interface,
				Status:        boolStatus(okT, "ENCAPSULATED", "SKIPPED"),
				Observed:      okT,
				TimestampUs:   pt.TimestampNs / 1000,
				DeltaMs:       deltaMsFromBase(pt.TimestampNs),
				PacketSummary: pt.Summary,
			}, "Encapsulated", nonEmpty(pt.Summary, in.SrcResp.EncapDetected))
		}

		// Source Physical NIC egress
		pNic, okNic := fwd["src_phy_nic"]
		addHop(model.TraceHop{
			Lane:          "source-node",
			NodeName:      in.SrcNode,
			Component:     "host_nic",
			Title:         "Source Host NIC Egress",
			Subtitle:      nonEmpty(pNic.Interface, in.SrcResp.EgressInterface),
			Interface:     nonEmpty(pNic.Interface, in.SrcResp.EgressInterface),
			Status:        boolStatus(okNic, "OBSERVED", "DROPPED"),
			Observed:      okNic,
			TimestampUs:   pNic.TimestampNs / 1000,
			DeltaMs:       deltaMsFromBase(pNic.TimestampNs),
			PacketSummary: pNic.Summary,
			OuterHeader:   pNic.OuterHeader,
		}, boolStatus(okNic, "Forwarded", "Dropped"), nonEmpty(pNic.OuterHeader+" "+pNic.Summary, "Packet egress on node physical NIC"))

		// Underlay / Overlay Fabric Hop
		fabricTitle := fmt.Sprintf("Underlay Fabric (%s)", in.EncapMode)
		if pNic.OuterHeader != "" {
			fabricTitle = "Overlay Tunnel Fabric"
		} else if in.IsExternal {
			fabricTitle = "External Network / Gateway"
		}
		dNic, okDNic := fwd["dst_phy_nic"]
		fabricOK := okDNic || (in.IsExternal && (in.SrcResp.InjectResult == "CONNECTED" || in.SrcResp.InjectResult == "ECHO_REPLY" || in.SrcResp.InjectResult == "RST_REFUSED"))
		addHop(model.TraceHop{
			Lane:        "fabric",
			Component:   "underlay_fabric",
			Title:       fabricTitle,
			Subtitle:    nonEmpty(pNic.OuterHeader, fmt.Sprintf("%s → %s", in.SrcNode, nonEmpty(in.DstNode, in.Destination.IP))),
			Status:      boolStatus(fabricOK, "PASSED", boolStatus(okNic, "DROPPED", "SKIPPED")),
			Observed:    fabricOK,
			OuterHeader: nonEmpty(pNic.OuterHeader, dNic.OuterHeader),
			RuleDetail:  nonEmpty(in.SrcResp.NextHop, "L2/L3 physical fabric"),
		}, boolStatus(fabricOK, "Forwarded", "Dropped"), nonEmpty(pNic.OuterHeader, fabricTitle))

		// Destination Node Ingress Hops (for Cross-Node K8s targets)
		if in.CrossNode && in.DstResp != nil {
			addHop(model.TraceHop{
				Lane:          "dest-node",
				NodeName:      in.DstNode,
				Component:     "host_nic",
				Title:         "Dest Host NIC Ingress",
				Subtitle:      dNic.Interface,
				Interface:     dNic.Interface,
				Status:        boolStatus(okDNic, "OBSERVED", "DROPPED"),
				Observed:      okDNic,
				TimestampUs:   dNic.TimestampNs / 1000,
				DeltaMs:       deltaMsFromBase(dNic.TimestampNs),
				PacketSummary: dNic.Summary,
				OuterHeader:   dNic.OuterHeader,
			}, boolStatus(okDNic, "Received", "Dropped"), nonEmpty(dNic.Summary, "Packet arrival on destination node NIC"))

			if dt, okDT := fwd["dst_tunnel"]; okDT {
				addHop(model.TraceHop{
					Lane:          "dest-node",
					NodeName:      in.DstNode,
					Component:     "tunnel_decap",
					Title:         "Calico Tunnel Decap",
					Subtitle:      dt.Interface,
					Interface:     dt.Interface,
					Status:        "DECAPSULATED",
					Observed:      true,
					TimestampUs:   dt.TimestampNs / 1000,
					DeltaMs:       deltaMsFromBase(dt.TimestampNs),
					PacketSummary: dt.Summary,
				}, "Decapsulated", dt.Summary)
			}
		}
	}

	// --- Destination Calico Policy, veth & Pod Delivery (for Pod / Service targets) ---
	if !in.IsExternal && in.Destination.Kind != "Node" {
		dstLane := "dest-node"
		dstNodeLabel := in.DstNode
		if !in.CrossNode {
			dstLane = "source-node"
			dstNodeLabel = in.SrcNode
		}

		pDstVeth, okDstVeth := fwd["dst_cali_veth"]
		pDstPod, okDstPod := fwd["dst_pod_eth0"]

		ingPolicyStr := "Default Allow (no restricting Ingress NetworkPolicy)"
		if len(in.DstPolicies) > 0 {
			names := make([]string, 0, len(in.DstPolicies))
			for _, p := range in.DstPolicies {
				names = append(names, p.Name)
			}
			ingPolicyStr = fmt.Sprintf("Matched %d policy(s): %s", len(names), strings.Join(names, ", "))
		}
		if in.DstResp != nil && in.DstResp.PolicyChainInfo != "" {
			ingPolicyStr += " | " + in.DstResp.PolicyChainInfo
		}

		addHop(model.TraceHop{
			Lane:       dstLane,
			NodeName:   dstNodeLabel,
			Component:  "ingress_policy",
			Title:      "Calico Ingress Policy",
			Subtitle:   "cali-tw-" + nonEmpty(pDstVeth.Interface, "cali*"),
			Status:     boolStatus(okDstVeth || okDstPod || in.SrcResp.InjectResult == "CONNECTED" || in.SrcResp.InjectResult == "RST_REFUSED", "PASSED", "DROPPED"),
			Observed:   okDstVeth || okDstPod,
			RuleDetail: ingPolicyStr,
		}, boolStatus(okDstVeth || okDstPod, "Forwarded", "Dropped"), ingPolicyStr)

		addHop(model.TraceHop{
			Lane:          dstLane,
			NodeName:      dstNodeLabel,
			Component:     "host_veth",
			Title:         "Dest Calico veth",
			Subtitle:      nonEmpty(pDstVeth.Interface, "cali*"),
			Interface:     pDstVeth.Interface,
			Status:        boolStatus(okDstVeth, "OBSERVED", "DROPPED"),
			Observed:      okDstVeth,
			TimestampUs:   pDstVeth.TimestampNs / 1000,
			DeltaMs:       deltaMsFromBase(pDstVeth.TimestampNs),
			PacketSummary: pDstVeth.Summary,
		}, boolStatus(okDstVeth, "Forwarded", "Dropped"), nonEmpty(pDstVeth.Summary, "Forwarding into destination Pod veth"))

		dstTitle := "Destination Pod eth0"
		dstSub := in.Destination.DisplayName
		if res.ResolvedBackend != nil {
			dstSub = res.ResolvedBackend.DisplayName
		}
		podStatus := "DROPPED"
		if okDstPod || in.SrcResp.InjectResult == "CONNECTED" || in.SrcResp.InjectResult == "ECHO_REPLY" {
			podStatus = "DELIVERED"
		} else if in.SrcResp.InjectResult == "RST_REFUSED" {
			podStatus = "RESET"
		}
		addHop(model.TraceHop{
			Lane:          dstLane,
			NodeName:      dstNodeLabel,
			Component:     "dst_pod",
			Title:         dstTitle,
			Subtitle:      dstSub,
			Interface:     "eth0",
			Status:        podStatus,
			Observed:      okDstPod,
			TimestampUs:   pDstPod.TimestampNs / 1000,
			DeltaMs:       deltaMsFromBase(pDstPod.TimestampNs),
			PacketSummary: pDstPod.Summary,
		}, boolStatus(podStatus == "DELIVERED" || podStatus == "RESET", "Delivered", "Dropped"), nonEmpty(pDstPod.Summary, "Delivery to destination Pod network stack"))
	} else if in.IsExternal || in.Destination.Kind == "Node" {
		delivered := in.SrcResp.InjectResult == "CONNECTED" || in.SrcResp.InjectResult == "ECHO_REPLY" || in.SrcResp.InjectResult == "RST_REFUSED" || len(ret) > 0
		lane := "dest-node"
		if in.IsExternal {
			lane = "external"
		}
		addHop(model.TraceHop{
			Lane:      lane,
			NodeName:  in.DstNode,
			Component: "dst_pod",
			Title:     boolStatus(in.IsExternal, "External Endpoint", "Target Node Stack"),
			Subtitle:  in.Destination.DisplayName,
			Status:    boolStatus(delivered, "DELIVERED", "TIMEOUT"),
			Observed:  delivered,
		}, boolStatus(delivered, "Delivered", "Timeout"), fmt.Sprintf("Target %s:%d (%s)", in.Destination.IP, in.DstPort, in.SrcResp.InjectResult))
	}

	// --- Return Path Observations ---
	returnStages := []struct {
		stage string
		title string
		lane  string
		node  string
	}{
		{"dst_pod_eth0", "Destination Pod Reply", "dest-node", in.DstNode},
		{"dst_cali_veth", "Dest Calico veth (Return)", "dest-node", in.DstNode},
		{"dst_phy_nic", "Dest Host NIC Egress (Return)", "dest-node", in.DstNode},
		{"src_phy_nic", "Source Host NIC Ingress (Return)", "source-node", in.SrcNode},
		{"src_cali_veth", "Source Calico veth (Return)", "source-node", in.SrcNode},
		{"src_pod_eth0", "Source Pod eth0 (Reply Received)", "source-node", in.SrcNode},
	}
	var retHops []model.TraceHop
	rIdx := 0
	for _, rs := range returnStages {
		if rp, ok := ret[rs.stage]; ok {
			rIdx++
			dMs := deltaMsFromBase(rp.TimestampNs)
			retHops = append(retHops, model.TraceHop{
				Index:         rIdx,
				Lane:          rs.lane,
				NodeName:      rs.node,
				Component:     rs.stage,
				Title:         rs.title,
				Interface:     rp.Interface,
				Status:        boolStatus(strings.Contains(rp.TCPFlags, "RST"), "RESET", "OBSERVED"),
				Observed:      true,
				TimestampUs:   rp.TimestampNs / 1000,
				DeltaMs:       dMs,
				PacketSummary: rp.Summary,
				OuterHeader:   rp.OuterHeader,
			})
			obsSeq++
			obs = append(obs, model.TraceObservation{
				Seq:         obsSeq,
				Direction:   "Return",
				Type:        "Replied",
				NodeName:    rs.node,
				Component:   rs.title,
				Interface:   rp.Interface,
				Detail:      rp.Summary,
				TimestampMs: dMs,
			})
		}
	}

	// Calculate total round-trip latency
	if pRet, ok := ret["src_pod_eth0"]; ok && baseNs > 0 && pRet.TimestampNs > baseNs {
		res.TotalLatencyMs = round2(float64(pRet.TimestampNs-baseNs) / 1e6)
	} else {
		res.TotalLatencyMs = round2(float64(in.CompletedAt.Sub(in.StartedAt).Microseconds()) / 1000.0)
	}

	// Determine final verdict & root cause hint
	switch in.SrcResp.InjectResult {
	case "CONNECTED", "ECHO_REPLY":
		res.Status = "REACHABLE"
		res.VerdictSummary = fmt.Sprintf("端到端路径可达 (%s)，已通过 %d 个数据面跳点观测确认，往返耗时 %.2f ms", in.EncapMode, len(hops), res.TotalLatencyMs)
	case "RST_REFUSED":
		res.Status = "RESET"
		res.VerdictSummary = fmt.Sprintf("网络路径全部连通，但目标 %s:%d 返回 TCP RST (端口未监听)", in.Destination.IP, in.DstPort)
		res.RootCauseHint = "Calico 路由、隧道与 NetworkPolicy 均已放行；请检查目标 Pod 进程是否正在监听该端口或 Service targetPort 配置是否正确。"
	case "SENT": // UDP
		if _, ok := fwd["dst_pod_eth0"]; ok || (len(ret) > 0) {
			res.Status = "REACHABLE"
			res.VerdictSummary = fmt.Sprintf("UDP 报文已成功送达目标 Pod (%s)，路径正常", in.EncapMode)
		} else {
			res.Status = "TIMEOUT"
			res.VerdictSummary = "UDP 探测包已发出，未捕获到目标回包（UDP 无连接特性或中途丢包）"
			res.RootCauseHint = localizeDropHint(fwd, in.CrossNode, in.SrcPolicies, in.DstPolicies)
		}
	default:
		if _, ok := ret["src_pod_eth0"]; ok {
			res.Status = "REACHABLE"
			res.VerdictSummary = "端到端往返报文已观测到，路径连通"
		} else {
			res.Status = "DROPPED"
			res.RootCauseHint = localizeDropHint(fwd, in.CrossNode, in.SrcPolicies, in.DstPolicies)
			res.VerdictSummary = "探测报文未收到响应 — " + res.RootCauseHint
		}
	}

	res.ForwardHops = hops
	res.ReturnHops = retHops
	res.Observations = obs
	return res
}

func localizeDropHint(fwd map[string]model.CapturedTracePacket, crossNode bool, srcPol, dstPol []model.MatchedPolicySummary) string {
	_, okSrcVeth := fwd["src_cali_veth"]
	_, okSrcNic := fwd["src_phy_nic"]
	_, okDstNic := fwd["dst_phy_nic"]
	_, okDstVeth := fwd["dst_cali_veth"]
	_, okDstPod := fwd["dst_pod_eth0"]

	switch {
	case !okSrcVeth:
		return "报文未离开源 Pod eth0 或未抵达宿主机 cali* veth，请检查源 Pod 内部路由与 veth 状态。"
	case crossNode && !okSrcNic:
		if len(srcPol) > 0 {
			return "报文进入源宿主机后未从物理网卡发出，极可能被源节点 Calico Egress NetworkPolicy 阻断或缺少去往目标网段的路由。"
		}
		return "报文进入源宿主机后未从物理网卡发出，请检查源节点路由表（BIRD BGP / IPIP / VXLAN 路由）与 Calico Egress 规则。"
	case crossNode && okSrcNic && !okDstNic:
		return "报文已从源节点物理网卡发出但未到达目标节点网卡：故障位于底层物理网络 / 防火墙（检查 BGP 路由、IPIP Proto 4 或 VXLAN UDP/4789 端口放行与 MTU）。"
	case okDstNic && !okDstVeth:
		if len(dstPol) > 0 {
			return "报文已抵达目标节点网卡，但未进入目标 Pod cali* veth：极大概率被目标 Pod 的 Ingress NetworkPolicy 丢弃。"
		}
		return "报文已抵达目标节点网卡，但未转发至目标 Pod veth，请检查目标节点隧道解封装、本地 /32 路由与 Calico Ingress 链。"
	case okDstVeth && !okDstPod:
		return "报文已进入目标 veth 但目标 Pod 未响应，请检查目标 Pod 内部防火墙/路由或应用状态。"
	default:
		return "正向路径已达目标但未收到回包，请检查目标侧回程路由、源端 Ingress Policy 或目标服务响应超时。"
	}
}

func hasAny(m map[string]model.CapturedTracePacket, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

func boolStatus(cond bool, t, f string) string {
	if cond {
		return t
	}
	return f
}

func nonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func genID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "tf-" + hex.EncodeToString(b)
}

func randomEphemeralPort() int {
	var b [2]byte
	_, _ = rand.Read(b[:])
	v := binary.BigEndian.Uint16(b[:])
	return 49152 + int(v%10000)
}

func randomICMPID() uint16 {
	var b [2]byte
	_, _ = rand.Read(b[:])
	v := binary.BigEndian.Uint16(b[:])
	if v == 0 {
		v = 4242
	}
	return v
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
