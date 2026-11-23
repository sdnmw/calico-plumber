import type {
  ClusterNetworkOverview,
  EnrichedFlow,
  GlobalHealthReport,
  HTTPProbeResponse,
  IPAMSnapshot,
  PodBrief,
  PodNetworkDetail,
  SearchResultItem,
  ServiceBrief,
  TCPHandshakeResponse,
  TopologyGraph,
  TraceFlowRequest,
  TraceFlowResult,
} from './types';

async function fetchJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, init);
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    try {
      const body = await res.json();
      if (body.error) msg = body.error;
    } catch {
      // ignore
    }
    throw new Error(msg);
  }
  return res.json();
}

export const api = {
  getOverview: () => fetchJSON<ClusterNetworkOverview>('/api/v1/overview'),

  getNamespaces: () =>
    fetchJSON<{ namespaces: string[] }>('/api/v1/namespaces').then((r) => r.namespaces || []),

  getIPAM: () => fetchJSON<IPAMSnapshot>('/api/v1/ipam'),

  search: (q: string, namespace = '', kind = '', limit = 80) => {
    const p = new URLSearchParams();
    if (q) p.set('q', q);
    if (namespace) p.set('namespace', namespace);
    if (kind) p.set('kind', kind);
    p.set('limit', String(limit));
    return fetchJSON<{ total: number; results: SearchResultItem[] }>(`/api/v1/search?${p}`);
  },

  listPods: (namespace = '') => {
    const p = namespace ? `?namespace=${encodeURIComponent(namespace)}` : '';
    return fetchJSON<{ total: number; pods: PodBrief[] }>(`/api/v1/pods${p}`).then((r) => r.pods || []);
  },

  listServices: (namespace = '') => {
    const p = namespace ? `?namespace=${encodeURIComponent(namespace)}` : '';
    return fetchJSON<{ services: ServiceBrief[] }>(`/api/v1/services${p}`).then((r) => r.services || []);
  },

  getPodDetail: (namespace: string, name: string) =>
    fetchJSON<PodNetworkDetail>(
      `/api/v1/pods/detail?namespace=${encodeURIComponent(namespace)}&name=${encodeURIComponent(name)}`,
    ),

  getTopology: (params: {
    granularity: string;
    window: number;
    namespaces?: string[];
    search?: string;
    protocol?: string;
  }) => {
    const p = new URLSearchParams();
    p.set('granularity', params.granularity);
    p.set('window', String(params.window));
    if (params.namespaces && params.namespaces.length > 0) p.set('namespaces', params.namespaces.join(','));
    if (params.search) p.set('search', params.search);
    if (params.protocol) p.set('protocol', params.protocol);
    return fetchJSON<TopologyGraph>(`/api/v1/topology?${p}`);
  },

  getFlows: (params: { window: number; namespace?: string; search?: string; protocol?: string; limit?: number }) => {
    const p = new URLSearchParams();
    p.set('window', String(params.window));
    if (params.namespace) p.set('namespace', params.namespace);
    if (params.search) p.set('search', params.search);
    if (params.protocol) p.set('protocol', params.protocol);
    if (params.limit) p.set('limit', String(params.limit));
    return fetchJSON<{ total: number; flows: EnrichedFlow[] }>(`/api/v1/flows?${p}`);
  },

  runTraceFlow: (req: TraceFlowRequest) =>
    fetchJSON<TraceFlowResult>('/api/v1/traceflow', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    }),

  getTraceFlowHistory: () =>
    fetchJSON<{ history: TraceFlowResult[] }>('/api/v1/traceflow/history').then((r) => r.history || []),

  runTCPHandshake: (req: {
    srcNamespace: string;
    srcPod: string;
    targetHost: string;
    targetPort: number;
    count: number;
    timeoutMs: number;
  }) =>
    fetchJSON<TCPHandshakeResponse>('/api/v1/diag/tcp-handshake', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    }),

  runHTTPProbe: (req: {
    srcNamespace: string;
    srcPod: string;
    url: string;
    method: string;
    hostHeader?: string;
    headers?: Record<string, string>;
    insecureSkipTLS: boolean;
    followRedirects: boolean;
    timeoutMs: number;
  }) =>
    fetchJSON<HTTPProbeResponse>('/api/v1/diag/http-probe', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    }),

  getHealth: () => fetchJSON<GlobalHealthReport>('/api/v1/health'),

  runHealthNow: () =>
    fetchJSON<GlobalHealthReport>('/api/v1/health/run', {
      method: 'POST',
    }),

  getTCPDumpWSUrl: () => {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    return `${proto}//${window.location.host}/api/v1/diag/tcpdump/ws`;
  },

  getPcapDownloadUrl: (nodeName: string, captureId: string) =>
    `/api/v1/diag/pcap?node=${encodeURIComponent(nodeName)}&id=${encodeURIComponent(captureId)}`,
};
