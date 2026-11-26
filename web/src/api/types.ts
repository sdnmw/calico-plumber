export interface ClusterNetworkOverview {
  updatedAt: string;
  k8sVersion: string;
  calicoVersion: string;
  calicoVariant: string;
  primaryEncapMode: string;
  linuxDataplane: string;
  bgpEnabled: boolean;
  bgpAsNumber?: number;
  nodeToNodeMesh: boolean;
  kubeProxyMode: string;
  clusterCIDR?: string;
  serviceCIDR?: string;
  mtu: number;
  vxlanVni?: number;
  vxlanPort?: number;
  podCount: number;
  serviceCount: number;
  namespaceCount: number;
  networkPolicyCount: number;
  ipPools: IPPoolSummary[];
  nodes: NodeNetworkStatus[];
  bgpPeers: BGPPeerSummary[];
  globalTraffic: TrafficSummaryStats;
}

export interface NodeNetworkStatus {
  name: string;
  roles: string[];
  ready: boolean;
  internalIP: string;
  osImage: string;
  kernelVersion: string;
  containerRuntime: string;
  architecture: string;
  podCIDR?: string;
  ipipTunnelIP?: string;
  vxlanTunnelIP?: string;
  vxlanTunnelMAC?: string;
  primaryNIC?: string;
  nicMTU?: number;
  tunnelInterfaces?: TunnelIfaceInfo[];
  podCount: number;
  blockCount: number;
  allocatedIPs: number;
  agentReady: boolean;
  agentPodIP?: string;
  probePodIP?: string;
  kubeProxyMode: string;
  telemetryEngine: 'ebpf' | 'conntrack' | string;
  telemetryReason?: string;
  btfAvailable: boolean;
  conntrackAcct: boolean;
  bgpProtocols?: BIRDProtocol[];
  activeFlowsCount: number;
  lastHeartbeat?: string;
}

export interface TunnelIfaceInfo {
  name: string;
  type: string;
  ip?: string;
  mac?: string;
  mtu: number;
  state: string;
}

export interface BIRDProtocol {
  name: string;
  proto: string;
  table: string;
  state: string;
  since: string;
  info: string;
  peerIP?: string;
  node?: string;
}

export interface BGPPeerSummary {
  name: string;
  sourceNode: string;
  peerIP: string;
  peerNode?: string;
  asNumber?: number;
  scope: string;
  state: string;
  since?: string;
}

export interface TrafficSummaryStats {
  activeFlows: number;
  totalBytesPerSec: number;
  totalPktsPerSec: number;
  avgLatencyMs: number;
  retransmitsPerMin: number;
  ebpfNodes: number;
  conntrackNodes: number;
}

export interface IPPoolSummary {
  name: string;
  cidr: string;
  blockSize: number;
  ipipMode: string;
  vxlanMode: string;
  encapMode: string;
  natOutgoing: boolean;
  disabled: boolean;
  disableBGPExport: boolean;
  nodeSelector: string;
  allowedUses?: string[];
  totalIPs: number;
  totalBlocks: number;
  allocatedBlocks: number;
  allocatedIPs: number;
  activePodIPs: number;
  tunnelIPs: number;
  borrowedIPs: number;
  orphanIPs: number;
  utilizationPercent: number;
}

export interface IPSlotDetail {
  index: number;
  ip: string;
  status: 'free' | 'pod' | 'tunnel' | 'borrowed' | 'orphan';
  handleId?: string;
  namespace?: string;
  podName?: string;
  podNode?: string;
  workloadKind?: string;
  workloadName?: string;
  podPhase?: string;
  allocatedAt?: string;
}

export interface IPAMBlockDetail {
  name: string;
  cidr: string;
  poolName: string;
  affinityNode: string;
  affinityState: string;
  strictAffinity: boolean;
  totalSlots: number;
  allocatedSlots: number;
  borrowedSlots: number;
  orphanSlots: number;
  utilizationPercent: number;
  slots: IPSlotDetail[];
}

export interface IPAMSnapshot {
  updatedAt: string;
  pools: IPPoolSummary[];
  blocks: IPAMBlockDetail[];
}

export interface PodBrief {
  namespace: string;
  name: string;
  ip: string;
  nodeName: string;
  hostIP: string;
  phase: string;
  ready: boolean;
  hostNetwork: boolean;
  ipPool?: string;
  ipamBlock?: string;
  borrowed?: boolean;
  workloadKind?: string;
  workloadName?: string;
  restartCount?: number;
}

export interface ServiceBrief {
  namespace: string;
  name: string;
  type: string;
  clusterIP: string;
  ports: number[];
  portDesc: string[];
}

export interface SearchResultItem {
  kind: string;
  name: string;
  namespace?: string;
  ip?: string;
  clusterIP?: string;
  nodeName?: string;
  hostIP?: string;
  ipPool?: string;
  ipamBlock?: string;
  borrowed?: boolean;
  status?: string;
  workloadKind?: string;
  workloadName?: string;
  ports?: string[];
  labels?: Record<string, string>;
  podCount?: number;
  readyPods?: number;
  pods?: PodBrief[];
}

export interface PodNetworkDetail {
  pod: PodBrief;
  uid: string;
  createdAt: string;
  labels?: Record<string, string>;
  containers?: string[];
  encapMode: string;
  hostVeth?: string;
  podIface?: string;
  podMAC?: string;
  podMTU?: number;
  podRoutes?: { dst: string; gateway?: string; dev: string; protocol?: string; src?: string }[];
  hostRouteToPod?: string;
  services?: { namespace: string; name: string; type: string; clusterIP: string; ports: string[]; ready: boolean }[];
  networkPolicies?: {
    source: string;
    namespace?: string;
    name: string;
    tier?: string;
    policyTypes: string[];
    ingressRuleCount: number;
    egressRuleCount: number;
    summary: string;
  }[];
  dataplaneError?: string;
}

export interface EndpointIdentity {
  ip: string;
  port?: number;
  kind: string;
  namespace?: string;
  podName?: string;
  workloadKind?: string;
  workloadName?: string;
  serviceName?: string;
  nodeName?: string;
  displayName: string;
}

export interface EnrichedFlow {
  id: string;
  src: EndpointIdentity;
  dst: EndpointIdentity;
  viaService?: EndpointIdentity;
  protocol: string;
  dstPort: number;
  srcPort: number;
  bytesTotal: number;
  txBytes: number;
  rxBytes: number;
  packetsTotal: number;
  bytesPerSec: number;
  packetsPerSec: number;
  latencyAvgMs: number;
  latencyP95Ms: number;
  latencyMaxMs: number;
  retransmits: number;
  resets: number;
  connections: number;
  state?: string;
  crossNode: boolean;
  engine: string;
  health: 'healthy' | 'warning' | 'critical';
  lastSeen: string;
  windowSec?: number;
  prevRetransmits?: number;
  retransmitDeltaPct?: number;
  baselineAvailable?: boolean;
  retransmitPerMB?: number;
  estRetransmitRatePct?: number;
  rateNote?: string;
  anomalyScore?: number;
  anomalyReasons?: string[];
}

export interface TopologyNode {
  id: string;
  label: string;
  kind: string;
  namespace?: string;
  nodeName?: string;
  ip?: string;
  podCount?: number;
  ports?: string[];
  inboundBps: number;
  outboundBps: number;
  avgLatencyMs: number;
  retransmits: number;
  health: 'healthy' | 'warning' | 'critical';
}

export interface TopologyEdge {
  id: string;
  source: string;
  target: string;
  protocols: string[];
  dstPorts: number[];
  primaryLabel: string;
  bytesTotal: number;
  bytesPerSec: number;
  packetsPerSec: number;
  latencyAvgMs: number;
  latencyP95Ms: number;
  latencyMaxMs: number;
  retransmits: number;
  resets: number;
  flowCount: number;
  crossNode: boolean;
  health: 'healthy' | 'warning' | 'critical';
  windowSec?: number;
  prevRetransmits?: number;
  retransmitDeltaPct?: number;
  baselineAvailable?: boolean;
  retransmitPerMB?: number;
  estRetransmitRatePct?: number;
  rateNote?: string;
  anomalyScore?: number;
  anomalyReasons?: string[];
  fiveTuples: EnrichedFlow[];
}

export interface TopologyGraph {
  generatedAt: string;
  granularity: string;
  windowSec: number;
  namespaces: string[];
  nodes: TopologyNode[];
  edges: TopologyEdge[];
  summary: {
    nodeCount: number;
    edgeCount: number;
    totalFlows: number;
    totalBytesPerSec: number;
    avgLatencyMs: number;
    warningEdges: number;
    criticalEdges: number;
  };
}

export interface TraceFlowRequest {
  srcNamespace: string;
  srcPod: string;
  targetType: 'pod' | 'service' | 'node' | 'ip';
  dstNamespace?: string;
  dstName?: string;
  dstIP?: string;
  protocol: 'TCP' | 'UDP' | 'ICMP';
  dstPort?: number;
  timeoutMs?: number;
}

export interface TraceHop {
  index: number;
  lane: string;
  nodeName?: string;
  component: string;
  title: string;
  subtitle?: string;
  interface?: string;
  status: string;
  observed: boolean;
  timestampUs?: number;
  deltaMs?: number;
  packetSummary?: string;
  outerHeader?: string;
  ruleDetail?: string;
}

export interface TraceObservation {
  seq: number;
  direction: string;
  type: string;
  nodeName: string;
  component: string;
  interface?: string;
  detail: string;
  timestampMs: number;
}

export interface TraceFlowResult {
  id: string;
  startedAt: string;
  completedAt: string;
  totalLatencyMs: number;
  status: 'REACHABLE' | 'DROPPED' | 'RESET' | 'TIMEOUT' | 'ERROR';
  verdictSummary: string;
  rootCauseHint?: string;
  encapMode: string;
  crossNode: boolean;
  kubeProxyMode: string;
  source: EndpointIdentity;
  destination: EndpointIdentity;
  resolvedBackend?: EndpointIdentity;
  protocol: string;
  srcPort?: number;
  dstPort?: number;
  traceSignature: string;
  forwardHops: TraceHop[];
  returnHops?: TraceHop[];
  observations: TraceObservation[];
}

export interface PacketFrame {
  number: number;
  timestamp: string;
  relTimeMs: number;
  interface: string;
  srcIP: string;
  srcPort?: number;
  dstIP: string;
  dstPort?: number;
  protocol: string;
  length: number;
  tcpFlags?: string;
  info: string;
  hexDump?: string;
  captureId?: string;
}

export interface TCPDumpControlMessage {
  type: 'started' | 'packet' | 'stats' | 'done' | 'error';
  captureId?: string;
  interface?: string;
  message?: string;
  packetCount?: number;
  pcapReady?: boolean;
  frame?: PacketFrame;
}

export interface TCPHandshakeResponse {
  srcPod: string;
  srcIP: string;
  targetHost: string;
  resolvedIP: string;
  targetPort: number;
  successCount: number;
  totalCount: number;
  minRttMs: number;
  avgRttMs: number;
  maxRttMs: number;
  diagnosis: string;
  attempts: {
    round: number;
    success: boolean;
    result: string;
    resolvedIP?: string;
    dnsLookupMs: number;
    synAckRttMs: number;
    totalMs: number;
    error?: string;
    steps: {
      step: string;
      direction: string;
      timestampMs: number;
      deltaMs: number;
      seq?: number;
      ack?: number;
      window?: number;
      detail: string;
    }[];
  }[];
}

export interface HTTPProbeResponse {
  srcPod: string;
  srcIP: string;
  url: string;
  method: string;
  success: boolean;
  statusCode: number;
  statusText?: string;
  protocol?: string;
  resolvedIP?: string;
  remoteAddr?: string;
  dnsLookupMs: number;
  tcpConnectMs: number;
  tlsHandshakeMs: number;
  serverWaitTtfbMs: number;
  contentTransferMs: number;
  totalMs: number;
  tlsVersion?: string;
  tlsCipher?: string;
  tlsCertSubject?: string;
  tlsCertExpiry?: string;
  responseHeaders?: Record<string, string[]>;
  bodyPreview?: string;
  bodyBytes: number;
  error?: string;
  diagnosis: string;
}

export interface HealthSummaryHeadline {
  nodeCount: number;
  crossNodeHealthy: number;
  crossNodeTotal: number;
  crossNodeConnected: boolean;
  totalServices: number;
  brokenServices: number;
  degradedServices: number;
  noEndpointServices: number;
  needsAttention: boolean;
  headlineText: string;
}

export interface BackendEndpointHealth {
  podNamespace?: string;
  podName?: string;
  podIP: string;
  nodeName?: string;
  servicePort?: number;
  targetPort: number;
  protocol: string;
  k8sReady: boolean;
  probeState?: 'tested' | 'untested';
  probeSource?: string;
  probeOk: boolean;
  latencyMs: number;
  failureKind?: string;
  failureLabel?: string;
  error?: string;
}

export interface ServicePortHealth {
  portName?: string;
  servicePort: number;
  protocol: string;
  targetPort: string;
  resolvedTargetPort: number;
  probeSource: string;
  probeSourcePod?: string;
  probeSourceNode?: string;
  probeState: 'tested' | 'untested';
  untestedReason?: string;
  clusterIPState: 'ok' | 'failed' | 'untested' | 'headless';
  clusterIPLatencyMs?: number;
  clusterIPFailure?: string;
  clusterIPError?: string;
  totalBackends: number;
  readyBackends: number;
  testedBackends: number;
  reachableBackends: number;
  failureKind?: string;
  failureSummary: string;
  status: 'healthy' | 'degraded' | 'broken' | 'no-endpoints' | 'untested';
  diagnosis: string;
  recommendedAction: 'inspect_empty_backends' | 'check_port_listening' | 'trace_service_dnat' | 'compare_policy_sources' | 'trace_normal';
  backends: BackendEndpointHealth[];
}

export interface ServiceHealthItem {
  namespace: string;
  name: string;
  type: string;
  clusterIP: string;
  ports: string[];
  selector?: Record<string, string>;
  probeSource?: string;
  probeSourcePod?: string;
  probeSourceNode?: string;
  status: 'healthy' | 'degraded' | 'broken' | 'no-endpoints' | 'headless' | 'untested';
  totalBackends: number;
  readyBackends: number;
  reachableBackends: number;
  clusterIPReachable?: boolean;
  clusterIPLatencyMs?: number;
  clusterIPError?: string;
  diagnosis: string;
  recommendedAction?: 'inspect_empty_backends' | 'check_port_listening' | 'trace_service_dnat' | 'compare_policy_sources' | 'trace_normal';
  portChecks?: ServicePortHealth[];
  backends: BackendEndpointHealth[];
}

export interface GlobalHealthReport {
  checkedAt: string;
  durationMs: number;
  overallScore: number;
  overallStatus: 'healthy' | 'degraded' | 'critical';
  summaryHeadline?: HealthSummaryHeadline;
  podToPod: {
    totalPairs: number;
    healthyPairs: number;
    failedPairs: number;
    crossNodeTotal?: number;
    crossNodeHealthy?: number;
    crossNodeFailed?: number;
    selfCheckTotal?: number;
    selfCheckHealthy?: number;
    avgLatencyMs: number;
    maxLatencyMs: number;
    nodes: string[];
    cells: {
      srcNode: string;
      srcPod: string;
      srcIP: string;
      dstNode: string;
      dstPod: string;
      dstIP: string;
      sameNode: boolean;
      selfCheck?: boolean;
      reachable: boolean;
      latencyMs: number;
      error?: string;
    }[];
  };
  podToNode: {
    totalChecks: number;
    healthyChecks: number;
    avgLatencyMs: number;
    checks: {
      srcNode: string;
      srcPod: string;
      dstNode: string;
      dstNodeIP: string;
      targetPort: number;
      reachable: boolean;
      latencyMs: number;
      error?: string;
    }[];
  };
  podToService: {
    totalChecks: number;
    healthyChecks: number;
    dnsAvgMs: number;
    checks: {
      srcNode: string;
      srcPod: string;
      targetType: string;
      targetName: string;
      targetAddr: string;
      reachable: boolean;
      latencyMs: number;
      detail?: string;
      error?: string;
    }[];
  };
  serviceBackends: {
    totalServices: number;
    healthyServices: number;
    degradedServices: number;
    brokenServices: number;
    noEndpointServices: number;
    services: ServiceHealthItem[];
  };
  infrastructure: {
    calicoNodesReady: number;
    calicoNodesTotal: number;
    bgpEstablished: number;
    bgpTotal: number;
    mtuConsistent: boolean;
    maxPoolUsagePct: number;
    orphanIPCount: number;
    borrowedIPCount: number;
    checks: { category: string; name: string; status: 'pass' | 'warn' | 'fail'; detail: string }[];
  };
}

// ===== Guided Service Inspection, Dual-Source Policy Comparison & Incidents =====

export interface ListeningSocket {
  protocol: string;
  localAddr: string;
  port: number;
  loopback: boolean;
}

export interface ServiceInspectResponse {
  namespace: string;
  name: string;
  type: string;
  clusterIP: string;
  selector?: Record<string, string>;
  ports: { name?: string; port: number; targetPort: string; protocol: string }[];
  focusPort?: number;
  matchingPods: {
    namespace: string;
    name: string;
    ip: string;
    nodeName: string;
    phase: string;
    ready: boolean;
    notReadyReason?: string;
    restartCount: number;
    containerPorts?: string[];
    listeningSockets?: ListeningSocket[];
    socketInspectErr?: string;
    targetPortMatch?: 'listening_all' | 'listening_loopback_only' | 'not_listening' | 'unknown';
  }[];
  endpointSlices: {
    name: string;
    addressType: string;
    ports: string[];
    endpoints: {
      addresses: string[];
      ready?: boolean;
      serving?: boolean;
      terminating?: boolean;
      targetPod?: string;
      nodeName?: string;
    }[];
  }[];
  conclusion: string;
  evidenceSource: string;
  nextStepHint: string;
}

export interface PolicyAttributionItem {
  source: string;
  namespace?: string;
  name: string;
  tier?: string;
  order?: number;
  direction: 'Egress' | 'Ingress';
  selector: string;
  ruleSummaries: string[];
  effectHint: 'allow_likely' | 'restrict_possible' | 'default_deny';
  evidenceKind: string; // "按配置推算"
}

export interface SourceProbeOutcome {
  role: 'probe' | 'business';
  podNamespace: string;
  podName: string;
  podIP: string;
  nodeName: string;
  podLabels?: Record<string, string>;
  clusterIPOk: boolean;
  clusterIPLatencyMs: number;
  clusterIPFailure?: string;
  clusterIPError?: string;
  directPodOk: boolean;
  directPodLatencyMs: number;
  directPodFailure?: string;
  directPodError?: string;
  evidenceKind: string; // "实际观测到"
  egressPolicies?: PolicyAttributionItem[];
}

export interface CompareSourcesResponse {
  checkedAt: string;
  targetService: string;
  targetClusterIP: string;
  targetPort: number;
  backendPod?: string;
  backendIP?: string;
  backendNode?: string;
  backendPort?: number;
  backendLabels?: Record<string, string>;
  probeOutcome: SourceProbeOutcome;
  businessOutcome: SourceProbeOutcome;
  targetIngressPolicies?: PolicyAttributionItem[];
  policyDiffSummary: string;
  conclusion: string;
  recommendedNext: string;
}

export interface ClusterChangeEvent {
  timestamp: string;
  category: 'pod_rebuild' | 'endpoint_change' | 'policy_change' | 'bgp_change' | 'k8s_event';
  severity: 'info' | 'warning' | 'critical';
  namespace?: string;
  object: string;
  summary: string;
}

export interface IncidentRetestRecord {
  testedAt: string;
  probeSource: string;
  clusterIPOk: boolean;
  clusterIPLatencyMs?: number;
  clusterIPFailure?: string;
  directBackendOk: boolean;
  directLatencyMs?: number;
  directFailure?: string;
  currentBackends?: string[];
  verdict: 'recovered' | 'still_failing' | 'partial_improved';
  verdictText: string;
  diff: {
    statusBefore: string;
    statusAfter: string;
    failureBefore: string;
    failureAfter: string;
    latencyBefore: number;
    latencyAfter: number;
    backendsAdded?: string[];
    backendsRemoved?: string[];
    changesSummary: string[];
  };
}

export interface IncidentSnapshot {
  id: string;
  key: string;
  title: string;
  kind: 'service_port' | 'flow_anomaly' | 'traceflow';
  status: 'active' | 'recovered' | 'intermittent';
  firstSeenAt: string;
  lastSeenAt: string;
  failureCount: number;
  srcNamespace?: string;
  srcPod?: string;
  srcNode?: string;
  targetNamespace?: string;
  targetService?: string;
  targetClusterIP?: string;
  targetBackendPod?: string;
  targetBackendIP?: string;
  targetBackendNode?: string;
  servicePort?: number;
  backendPort?: number;
  protocol: string;
  initialResult: string;
  failureKind?: string;
  failureSummary: string;
  initialLatencyMs?: number;
  initialBackends?: string[];
  timeline?: ClusterChangeEvent[];
  retests?: IncidentRetestRecord[];
}

// ===== Kernel / Conntrack / NAT / MTU Audit =====

export interface ConntrackStats {
  count: number;
  max: number;
  buckets: number;
  usagePct: number;
  searched: number;
  found: number;
  new: number;
  invalid: number;
  ignore: number;
  insert: number;
  insertFailed: number;
  drop: number;
  earlyDrop: number;
  icmpError: number;
  searchRestart: number;
  natOutgoingEnabled: boolean;
  snatRandomFully: boolean;
  acctEnabled: boolean;
}

export interface SysctlCheck {
  key: string;
  value: string;
  expected: string;
  category: string;
  status: 'pass' | 'warn' | 'critical' | 'info';
  explanation: string;
  remediation?: string;
}

export interface MTUCheck {
  primaryNIC: string;
  primaryMTU: number;
  tunnelIface?: string;
  tunnelType?: string;
  tunnelMTU?: number;
  expectedTunnelMTU?: number;
  sampledVethCount: number;
  minVethMTU?: number;
  maxVethMTU?: number;
  mismatchedVeths?: string[];
  status: 'pass' | 'warn' | 'critical';
  detail: string;
}

export interface ARPEntry {
  ip: string;
  mac: string;
  dev: string;
  state: string;
  role?: 'gateway' | 'cluster-node' | 'pod-veth' | 'other' | string;
  peerName?: string;
}

export interface L2NetworkInfo {
  primaryNIC: string;
  primaryMAC: string;
  primaryIP: string;
  defaultGateway?: string;
  gatewayMAC?: string;
  gatewayARPState?: string;
  proxyARPVeths: number;
  totalCaliVeths: number;
  arpCount: number;
  arpFailedCount: number;
  arpGCThresh3: number;
  arpUsagePct: number;
  neighbors?: ARPEntry[];
}

export interface NetworkModeProfile {
  encapMode: string;
  dataplane: string;
  kubeProxyMode: string;
  bgpEnabled: boolean;
  bgpASNumber: number;
  nodeToNodeMesh: boolean;
  configuredMTU: number;
  physicalMTU: number;
  encapOverheadBytes: number;
  recommendedPodMTU: number;
  summary: string;
  natOutgoingPools: number;
  totalPools: number;
}

export interface AuditFinding {
  id: string;
  nodeName?: string;
  severity: 'critical' | 'warning' | 'info';
  category: string;
  title: string;
  detail: string;
  kernelEvidence?: string;
  remediation?: string;
}

export interface NodeKernelAudit {
  nodeName: string;
  internalIP?: string;
  kernelVersion?: string;
  score: number;
  status: 'healthy' | 'warning' | 'critical' | 'unreachable';
  error?: string;
  l2?: L2NetworkInfo;
  conntrack: ConntrackStats;
  sysctls: SysctlCheck[];
  mtu: MTUCheck;
  findings?: AuditFinding[];
  collectedAt: string;
}

export interface ClusterKernelAuditReport {
  generatedAt: string;
  overallStatus: 'healthy' | 'warning' | 'critical';
  averageScore: number;
  encapMode: string;
  networkMode?: NetworkModeProfile;
  bgpNeighbors?: BGPPeerSummary[];
  criticalCount: number;
  warningCount: number;
  nodes: NodeKernelAudit[];
  findings: AuditFinding[];
}

export interface PMTUDProbeRequest {
  srcNamespace: string;
  srcPod: string;
  targetIP: string;
  maxMTU?: number;
  timeoutMs?: number;
}

export interface PMTUDAttempt {
  packetSize: number;
  success: boolean;
  result: 'ECHO_REPLY' | 'FRAG_NEEDED' | 'TIMEOUT' | 'ERROR';
  latencyMs: number;
  detail?: string;
}

export interface PMTUDProbeResult {
  srcPod: string;
  srcIP: string;
  targetIP: string;
  podMTU: number;
  discoveredMTU: number;
  blackhole: boolean;
  status: 'pass' | 'warn' | 'critical';
  diagnosis: string;
  attempts: PMTUDAttempt[];
}

// ===== Route & BGP Consistency Audit =====

export interface HostRouteEntry {
  dst: string;
  gateway?: string;
  dev?: string;
  protocol: string;
  type: string;
  scope?: string;
  metric?: number;
  flags?: string;
}

export interface RouteDriftItem {
  nodeName: string;
  kind:
    | 'missing_block'
    | 'missing_pod'
    | 'ghost_pod_route'
    | 'unexpected_blackhole'
    | 'missing_blackhole'
    | 'encap_mismatch'
    | 'asymmetric_path';
  severity: 'critical' | 'warning' | 'info';
  cidr: string;
  expected: string;
  actual: string;
  detail: string;
  remediation?: string;
}

export interface BGPEvent {
  timestamp: string;
  nodeName: string;
  peerName: string;
  peerIP: string;
  prevState: string;
  newState: string;
  severity: 'critical' | 'warning' | 'info';
  message: string;
}

export interface NodeRouteAudit {
  nodeName: string;
  internalIP: string;
  status: 'healthy' | 'warning' | 'critical' | 'unreachable';
  error?: string;
  totalRoutes: number;
  birdRoutes: number;
  localPodRoutes: number;
  blackholeRoutes: number;
  expectedBlocks: number;
  expectedLocalPods: number;
  driftCount: number;
  bgpPeersUp: number;
  bgpPeersTotal: number;
  routes: HostRouteEntry[];
  drifts?: RouteDriftItem[];
}

export interface AsymmetricPair {
  nodeA: string;
  nodeB: string;
  aToBCIDR: string;
  aToBPath: string;
  bToACIDR: string;
  bToAPath: string;
  detail: string;
}

export interface ClusterRouteAuditReport {
  generatedAt: string;
  overallStatus: 'healthy' | 'warning' | 'critical';
  encapMode: string;
  totalDrifts: number;
  missingCount: number;
  ghostPodCount: number;
  asymmetricCount: number;
  nodes: NodeRouteAudit[];
  drifts: RouteDriftItem[];
  asymmetricPairs?: AsymmetricPair[];
  bgpEvents?: BGPEvent[];
}
