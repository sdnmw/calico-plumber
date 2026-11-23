package k8s

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"calico-netdiag/pkg/model"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// Store maintains an in-memory synchronized cache of Kubernetes & Calico resources.
type Store struct {
	clients *Clients
	mu      sync.RWMutex

	updatedAt        time.Time
	k8sVersion       string
	nodes            map[string]*corev1.Node
	nodeByIP         map[string]*corev1.Node
	namespaces       []string
	pods             map[string]*corev1.Pod     // ns/name
	podsByIP         map[string]*corev1.Pod     // non-hostNetwork pod IP -> Pod
	podBriefs        map[string]model.PodBrief  // ns/name
	podBriefsByIP    map[string]model.PodBrief  // podIP -> brief
	services         map[string]*corev1.Service // ns/name
	serviceByIP      map[string]*corev1.Service // clusterIP -> Service
	endpointSlices   map[string][]*discoveryv1.EndpointSlice // svc ns/name -> slices
	deployments      []appsv1.Deployment
	statefulSets     []appsv1.StatefulSet
	daemonSets       []appsv1.DaemonSet
	replicaSetOwners map[string]ownerRef // ns/rsName -> Deployment owner
	k8sNetPols       []networkingv1.NetworkPolicy
	calicoNetPols    []RawCalicoPolicy

	installInfo    CalicoInstallInfo
	felixConfig    FelixConfigInfo
	bgpConfig      BGPConfigInfo
	rawBGPPeers    []RawBGPPeer
	ipamSnapshot   model.IPAMSnapshot
	ipLookup       map[string]IPLookupMeta
	kubeProxyCM    kubeProxyConfigMeta
}

type ownerRef struct {
	kind string
	name string
}

type kubeProxyConfigMeta struct {
	mode        model.KubeProxyMode
	clusterCIDR string
}

// NewStore creates a K8s/Calico state store.
func NewStore(clients *Clients) *Store {
	return &Store{
		clients:          clients,
		nodes:            make(map[string]*corev1.Node),
		nodeByIP:         make(map[string]*corev1.Node),
		pods:             make(map[string]*corev1.Pod),
		podsByIP:         make(map[string]*corev1.Pod),
		podBriefs:        make(map[string]model.PodBrief),
		podBriefsByIP:    make(map[string]model.PodBrief),
		services:         make(map[string]*corev1.Service),
		serviceByIP:      make(map[string]*corev1.Service),
		endpointSlices:   make(map[string][]*discoveryv1.EndpointSlice),
		replicaSetOwners: make(map[string]ownerRef),
		ipLookup:         make(map[string]IPLookupMeta),
	}
}

// Start performs an initial sync and runs periodic refreshes until ctx is cancelled.
func (s *Store) Start(ctx context.Context, interval time.Duration) error {
	if sv, err := s.clients.Typed.Discovery().ServerVersion(); err == nil {
		s.mu.Lock()
		s.k8sVersion = sv.GitVersion
		s.mu.Unlock()
	}
	if err := s.Refresh(ctx); err != nil {
		log.Printf("[k8s-store] initial refresh warning: %v", err)
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.Refresh(ctx); err != nil {
					log.Printf("[k8s-store] refresh error: %v", err)
				}
			}
		}
	}()
	return nil
}

// Refresh syncs all core Kubernetes and Calico resources into memory.
func (s *Store) Refresh(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	nodesList, err := s.clients.Typed.CoreV1().Nodes().List(cctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	nsList, _ := s.clients.Typed.CoreV1().Namespaces().List(cctx, metav1.ListOptions{})
	podsList, err := s.clients.Typed.CoreV1().Pods("").List(cctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list pods: %w", err)
	}
	svcList, _ := s.clients.Typed.CoreV1().Services("").List(cctx, metav1.ListOptions{})
	epSliceList, _ := s.clients.Typed.DiscoveryV1().EndpointSlices("").List(cctx, metav1.ListOptions{})
	deployList, _ := s.clients.Typed.AppsV1().Deployments("").List(cctx, metav1.ListOptions{})
	stsList, _ := s.clients.Typed.AppsV1().StatefulSets("").List(cctx, metav1.ListOptions{})
	dsList, _ := s.clients.Typed.AppsV1().DaemonSets("").List(cctx, metav1.ListOptions{})
	rsList, _ := s.clients.Typed.AppsV1().ReplicaSets("").List(cctx, metav1.ListOptions{})
	npList, _ := s.clients.Typed.NetworkingV1().NetworkPolicies("").List(cctx, metav1.ListOptions{})

	kpMeta := s.readKubeProxyConfigMap(cctx)

	// ReplicaSet -> Deployment mapping
	rsOwners := make(map[string]ownerRef)
	if rsList != nil {
		for _, rs := range rsList.Items {
			for _, ref := range rs.OwnerReferences {
				if ref.Kind == "Deployment" {
					rsOwners[rs.Namespace+"/"+rs.Name] = ownerRef{kind: "Deployment", name: ref.Name}
					break
				}
			}
		}
	}

	// Build nodes maps
	nodesMap := make(map[string]*corev1.Node, len(nodesList.Items))
	nodeByIP := make(map[string]*corev1.Node, len(nodesList.Items))
	for i := range nodesList.Items {
		n := &nodesList.Items[i]
		nodesMap[n.Name] = n
		if ip := NodeInternalIP(n); ip != "" {
			nodeByIP[ip] = n
		}
	}

	// Namespaces
	namespaces := make([]string, 0)
	if nsList != nil {
		for _, ns := range nsList.Items {
			namespaces = append(namespaces, ns.Name)
		}
		sort.Strings(namespaces)
	}

	// Pods
	podsMap := make(map[string]*corev1.Pod, len(podsList.Items))
	podsByIP := make(map[string]*corev1.Pod, len(podsList.Items))
	podBriefs := make(map[string]model.PodBrief, len(podsList.Items))
	podBriefsByIP := make(map[string]model.PodBrief, len(podsList.Items))

	for i := range podsList.Items {
		p := &podsList.Items[i]
		key := p.Namespace + "/" + p.Name
		podsMap[key] = p
		wkKind, wkName := resolvePodWorkload(p, rsOwners)
		brief := model.PodBrief{
			Namespace:    p.Namespace,
			Name:         p.Name,
			IP:           p.Status.PodIP,
			NodeName:     p.Spec.NodeName,
			HostIP:       p.Status.HostIP,
			Phase:        string(p.Status.Phase),
			Ready:        IsPodReady(p),
			HostNetwork:  p.Spec.HostNetwork,
			WorkloadKind: wkKind,
			WorkloadName: wkName,
			RestartCount: totalRestarts(p),
		}
		podBriefs[key] = brief
		if p.Status.PodIP != "" && !p.Spec.HostNetwork && p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed {
			podsByIP[p.Status.PodIP] = p
			podBriefsByIP[p.Status.PodIP] = brief
		}
	}

	// Services & EndpointSlices
	svcMap := make(map[string]*corev1.Service)
	svcByIP := make(map[string]*corev1.Service)
	if svcList != nil {
		for i := range svcList.Items {
			svc := &svcList.Items[i]
			svcMap[svc.Namespace+"/"+svc.Name] = svc
			if svc.Spec.ClusterIP != "" && svc.Spec.ClusterIP != "None" {
				svcByIP[svc.Spec.ClusterIP] = svc
			}
		}
	}
	epSlices := make(map[string][]*discoveryv1.EndpointSlice)
	if epSliceList != nil {
		for i := range epSliceList.Items {
			es := &epSliceList.Items[i]
			svcName := es.Labels[discoveryv1.LabelServiceName]
			if svcName != "" {
				key := es.Namespace + "/" + svcName
				epSlices[key] = append(epSlices[key], es)
			}
		}
	}

	// Calico CRDs (best effort — tolerate missing CRDs gracefully)
	rawPools := s.listIPPools(cctx)
	rawBlocks := s.listIPAMBlocks(cctx)
	rawAffinities := s.listBlockAffinities(cctx)
	installInfo := s.readInstallation(cctx)
	felixCfg := s.readFelixConfig(cctx)
	bgpCfg := s.readBGPConfig(cctx)
	bgpPeers := s.listBGPPeers(cctx)
	calicoPols := s.listCalicoPolicies(cctx)

	ipamSnap, ipLookup := BuildIPAMSnapshot(
		rawPools, rawBlocks, rawAffinities,
		podBriefsByIP, podBriefs,
		installInfo.BGPEnabled,
	)

	// Backfill IPPool & IPAMBlock info into pod briefs
	for key, brief := range podBriefs {
		if meta, ok := ipLookup[brief.IP]; ok {
			brief.IPPool = meta.PoolName
			brief.IPAMBlock = meta.BlockCIDR
			brief.Borrowed = meta.Borrowed
			podBriefs[key] = brief
			if !brief.HostNetwork && brief.IP != "" {
				podBriefsByIP[brief.IP] = brief
			}
		}
	}

	s.mu.Lock()
	s.updatedAt = time.Now()
	s.nodes = nodesMap
	s.nodeByIP = nodeByIP
	s.namespaces = namespaces
	s.pods = podsMap
	s.podsByIP = podsByIP
	s.podBriefs = podBriefs
	s.podBriefsByIP = podBriefsByIP
	s.services = svcMap
	s.serviceByIP = svcByIP
	s.endpointSlices = epSlices
	if deployList != nil {
		s.deployments = deployList.Items
	}
	if stsList != nil {
		s.statefulSets = stsList.Items
	}
	if dsList != nil {
		s.daemonSets = dsList.Items
	}
	s.replicaSetOwners = rsOwners
	if npList != nil {
		s.k8sNetPols = npList.Items
	}
	s.calicoNetPols = calicoPols
	s.installInfo = installInfo
	s.felixConfig = felixCfg
	s.bgpConfig = bgpCfg
	s.rawBGPPeers = bgpPeers
	s.ipamSnapshot = ipamSnap
	s.ipLookup = ipLookup
	s.kubeProxyCM = kpMeta
	s.mu.Unlock()

	return nil
}

func (s *Store) readKubeProxyConfigMap(ctx context.Context) kubeProxyConfigMeta {
	meta := kubeProxyConfigMeta{mode: model.KubeProxyUnknown}
	cm, err := s.clients.Typed.CoreV1().ConfigMaps("kube-system").Get(ctx, "kube-proxy", metav1.GetOptions{})
	if err != nil {
		return meta
	}
	raw := cm.Data["config.conf"]
	for _, line := range strings.Split(raw, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "mode:") {
			val := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trim, "mode:")), `"'`)
			switch strings.ToLower(val) {
			case "ipvs":
				meta.mode = model.KubeProxyIPVS
			case "nftables":
				meta.mode = model.KubeProxyNFTables
			case "iptables", "":
				meta.mode = model.KubeProxyIptables
			}
		}
		if strings.HasPrefix(trim, "clusterCIDR:") {
			meta.clusterCIDR = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trim, "clusterCIDR:")), `"'`)
		}
	}
	return meta
}

func (s *Store) listIPPools(ctx context.Context) []RawIPPool {
	list, err := s.clients.Dynamic.Resource(gvrIPPools).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	out := make([]RawIPPool, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, ParseIPPool(&list.Items[i]))
	}
	return out
}

func (s *Store) listIPAMBlocks(ctx context.Context) []RawIPAMBlock {
	list, err := s.clients.Dynamic.Resource(gvrIPAMBlocks).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	out := make([]RawIPAMBlock, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, ParseIPAMBlock(&list.Items[i]))
	}
	return out
}

func (s *Store) listBlockAffinities(ctx context.Context) []RawBlockAffinity {
	list, err := s.clients.Dynamic.Resource(gvrBlockAffinities).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	out := make([]RawBlockAffinity, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, ParseBlockAffinity(&list.Items[i]))
	}
	return out
}

func (s *Store) readInstallation(ctx context.Context) CalicoInstallInfo {
	def := CalicoInstallInfo{Variant: "Calico", LinuxDataplane: "Iptables", BGPEnabled: true, MTU: 1500}
	list, err := s.clients.Dynamic.Resource(gvrInstallations).List(ctx, metav1.ListOptions{})
	if err != nil || len(list.Items) == 0 {
		return def
	}
	return ParseInstallation(&list.Items[0])
}

func (s *Store) readFelixConfig(ctx context.Context) FelixConfigInfo {
	def := FelixConfigInfo{VXLANVNI: 4096, VXLANPort: 4789}
	list, err := s.clients.Dynamic.Resource(gvrFelixConfigs).List(ctx, metav1.ListOptions{})
	if err != nil || len(list.Items) == 0 {
		return def
	}
	for i := range list.Items {
		if list.Items[i].GetName() == "default" {
			return ParseFelixConfig(&list.Items[i])
		}
	}
	return ParseFelixConfig(&list.Items[0])
}

func (s *Store) readBGPConfig(ctx context.Context) BGPConfigInfo {
	def := BGPConfigInfo{ASNumber: 64512, NodeToNodeMesh: true}
	list, err := s.clients.Dynamic.Resource(gvrBGPConfigs).List(ctx, metav1.ListOptions{})
	if err != nil || len(list.Items) == 0 {
		return def
	}
	for i := range list.Items {
		if list.Items[i].GetName() == "default" {
			return ParseBGPConfig(&list.Items[i])
		}
	}
	return ParseBGPConfig(&list.Items[0])
}

func (s *Store) listBGPPeers(ctx context.Context) []RawBGPPeer {
	list, err := s.clients.Dynamic.Resource(gvrBGPPeers).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	out := make([]RawBGPPeer, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, ParseBGPPeer(&list.Items[i]))
	}
	return out
}

func (s *Store) listCalicoPolicies(ctx context.Context) []RawCalicoPolicy {
	var out []RawCalicoPolicy
	if list, err := s.clients.Dynamic.Resource(gvrCalicoNetPols).List(ctx, metav1.ListOptions{}); err == nil {
		for i := range list.Items {
			out = append(out, ParseCalicoPolicy(&list.Items[i], false))
		}
	}
	if list, err := s.clients.Dynamic.Resource(gvrCalicoGlobalNetPols).List(ctx, metav1.ListOptions{}); err == nil {
		for i := range list.Items {
			out = append(out, ParseCalicoPolicy(&list.Items[i], true))
		}
	}
	return out
}

// GetIPAMSnapshot returns the current IPAM pools and blocks snapshot.
func (s *Store) GetIPAMSnapshot() model.IPAMSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ipamSnapshot
}

// GetNamespaces returns sorted cluster namespaces.
func (s *Store) GetNamespaces() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.namespaces))
	copy(out, s.namespaces)
	return out
}

// GetPod returns a Pod and its enriched brief.
func (s *Store) GetPod(namespace, name string) (*corev1.Pod, model.PodBrief, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := namespace + "/" + name
	p, ok := s.pods[key]
	if !ok {
		return nil, model.PodBrief{}, false
	}
	return p, s.podBriefs[key], true
}

// GetPodByIP looks up a non-hostNetwork Pod by Pod IP.
func (s *Store) GetPodByIP(ip string) (*corev1.Pod, model.PodBrief, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.podsByIP[ip]
	if !ok {
		return nil, model.PodBrief{}, false
	}
	return p, s.podBriefsByIP[ip], true
}

// GetService returns a Service and its EndpointSlices.
func (s *Store) GetService(namespace, name string) (*corev1.Service, []*discoveryv1.EndpointSlice, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := namespace + "/" + name
	svc, ok := s.services[key]
	if !ok {
		return nil, nil, false
	}
	return svc, s.endpointSlices[key], true
}

// GetServiceByIP looks up a Service by ClusterIP.
func (s *Store) GetServiceByIP(ip string) (*corev1.Service, []*discoveryv1.EndpointSlice, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	svc, ok := s.serviceByIP[ip]
	if !ok {
		return nil, nil, false
	}
	return svc, s.endpointSlices[svc.Namespace+"/"+svc.Name], true
}

// ListAllServices returns all Services and their EndpointSlices.
func (s *Store) ListAllServices() ([]*corev1.Service, map[string][]*discoveryv1.EndpointSlice) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	svcs := make([]*corev1.Service, 0, len(s.services))
	for _, svc := range s.services {
		svcs = append(svcs, svc)
	}
	sort.Slice(svcs, func(i, j int) bool {
		if svcs[i].Namespace != svcs[j].Namespace {
			return svcs[i].Namespace < svcs[j].Namespace
		}
		return svcs[i].Name < svcs[j].Name
	})
	eps := make(map[string][]*discoveryv1.EndpointSlice, len(s.endpointSlices))
	for k, v := range s.endpointSlices {
		eps[k] = v
	}
	return svcs, eps
}

// ListNodes returns all K8s Nodes sorted by name.
func (s *Store) ListNodes() []*corev1.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*corev1.Node, 0, len(s.nodes))
	for _, n := range s.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// GetNode returns a Node by name.
func (s *Store) GetNode(name string) (*corev1.Node, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.nodes[name]
	return n, ok
}

// ListPods returns all Pod briefs, optionally filtered by namespace.
func (s *Store) ListPods(namespace string) []model.PodBrief {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.PodBrief, 0, len(s.podBriefs))
	for _, b := range s.podBriefs {
		if namespace != "" && b.Namespace != namespace {
			continue
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ListComponentPods finds pods matching a label selector in a namespace (e.g. agent / probe pods).
func (s *Store) ListComponentPods(namespace, labelKey, labelVal string) []*corev1.Pod {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*corev1.Pod
	for _, p := range s.pods {
		if p.Namespace == namespace && p.Labels[labelKey] == labelVal {
			out = append(out, p)
		}
	}
	return out
}

// GetClusterMetadata returns Calico, kube-proxy, and version overview data.
func (s *Store) GetClusterMetadata() (
	k8sVer string,
	inst CalicoInstallInfo,
	felix FelixConfigInfo,
	bgp BGPConfigInfo,
	peers []RawBGPPeer,
	kpMode model.KubeProxyMode,
	clusterCIDR string,
	podCnt, svcCnt, nsCnt, polCnt int,
) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	mode := s.kubeProxyCM.mode
	if s.felixConfig.BPFEnabled {
		mode = model.KubeProxyEBPF
	}
	return s.k8sVersion, s.installInfo, s.felixConfig, s.bgpConfig, s.rawBGPPeers,
		mode, s.kubeProxyCM.clusterCIDR,
		len(s.pods), len(s.services), len(s.namespaces), len(s.k8sNetPols) + len(s.calicoNetPols)
}

// PrimaryEncapMode returns the primary Calico encapsulation mode for the cluster.
func (s *Store) PrimaryEncapMode() model.CalicoEncapMode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.ipamSnapshot.Pools {
		if !p.Disabled {
			return p.EncapMode
		}
	}
	return model.EncapBGPDirect
}

// ResolveIP enriches an IP address into a Kubernetes EndpointIdentity.
func (s *Store) ResolveIP(ip string, port uint16) model.EndpointIdentity {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if brief, ok := s.podBriefsByIP[ip]; ok {
		return model.EndpointIdentity{
			IP:           ip,
			Port:         port,
			Kind:         "Pod",
			Namespace:    brief.Namespace,
			PodName:      brief.Name,
			WorkloadKind: brief.WorkloadKind,
			WorkloadName: brief.WorkloadName,
			NodeName:     brief.NodeName,
			DisplayName:  brief.Namespace + "/" + brief.Name,
		}
	}
	if svc, ok := s.serviceByIP[ip]; ok {
		return model.EndpointIdentity{
			IP:          ip,
			Port:        port,
			Kind:        "Service",
			Namespace:   svc.Namespace,
			ServiceName: svc.Name,
			DisplayName: fmt.Sprintf("svc/%s/%s", svc.Namespace, svc.Name),
		}
	}
	if node, ok := s.nodeByIP[ip]; ok {
		return model.EndpointIdentity{
			IP:          ip,
			Port:        port,
			Kind:        "Node",
			NodeName:    node.Name,
			DisplayName: "node/" + node.Name,
		}
	}
	return model.EndpointIdentity{
		IP:          ip,
		Port:        port,
		Kind:        "External",
		DisplayName: ip,
	}
}

// GetPodServicesAndPolicies returns Services selecting a Pod and NetworkPolicies applying to it.
func (s *Store) GetPodServicesAndPolicies(namespace, name string) ([]model.PodServiceRef, []model.MatchedPolicySummary) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pod, ok := s.pods[namespace+"/"+name]
	if !ok {
		return nil, nil
	}

	var svcRefs []model.PodServiceRef
	podLabels := labels.Set(pod.Labels)
	for _, svc := range s.services {
		if svc.Namespace != namespace || len(svc.Spec.Selector) == 0 {
			continue
		}
		sel := labels.SelectorFromSet(svc.Spec.Selector)
		if sel.Matches(podLabels) {
			ports := make([]string, 0, len(svc.Spec.Ports))
			for _, p := range svc.Spec.Ports {
				ports = append(ports, fmt.Sprintf("%d→%s/%s", p.Port, p.TargetPort.String(), p.Protocol))
			}
			svcRefs = append(svcRefs, model.PodServiceRef{
				Namespace: svc.Namespace,
				Name:      svc.Name,
				Type:      string(svc.Spec.Type),
				ClusterIP: svc.Spec.ClusterIP,
				Ports:     ports,
				Ready:     IsPodReady(pod),
			})
		}
	}

	var matched []model.MatchedPolicySummary
	for _, np := range s.k8sNetPols {
		if np.Namespace != namespace {
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(&np.Spec.PodSelector)
		if err != nil || !sel.Matches(podLabels) {
			continue
		}
		types := make([]string, 0, len(np.Spec.PolicyTypes))
		for _, pt := range np.Spec.PolicyTypes {
			types = append(types, string(pt))
		}
		if len(types) == 0 {
			types = []string{"Ingress"}
		}
		matched = append(matched, model.MatchedPolicySummary{
			Source:      "K8s NetworkPolicy",
			Namespace:   np.Namespace,
			Name:        np.Name,
			PolicyTypes: types,
			IngressCnt:  len(np.Spec.Ingress),
			EgressCnt:   len(np.Spec.Egress),
			Summary:     fmt.Sprintf("Ingress rules: %d, Egress rules: %d", len(np.Spec.Ingress), len(np.Spec.Egress)),
		})
	}
	for _, cp := range s.calicoNetPols {
		if !cp.Global && cp.Namespace != namespace {
			continue
		}
		if calicoSelectorMatches(cp.Selector, pod.Labels) {
			src := "Calico NetworkPolicy"
			if cp.Global {
				src = "Calico GlobalNetworkPolicy"
			}
			matched = append(matched, model.MatchedPolicySummary{
				Source:      src,
				Namespace:   cp.Namespace,
				Name:        cp.Name,
				Tier:        cp.Tier,
				Order:       cp.Order,
				PolicyTypes: cp.PolicyTypes,
				IngressCnt:  cp.IngressCnt,
				EgressCnt:   cp.EgressCnt,
				Summary:     fmt.Sprintf("Tier=%s Selector=%q (In:%d Out:%d)", cp.Tier, cp.Selector, cp.IngressCnt, cp.EgressCnt),
			})
		}
	}

	return svcRefs, matched
}

// calicoSelectorMatches handles common Calico selector expressions (all(), empty, k == 'v').
func calicoSelectorMatches(sel string, podLabels map[string]string) bool {
	sel = strings.TrimSpace(sel)
	if sel == "" || sel == "all()" {
		return true
	}
	// Simple k == 'v' evaluation (handles && clauses)
	parts := strings.Split(sel, "&&")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "==") {
			kv := strings.SplitN(part, "==", 2)
			k := strings.TrimSpace(kv[0])
			v := strings.Trim(strings.TrimSpace(kv[1]), `"'`)
			if podLabels[k] != v {
				return false
			}
		}
	}
	return true
}

// GetIPMeta returns IPPool/IPAMBlock metadata for a given IP.
func (s *Store) GetIPMeta(ip string) (IPLookupMeta, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.ipLookup[ip]
	return m, ok
}

// Helper functions
func NodeInternalIP(n *corev1.Node) string {
	for _, a := range n.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			return a.Address
		}
	}
	return ""
}

func NodeRoles(n *corev1.Node) []string {
	var roles []string
	for k := range n.Labels {
		if strings.HasPrefix(k, "node-role.kubernetes.io/") {
			r := strings.TrimPrefix(k, "node-role.kubernetes.io/")
			if r != "" {
				roles = append(roles, r)
			}
		}
	}
	if len(roles) == 0 {
		roles = []string{"worker"}
	}
	sort.Strings(roles)
	return roles
}

func IsNodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func IsPodReady(p *corev1.Pod) bool {
	if p.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func totalRestarts(p *corev1.Pod) int32 {
	var sum int32
	for _, cs := range p.Status.ContainerStatuses {
		sum += cs.RestartCount
	}
	return sum
}

func resolvePodWorkload(p *corev1.Pod, rsOwners map[string]ownerRef) (string, string) {
	for _, ref := range p.OwnerReferences {
		if ref.Kind == "ReplicaSet" {
			if owner, ok := rsOwners[p.Namespace+"/"+ref.Name]; ok {
				return owner.kind, owner.name
			}
			return "ReplicaSet", ref.Name
		}
		return ref.Kind, ref.Name
	}
	return "Pod", p.Name
}
