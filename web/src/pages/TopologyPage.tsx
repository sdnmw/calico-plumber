import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ReactFlow,
  Background,
  Controls,
  MiniMap,
  Handle,
  Position,
  BaseEdge,
  EdgeLabelRenderer,
  getBezierPath,
  MarkerType,
  useReactFlow,
  useNodesInitialized,
  type Node,
  type Edge,
  type NodeProps,
  type EdgeProps,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import dagre from '@dagrejs/dagre';
import {
  Play,
  Pause,
  RefreshCw,
  X,
  Route,
  Terminal,
  ChevronUp,
  ChevronDown,
  Filter,
  Box,
  Layers,
  Server,
  Globe,
  Cpu,
} from 'lucide-react';
import { api } from '../api/client';
import type { EnrichedFlow, TopologyEdge, TopologyGraph, TopologyNode, TraceFlowRequest } from '../api/types';
import { EngineBadge, StatusPill } from '../components/common/Badges';
import { formatBps, formatBytes, formatLatency } from '../utils/format';

// Custom Service / Workload / Pod Card Node
function ServiceNodeCard({ data, selected }: NodeProps) {
  const n = data.raw as TopologyNode;
  const accent =
    n.kind === 'Service'
      ? 'border-l-teal-500'
      : n.kind === 'External'
        ? 'border-l-amber-500'
        : n.kind === 'Node'
          ? 'border-l-slate-500'
          : n.kind === 'DaemonSet'
            ? 'border-l-violet-500'
            : n.kind === 'StatefulSet'
              ? 'border-l-indigo-500'
              : 'border-l-blue-500';

  const healthDot =
    n.health === 'critical' ? 'bg-rose-500' : n.health === 'warning' ? 'bg-amber-500' : 'bg-emerald-500';

  const Icon =
    n.kind === 'Service' ? Server : n.kind === 'External' ? Globe : n.kind === 'Node' ? Cpu : n.kind === 'Pod' ? Box : Layers;

  return (
    <div
      className={`w-[238px] rounded-xl bg-white dark:bg-slate-900 border border-l-[3.5px] ${accent} ${
        selected
          ? 'ring-2 ring-blue-500 border-blue-400 shadow-lg'
          : 'border-slate-200/90 dark:border-slate-700/90 shadow-sm hover:shadow-md'
      } transition-all`}
    >
      <Handle type="target" position={Position.Left} className="!w-2 !h-2 !bg-blue-500 !border-white dark:!border-slate-900" />
      <Handle type="source" position={Position.Right} className="!w-2 !h-2 !bg-blue-500 !border-white dark:!border-slate-900" />

      <div className="px-3 py-2 border-b border-slate-100 dark:border-slate-800 flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 min-w-0">
          <Icon className="w-3.5 h-3.5 text-slate-500 shrink-0" />
          <span className="text-xs font-semibold text-slate-800 dark:text-slate-100 truncate font-mono" title={n.label}>
            {n.label}
          </span>
        </div>
        <span className={`w-2 h-2 rounded-full shrink-0 ${healthDot}`} />
      </div>

      <div className="px-3 py-2 space-y-1.5">
        <div className="flex items-center justify-between gap-1 text-[10px]">
          <span className="px-1.5 py-0.5 rounded bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-300 font-medium truncate max-w-[130px]">
            {n.namespace || n.kind}
          </span>
          <span className="text-slate-400 font-mono">
            {n.podCount ? `${n.podCount} pods` : n.ip || n.kind}
          </span>
        </div>

        {n.ports && n.ports.length > 0 && (
          <div className="flex flex-wrap gap-1">
            {n.ports.slice(0, 3).map((pt) => (
              <span
                key={pt}
                className="px-1.5 py-0.5 rounded text-[10px] font-mono bg-blue-50/80 dark:bg-blue-950/50 text-blue-700 dark:text-blue-300 border border-blue-100 dark:border-blue-900/60"
              >
                {pt}
              </span>
            ))}
            {n.ports.length > 3 && <span className="text-[10px] text-slate-400">+{n.ports.length - 3}</span>}
          </div>
        )}

        <div className="pt-1 border-t border-slate-100 dark:border-slate-800/80 flex items-center justify-between text-[10px] font-mono text-slate-500">
          <span>↓ {formatBps(n.inboundBps)}</span>
          <span>↑ {formatBps(n.outboundBps)}</span>
          <span className={n.avgLatencyMs > 25 ? 'text-amber-600 font-semibold' : 'text-emerald-600 dark:text-emerald-400'}>
            {formatLatency(n.avgLatencyMs)}
          </span>
        </div>
      </div>
    </div>
  );
}

// Custom Animated Directional Flow Edge
function TrafficFlowEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
  selected,
}: EdgeProps) {
  const e = data?.raw as TopologyEdge | undefined;
  const [edgePath, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  });

  const strokeColor =
    e?.health === 'critical' ? '#EF4444' : e?.health === 'warning' ? '#F59E0B' : selected ? '#2563EB' : '#10B981';

  const bps = e?.bytesPerSec || 0;
  const strokeWidth = Math.min(5.5, Math.max(1.5, 1.5 + Math.log10(Math.max(1, bps / 500))));

  return (
    <>
      <BaseEdge
        id={id}
        path={edgePath}
        style={{
          stroke: strokeColor,
          strokeWidth: selected ? strokeWidth + 1.5 : strokeWidth,
          opacity: selected ? 1 : 0.78,
        }}
        markerEnd={`url(#arrow-${e?.health || 'healthy'})`}
      />
      <path d={edgePath} fill="none" stroke={strokeColor} strokeWidth={1.5} className="animate-flow-dash opacity-60 pointer-events-none" />
      {e && (
        <EdgeLabelRenderer>
          <div
            style={{
              position: 'absolute',
              transform: `translate(-50%, -50%) translate(${labelX}px,${labelY}px)`,
              pointerEvents: 'all',
            }}
            className={`px-2 py-0.5 rounded-full text-[10px] font-mono border shadow-sm cursor-pointer transition whitespace-nowrap ${
              selected
                ? 'bg-blue-600 text-white border-blue-600 scale-105 z-20'
                : 'bg-white/95 dark:bg-slate-900/95 text-slate-700 dark:text-slate-200 border-slate-200 dark:border-slate-700 hover:border-blue-400'
            }`}
          >
            <span className="font-semibold">{e.primaryLabel}</span>
            <span className="mx-1 opacity-50">·</span>
            <span>{formatBps(e.bytesPerSec)}</span>
            {e.latencyAvgMs > 0 && (
              <>
                <span className="mx-1 opacity-50">·</span>
                <span>{formatLatency(e.latencyAvgMs)}</span>
              </>
            )}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}

const nodeTypes = { serviceNode: ServiceNodeCard };
const edgeTypes = { trafficEdge: TrafficFlowEdge };

function layoutGraph(graph: TopologyGraph): { nodes: Node[]; edges: Edge[] } {
  const g = new dagre.graphlib.Graph();
  g.setDefaultEdgeLabel(() => ({}));
  g.setGraph({ rankdir: 'LR', nodesep: 42, ranksep: 150, marginx: 30, marginy: 30 });

  for (const n of graph.nodes) {
    g.setNode(n.id, { width: 244, height: 112 });
  }
  for (const e of graph.edges) {
    g.setEdge(e.source, e.target);
  }
  dagre.layout(g);

  const nodes: Node[] = graph.nodes.map((n) => {
    const pos = g.node(n.id);
    return {
      id: n.id,
      type: 'serviceNode',
      position: { x: (pos?.x || 0) - 122, y: (pos?.y || 0) - 56 },
      initialWidth: 244,
      initialHeight: 112,
      data: { raw: n },
    };
  });

  const edges: Edge[] = graph.edges.map((e) => ({
    id: e.id,
    source: e.source,
    target: e.target,
    type: 'trafficEdge',
    data: { raw: e },
    markerEnd: { type: MarkerType.ArrowClosed, color: e.health === 'critical' ? '#EF4444' : e.health === 'warning' ? '#F59E0B' : '#10B981' },
  }));

  return { nodes, edges };
}

function AutoFitViewport({ fitKey, nodeCount }: { fitKey: string; nodeCount: number }) {
  const { fitView } = useReactFlow();
  const nodesInitialized = useNodesInitialized();
  const lastFittedRef = useRef<string>('');

  useEffect(() => {
    if (!nodesInitialized || nodeCount === 0) return;
    if (lastFittedRef.current === fitKey) return;
    let raf2 = 0;
    const raf1 = requestAnimationFrame(() => {
      raf2 = requestAnimationFrame(() => {
        lastFittedRef.current = fitKey;
        fitView({ padding: 0.18, maxZoom: 1.1, duration: 220 });
      });
    });
    return () => {
      cancelAnimationFrame(raf1);
      if (raf2) cancelAnimationFrame(raf2);
    };
  }, [fitKey, nodeCount, nodesInitialized, fitView]);

  return null;
}

interface TopologyPageProps {
  onLaunchTrace: (req: Partial<TraceFlowRequest>) => void;
  onLaunchTCPDump: (ns: string, pod: string) => void;
}

export function TopologyPage({ onLaunchTrace, onLaunchTCPDump }: TopologyPageProps) {
  const [granularity, setGranularity] = useState<'namespace' | 'workload' | 'pod' | 'node'>('namespace');
  const [windowSec, setWindowSec] = useState(300);
  const [namespace, setNamespace] = useState('all');
  const [protocol, setProtocol] = useState('');
  const [search, setSearch] = useState('');
  const [live, setLive] = useState(true);

  const [graph, setGraph] = useState<TopologyGraph | null>(null);
  const [graphFitKey, setGraphFitKey] = useState('');
  const [liveFlows, setLiveFlows] = useState<EnrichedFlow[]>([]);
  const [selectedEdge, setSelectedEdge] = useState<TopologyEdge | null>(null);
  const [drawerExpanded, setDrawerExpanded] = useState(true);
  const [loading, setLoading] = useState(true);
  const reqSeqRef = useRef(0);

  const filterKey = `${namespace}|${granularity}|${protocol}|${search}|${windowSec}`;

  // Clear stale edge inspector drawer when user switches namespace / granularity / filter
  useEffect(() => {
    setSelectedEdge(null);
  }, [namespace, granularity, protocol, search]);

  const load = useCallback(() => {
    const seq = ++reqSeqRef.current;
    const nsArg = namespace === 'all' ? [] : [namespace];
    const currentKey = filterKey;
    Promise.all([
      api.getTopology({ granularity, window: windowSec, namespaces: nsArg, search, protocol }),
      api.getFlows({ window: windowSec, namespace: namespace === 'all' ? '' : namespace, search, protocol, limit: 150 }),
    ])
      .then(([topo, fl]) => {
        if (seq !== reqSeqRef.current) return;
        setGraph(topo);
        setGraphFitKey(currentKey);
        setLiveFlows(fl.flows || []);
        setSelectedEdge((prev) => {
          if (!prev) return null;
          return topo.edges.find((e) => e.id === prev.id) || null;
        });
      })
      .finally(() => {
        if (seq === reqSeqRef.current) setLoading(false);
      });
  }, [granularity, windowSec, namespace, search, protocol, filterKey]);

  useEffect(() => {
    load();
    if (!live) return;
    const t = setInterval(load, 4000);
    return () => clearInterval(t);
  }, [load, live]);

  const { nodes, edges } = useMemo(() => {
    if (!graph) return { nodes: [], edges: [] };
    return layoutGraph(graph);
  }, [graph]);

  const jumpTraceFromFlow = (f: EnrichedFlow) => {
    const req: Partial<TraceFlowRequest> = {
      protocol: (f.protocol === 'UDP' || f.protocol === 'ICMP' ? f.protocol : 'TCP') as 'TCP' | 'UDP' | 'ICMP',
      dstPort: f.viaService?.port || f.dstPort,
    };
    if (f.src.kind === 'Pod' && f.src.namespace && f.src.podName) {
      req.srcNamespace = f.src.namespace;
      req.srcPod = f.src.podName;
    }
    if (f.viaService?.kind === 'Service' && f.viaService.namespace && f.viaService.serviceName) {
      req.targetType = 'service';
      req.dstNamespace = f.viaService.namespace;
      req.dstName = f.viaService.serviceName;
    } else if (f.dst.kind === 'Pod' && f.dst.namespace && f.dst.podName) {
      req.targetType = 'pod';
      req.dstNamespace = f.dst.namespace;
      req.dstName = f.dst.podName;
    } else if (f.dst.kind === 'Node' && f.dst.nodeName) {
      req.targetType = 'node';
      req.dstName = f.dst.nodeName;
    } else {
      req.targetType = 'ip';
      req.dstIP = f.dst.ip;
    }
    onLaunchTrace(req);
  };

  return (
    <div className="flex flex-col h-[calc(100vh-56px)] overflow-hidden">
      {/* Top Filter Bar */}
      <div className="px-4 py-2.5 bg-white dark:bg-slate-900 border-b border-slate-200 dark:border-slate-800 flex flex-wrap items-center justify-between gap-3 shrink-0">
        <div className="flex flex-wrap items-center gap-2">
          {/* Namespace selector */}
          <select
            value={namespace}
            onChange={(e) => {
              const v = e.target.value;
              setNamespace(v);
              setGranularity(v === 'all' ? 'namespace' : 'workload');
            }}
            className="px-2.5 py-1.5 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-medium outline-none focus:ring-2 focus:ring-blue-500"
          >
            <option value="all">全部命名空间</option>
            {(graph?.namespaces || []).map((ns) => (
              <option key={ns} value={ns}>{ns}</option>
            ))}
          </select>

          {/* Granularity segmented control */}
          <div className="inline-flex rounded-lg bg-slate-100 dark:bg-slate-800 p-0.5 border border-slate-200/70 dark:border-slate-700">
            {(['namespace', 'workload', 'pod', 'node'] as const).map((g) => (
              <button
                key={g}
                onClick={() => setGranularity(g)}
                className={`px-2.5 py-1 rounded-md text-xs font-medium transition ${
                  granularity === g
                    ? 'bg-white dark:bg-slate-900 text-blue-600 dark:text-blue-400 shadow-sm'
                    : 'text-slate-600 dark:text-slate-400 hover:text-slate-900 dark:hover:text-white'
                }`}
              >
                {g === 'namespace' ? 'Namespace' : g === 'workload' ? 'Workload' : g === 'pod' ? 'Pod' : 'Node'}
              </button>
            ))}
          </div>

          {/* Window */}
          <select
            value={windowSec}
            onChange={(e) => setWindowSec(Number(e.target.value))}
            className="px-2.5 py-1.5 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
          >
            <option value={60}>最近 1m</option>
            <option value={300}>最近 5m</option>
            <option value={900}>最近 15m</option>
            <option value={3600}>最近 1h</option>
          </select>

          {/* Protocol */}
          <select
            value={protocol}
            onChange={(e) => setProtocol(e.target.value)}
            className="px-2.5 py-1.5 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
          >
            <option value="">全部协议</option>
            <option value="TCP">TCP</option>
            <option value="UDP">UDP</option>
            <option value="ICMP">ICMP</option>
          </select>

          {/* Quick filter */}
          <div className="relative">
            <Filter className="w-3.5 h-3.5 text-slate-400 absolute left-2.5 top-1/2 -translate-y-1/2" />
            <input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="过滤 IP / 端口 / 名称..."
              className="pl-8 pr-3 py-1.5 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 w-44 outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
        </div>

        <div className="flex items-center gap-2 text-xs">
          {graph && (
            <div className="hidden xl:flex items-center gap-3 px-3 py-1 rounded-lg bg-slate-50 dark:bg-slate-800/80 border border-slate-200/70 dark:border-slate-700 font-mono text-[11px]">
              <span>节点: <b>{graph.summary.nodeCount}</b></span>
              <span>连线: <b>{graph.summary.edgeCount}</b></span>
              <span>吞吐: <b className="text-blue-600 dark:text-blue-400">{formatBps(graph.summary.totalBytesPerSec)}</b></span>
              <span>均值延迟: <b className="text-emerald-600 dark:text-emerald-400">{formatLatency(graph.summary.avgLatencyMs)}</b></span>
            </div>
          )}
          <button
            onClick={() => setLive((v) => !v)}
            className={`inline-flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-xs font-medium border transition ${
              live
                ? 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-950/50 dark:text-emerald-300 dark:border-emerald-800'
                : 'bg-slate-100 text-slate-600 border-slate-200 dark:bg-slate-800 dark:text-slate-300'
            }`}
          >
            {live ? <Pause className="w-3.5 h-3.5" /> : <Play className="w-3.5 h-3.5" />}
            {live ? 'Live' : '已暂停'}
          </button>
          <button
            onClick={load}
            className="p-1.5 rounded-lg border border-slate-200 dark:border-slate-700 hover:bg-slate-100 dark:hover:bg-slate-800"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
          </button>
        </div>
      </div>

      {/* Main Split Workspace: Canvas + Optional Right Edge Inspector */}
      <div className="flex-1 flex min-h-0 relative">
        <div className="flex-1 h-full bg-slate-50 dark:bg-[#0B1120] relative">
          {nodes.length === 0 ? (
            <div className="h-full flex flex-col items-center justify-center text-slate-400 text-xs">
              <p className="text-sm font-medium text-slate-500">当前筛选条件下暂无活跃流量边</p>
              <p className="mt-1">请等待 2~4 秒采集上报，或扩大时间窗口 / 切换到“全部命名空间”</p>
            </div>
          ) : (
            <ReactFlow
              key={graphFitKey}
              nodes={nodes}
              edges={edges}
              nodeTypes={nodeTypes}
              edgeTypes={edgeTypes}
              onEdgeClick={(_, edge) => setSelectedEdge((edge.data?.raw as TopologyEdge) || null)}
              onPaneClick={() => setSelectedEdge(null)}
              fitView
              fitViewOptions={{ padding: 0.18, maxZoom: 1.1 }}
              minZoom={0.1}
              maxZoom={2}
            >
              <AutoFitViewport fitKey={graphFitKey} nodeCount={nodes.length} />
              <Background gap={20} size={1} color="#94a3b8" className="opacity-25" />
              <Controls position="top-left" />
              <MiniMap position="bottom-right" nodeStrokeWidth={2} pannable zoomable />
            </ReactFlow>
          )}
        </div>

        {/* Edge Five-Tuple Inspector Drawer */}
        {selectedEdge && (
          <div className="w-[420px] border-l border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 flex flex-col h-full shadow-xl z-20">
            <div className="px-4 py-3 border-b border-slate-200 dark:border-slate-800 flex items-center justify-between gap-2">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <StatusPill status={selectedEdge.health} />
                  {selectedEdge.crossNode && (
                    <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-violet-50 text-violet-700 dark:bg-violet-950/60 dark:text-violet-300">
                      跨节点流量
                    </span>
                  )}
                </div>
                <div className="text-xs font-mono font-semibold text-slate-800 dark:text-slate-100 mt-1.5 truncate">
                  {selectedEdge.source} → {selectedEdge.target}
                </div>
              </div>
              <button
                onClick={() => setSelectedEdge(null)}
                className="p-1.5 rounded-lg hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-500"
              >
                <X className="w-4 h-4" />
              </button>
            </div>

            <div className="p-4 border-b border-slate-200 dark:border-slate-800 grid grid-cols-3 gap-2 text-xs">
              <MetricMini label="实时吞吐" value={formatBps(selectedEdge.bytesPerSec)} sub={`累计 ${formatBytes(selectedEdge.bytesTotal)}`} />
              <MetricMini label="平均 / P95 延迟" value={formatLatency(selectedEdge.latencyAvgMs)} sub={`P95 ${formatLatency(selectedEdge.latencyP95Ms)}`} />
              <MetricMini label="重传 / RST" value={`${selectedEdge.retransmits} / ${selectedEdge.resets}`} sub={`${selectedEdge.flowCount} 五元组`} />
            </div>

            <div className="flex-1 overflow-y-auto p-3 space-y-2">
              <div className="text-[11px] font-semibold uppercase tracking-wider text-slate-400 px-1">
                五元组连接明细 ({selectedEdge.fiveTuples.length})
              </div>
              {selectedEdge.fiveTuples.map((f) => (
                <div
                  key={f.id}
                  className="p-2.5 rounded-lg border border-slate-200 dark:border-slate-800 bg-slate-50/70 dark:bg-slate-950/60 space-y-1.5 text-[11px]"
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-mono font-semibold text-blue-600 dark:text-blue-400">
                      {f.protocol} :{f.dstPort}
                    </span>
                    <div className="flex items-center gap-1.5">
                      <EngineBadge engine={f.engine} />
                      <StatusPill status={f.health} />
                    </div>
                  </div>
                  <div className="font-mono text-slate-700 dark:text-slate-300 break-all">
                    {f.src.ip}:{f.srcPort} → {f.dst.ip}:{f.dstPort}
                  </div>
                  <div
                    className="text-slate-500 truncate"
                    title={`${f.src.displayName} → ${f.viaService ? `[${f.viaService.displayName}] → ` : ''}${f.dst.displayName}`}
                  >
                    {f.src.displayName} → {f.viaService ? `[${f.viaService.displayName}] → ` : ''}{f.dst.displayName}
                  </div>
                  <div className="flex items-center justify-between pt-1 border-t border-slate-200/70 dark:border-slate-800 font-mono text-[10px] text-slate-500">
                    <span>{formatBytes(f.bytesTotal)} ({formatBps(f.bytesPerSec)})</span>
                    <span>RTT {formatLatency(f.latencyAvgMs)}</span>
                    <div className="flex items-center gap-1">
                      <button
                        onClick={() => jumpTraceFromFlow(f)}
                        className="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded bg-blue-600 text-white hover:bg-blue-700"
                      >
                        <Route className="w-2.5 h-2.5" /> Trace
                      </button>
                      {f.src.kind === 'Pod' && f.src.namespace && f.src.podName && (
                        <button
                          onClick={() => onLaunchTCPDump(f.src.namespace!, f.src.podName!)}
                          className="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded bg-slate-200 dark:bg-slate-800 text-slate-700 dark:text-slate-200 hover:bg-slate-300"
                        >
                          <Terminal className="w-2.5 h-2.5" /> 抓包
                        </button>
                      )}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>

      {/* Collapsible Bottom Live 5-Tuple Flows Table */}
      <div className="border-t border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 shrink-0">
        <div
          onClick={() => setDrawerExpanded((v) => !v)}
          className="px-4 py-2 flex items-center justify-between cursor-pointer hover:bg-slate-50 dark:hover:bg-slate-800/50"
        >
          <div className="flex items-center gap-2 text-xs font-semibold text-slate-700 dark:text-slate-200">
            <span>实时五元组流表 (Live Flows)</span>
            <span className="px-1.5 py-0.5 rounded-full text-[10px] font-mono bg-blue-50 text-blue-700 dark:bg-blue-950 dark:text-blue-300">
              {liveFlows.length}
            </span>
          </div>
          {drawerExpanded ? <ChevronDown className="w-4 h-4 text-slate-400" /> : <ChevronUp className="w-4 h-4 text-slate-400" />}
        </div>

        {drawerExpanded && (
          <div className="h-[220px] overflow-auto border-t border-slate-100 dark:border-slate-800">
            <table className="w-full text-[11px] font-mono">
              <thead className="bg-slate-50 dark:bg-slate-800/70 text-slate-500 sticky top-0 z-10">
                <tr>
                  <th className="text-left px-3 py-1.5 font-medium">源端点 (Src Pod / IP:Port)</th>
                  <th className="text-left px-3 py-1.5 font-medium">目标端点 (Via SVC → Dst Pod / IP:Port)</th>
                  <th className="text-left px-3 py-1.5 font-medium">协议</th>
                  <th className="text-right px-3 py-1.5 font-medium">流量大小 / 速率</th>
                  <th className="text-right px-3 py-1.5 font-medium">平均 / 最大延迟</th>
                  <th className="text-right px-3 py-1.5 font-medium">重传 / RST</th>
                  <th className="text-left px-3 py-1.5 font-medium">引擎</th>
                  <th className="text-right px-3 py-1.5 font-medium">排障</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100 dark:divide-slate-800/80">
                {liveFlows.slice(0, 80).map((f, idx) => {
                  const dstFull = `${f.viaService ? `[${f.viaService.serviceName}] → ` : ''}${f.dst.displayName}`;
                  const tipPos = idx === 0 ? 'top-full mt-1' : 'bottom-full mb-1';
                  return (
                    <tr key={f.id} className="hover:bg-blue-50/40 dark:hover:bg-slate-800/50">
                      <td className="px-3 py-1.5">
                        <div className="relative group inline-block max-w-[230px] align-top">
                          <div
                            title={`${f.src.displayName} (${f.src.ip}:${f.srcPort})`}
                            className="text-slate-800 dark:text-slate-100 font-sans font-medium truncate cursor-default"
                          >
                            {f.src.displayName}
                          </div>
                          <div className={`pointer-events-none hidden group-hover:block absolute left-0 ${tipPos} z-30 w-max max-w-[460px] px-2.5 py-1.5 rounded-lg bg-slate-900 dark:bg-slate-800 text-white text-[11px] font-mono shadow-lg border border-slate-700 break-all`}>
                            <div>{f.src.displayName}</div>
                            <div className="text-[10px] text-slate-300 mt-0.5">{f.src.ip}:{f.srcPort}</div>
                          </div>
                        </div>
                        <div className="text-[10px] text-slate-400">{f.src.ip}:{f.srcPort}</div>
                      </td>
                      <td className="px-3 py-1.5">
                        <div className="relative group inline-block max-w-[260px] align-top">
                          <div
                            title={`${dstFull} (${f.dst.ip}:${f.dstPort})`}
                            className="text-slate-800 dark:text-slate-100 font-sans font-medium truncate cursor-default"
                          >
                            {f.viaService ? <span className="text-teal-600 dark:text-teal-400">[{f.viaService.serviceName}] → </span> : null}
                            {f.dst.displayName}
                          </div>
                          <div className={`pointer-events-none hidden group-hover:block absolute left-0 ${tipPos} z-30 w-max max-w-[460px] px-2.5 py-1.5 rounded-lg bg-slate-900 dark:bg-slate-800 text-white text-[11px] font-mono shadow-lg border border-slate-700 break-all`}>
                            <div>{dstFull}</div>
                            <div className="text-[10px] text-slate-300 mt-0.5">{f.dst.ip}:{f.dstPort}</div>
                          </div>
                        </div>
                        <div className="text-[10px] text-slate-400">{f.dst.ip}:{f.dstPort}</div>
                      </td>
                    <td className="px-3 py-1.5">
                      <span className="px-1.5 py-0.5 rounded bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-200">
                        {f.protocol}
                      </span>
                    </td>
                    <td className="px-3 py-1.5 text-right">
                      <div>{formatBytes(f.bytesTotal)}</div>
                      <div className="text-[10px] text-slate-400">{formatBps(f.bytesPerSec)}</div>
                    </td>
                    <td className="px-3 py-1.5 text-right">
                      <span className={f.latencyAvgMs > 25 ? 'text-amber-600 font-semibold' : 'text-emerald-600 dark:text-emerald-400'}>
                        {formatLatency(f.latencyAvgMs)}
                      </span>
                      <div className="text-[10px] text-slate-400">max {formatLatency(f.latencyMaxMs)}</div>
                    </td>
                    <td className="px-3 py-1.5 text-right">
                      <span className={f.retransmits > 0 || f.resets > 0 ? 'text-rose-600 font-semibold' : 'text-slate-400'}>
                        {f.retransmits} / {f.resets}
                      </span>
                    </td>
                    <td className="px-3 py-1.5">
                      <span className={`text-[10px] px-1.5 py-0.5 rounded font-medium ${f.engine === 'ebpf' ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-300' : 'bg-sky-50 text-sky-700 dark:bg-sky-950/60 dark:text-sky-300'}`}>
                        {f.engine === 'ebpf' ? 'ebpf' : 'legacy'}
                      </span>
                    </td>
                      <td className="px-3 py-1.5 text-right">
                        <button
                          onClick={() => jumpTraceFromFlow(f)}
                          className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-blue-50 text-blue-700 hover:bg-blue-100 dark:bg-blue-950/60 dark:text-blue-300 font-sans font-medium"
                        >
                          <Route className="w-3 h-3" /> TraceFlow
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}

function MetricMini({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <div className="p-2 rounded-lg bg-slate-50 dark:bg-slate-950/60 border border-slate-200/80 dark:border-slate-800">
      <div className="text-[10px] text-slate-400">{label}</div>
      <div className="text-xs font-bold font-mono text-slate-900 dark:text-white mt-0.5">{value}</div>
      <div className="text-[10px] text-slate-400 mt-0.5 truncate">{sub}</div>
    </div>
  );
}
