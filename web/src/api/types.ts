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

export interface GlobalHealthReport {
  checkedAt: string;
  durationMs: number;
  overallScore: number;
  overallStatus: 'healthy' | 'degraded' | 'critical';
  podToPod: {
    totalPairs: number;
    healthyPairs: number;
    failedPairs: number;
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
    services: {
      namespace: string;
      name: string;
      type: string;
      clusterIP: string;
      ports: string[];
      status: 'healthy' | 'degraded' | 'broken' | 'no-endpoints' | 'headless';
      totalBackends: number;
      readyBackends: number;
      reachableBackends: number;
      clusterIPReachable?: boolean;
      clusterIPLatencyMs?: number;
      clusterIPError?: string;
      diagnosis: string;
      backends: {
        podNamespace?: string;
        podName?: string;
        podIP: string;
        nodeName?: string;
        targetPort: number;
        protocol: string;
        k8sReady: boolean;
        probeOk: boolean;
        latencyMs: number;
        error?: string;
      }[];
    }[];
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
