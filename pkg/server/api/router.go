package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"calico-netdiag/pkg/k8s"
	"calico-netdiag/pkg/model"
	"calico-netdiag/pkg/server/agentclient"
	"calico-netdiag/pkg/server/flowstore"
	"calico-netdiag/pkg/server/healthcheck"
	"calico-netdiag/pkg/server/traceflow"

	"github.com/gorilla/websocket"
)

// Router serves all REST, WebSocket, internal Agent ingestion, Hugo docs, and SPA Web UI routes.
type Router struct {
	namespace  string
	k8sStore   *k8s.Store
	flows      *flowstore.Store
	agents     *agentclient.Client
	tracer     *traceflow.Orchestrator
	health     *healthcheck.Runner
	staticFS   fs.FS
	docsFS     fs.FS
	upgrader   websocket.Upgrader
}

// NewRouter constructs the HTTP router.
func NewRouter(
	namespace string,
	k8sStore *k8s.Store,
	flows *flowstore.Store,
	agents *agentclient.Client,
	tracer *traceflow.Orchestrator,
	health *healthcheck.Runner,
	staticFS fs.FS,
	docsFS fs.FS,
) http.Handler {
	r := &Router{
		namespace: namespace,
		k8sStore:  k8sStore,
		flows:     flows,
		agents:    agents,
		tracer:    tracer,
		health:    health,
		staticFS:  staticFS,
		docsFS:    docsFS,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(req *http.Request) bool { return true },
		},
	}

	mux := http.NewServeMux()
	// Internal agent ingestion
	mux.HandleFunc("/api/internal/agent/report", r.handleAgentReport)

	// Public REST API v1
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/v1/overview", r.handleOverview)
	mux.HandleFunc("/api/v1/namespaces", r.handleNamespaces)
	mux.HandleFunc("/api/v1/ipam", r.handleIPAM)
	mux.HandleFunc("/api/v1/search", r.handleSearch)
	mux.HandleFunc("/api/v1/pods", r.handleListPods)
	mux.HandleFunc("/api/v1/pods/detail", r.handlePodDetail)
	mux.HandleFunc("/api/v1/services", r.handleListServices)
	mux.HandleFunc("/api/v1/topology", r.handleTopology)
	mux.HandleFunc("/api/v1/flows", r.handleFlows)
	mux.HandleFunc("/api/v1/traceflow", r.handleTraceFlow)
	mux.HandleFunc("/api/v1/traceflow/history", r.handleTraceFlowHistory)
	mux.HandleFunc("/api/v1/diag/tcp-handshake", r.handleTCPHandshake)
	mux.HandleFunc("/api/v1/diag/http-probe", r.handleHTTPProbe)
	mux.HandleFunc("/api/v1/diag/tcpdump/ws", r.handleTCPDumpWS)
	mux.HandleFunc("/api/v1/diag/pcap", r.handlePcapDownload)
	mux.HandleFunc("/api/v1/health", r.handleHealth)
	mux.HandleFunc("/api/v1/health/run", r.handleHealthRun)

	// Embedded Hugo Documentation Site (/docs/)
	mux.HandleFunc("/docs", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/docs/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/docs/", r.handleDocs)

	// Embedded React SPA
	mux.HandleFunc("/", r.handleSPA)

	return withCORS(mux)
}

func (r *Router) handleAgentReport(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var batch model.AgentFlowBatch
	if err := json.NewDecoder(req.Body).Decode(&batch); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	r.flows.Ingest(batch)
	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

func (r *Router) handleOverview(w http.ResponseWriter, _ *http.Request) {
	k8sVer, inst, felix, bgpCfg, rawPeers, kpMode, clusterCIDR, podCnt, svcCnt, nsCnt, polCnt := r.k8sStore.GetClusterMetadata()
	ipam := r.k8sStore.GetIPAMSnapshot()
	snaps := r.flows.ListNodeSnapshots()
	nodes := r.k8sStore.ListNodes()
	allPods := r.k8sStore.ListPods("")
	probePods := r.k8sStore.ListComponentPods(r.namespace, "app.kubernetes.io/component", "probe")

	probeIPByNode := make(map[string]string)
	for _, p := range probePods {
		if p.Spec.NodeName != "" && p.Status.PodIP != "" {
			probeIPByNode[p.Spec.NodeName] = p.Status.PodIP
		}
	}
	podCntByNode := make(map[string]int)
	for _, p := range allPods {
		if p.NodeName != "" {
			podCntByNode[p.NodeName]++
		}
	}
	blockCntByNode := make(map[string]int)
	allocByNode := make(map[string]int)
	for _, b := range ipam.Blocks {
		if b.AffinityNode != "" {
			blockCntByNode[b.AffinityNode]++
			allocByNode[b.AffinityNode] += b.AllocatedSlots
		}
	}

	nodeIPToName := make(map[string]string)
	for _, n := range nodes {
		if ip := k8s.NodeInternalIP(n); ip != "" {
			nodeIPToName[ip] = n.Name
		}
	}

	var nodeStatuses []model.NodeNetworkStatus
	var bgpPeersOut []model.BGPPeerSummary
	detectedKP := kpMode

	for _, n := range nodes {
		ns := model.NodeNetworkStatus{
			Name:             n.Name,
			Roles:            k8s.NodeRoles(n),
			Ready:            k8s.IsNodeReady(n),
			InternalIP:       k8s.NodeInternalIP(n),
			OSImage:          n.Status.NodeInfo.OSImage,
			KernelVersion:    n.Status.NodeInfo.KernelVersion,
			ContainerRuntime: n.Status.NodeInfo.ContainerRuntimeVersion,
			Architecture:     n.Status.NodeInfo.Architecture,
			PodCIDR:          n.Spec.PodCIDR,
			IPIPTunnelIP:     n.Annotations["projectcalico.org/IPv4IPIPTunnelAddr"],
			VXLANTunnelIP:    n.Annotations["projectcalico.org/IPv4VXLANTunnelAddr"],
			VXLANTunnelMAC:   n.Annotations["projectcalico.org/IPv4VXLANTunnelMACAddr"],
			PodCount:         podCntByNode[n.Name],
			BlockCount:       blockCntByNode[n.Name],
			AllocatedIPs:     allocByNode[n.Name],
			ProbePodIP:       probeIPByNode[n.Name],
			KubeProxyMode:    kpMode,
			TelemetryEngine:  model.TelemetryConntrack,
		}
		if sn, ok := snaps[n.Name]; ok {
			ns.AgentReady = time.Since(sn.LastHeartbeat) < 30*time.Second
			ns.AgentPodIP = sn.AgentPodIP
			ns.KubeProxyMode = sn.KubeProxyMode
			if detectedKP == model.KubeProxyUnknown || detectedKP == "" {
				detectedKP = sn.KubeProxyMode
			}
			ns.TelemetryEngine = sn.Engine
			ns.TelemetryReason = sn.EngineReason
			ns.BTFAvailable = sn.BTFAvailable
			ns.ConntrackAcct = sn.ConntrackAcct
			ns.PrimaryNIC = sn.PrimaryNIC
			ns.NICMTU = sn.NICMTU
			ns.TunnelInterfaces = sn.TunnelIfaces
			ns.BGPProtocols = sn.BGPProtocols
			ns.ActiveFlowsCount = sn.ActiveFlows
			ns.LastHeartbeat = sn.LastHeartbeat

			for _, bp := range sn.BGPProtocols {
				if !strings.EqualFold(bp.Proto, "BGP") {
					continue
				}
				scope := "Global"
				if strings.HasPrefix(bp.Name, "Mesh_") {
					scope = "Mesh"
				} else if strings.HasPrefix(bp.Name, "Node_") {
					scope = "Node"
				}
				state := bp.Info
				if state == "" {
					state = bp.State
				}
				bgpPeersOut = append(bgpPeersOut, model.BGPPeerSummary{
					Name:       bp.Name,
					SourceNode: n.Name,
					PeerIP:     bp.PeerIP,
					PeerNode:   nodeIPToName[bp.PeerIP],
					ASNumber:   bgpCfg.ASNumber,
					Scope:      scope,
					State:      state,
					Since:      bp.Since,
				})
			}
		}
		nodeStatuses = append(nodeStatuses, ns)
	}

	// Include configured external BGPPeer CRDs if not already listed
	for _, rp := range rawPeers {
		bgpPeersOut = append(bgpPeersOut, model.BGPPeerSummary{
			Name:       rp.Name,
			SourceNode: rp.Node,
			PeerIP:     rp.PeerIP,
			ASNumber:   rp.ASNumber,
			Scope:      "CRD Peer",
			State:      "Configured",
		})
	}

	linuxDP := inst.LinuxDataplane
	if felix.BPFEnabled {
		linuxDP = "eBPF"
	} else if strings.EqualFold(felix.NFTablesMode, "Enabled") {
		linuxDP = "NFTables"
	}

	// Derive Service CIDR hint from kubernetes service IP if available
	svcCIDR := ""
	if svc, _, ok := r.k8sStore.GetService("default", "kubernetes"); ok && svc.Spec.ClusterIP != "" {
		parts := strings.Split(svc.Spec.ClusterIP, ".")
		if len(parts) == 4 {
			svcCIDR = fmt.Sprintf("%s.%s.0.0/16 (approx, API=%s)", parts[0], parts[1], svc.Spec.ClusterIP)
		}
	}

	overview := model.ClusterNetworkOverview{
		UpdatedAt:        time.Now(),
		K8sVersion:       k8sVer,
		CalicoVersion:    inst.Version,
		CalicoVariant:    inst.Variant,
		PrimaryEncapMode: r.k8sStore.PrimaryEncapMode(),
		LinuxDataplane:   linuxDP,
		BGPEnabled:       inst.BGPEnabled,
		BGPASNumber:      bgpCfg.ASNumber,
		NodeToNodeMesh:   bgpCfg.NodeToNodeMesh,
		KubeProxyMode:    detectedKP,
		ClusterCIDR:      clusterCIDR,
		ServiceCIDR:      svcCIDR,
		MTU:              inst.MTU,
		VXLANVNI:         felix.VXLANVNI,
		VXLANPort:        felix.VXLANPort,
		PodCount:         podCnt,
		ServiceCount:     svcCnt,
		NamespaceCount:   nsCnt,
		NetworkPolicyCnt: polCnt,
		IPPools:          ipam.Pools,
		Nodes:            nodeStatuses,
		BGPPeers:         bgpPeersOut,
		GlobalTraffic:    r.flows.GlobalTrafficSummary(),
	}
	writeJSON(w, http.StatusOK, overview)
}

func (r *Router) handleNamespaces(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"namespaces": r.k8sStore.GetNamespaces(),
	})
}

func (r *Router) handleIPAM(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, r.k8sStore.GetIPAMSnapshot())
}

func (r *Router) handleSearch(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query().Get("q")
	ns := req.URL.Query().Get("namespace")
	kind := req.URL.Query().Get("kind")
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	items := r.k8sStore.Search(q, ns, kind, limit)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"query":   q,
		"total":   len(items),
		"results": items,
	})
}

func (r *Router) handleListPods(w http.ResponseWriter, req *http.Request) {
	ns := req.URL.Query().Get("namespace")
	pods := r.k8sStore.ListPods(ns)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total": len(pods),
		"pods":  pods,
	})
}

func (r *Router) handleListServices(w http.ResponseWriter, req *http.Request) {
	nsFilter := req.URL.Query().Get("namespace")
	svcs, _ := r.k8sStore.ListAllServices()
	type svcBrief struct {
		Namespace string   `json:"namespace"`
		Name      string   `json:"name"`
		Type      string   `json:"type"`
		ClusterIP string   `json:"clusterIP"`
		Ports     []int32  `json:"ports"`
		PortDesc  []string `json:"portDesc"`
	}
	out := make([]svcBrief, 0, len(svcs))
	for _, s := range svcs {
		if nsFilter != "" && s.Namespace != nsFilter {
			continue
		}
		var ps []int32
		var pd []string
		for _, p := range s.Spec.Ports {
			ps = append(ps, p.Port)
			pd = append(pd, fmt.Sprintf("%d/%s", p.Port, p.Protocol))
		}
		out = append(out, svcBrief{
			Namespace: s.Namespace,
			Name:      s.Name,
			Type:      string(s.Spec.Type),
			ClusterIP: s.Spec.ClusterIP,
			Ports:     ps,
			PortDesc:  pd,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"services": out})
}

func (r *Router) handlePodDetail(w http.ResponseWriter, req *http.Request) {
	ns := req.URL.Query().Get("namespace")
	name := req.URL.Query().Get("name")
	pod, brief, ok := r.k8sStore.GetPod(ns, name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("pod %s/%s not found", ns, name)})
		return
	}

	svcRefs, pols := r.k8sStore.GetPodServicesAndPolicies(ns, name)
	containers := make([]string, 0, len(pod.Spec.Containers))
	for _, c := range pod.Spec.Containers {
		containers = append(containers, c.Name)
	}
	encap := r.k8sStore.PrimaryEncapMode()
	if meta, ok := r.k8sStore.GetIPMeta(brief.IP); ok {
		encap = meta.EncapMode
	}

	detail := model.PodNetworkDetail{
		Pod:             brief,
		UID:             string(pod.UID),
		CreatedAt:       pod.CreationTimestamp.Time,
		Labels:          pod.Labels,
		Containers:      containers,
		EncapMode:       encap,
		Services:        svcRefs,
		NetworkPolicies: pols,
	}

	if brief.NodeName != "" {
		dp, err := r.agents.GetPodDataplane(req.Context(), brief.NodeName, string(pod.UID), brief.IP)
		if err != nil {
			detail.DataplaneError = err.Error()
		} else {
			detail.HostVeth = dp.HostVeth
			detail.PodIface = dp.PodIface
			detail.PodMAC = dp.PodMAC
			detail.PodMTU = dp.PodMTU
			detail.PodRoutes = dp.PodRoutes
			detail.HostRouteToPod = dp.HostRouteToPod
			if dp.Error != "" {
				detail.DataplaneError = dp.Error
			}
		}
	}

	writeJSON(w, http.StatusOK, detail)
}

func (r *Router) handleTopology(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	gran := q.Get("granularity")
	win, _ := strconv.Atoi(q.Get("window"))
	nsRaw := q.Get("namespaces")
	var nsList []string
	if nsRaw != "" {
		nsList = strings.Split(nsRaw, ",")
	}
	graph := r.flows.BuildTopology(gran, win, nsList, q.Get("search"), q.Get("protocol"))
	writeJSON(w, http.StatusOK, graph)
}

func (r *Router) handleFlows(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	win, _ := strconv.Atoi(q.Get("window"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	flows := r.flows.QueryFlows(win, q.Get("namespace"), q.Get("search"), q.Get("protocol"), limit)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total": len(flows),
		"flows": flows,
	})
}

func (r *Router) handleTraceFlow(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var treq model.TraceFlowRequest
	if err := json.NewDecoder(req.Body).Decode(&treq); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	res, err := r.tracer.Run(req.Context(), treq)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (r *Router) handleTraceFlowHistory(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"history": r.tracer.History(),
	})
}

func (r *Router) handleTCPHandshake(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var hreq model.TCPHandshakeRequest
	if err := json.NewDecoder(req.Body).Decode(&hreq); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	pod, brief, ok := r.k8sStore.GetPod(hreq.SrcNamespace, hreq.SrcPod)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("pod %s/%s not found", hreq.SrcNamespace, hreq.SrcPod)})
		return
	}
	res, err := r.agents.RunTCPHandshake(req.Context(), brief.NodeName, string(pod.UID), brief.IP, brief.Namespace+"/"+brief.Name, hreq)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (r *Router) handleHTTPProbe(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var preq model.HTTPProbeRequest
	if err := json.NewDecoder(req.Body).Decode(&preq); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	pod, brief, ok := r.k8sStore.GetPod(preq.SrcNamespace, preq.SrcPod)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("pod %s/%s not found", preq.SrcNamespace, preq.SrcPod)})
		return
	}
	res, err := r.agents.RunHTTPProbe(req.Context(), brief.NodeName, string(pod.UID), brief.IP, brief.Namespace+"/"+brief.Name, preq)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (r *Router) handleTCPDumpWS(w http.ResponseWriter, req *http.Request) {
	conn, err := r.upgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	var dreq model.TCPDumpRequest
	if err := conn.ReadJSON(&dreq); err != nil {
		_ = conn.WriteJSON(model.TCPDumpControlMessage{Type: "error", Message: "invalid tcpdump request: " + err.Error()})
		return
	}

	nodeName := dreq.NodeName
	podUID := ""
	podIP := ""
	if dreq.Namespace != "" && dreq.PodName != "" {
		pod, brief, ok := r.k8sStore.GetPod(dreq.Namespace, dreq.PodName)
		if !ok {
			_ = conn.WriteJSON(model.TCPDumpControlMessage{Type: "error", Message: fmt.Sprintf("pod %s/%s not found", dreq.Namespace, dreq.PodName)})
			return
		}
		nodeName = brief.NodeName
		podUID = string(pod.UID)
		podIP = brief.IP
	}
	if nodeName == "" {
		_ = conn.WriteJSON(model.TCPDumpControlMessage{Type: "error", Message: "node or pod must be specified"})
		return
	}

	if err := r.agents.ProxyTCPDumpWS(conn, nodeName, podUID, podIP, dreq); err != nil {
		_ = conn.WriteJSON(model.TCPDumpControlMessage{Type: "error", Message: err.Error()})
	}
}

func (r *Router) handlePcapDownload(w http.ResponseWriter, req *http.Request) {
	nodeName := req.URL.Query().Get("node")
	captureID := req.URL.Query().Get("id")
	if nodeName == "" || captureID == "" {
		http.Error(w, "missing node or id query param", http.StatusBadRequest)
		return
	}
	if err := r.agents.ProxyPcapDownload(w, req, nodeName, captureID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
	}
}

func (r *Router) handleHealth(w http.ResponseWriter, req *http.Request) {
	rep, err := r.health.Latest(req.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (r *Router) handleHealthRun(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rep, err := r.health.RunNow(req.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (r *Router) handleDocs(w http.ResponseWriter, req *http.Request) {
	if r.docsFS == nil {
		http.NotFound(w, req)
		return
	}
	http.StripPrefix("/docs/", http.FileServer(http.FS(r.docsFS))).ServeHTTP(w, req)
}

func (r *Router) handleSPA(w http.ResponseWriter, req *http.Request) {
	if r.staticFS == nil {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("calico-plumber API Server running (Web UI static bundle not embedded)."))
		return
	}

	cleanPath := strings.TrimPrefix(req.URL.Path, "/")
	if cleanPath == "" {
		cleanPath = "index.html"
	}
	if f, err := r.staticFS.Open(cleanPath); err == nil {
		_ = f.Close()
		http.FileServer(http.FS(r.staticFS)).ServeHTTP(w, req)
		return
	}

	// SPA fallback to index.html for client-side React routes
	data, err := fs.ReadFile(r.staticFS, "index.html")
	if err != nil {
		http.NotFound(w, req)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
