import React, { useEffect, useState } from 'react';
import {
  Play,
  ArrowRight,
  Shield,
  Network,
  Layers,
  Box,
  Server,
  Globe,
  Cpu,
  History,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  Shuffle,
} from 'lucide-react';
import { api } from '../api/client';
import type { NodeNetworkStatus, PodBrief, ServiceBrief, TraceFlowRequest, TraceFlowResult, TraceHop } from '../api/types';
import { EncapBadge, SectionCard, StatusPill } from '../components/common/Badges';
import { LayeredTraceTopology } from '../components/traceflow/LayeredTraceTopology';

interface TraceFlowPageProps {
  initialReq?: Partial<TraceFlowRequest> | null;
}

export function TraceFlowPage({ initialReq }: TraceFlowPageProps) {
  const [pods, setPods] = useState<PodBrief[]>([]);
  const [services, setServices] = useState<ServiceBrief[]>([]);
  const [nodeDetails, setNodeDetails] = useState<NodeNetworkStatus[]>([]);
  const [nodes, setNodes] = useState<{ name: string; ip: string }[]>([]);
  const [history, setHistory] = useState<TraceFlowResult[]>([]);
  const [viewMode, setViewMode] = useState<'layered' | 'linear'>('layered');

  const [srcNs, setSrcNs] = useState('');
  const [srcPod, setSrcPod] = useState('');
  const [targetType, setTargetType] = useState<'pod' | 'service' | 'node' | 'ip'>('pod');
  const [dstNs, setDstNs] = useState('');
  const [dstName, setDstName] = useState('');
  const [dstIP, setDstIP] = useState('');
  const [protocol, setProtocol] = useState<'TCP' | 'UDP' | 'ICMP'>('TCP');
  const [dstPort, setDstPort] = useState(80);

  const [running, setRunning] = useState(false);
  const [error, setError] = useState('');
  const [result, setResult] = useState<TraceFlowResult | null>(null);
  const [selectedHop, setSelectedHop] = useState<TraceHop | null>(null);

  useEffect(() => {
    Promise.all([api.listPods(), api.listServices(), api.getOverview(), api.getTraceFlowHistory()]).then(
      ([podList, svcList, ov, hist]) => {
        const runningPods = podList.filter((p) => p.phase === 'Running' && p.ip && !p.hostNetwork);
        setPods(runningPods);
        setServices(svcList);
        setNodeDetails(ov.nodes || []);
        setNodes((ov.nodes || []).map((n) => ({ name: n.name, ip: n.internalIP })));
        setHistory(hist);
        if (hist.length > 0 && !result) {
          setResult(hist[0]);
        }
        // Smart initial defaults if none passed
        if (!initialReq?.srcPod && runningPods.length > 0 && !srcPod) {
          const defSrc = runningPods.find((p) => p.namespace === 'calico-netdiag' && p.name.includes('probe')) || runningPods[0];
          setSrcNs(defSrc.namespace);
          setSrcPod(defSrc.name);
        }
        if (!initialReq?.dstName && !initialReq?.dstIP && runningPods.length > 1 && !dstName) {
          const defDst = runningPods.find((p) => p.nodeName !== runningPods[0].nodeName) || runningPods[1];
          setDstNs(defDst.namespace);
          setDstName(defDst.name);
        }
      },
    );
  }, []);

  useEffect(() => {
    if (!initialReq) return;
    if (initialReq.srcNamespace) setSrcNs(initialReq.srcNamespace);
    if (initialReq.srcPod) setSrcPod(initialReq.srcPod);
    if (initialReq.targetType) setTargetType(initialReq.targetType);
    if (initialReq.dstNamespace) setDstNs(initialReq.dstNamespace);
    if (initialReq.dstName) setDstName(initialReq.dstName);
    if (initialReq.dstIP) setDstIP(initialReq.dstIP);
    if (initialReq.protocol) setProtocol(initialReq.protocol);
    if (initialReq.dstPort) setDstPort(initialReq.dstPort);
  }, [initialReq]);

  const namespaces = Array.from(new Set(pods.map((p) => p.namespace))).sort();
  const srcPodsInNs = pods.filter((p) => !srcNs || p.namespace === srcNs);
  const dstPodsInNs = pods.filter((p) => !dstNs || p.namespace === dstNs);
  const dstSvcsInNs = services.filter((s) => !dstNs || s.namespace === dstNs);

  const runTrace = async () => {
    if (!srcNs || !srcPod) {
      setError('请选择源 Pod');
      return;
    }
    setRunning(true);
    setError('');
    setSelectedHop(null);
    try {
      const req: TraceFlowRequest = {
        srcNamespace: srcNs,
        srcPod,
        targetType,
        dstNamespace: targetType === 'pod' || targetType === 'service' ? dstNs : undefined,
        dstName: targetType !== 'ip' ? dstName : undefined,
        dstIP: targetType === 'ip' ? dstIP : undefined,
        protocol,
        dstPort: protocol === 'ICMP' ? 0 : dstPort,
        timeoutMs: 2500,
      };
      const res = await api.runTraceFlow(req);
      setResult(res);
      setHistory((prev) => [res, ...prev.filter((h) => h.id !== res.id)].slice(0, 20));
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setRunning(false);
    }
  };

  // Group forward hops by dataplane swimlane
  const laneOrder = ['source-node', 'fabric', 'dest-node', 'external'];
  const destLaneNode = (result?.forwardHops || []).find((h) => h.lane === 'dest-node' && h.nodeName)?.nodeName;
  const laneLabels: Record<string, string> = {
    'source-node': `源节点数据面 (${result?.source.nodeName || 'Source Node'})`,
    fabric: `底层 / 隧道网络织物 (${result?.encapMode || 'Fabric'})`,
    'dest-node': `目标节点数据面 (${result?.resolvedBackend?.nodeName || result?.destination.nodeName || destLaneNode || 'Dest Node'})`,
    external: '外部网络 / 网关出口',
  };

  const lanes = laneOrder
    .map((laneKey) => ({
      key: laneKey,
      title: laneLabels[laneKey],
      hops: (result?.forwardHops || []).filter((h) => h.lane === laneKey),
    }))
    .filter((l) => l.hops.length > 0);

  return (
    <div className="p-6 space-y-5 max-w-[1600px] mx-auto">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-slate-900 dark:text-white">
            多跳 TraceFlow 路径追踪
          </h1>
          <p className="text-xs text-slate-500 dark:text-slate-400 mt-1">
            真实进入源 Pod Netns 注入唯一特征报文，并在源/目的双节点各关键接口（eth0 / cali* veth / kube-proxy NAT / tunl0 / vxlan.calico / 物理网卡）实时抓包观测，精准定位丢包跳点
          </p>
        </div>
      </div>

      {/* TraceFlow Config Builder Bar */}
      <SectionCard className="p-4">
        <div className="grid grid-cols-1 lg:grid-cols-12 gap-3 items-end">
          {/* Source Pod */}
          <div className="lg:col-span-4 space-y-1">
            <label className="text-[11px] font-semibold uppercase tracking-wider text-slate-500">
              源 Pod (Source)
            </label>
            <div className="grid grid-cols-5 gap-1.5">
              <select
                value={srcNs}
                onChange={(e) => {
                  const ns = e.target.value;
                  setSrcNs(ns);
                  const first = pods.find((p) => p.namespace === ns);
                  if (first) setSrcPod(first.name);
                }}
                className="col-span-2 px-2.5 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 outline-none"
              >
                {namespaces.map((ns) => (
                  <option key={ns} value={ns}>{ns}</option>
                ))}
              </select>
              <select
                value={srcPod}
                onChange={(e) => setSrcPod(e.target.value)}
                className="col-span-3 px-2.5 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
              >
                {srcPodsInNs.map((p) => (
                  <option key={p.name} value={p.name}>
                    {p.name} ({p.ip})
                  </option>
                ))}
              </select>
            </div>
          </div>

          {/* Target Type + Target Selector */}
          <div className="lg:col-span-5 space-y-1">
            <div className="flex items-center justify-between">
              <label className="text-[11px] font-semibold uppercase tracking-wider text-slate-500">
                目标端点 (Destination)
              </label>
              <div className="inline-flex rounded-md bg-slate-100 dark:bg-slate-800 p-0.5 text-[10px]">
                {(['pod', 'service', 'node', 'ip'] as const).map((tt) => (
                  <button
                    key={tt}
                    onClick={() => {
                      setTargetType(tt);
                      if (tt === 'node' && nodes.length > 0) setDstName(nodes[0].name);
                      if (tt === 'service' && services.length > 0) {
                        const s = services.find((x) => x.namespace === dstNs) || services[0];
                        setDstNs(s.namespace);
                        setDstName(s.name);
                        if (s.ports?.[0]) setDstPort(s.ports[0]);
                      }
                    }}
                    className={`px-2 py-0.5 rounded font-medium ${
                      targetType === tt
                        ? 'bg-white dark:bg-slate-900 text-blue-600 dark:text-blue-400 shadow-sm'
                        : 'text-slate-500'
                    }`}
                  >
                    {tt === 'pod' ? 'Pod' : tt === 'service' ? 'Service' : tt === 'node' ? 'Node' : 'IP / 域名'}
                  </button>
                ))}
              </div>
            </div>

            {targetType === 'pod' && (
              <div className="grid grid-cols-5 gap-1.5">
                <select
                  value={dstNs}
                  onChange={(e) => {
                    const ns = e.target.value;
                    setDstNs(ns);
                    const first = pods.find((p) => p.namespace === ns);
                    if (first) setDstName(first.name);
                  }}
                  className="col-span-2 px-2.5 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 outline-none"
                >
                  {namespaces.map((ns) => (
                    <option key={ns} value={ns}>{ns}</option>
                  ))}
                </select>
                <select
                  value={dstName}
                  onChange={(e) => setDstName(e.target.value)}
                  className="col-span-3 px-2.5 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
                >
                  {dstPodsInNs.map((p) => (
                    <option key={p.name} value={p.name}>
                      {p.name} ({p.ip} · {p.nodeName})
                    </option>
                  ))}
                </select>
              </div>
            )}

            {targetType === 'service' && (
              <div className="grid grid-cols-5 gap-1.5">
                <select
                  value={dstNs}
                  onChange={(e) => {
                    const ns = e.target.value;
                    setDstNs(ns);
                    const first = services.find((s) => s.namespace === ns);
                    if (first) {
                      setDstName(first.name);
                      if (first.ports?.[0]) setDstPort(first.ports[0]);
                    }
                  }}
                  className="col-span-2 px-2.5 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 outline-none"
                >
                  {Array.from(new Set(services.map((s) => s.namespace))).sort().map((ns) => (
                    <option key={ns} value={ns}>{ns}</option>
                  ))}
                </select>
                <select
                  value={dstName}
                  onChange={(e) => {
                    setDstName(e.target.value);
                    const svc = dstSvcsInNs.find((s) => s.name === e.target.value);
                    if (svc?.ports?.[0]) setDstPort(svc.ports[0]);
                  }}
                  className="col-span-3 px-2.5 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
                >
                  {dstSvcsInNs.map((s) => (
                    <option key={s.name} value={s.name}>
                      {s.name} ({s.clusterIP})
                    </option>
                  ))}
                </select>
              </div>
            )}

            {targetType === 'node' && (
              <select
                value={dstName}
                onChange={(e) => setDstName(e.target.value)}
                className="w-full px-2.5 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
              >
                {nodes.map((n) => (
                  <option key={n.name} value={n.name}>
                    {n.name} ({n.ip})
                  </option>
                ))}
              </select>
            )}

            {targetType === 'ip' && (
              <input
                value={dstIP}
                onChange={(e) => setDstIP(e.target.value)}
                placeholder="例如 192.168.118.206 或 10.250.252.10 或 223.5.5.5"
                className="w-full px-3 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
              />
            )}
          </div>

          {/* Protocol & Port & Run */}
          <div className="lg:col-span-3 flex items-end gap-2">
            <div className="w-24 space-y-1">
              <label className="text-[11px] font-semibold uppercase tracking-wider text-slate-500">协议</label>
              <select
                value={protocol}
                onChange={(e) => setProtocol(e.target.value as 'TCP' | 'UDP' | 'ICMP')}
                className="w-full px-2 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
              >
                <option value="TCP">TCP</option>
                <option value="UDP">UDP</option>
                <option value="ICMP">ICMP</option>
              </select>
            </div>
            {protocol !== 'ICMP' && (
              <div className="w-20 space-y-1">
                <label className="text-[11px] font-semibold uppercase tracking-wider text-slate-500">端口</label>
                <input
                  type="number"
                  value={dstPort}
                  onChange={(e) => setDstPort(Number(e.target.value))}
                  className="w-full px-2 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
                />
              </div>
            )}
            <button
              onClick={runTrace}
              disabled={running}
              className="flex-1 inline-flex items-center justify-center gap-1.5 px-4 py-2 rounded-lg bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white text-xs font-semibold shadow-sm transition"
            >
              <Play className="w-3.5 h-3.5" />
              {running ? '追踪中...' : '执行 Trace'}
            </button>
          </div>
        </div>
        {error && <p className="mt-2 text-xs text-rose-600">{error}</p>}
      </SectionCard>

      {/* Empty State Quick Presets when no trace yet */}
      {!result && (
        <SectionCard
          title="快速发起典型 Calico 排障场景"
          subtitle="选择一个预设场景或直接在上方自定义源 Pod 与目标端点执行实时多跳 TraceFlow"
        >
          <div className="p-4 grid grid-cols-1 md:grid-cols-3 gap-3 text-xs">
            <button
              onClick={() => {
                const probeList = pods.filter((p) => p.namespace === 'calico-netdiag' && p.name.includes('probe'));
                if (probeList.length >= 2) {
                  setSrcNs(probeList[0].namespace);
                  setSrcPod(probeList[0].name);
                  setTargetType('pod');
                  setDstNs(probeList[1].namespace);
                  setDstName(probeList[1].name);
                  setProtocol('TCP');
                  setDstPort(8099);
                }
              }}
              className="text-left p-3.5 rounded-xl border border-slate-200 dark:border-slate-800 hover:border-blue-400 bg-slate-50/70 dark:bg-slate-950/50 transition"
            >
              <div className="font-semibold text-slate-800 dark:text-slate-100">跨节点 Pod → Pod (BGP / 隧道)</div>
              <p className="text-[11px] text-slate-500 mt-1">完整观测源 Pod eth0 → cali* veth → 物理/隧道口 → 跨节点 Fabric → 目标 Pod 逐跳延迟与封装头</p>
            </button>
            <button
              onClick={() => {
                const svc = services.find((s) => s.clusterIP && s.clusterIP !== 'None' && s.ports?.length > 0 && s.namespace !== 'kube-system') || services.find((s) => s.name === 'kube-dns');
                if (svc) {
                  setTargetType('service');
                  setDstNs(svc.namespace);
                  setDstName(svc.name);
                  setProtocol('TCP');
                  setDstPort(svc.ports[0] || 80);
                }
              }}
              className="text-left p-3.5 rounded-xl border border-slate-200 dark:border-slate-800 hover:border-blue-400 bg-slate-50/70 dark:bg-slate-950/50 transition"
            >
              <div className="font-semibold text-slate-800 dark:text-slate-100">Pod → Service ClusterIP (kube-proxy DNAT)</div>
              <p className="text-[11px] text-slate-500 mt-1">透视 iptables KUBE-SVC/SEP 或 IPVS 规则，展示 DNAT 选中的真实后端 Pod</p>
            </button>
            <button
              onClick={() => {
                if (nodes.length > 0) {
                  setTargetType('node');
                  setDstName(nodes[0].name);
                  setProtocol('ICMP');
                }
              }}
              className="text-left p-3.5 rounded-xl border border-slate-200 dark:border-slate-800 hover:border-blue-400 bg-slate-50/70 dark:bg-slate-950/50 transition"
            >
              <div className="font-semibold text-slate-800 dark:text-slate-100">Pod → 节点 InternalIP (ICMP)</div>
              <p className="text-[11px] text-slate-500 mt-1">验证 Pod 到宿主机 / 跨节点主机的底层路由与往返 Echo 连通性</p>
            </button>
          </div>
        </SectionCard>
      )}

      {/* Trace Result Visualization */}
      {result && (
        <>
          {/* Verdict Banner */}
          <div
            className={`p-4 rounded-xl border flex flex-wrap items-center justify-between gap-4 ${
              result.status === 'REACHABLE'
                ? 'bg-emerald-50/70 dark:bg-emerald-950/30 border-emerald-200 dark:border-emerald-900'
                : result.status === 'RESET'
                  ? 'bg-amber-50/70 dark:bg-amber-950/30 border-amber-200 dark:border-amber-900'
                  : 'bg-rose-50/70 dark:bg-rose-950/30 border-rose-200 dark:border-rose-900'
            }`}
          >
            <div className="flex items-start gap-3">
              {result.status === 'REACHABLE' ? (
                <CheckCircle2 className="w-5 h-5 text-emerald-600 mt-0.5 shrink-0" />
              ) : result.status === 'RESET' ? (
                <AlertTriangle className="w-5 h-5 text-amber-600 mt-0.5 shrink-0" />
              ) : (
                <XCircle className="w-5 h-5 text-rose-600 mt-0.5 shrink-0" />
              )}
              <div>
                <div className="flex flex-wrap items-center gap-2">
                  <StatusPill status={result.status} />
                  <EncapBadge mode={result.encapMode} />
                  <span className="text-xs font-mono px-2 py-0.5 rounded bg-white/80 dark:bg-slate-900/80 border border-slate-200/70 dark:border-slate-700">
                    {result.destination.kind === 'External' || result.forwardHops?.some((h) => h.lane === 'external')
                      ? '集群外部 (External)'
                      : result.crossNode
                        ? '跨节点 (Cross-Node)'
                        : '同节点 (Same-Node)'}
                  </span>
                  <span className="text-xs font-mono text-slate-600 dark:text-slate-300">
                    RTT: <b>{result.totalLatencyMs} ms</b>
                  </span>
                </div>
                <p className="text-sm font-medium text-slate-800 dark:text-slate-100 mt-1.5">
                  {result.verdictSummary}
                </p>
                {result.rootCauseHint && (
                  <p className="text-xs text-slate-600 dark:text-slate-300 mt-1">
                    <b>根因诊断建议：</b>{result.rootCauseHint}
                  </p>
                )}
              </div>
            </div>

            <div className="text-right font-mono text-[11px] text-slate-500 space-y-0.5">
              <div>Trace ID: {result.id}</div>
              <div>{result.traceSignature}</div>
              {result.resolvedBackend && (
                <div className="text-teal-700 dark:text-teal-300 font-semibold">
                  DNAT → {result.resolvedBackend.displayName} ({result.resolvedBackend.ip})
                </div>
              )}
            </div>
          </div>

          {/* Layered Node-Stack Dataplane Topology (Default) / Optional Linear Pipeline */}
          <SectionCard
            title="数据面分层拓扑路径视图（Node / OS / Kernel / vNIC / Container）"
            subtitle="按真实宿主机分层结构（容器 → OS 用户态/内核态 → 虚拟网卡/隧道 → 物理网卡 → Node 底层）还原报文流向，点击任意跳点查看实时抓包与规则明细"
            action={
              <div className="inline-flex rounded-lg bg-slate-100 dark:bg-slate-800 p-0.5 text-[11px] shrink-0">
                <button
                  onClick={() => setViewMode('layered')}
                  className={`px-2.5 py-1 rounded-md font-medium transition ${
                    viewMode === 'layered'
                      ? 'bg-white dark:bg-slate-900 text-blue-600 dark:text-blue-400 shadow-xs'
                      : 'text-slate-500 hover:text-slate-700 dark:hover:text-slate-300'
                  }`}
                >
                  分层拓扑视图
                </button>
                <button
                  onClick={() => setViewMode('linear')}
                  className={`px-2.5 py-1 rounded-md font-medium transition ${
                    viewMode === 'linear'
                      ? 'bg-white dark:bg-slate-900 text-blue-600 dark:text-blue-400 shadow-xs'
                      : 'text-slate-500 hover:text-slate-700 dark:hover:text-slate-300'
                  }`}
                >
                  线性流水线
                </button>
              </div>
            }
          >
            <div className="p-4 sm:p-5">
              {viewMode === 'layered' ? (
                <LayeredTraceTopology
                  result={result}
                  nodes={nodeDetails}
                  selectedHop={selectedHop}
                  onSelectHop={setSelectedHop}
                />
              ) : (
                <div className="overflow-x-auto">
                  <div className="flex items-stretch gap-4 min-w-max">
                    {lanes.map((lane, lIdx) => (
                      <React.Fragment key={lane.key}>
                        <div
                          className={`rounded-xl border p-3.5 flex flex-col gap-3 ${
                            lane.key === 'fabric'
                              ? 'bg-violet-50/40 dark:bg-violet-950/20 border-violet-200/80 dark:border-violet-900/60 border-dashed'
                              : 'bg-slate-50/70 dark:bg-slate-950/50 border-slate-200/90 dark:border-slate-800'
                          }`}
                        >
                          <div className="text-[11px] font-semibold text-slate-500 dark:text-slate-400 uppercase tracking-wider flex items-center gap-1.5">
                            {lane.key === 'fabric' ? <Globe className="w-3.5 h-3.5 text-violet-500" /> : <Cpu className="w-3.5 h-3.5 text-blue-500" />}
                            {lane.title}
                          </div>

                          <div className="flex items-center gap-2.5">
                            {lane.hops.map((hop, hIdx) => (
                              <React.Fragment key={hop.index}>
                                <HopCard
                                  hop={hop}
                                  selected={selectedHop?.index === hop.index}
                                  onClick={() => setSelectedHop(hop)}
                                />
                                {hIdx < lane.hops.length - 1 && (
                                  <ArrowRight className="w-4 h-4 text-slate-400 shrink-0" />
                                )}
                              </React.Fragment>
                            ))}
                          </div>
                        </div>

                        {lIdx < lanes.length - 1 && (
                          <div className="flex items-center">
                            <ArrowRight className="w-5 h-5 text-blue-500 shrink-0" />
                          </div>
                        )}
                      </React.Fragment>
                    ))}
                  </div>
                </div>
              )}

              {/* Return Path Observed Strip */}
              {result.returnHops && result.returnHops.length > 0 && (
                <div className="mt-4 pt-4 border-t border-slate-100 dark:border-slate-800">
                  <div className="text-[11px] font-semibold uppercase tracking-wider text-slate-400 mb-2">
                    反向回包观测路径 (Return Path)
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    {result.returnHops.map((rh, i) => (
                      <React.Fragment key={rh.index}>
                        <button
                          onClick={() => setSelectedHop(rh)}
                          className="px-2.5 py-1.5 rounded-lg bg-emerald-50/70 dark:bg-emerald-950/30 border border-emerald-200 dark:border-emerald-900 text-[11px] font-mono text-emerald-800 dark:text-emerald-200 hover:ring-2 hover:ring-emerald-400 transition"
                        >
                          <span className="font-semibold">{rh.title}</span>
                          {rh.interface && <span className="ml-1.5 opacity-70">[{rh.interface}]</span>}
                          <span className="ml-1.5 text-[10px] opacity-80">+{rh.deltaMs}ms</span>
                        </button>
                        {i < result.returnHops!.length - 1 && <ArrowRight className="w-3.5 h-3.5 text-emerald-500" />}
                      </React.Fragment>
                    ))}
                  </div>
                </div>
              )}
            </div>

            {/* Selected Hop Inspector */}
            {selectedHop && (
              <div className="px-5 py-3.5 bg-blue-50/50 dark:bg-blue-950/20 border-t border-blue-200/70 dark:border-blue-900/60 text-xs space-y-1.5">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2 font-semibold text-slate-800 dark:text-slate-100">
                    <span>Hop #{selectedHop.index}: {selectedHop.title}</span>
                    <StatusPill status={selectedHop.status} />
                    {selectedHop.interface && <span className="font-mono text-blue-600 dark:text-blue-400">iface: {selectedHop.interface}</span>}
                  </div>
                  <span className="font-mono text-slate-500">+{selectedHop.deltaMs || 0} ms</span>
                </div>
                {selectedHop.outerHeader && (
                  <div className="font-mono text-[11px] text-violet-700 dark:text-violet-300">
                    <b>外层隧道封装头:</b> {selectedHop.outerHeader}
                  </div>
                )}
                {selectedHop.packetSummary && (
                  <div className="font-mono text-[11px] text-slate-700 dark:text-slate-300">
                    <b>实测捕获报文:</b> {selectedHop.packetSummary}
                  </div>
                )}
                {selectedHop.ruleDetail && (
                  <div className="font-mono text-[11px] text-slate-600 dark:text-slate-400">
                    <b>规则 / 路由明细:</b> {selectedHop.ruleDetail}
                  </div>
                )}
              </div>
            )}
          </SectionCard>

          {/* Bottom Grid: Observations Table + Trace History */}
          <div className="grid grid-cols-1 xl:grid-cols-3 gap-5">
            <SectionCard title="逐跳观测日志（Observations）" className="xl:col-span-2">
              <div className="overflow-x-auto max-h-[340px]">
                <table className="w-full text-[11px] font-mono">
                  <thead className="bg-slate-50 dark:bg-slate-800/70 text-slate-500 sticky top-0">
                    <tr>
                      <th className="text-left px-3 py-2">#</th>
                      <th className="text-left px-3 py-2">方向</th>
                      <th className="text-left px-3 py-2">观测类型</th>
                      <th className="text-left px-3 py-2">节点 / 组件</th>
                      <th className="text-left px-3 py-2">接口</th>
                      <th className="text-left px-3 py-2">详情</th>
                      <th className="text-right px-3 py-2">耗时</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                    {result.observations.map((ob) => (
                      <tr key={ob.seq} className="hover:bg-slate-50 dark:hover:bg-slate-800/40">
                        <td className="px-3 py-2 text-slate-400">{ob.seq}</td>
                        <td className="px-3 py-2">
                          <span className={`px-1.5 py-0.5 rounded text-[10px] ${ob.direction === 'Return' ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-300' : 'bg-blue-50 text-blue-700 dark:bg-blue-950/60 dark:text-blue-300'}`}>
                            {ob.direction}
                          </span>
                        </td>
                        <td className="px-3 py-2"><StatusPill status={ob.type} /></td>
                        <td className="px-3 py-2">
                          <div className="font-sans font-medium text-slate-800 dark:text-slate-100">{ob.component}</div>
                          <div className="text-[10px] text-slate-400">{ob.nodeName || 'fabric'}</div>
                        </td>
                        <td className="px-3 py-2 text-blue-600 dark:text-blue-400">{ob.interface || '—'}</td>
                        <td className="px-3 py-2 text-slate-600 dark:text-slate-300 max-w-[320px] truncate" title={ob.detail}>
                          {ob.detail}
                        </td>
                        <td className="px-3 py-2 text-right text-slate-500">+{ob.timestampMs}ms</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </SectionCard>

            <SectionCard
              title="最近 TraceFlow 记录"
              action={<History className="w-4 h-4 text-slate-400" />}
            >
              <div className="divide-y divide-slate-100 dark:divide-slate-800 max-h-[340px] overflow-y-auto">
                {history.length === 0 && (
                  <div className="p-6 text-center text-xs text-slate-400">暂无历史追踪记录</div>
                )}
                {history.map((h) => (
                  <button
                    key={h.id}
                    onClick={() => {
                      setResult(h);
                      setSelectedHop(null);
                    }}
                    className={`w-full text-left px-4 py-2.5 hover:bg-slate-50 dark:hover:bg-slate-800/50 transition ${
                      result?.id === h.id ? 'bg-blue-50/60 dark:bg-blue-950/30' : ''
                    }`}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <StatusPill status={h.status} />
                      <span className="font-mono text-[10px] text-slate-400">{h.totalLatencyMs} ms</span>
                    </div>
                    <div className="mt-1 text-[11px] font-mono text-slate-700 dark:text-slate-200 truncate">
                      {h.source.displayName} → {h.destination.displayName}
                    </div>
                    <div className="mt-0.5 text-[10px] text-slate-400 font-mono">
                      {h.protocol}{h.dstPort ? `:${h.dstPort}` : ''} · {h.encapMode}
                    </div>
                  </button>
                ))}
              </div>
            </SectionCard>
          </div>
        </>
      )}
    </div>
  );
}

function HopCard({ hop, selected, onClick }: { hop: TraceHop; selected: boolean; onClick: () => void }) {
  const isBad = hop.status === 'DROPPED' || hop.status === 'TIMEOUT';
  const isSkipped = hop.status === 'SKIPPED';
  const isWarn = hop.status === 'RESET' || hop.status === 'NAT' || hop.status === 'ENCAPSULATED' || hop.status === 'DECAPSULATED';
  const borderCls = isBad
    ? 'border-rose-400 bg-rose-50/70 dark:bg-rose-950/30'
    : isSkipped
      ? 'border-slate-200 dark:border-slate-800 border-dashed bg-slate-50/60 dark:bg-slate-900/50 opacity-75'
      : isWarn
        ? 'border-amber-300 dark:border-amber-800 bg-white dark:bg-slate-900'
        : 'border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900';

  const Icon =
    hop.component.includes('policy')
      ? Shield
      : hop.component.includes('nat')
        ? Shuffle
        : hop.component.includes('tunnel')
          ? Layers
          : hop.component.includes('route')
            ? Network
            : hop.component.includes('pod')
              ? Box
              : Server;

  return (
    <button
      onClick={onClick}
      className={`w-[178px] text-left rounded-xl border p-3 transition shadow-sm hover:shadow-md ${borderCls} ${
        selected ? 'ring-2 ring-blue-500' : ''
      }`}
    >
      <div className="flex items-center justify-between gap-1">
        <span className="inline-flex items-center justify-center w-6 h-6 rounded-lg bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-300">
          <Icon className="w-3.5 h-3.5" />
        </span>
        <StatusPill status={hop.status} />
      </div>
      <div className="mt-2 text-xs font-semibold text-slate-800 dark:text-slate-100 truncate">{hop.title}</div>
      <div className="mt-0.5 text-[10px] font-mono text-slate-500 dark:text-slate-400 truncate" title={hop.subtitle}>
        {hop.subtitle || hop.interface || '—'}
      </div>
      {hop.outerHeader && (
        <div className="mt-1.5 px-1.5 py-0.5 rounded bg-violet-50 dark:bg-violet-950/50 text-violet-700 dark:text-violet-300 font-mono text-[9px] truncate" title={hop.outerHeader}>
          {hop.outerHeader}
        </div>
      )}
      <div className="mt-1.5 flex items-center justify-between text-[10px] font-mono text-slate-400">
        <span>{hop.observed ? 'Wire Observed' : isSkipped ? 'Not Reached' : 'Evaluated'}</span>
        <span>{hop.deltaMs ? `+${hop.deltaMs}ms` : ''}</span>
      </div>
    </button>
  );
}
