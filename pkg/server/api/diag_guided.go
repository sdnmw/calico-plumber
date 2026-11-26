package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"calico-netdiag/pkg/model"
	"calico-netdiag/pkg/server/healthcheck"

	corev1 "k8s.io/api/core/v1"
)

// handleServiceInspect answers "why are backends empty?" and "is the Ready backend Pod actually listening on targetPort?".
func (r *Router) handleServiceInspect(w http.ResponseWriter, req *http.Request) {
	ns := req.URL.Query().Get("namespace")
	name := req.URL.Query().Get("name")
	focusPort, _ := strconv.Atoi(req.URL.Query().Get("port"))
	if ns == "" || name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "namespace and name are required"})
		return
	}

	svc, podInfos, slices, matchedPods, ok := r.k8sStore.InspectServiceState(ns, name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("service %s/%s not found", ns, name)})
		return
	}

	// Build port mappings & resolve target port number for focusPort
	portsOut := make([]model.ServicePortMapping, 0, len(svc.Spec.Ports))
	targetPortNum := focusPort
	targetPortName := ""
	for _, sp := range svc.Spec.Ports {
		proto := string(sp.Protocol)
		if proto == "" {
			proto = "TCP"
		}
		tp := sp.TargetPort.String()
		if tp == "" || tp == "0" {
			tp = fmt.Sprintf("%d", sp.Port)
		}
		portsOut = append(portsOut, model.ServicePortMapping{
			Name:       sp.Name,
			Port:       int(sp.Port),
			TargetPort: tp,
			Protocol:   proto,
		})
		if focusPort == 0 || int(sp.Port) == focusPort {
			if focusPort == 0 {
				focusPort = int(sp.Port)
			}
			if sp.TargetPort.IntVal > 0 {
				targetPortNum = int(sp.TargetPort.IntVal)
			} else if sp.TargetPort.StrVal != "" {
				targetPortName = sp.TargetPort.StrVal
			} else {
				targetPortNum = int(sp.Port)
			}
		}
	}

	// Also inspect real listening sockets inside up to 6 Running matching Pods (via Node Agent /proc/net/tcp)
	cctx, cancel := context.WithTimeout(req.Context(), 6*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	limit := len(matchedPods)
	if limit > 6 {
		limit = 6
	}
	for i := 0; i < limit; i++ {
		p := matchedPods[i]
		if p.Status.Phase != corev1.PodRunning || p.Spec.NodeName == "" {
			continue
		}
		// Resolve named containerPort if Service targetPort is a string name
		resolvedPort := targetPortNum
		if targetPortName != "" {
			for _, c := range p.Spec.Containers {
				for _, cp := range c.Ports {
					if cp.Name == targetPortName {
						resolvedPort = int(cp.ContainerPort)
						break
					}
				}
			}
		}
		wg.Add(1)
		go func(idx int, pod *corev1.Pod, expectPort int) {
			defer wg.Done()
			socks, err := r.agents.GetPodListeningSockets(cctx, pod.Spec.NodeName, string(pod.UID), pod.Status.PodIP, pod.Spec.HostNetwork)
			if err != nil {
				podInfos[idx].SocketInspectErr = err.Error()
				podInfos[idx].TargetPortMatch = "unknown"
				return
			}
			podInfos[idx].ListeningSockets = socks
			if expectPort <= 0 {
				podInfos[idx].TargetPortMatch = "unknown"
				return
			}
			matchedAll := false
			matchedLoopback := false
			for _, s := range socks {
				if s.Port == expectPort && strings.HasPrefix(s.Protocol, "TCP") {
					if s.Loopback {
						matchedLoopback = true
					} else {
						matchedAll = true
					}
				}
			}
			switch {
			case matchedAll:
				podInfos[idx].TargetPortMatch = "listening_all"
			case matchedLoopback:
				podInfos[idx].TargetPortMatch = "listening_loopback_only"
			default:
				podInfos[idx].TargetPortMatch = "not_listening"
			}
		}(i, p, resolvedPort)
	}
	wg.Wait()

	// Synthesize plain-language conclusion & single next step
	conclusion := ""
	nextStep := ""
	readyCnt := 0
	notListeningCnt := 0
	loopbackOnlyCnt := 0
	listeningAllCnt := 0
	var actualListenPorts []string
	listenSeen := make(map[int]bool)

	for _, pi := range podInfos {
		if pi.Ready {
			readyCnt++
		}
		switch pi.TargetPortMatch {
		case "not_listening":
			notListeningCnt++
		case "listening_loopback_only":
			loopbackOnlyCnt++
		case "listening_all":
			listeningAllCnt++
		}
		for _, s := range pi.ListeningSockets {
			if strings.HasPrefix(s.Protocol, "TCP") && !listenSeen[s.Port] {
				listenSeen[s.Port] = true
				actualListenPorts = append(actualListenPorts, fmt.Sprintf("%d(%s)", s.Port, s.LocalAddr))
			}
		}
	}

	switch {
	case len(svc.Spec.Selector) == 0:
		conclusion = "该 Service 未配置 Selector（手动维护 Endpoint / ExternalName 类型），需核对外部手工创建的 EndpointSlice。"
		nextStep = "核对集群内是否手工同步了对应的 Endpoints / EndpointSlice 对象。"
	case len(podInfos) == 0:
		conclusion = fmt.Sprintf("命名空间 %s 内没有任何 Pod 匹配该 Service 的 Selector（%s），因此后端为空。", ns, formatSelector(svc.Spec.Selector))
		nextStep = "检查工作负载是否已缩容为 0，或 Pod 模板 labels 与 Service selector 是否存在拼写不一致。"
	case readyCnt == 0:
		conclusion = fmt.Sprintf("Selector 匹配到 %d 个 Pod，但全部处于未就绪 (NotReady) 状态（首个原因：%s），K8s 未将其加入活跃后端。", len(podInfos), podInfos[0].NotReadyReason)
		nextStep = "优先排查 Pod 启动日志与 ReadinessProbe 就绪探针，恢复 Ready 后后端将自动补齐。"
	case loopbackOnlyCnt > 0:
		conclusion = fmt.Sprintf("实际观测到后端 Pod 仅在 127.0.0.1 / ::1 环回地址监听目标端口 %d，外部 Pod/ClusterIP 流量无法接入！", targetPortNum)
		nextStep = "将应用监听地址从 127.0.0.1 改为 0.0.0.0（或 ::）并重新发布。"
	case notListeningCnt > 0 && listeningAllCnt == 0:
		extra := ""
		if len(actualListenPorts) > 0 {
			extra = fmt.Sprintf("；容器内实际监听的 TCP 端口为：%s", strings.Join(actualListenPorts, ", "))
		} else {
			extra = "；容器内当前未观测到任何 TCP LISTEN 端口"
		}
		conclusion = fmt.Sprintf("实际观测到 %d 个 Ready 后端均未监听目标端口 %d/TCP%s。这解释了为何探测出现连接被拒绝 (Connection Refused)。", notListeningCnt, targetPortNum, extra)
		nextStep = fmt.Sprintf("核对 Service targetPort（当前 %d）与容器真实监听端口是否一致，或检查业务进程是否已启动该端口。", targetPortNum)
	default:
		conclusion = fmt.Sprintf("Selector 匹配 %d 个 Pod（Ready %d 个），且已观测到目标端口 %d/TCP 处于监听状态。", len(podInfos), readyCnt, targetPortNum)
		nextStep = "既然容器正常监听，若特定业务 Pod 仍访问失败，请使用「对比两个来源的路径与适用策略」排查 Calico/K8s NetworkPolicy 或发起 TraceFlow。"
	}

	writeJSON(w, http.StatusOK, model.ServiceInspectResponse{
		Namespace:      svc.Namespace,
		Name:           svc.Name,
		Type:           string(svc.Spec.Type),
		ClusterIP:      svc.Spec.ClusterIP,
		Selector:       svc.Spec.Selector,
		Ports:          portsOut,
		FocusPort:      focusPort,
		MatchingPods:   podInfos,
		EndpointSlices: slices,
		Conclusion:     conclusion,
		EvidenceSource: "实际观测到",
		NextStepHint:   nextStep,
	})
}

// handleCompareSources runs live probes from both the cluster Probe Pod and a chosen business Pod,
// placing their observed results side by side with config-inferred K8s & Calico Egress/Ingress policies.
func (r *Router) handleCompareSources(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var creq model.CompareSourcesRequest
	if err := json.NewDecoder(req.Body).Decode(&creq); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if creq.SrcNamespace == "" || creq.SrcPod == "" || creq.TargetNamespace == "" || creq.TargetService == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "srcNamespace, srcPod, targetNamespace, and targetService are required"})
		return
	}

	bizPod, _, ok := r.k8sStore.GetPod(creq.SrcNamespace, creq.SrcPod)
	if !ok || bizPod.Status.PodIP == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "business source pod not found or has no IP"})
		return
	}
	svc, slices, ok := r.k8sStore.GetService(creq.TargetNamespace, creq.TargetService)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "target service not found"})
		return
	}

	// Pick Service port & resolved backend port/Pod
	svcPort := creq.TargetPort
	backendPort := creq.TargetPort
	for _, sp := range svc.Spec.Ports {
		if svcPort == 0 && (sp.Protocol == "" || sp.Protocol == corev1.ProtocolTCP) {
			svcPort = int(sp.Port)
		}
		if int(sp.Port) == svcPort {
			if sp.TargetPort.IntVal > 0 {
				backendPort = int(sp.TargetPort.IntVal)
			} else {
				backendPort = int(sp.Port)
			}
			break
		}
	}

	var backendPod *corev1.Pod
	backendIP := ""
	backendNode := ""
	for _, sl := range slices {
		for _, sp := range sl.Ports {
			if sp.Port != nil && int(*sp.Port) == backendPort {
				backendPort = int(*sp.Port)
			}
		}
		for _, ep := range sl.Endpoints {
			if ep.Conditions.Ready != nil && !*ep.Conditions.Ready {
				continue
			}
			if len(ep.Addresses) > 0 {
				backendIP = ep.Addresses[0]
				if ep.NodeName != nil {
					backendNode = *ep.NodeName
				}
				if ep.TargetRef != nil && ep.TargetRef.Kind == "Pod" {
					if bp, _, found := r.k8sStore.GetPod(ep.TargetRef.Namespace, ep.TargetRef.Name); found {
						backendPod = bp
					}
				}
				break
			}
		}
		if backendIP != "" {
			break
		}
	}

	// Pick probe Pod (prefer same node as business Pod to isolate node differences, else any running probe)
	probes := r.k8sStore.ListComponentPods(r.namespace, "app.kubernetes.io/component", "probe")
	var probePod *corev1.Pod
	for _, p := range probes {
		if p.Status.Phase == corev1.PodRunning && p.Status.PodIP != "" {
			if probePod == nil || p.Spec.NodeName == bizPod.Spec.NodeName {
				probePod = p
			}
		}
	}

	cctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()

	runSource := func(role string, src *corev1.Pod) model.SourceProbeOutcome {
		out := model.SourceProbeOutcome{
			Role:         role,
			EvidenceKind: "实际观测到",
		}
		if src == nil {
			out.ClusterIPErr = "未找到可用探针 Pod"
			return out
		}
		out.PodNamespace = src.Namespace
		out.PodName = src.Name
		out.PodIP = src.Status.PodIP
		out.NodeName = src.Spec.NodeName
		out.PodLabels = src.Labels
		out.EgressPolicies = r.k8sStore.EvaluatePoliciesForDirection(src, "Egress")

		// Probe ClusterIP
		if svc.Spec.ClusterIP != "" && svc.Spec.ClusterIP != "None" && svcPort > 0 {
			hres, err := r.agents.RunTCPHandshake(cctx, src.Spec.NodeName, string(src.UID), src.Status.PodIP, src.Namespace+"/"+src.Name, model.TCPHandshakeRequest{
				SrcNamespace: src.Namespace,
				SrcPod:       src.Name,
				TargetHost:   svc.Spec.ClusterIP,
				TargetPort:   svcPort,
				Count:        1,
				TimeoutMs:    1800,
			})
			if err == nil && hres != nil {
				out.ClusterIPOK = hres.SuccessCount > 0
				out.ClusterIPLatMs = hres.AvgRTTMs
				if !out.ClusterIPOK && len(hres.Attempts) > 0 {
					_, fl := healthcheck.ClassifyProbeError(hres.Attempts[0].Error + " " + hres.Attempts[0].Result)
					if fl == "" {
						fl = hres.Attempts[0].Result
					}
					out.ClusterIPFail = fl
					out.ClusterIPErr = hres.Attempts[0].Error
				}
			} else if err != nil {
				_, out.ClusterIPFail = healthcheck.ClassifyProbeError(err.Error())
				out.ClusterIPErr = err.Error()
			}
		}

		// Probe direct backend Pod IP
		if backendIP != "" && backendPort > 0 {
			hres, err := r.agents.RunTCPHandshake(cctx, src.Spec.NodeName, string(src.UID), src.Status.PodIP, src.Namespace+"/"+src.Name, model.TCPHandshakeRequest{
				SrcNamespace: src.Namespace,
				SrcPod:       src.Name,
				TargetHost:   backendIP,
				TargetPort:   backendPort,
				Count:        1,
				TimeoutMs:    1800,
			})
			if err == nil && hres != nil {
				out.DirectPodOK = hres.SuccessCount > 0
				out.DirectPodLatMs = hres.AvgRTTMs
				if !out.DirectPodOK && len(hres.Attempts) > 0 {
					_, fl := healthcheck.ClassifyProbeError(hres.Attempts[0].Error + " " + hres.Attempts[0].Result)
					if fl == "" {
						fl = hres.Attempts[0].Result
					}
					out.DirectPodFail = fl
					out.DirectPodErr = hres.Attempts[0].Error
				}
			} else if err != nil {
				_, out.DirectPodFail = healthcheck.ClassifyProbeError(err.Error())
				out.DirectPodErr = err.Error()
			}
		}
		return out
	}

	var probeOut, bizOut model.SourceProbeOutcome
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		probeOut = runSource("probe", probePod)
	}()
	go func() {
		defer wg.Done()
		bizOut = runSource("business", bizPod)
	}()
	wg.Wait()

	targetIngressPols := r.k8sStore.EvaluatePoliciesForDirection(backendPod, "Ingress")

	// Synthesize comparison conclusion
	probeOK := probeOut.ClusterIPOK || probeOut.DirectPodOK
	bizOK := bizOut.ClusterIPOK || bizOut.DirectPodOK

	polDiff := fmt.Sprintf("按配置推算：业务 Pod 命中 %d 条出站策略，探针 Pod 命中 %d 条出站策略；目标后端 Pod 命中 %d 条入站策略。",
		len(bizOut.EgressPolicies), len(probeOut.EgressPolicies), len(targetIngressPols))

	conclusion := ""
	recNext := ""
	switch {
	case probeOK && !bizOK:
		if len(bizOut.EgressPolicies) > 0 || len(targetIngressPols) > 0 {
			conclusion = fmt.Sprintf("【实际观测到】巡检探针可达，但业务 Pod (%s/%s) 访问失败（%s）。【按配置推算】存在出站/入站 NetworkPolicy 差异，高度疑似策略阻断！",
				bizPod.Namespace, bizPod.Name, firstNonEmpty(bizOut.ClusterIPFail, bizOut.DirectPodFail, "不可达"))
			recNext = "对业务 Pod 发起 TraceFlow，实测确认报文是否在宿主机 cali* veth 的 Calico 策略链被丢弃。"
		} else {
			conclusion = fmt.Sprintf("【实际观测到】巡检探针可达，但业务 Pod (%s/%s) 失败；【按配置推算】未发现明显差异化 K8s/Calico 策略，建议核查节点路由、Namespace/Global 策略或业务 Pod 本地路由表。",
				bizPod.Namespace, bizPod.Name)
			recNext = "发起 TraceFlow 逐跳观测业务 Pod 发包的真实丢包位置。"
		}
	case !probeOK && !bizOK:
		conclusion = fmt.Sprintf("【实际观测到】巡检探针与业务 Pod 均无法连通 %s/%s:%d（%s），属于目标服务侧或公共网络问题，非单一来源策略隔离。",
			svc.Namespace, svc.Name, svcPort, firstNonEmpty(bizOut.DirectPodFail, bizOut.ClusterIPFail, probeOut.DirectPodFail, "失败"))
		recNext = "优先点击「核对目标端口与监听状态」确认后端进程监听与健康状态。"
	case bizOut.DirectPodOK && !bizOut.ClusterIPOK:
		conclusion = fmt.Sprintf("【实际观测到】业务 Pod 直连后端 %s:%d 成功，但访问 ClusterIP %s:%d 失败（%s）— 锁定 kube-proxy Service 转发异常。",
			backendIP, backendPort, svc.Spec.ClusterIP, svcPort, bizOut.ClusterIPFail)
		recNext = "点击「追踪这次 Service 转发」执行 TraceFlow 核查 iptables/IPVS DNAT 规则。"
	default:
		conclusion = fmt.Sprintf("【实际观测到】巡检探针与业务 Pod 当前均可正常连通 %s/%s:%d（业务 Pod ClusterIP 延迟 %.2f ms）。若为偶发问题，建议保存现场并观察重传/变更时间轴。",
			svc.Namespace, svc.Name, svcPort, bizOut.ClusterIPLatMs)
		recNext = "若刚发生过偶发超时，可前往「流量拓扑 → 看异常连接」或「故障现场」排查重传与周边事件。"
	}

	resp := model.CompareSourcesResponse{
		CheckedAt:         time.Now(),
		TargetService:     svc.Namespace + "/" + svc.Name,
		TargetClusterIP:   svc.Spec.ClusterIP,
		TargetPort:        svcPort,
		BackendIP:         backendIP,
		BackendNode:       backendNode,
		BackendPort:       backendPort,
		ProbeOutcome:      probeOut,
		BusinessOutcome:   bizOut,
		TargetIngressPols: targetIngressPols,
		PolicyDiffSummary: polDiff,
		Conclusion:        conclusion,
		RecommendedNext:   recNext,
	}
	if backendPod != nil {
		resp.BackendPod = backendPod.Namespace + "/" + backendPod.Name
		resp.BackendLabels = backendPod.Labels
	}
	writeJSON(w, http.StatusOK, resp)
}

func (r *Router) handleIncidents(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		list := r.incidents.List(req.Context())
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"total":     len(list),
			"incidents": list,
		})
	case http.MethodPost:
		var creq model.CreateIncidentRequest
		if err := json.NewDecoder(req.Body).Decode(&creq); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		snap := r.incidents.CreateManual(creq)
		writeJSON(w, http.StatusOK, snap)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (r *Router) handleIncidentRetest(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := req.URL.Query().Get("id")
	if id == "" {
		var body struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		id = body.ID
	}
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "incident id is required"})
		return
	}
	updated, err := r.incidents.Retest(req.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func formatSelector(sel map[string]string) string {
	parts := make([]string, 0, len(sel))
	for k, v := range sel {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, ", ")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
