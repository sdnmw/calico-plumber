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
  AlertTriangle,
  CheckCircle2,
  BookmarkPlus,
  Eye,
  Compass,
  Info,
} from 'lucide-react';
import { api } from '../api/client';
import type { EnrichedFlow, TopologyEdge, TopologyGraph, TopologyNode, TraceFlowRequest } from '../api/types';
import type { TopologyContextSnapshot } from '../App';
import { EngineBadge, StatusPill } from '../components/common/Badges';
import { formatBps, formatBytes, formatLatency } from '../utils/format';

function formatWindowLabel(sec: number): string {
  if (sec < 60) return `${sec}s`;
  if (sec % 3600 === 0) return `${sec / 3600}h`;
  return `${Math.round(sec / 60)}m`;
}

// Custom Service / Workload / Pod Card Node
function ServiceNodeCard({ data, selected }: NodeProps) {
  const n = data.raw as TopologyNode;
  const isFocal = Boolean(data.isFocal);
  const accent =
    n.kind === 'Service'
      ? 'border-l-[#F08C18]'
      : n.kind === 'External'
        ? 'border-l-[#D9720B]'
        : n.kind === 'Node'
          ? 'border-l-[#8C7F73]'
          : n.kind === 'DaemonSet'
            ? 'border-l-[#B85A08]'
            : n.kind === 'StatefulSet'
              ? 'border-l-[#9E4A05]'
              : 'border-l-[#F08C18]';

  const healthDot =
    n.health === 'critical' ? 'bg-rose-500' : n.health === 'warning' ? 'bg-amber-500' : 'bg-emerald-500';

  const Icon =
    n.kind === 'Service' ? Server : n.kind === 'External' ? Globe : n.kind === 'Node' ? Cpu : n.kind === 'Pod' ? Box : Layers;

  return (
    <div
      className={`w-[238px] rounded-xl bg-white dark:bg-[#1C1815] border border-l-[3.5px] ${accent} ${
        selected || isFocal
          ? 'ring-2 ring-[#F08C18] border-[#F08C18] shadow-lg'
          : 'border-[#E9E1D6] dark:border-[#2E2721] shadow-sm hover:shadow-md'
      } transition-all`}
    >
      <Handle type="target" position={Position.Left} className="!w-2 !h-2 !bg-[#F08C18] !border-white dark:!border-[#1C1815]" />
      <Handle type="source" position={Position.Right} className="!w-2 !h-2 !bg-[#F08C18] !border-white dark:!border-[#1C1815]" />

      <div className="px-3 py-2 border-b border-[#F2ECE4] dark:border-[#2E2721] flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 min-w-0">
          <Icon className="w-3.5 h-3.5 text-[#8C7F73] shrink-0" />
          <span className="text-xs font-semibold text-[#201B18] dark:text-[#F5F0EA] truncate font-mono" title={n.label}>
            {n.label}
          </span>
        </div>
        <span className={`w-2 h-2 rounded-full shrink-0 ${healthDot}`} />
      </div>

      <div className="px-3 py-2 space-y-1.5">
        <div className="flex items-center justify-between gap-1 text-[10px]">
          <span className="px-1.5 py-0.5 rounded bg-[#F7F3EE] dark:bg-[#25201C] text-[#574D44] dark:text-[#D5C9BC] font-medium truncate max-w-[130px]">
            {n.namespace || n.kind}
          </span>
          <span className="text-[#8C7F73] font-mono">
            {n.podCount ? `${n.podCount} pods` : n.ip || n.kind}
          </span>
        </div>

        {n.ports && n.ports.length > 0 && (
          <div className="flex flex-wrap gap-1">
            {n.ports.slice(0, 3).map((pt) => (
              <span
                key={pt}
                className="px-1.5 py-0.5 rounded text-[10px] font-mono bg-[#FFF4E4] dark:bg-[#F08C18]/18 text-[#9E4A05] dark:text-[#FBB040] border border-[#FAD4A1] dark:border-[#F08C18]/40"
              >
                {pt}
              </span>
            ))}
            {n.ports.length > 3 && <span className="text-[10px] text-[#8C7F73]">+{n.ports.length - 3}</span>}
          </div>
        )}

        <div className="pt-1 border-t border-[#F2ECE4] dark:border-[#2E2721] flex items-center justify-between text-[10px] font-mono text-[#8C7F73]">
          <span>↓ {formatBps(n.inboundBps)}</span>
          <span>↑ {formatBps(n.outboundBps)}</span>
          <span className={n.avgLatencyMs > 25 ? 'text-[#D9720B] font-semibold' : 'text-emerald-600 dark:text-emerald-400'}>
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
  const isFocal = Boolean(data?.isFocal);
  const [edgePath, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  });

  const active = selected || isFocal;
  const strokeColor =
    e?.health === 'critical' ? '#EF4444' : e?.health === 'warning' ? '#F08C18' : active ? '#F08C18' : '#10B981';

  const bps = e?.bytesPerSec || 0;
  const strokeWidth = Math.min(5.5, Math.max(1.5, 1.5 + Math.log10(Math.max(1, bps / 500))));

  return (
    <>
      <BaseEdge
        id={id}
        path={edgePath}
        style={{
          stroke: strokeColor,
          strokeWidth: active ? strokeWidth + 1.8 : strokeWidth,
          opacity: active ? 1 : 0.72,
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
              active
                ? e.health === 'critical'
                  ? 'bg-rose-600 text-white border-rose-700 scale-105 z-20'
                  : 'bg-[#F08C18] text-white border-[#D9720B] scale-105 z-20'
                : 'bg-white/95 dark:bg-[#1C1815]/95 text-[#201B18] dark:text-[#F5F0EA] border-[#E9E1D6] dark:border-[#2E2721] hover:border-[#F08C18]'
            }`}
          >
            <span className="font-semibold">{e.primaryLabel}</span>
            {e.retransmits > 0 && (
              <>
                <span className="mx-1 opacity-60">·</span>
                <span className={active ? 'font-bold' : 'text-rose-600 dark:text-rose-400 font-semibold'}>
                  重传 {e.retransmits}
                </span>
              </>
            )}
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

function layoutGraph(
  graphNodes: TopologyNode[],
  graphEdges: TopologyEdge[],
  focalEdge?: TopologyEdge | null
): { nodes: Node[]; edges: Edge[] } {
  const g = new dagre.graphlib.Graph();
  g.setDefaultEdgeLabel(() => ({}));
  g.setGraph({ rankdir: 'LR', nodesep: 48, ranksep: 165, marginx: 36, marginy: 36 });

  for (const n of graphNodes) {
    g.setNode(n.id, { width: 244, height: 112 });
  }
  for (const e of graphEdges) {
    g.setEdge(e.source, e.target);
  }
  dagre.layout(g);

  const nodes: Node[] = graphNodes.map((n) => {
    const pos = g.node(n.id);
    const isFocal = Boolean(focalEdge && (n.id === focalEdge.source || n.id === focalEdge.target));
    return {
      id: n.id,
      type: 'serviceNode',
      position: { x: (pos?.x || 0) - 122, y: (pos?.y || 0) - 56 },
      initialWidth: 244,
      initialHeight: 112,
      data: { raw: n, isFocal },
    };
  });

  const edges: Edge[] = graphEdges.map((e) => {
    const isFocal = Boolean(focalEdge && e.id === focalEdge.id);
    return {
      id: e.id,
      source: e.source,
      target: e.target,
      type: 'trafficEdge',
      selected: isFocal,
      data: { raw: e, isFocal },
      markerEnd: {
        type: MarkerType.ArrowClosed,
        color: e.health === 'critical' ? '#EF4444' : e.health === 'warning' ? '#F08C18' : '#10B981',
      },
    };
  });

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
        fitView({ padding: 0.2, maxZoom: 1.08, duration: 220 });
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
  initialContext?: TopologyContextSnapshot | null;
  onLaunchTrace: (req: Partial<TraceFlowRequest>, fromTopo?: TopologyContextSnapshot) => void;
  onLaunchTCPDump: (ns: string, pod: string) => void;
}

export function TopologyPage({ initialContext, onLaunchTrace, onLaunchTCPDump }: TopologyPageProps) {
  const [viewMode, setViewMode] = useState<'anomaly' | 'panorama'>(initialContext?.viewMode || 'anomaly');
  const [granularity, setGranularity] = useState<'namespace' | 'workload' | 'pod' | 'node'>(
    initialContext?.granularity || 'namespace'
  );
  const [windowSec, setWindowSec] = useState(initialContext?.windowSec || 300);
  const [namespace, setNamespace] = useState(initialContext?.namespace || 'all');
  const [protocol, setProtocol] = useState('');
  const [search, setSearch] = useState('');
  const [live, setLive] = useState(true);

  const [graph, setGraph] = useState<TopologyGraph | null>(null);
  const [liveFlows, setLiveFlows] = useState<EnrichedFlow[]>([]);
  const [selectedAnomalyEdgeId, setSelectedAnomalyEdgeId] = useState<string | null>(
    initialContext?.selectedEdgeId || null
  );
  const [selectedEdge, setSelectedEdge] = useState<TopologyEdge | null>(null);
  const [drawerExpanded, setDrawerExpanded] = useState(true);
  const [loading, setLoading] = useState(true);
  const [savingIncidentId, setSavingIncidentId] = useState<string | null>(null);
  const [savedIncidentEdgeIds, setSavedIncidentEdgeIds] = useState<Record<string, boolean>>({});
  const reqSeqRef = useRef(0);

  // Sync restored context when returning from TraceFlow
  useEffect(() => {
    if (!initialContext) return;
    setViewMode(initialContext.viewMode || 'anomaly');
    setGranularity(initialContext.granularity || 'namespace');
    setWindowSec(initialContext.windowSec || 300);
    setNamespace(initialContext.namespace || 'all');
    if (initialContext.selectedEdgeId) {
      setSelectedAnomalyEdgeId(initialContext.selectedEdgeId);
    }
  }, [initialContext]);

  const filterKey = `${namespace}|${granularity}|${protocol}|${search}|${windowSec}`;

  // Clear stale edge inspector drawer when user switches namespace / granularity / filter
  useEffect(() => {
    setSelectedEdge(null);
  }, [namespace, granularity, protocol, search]);

  const load = useCallback(() => {
    const seq = ++reqSeqRef.current;
    const nsArg = namespace === 'all' ? [] : [namespace];
    Promise.all([
      api.getTopology({ granularity, window: windowSec, namespaces: nsArg, search, protocol }),
      api.getFlows({ window: windowSec, namespace: namespace === 'all' ? '' : namespace, search, protocol, limit: 150 }),
    ])
      .then(([topo, fl]) => {
        if (seq !== reqSeqRef.current) return;
        setGraph(topo);
        setLiveFlows(fl.flows || []);
        setSelectedEdge((prev) => {
          if (!prev) return null;
          return topo.edges.find((e) => e.id === prev.id) || null;
        });
      })
      .finally(() => {
        if (seq === reqSeqRef.current) setLoading(false);
      });
  }, [granularity, windowSec, namespace, search, protocol]);

  useEffect(() => {
    load();
    if (!live) return;
    const t = setInterval(load, 4000);
    return () => clearInterval(t);
  }, [load, live]);

  // Human-readable node label lookup
  const nodeLabelMap = useMemo(() => {
    const m = new Map<string, TopologyNode>();
    for (const n of graph?.nodes || []) {
      m.set(n.id, n);
    }
    return m;
  }, [graph]);

  // Ranked anomalous edges for the default '看异常连接' view
  const anomalousEdges = useMemo(() => {
    if (!graph) return [];
    const list = graph.edges.filter(
      (e) =>
        (e.anomalyScore && e.anomalyScore > 0) ||
        e.retransmits > 0 ||
        e.resets > 0 ||
        e.health !== 'healthy' ||
        e.latencyAvgMs >= 25
    );
    return [...list].sort((a, b) => {
      const sa = a.anomalyScore || 0;
      const sb = b.anomalyScore || 0;
      if (sb !== sa) return sb - sa;
      if (b.retransmits !== a.retransmits) return b.retransmits - a.retransmits;
      if (b.resets !== a.resets) return b.resets - a.resets;
      return b.latencyP95Ms - a.latencyP95Ms;
    });
  }, [graph]);

  // Keep selectedAnomalyEdgeId pointing to a valid edge
  const focusedAnomalyEdge = useMemo(() => {
    if (anomalousEdges.length === 0) return null;
    if (selectedAnomalyEdgeId) {
      const match = anomalousEdges.find((e) => e.id === selectedAnomalyEdgeId);
      if (match) return match;
    }
    return anomalousEdges[0];
  }, [anomalousEdges, selectedAnomalyEdgeId]);

  useEffect(() => {
    if (focusedAnomalyEdge && focusedAnomalyEdge.id !== selectedAnomalyEdgeId) {
      setSelectedAnomalyEdgeId(focusedAnomalyEdge.id);
    }
  }, [focusedAnomalyEdge, selectedAnomalyEdgeId]);

  // Compute displayed nodes/edges based on viewMode
  const { displayNodes, displayEdges } = useMemo(() => {
    if (!graph) return { displayNodes: [] as TopologyNode[], displayEdges: [] as TopologyEdge[] };
    if (viewMode === 'panorama' || !focusedAnomalyEdge) {
      return { displayNodes: graph.nodes, displayEdges: graph.edges };
    }
    // Causal 1-hop upstream/downstream subgraph around focusedAnomalyEdge:
    // - The selected connection (and any reverse edge between the same pair)
    // - Up to 4 top upstream callers into source (e.target === src)
    // - Up to 4 top downstream callees from target (e.source === dst)
    const src = focusedAnomalyEdge.source;
    const dst = focusedAnomalyEdge.target;
    const pairEdges = graph.edges.filter(
      (e) => (e.source === src && e.target === dst) || (e.source === dst && e.target === src)
    );
    const rankEdge = (a: TopologyEdge, b: TopologyEdge) =>
      (b.anomalyScore || 0) - (a.anomalyScore || 0) || b.retransmits - a.retransmits || b.bytesPerSec - a.bytesPerSec;
    const upstreamEdges = graph.edges
      .filter((e) => e.target === src && e.source !== dst && e.source !== src)
      .sort(rankEdge)
      .slice(0, 4);
    const downstreamEdges = graph.edges
      .filter((e) => e.source === dst && e.target !== src && e.target !== dst)
      .sort(rankEdge)
      .slice(0, 4);
    const edgeMap = new Map<string, TopologyEdge>();
    for (const e of [...pairEdges, ...upstreamEdges, ...downstreamEdges]) {
      edgeMap.set(e.id, e);
    }
    const subEdges = Array.from(edgeMap.values());
    const subNodeIds = new Set<string>([src, dst]);
    for (const e of subEdges) {
      subNodeIds.add(e.source);
      subNodeIds.add(e.target);
    }
    const subNodes = graph.nodes.filter((n) => subNodeIds.has(n.id));
    return { displayNodes: subNodes, displayEdges: subEdges };
  }, [graph, viewMode, focusedAnomalyEdge]);

  const { nodes, edges } = useMemo(() => {
    if (displayNodes.length === 0) return { nodes: [], edges: [] };
    return layoutGraph(displayNodes, displayEdges, viewMode === 'anomaly' ? focusedAnomalyEdge : selectedEdge);
  }, [displayNodes, displayEdges, viewMode, focusedAnomalyEdge, selectedEdge]);

  const effectiveFitKey = `${filterKey}|${viewMode}|${viewMode === 'anomaly' ? focusedAnomalyEdge?.id || 'none' : 'all'}`;

  const currentTopoSnapshot = useCallback(
    (edgeId?: string | null): TopologyContextSnapshot => ({
      windowSec,
      namespace,
      granularity,
      viewMode,
      selectedEdgeId: edgeId ?? focusedAnomalyEdge?.id ?? selectedEdge?.id ?? null,
    }),
    [windowSec, namespace, granularity, viewMode, focusedAnomalyEdge, selectedEdge]
  );

  const jumpTraceFromFlow = (f: EnrichedFlow, edgeId?: string) => {
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
    onLaunchTrace(req, currentTopoSnapshot(edgeId));
  };

  const jumpTraceFromEdge = (edge: TopologyEdge) => {
    // Pick the most anomalous five-tuple on this edge so TraceFlow inherits exact src/dst/proto/port
    const sortedTuples = [...(edge.fiveTuples || [])].sort((a, b) => {
      if (b.retransmits !== a.retransmits) return b.retransmits - a.retransmits;
      if (b.resets !== a.resets) return b.resets - a.resets;
      return b.latencyMaxMs - a.latencyMaxMs;
    });
    if (sortedTuples.length > 0) {
      jumpTraceFromFlow(sortedTuples[0], edge.id);
      return;
    }
    onLaunchTrace(
      {
        protocol: (edge.protocols?.[0] === 'UDP' ? 'UDP' : 'TCP') as 'TCP' | 'UDP' | 'ICMP',
        dstPort: edge.dstPorts?.[0] || 80,
      },
      currentTopoSnapshot(edge.id)
    );
  };

  const saveEdgeIncident = async (edge: TopologyEdge) => {
    setSavingIncidentId(edge.id);
    try {
      const topFlow = [...(edge.fiveTuples || [])].sort((a, b) => b.retransmits - a.retransmits)[0];
      const srcLabel = nodeLabelMap.get(edge.source)?.label || edge.source;
      const dstLabel = nodeLabelMap.get(edge.target)?.label || edge.target;
      const winLabel = formatWindowLabel(edge.windowSec || windowSec);
      const summaryParts = [`最近 ${winLabel} 重传 ${edge.retransmits} 次 / RST ${edge.resets} 次`];
      if (edge.baselineAvailable && edge.retransmitDeltaPct !== undefined) {
        summaryParts.push(
          edge.prevRetransmits === 0
            ? `前 ${winLabel} 为 0 次（新增）`
            : `较前 ${winLabel} ${edge.retransmitDeltaPct >= 0 ? '+' : ''}${edge.retransmitDeltaPct.toFixed(0)}%`
        );
      }
      if (edge.rateNote) summaryParts.push(edge.rateNote);

      await api.createIncident({
        title: `连接异常：${srcLabel} → ${dstLabel} (${edge.primaryLabel})`,
        kind: 'flow_anomaly',
        srcNamespace: topFlow?.src.namespace,
        srcPod: topFlow?.src.podName,
        targetNamespace: topFlow?.viaService?.namespace || topFlow?.dst.namespace,
        targetService: topFlow?.viaService?.serviceName,
        targetClusterIP: topFlow?.viaService?.ip,
        targetBackendPod: topFlow?.dst.podName,
        targetBackendIP: topFlow?.dst.ip,
        servicePort: topFlow?.viaService?.port || topFlow?.dstPort || edge.dstPorts?.[0],
        backendPort: topFlow?.dstPort || edge.dstPorts?.[0],
        protocol: topFlow?.protocol || edge.protocols?.[0] || 'TCP',
        failureKind: edge.resets > 0 ? 'reset' : 'retransmit',
        failureSummary: summaryParts.join(' · '),
        initialLatencyMs: edge.latencyAvgMs,
      });
      setSavedIncidentEdgeIds((prev) => ({ ...prev, [edge.id]: true }));
    } catch {
      // ignore error toast to avoid noise
    } finally {
      setSavingIncidentId(null);
    }
  };

  const windowLabel = formatWindowLabel(windowSec);

  return (
    <div className="flex flex-col h-[calc(100vh-56px)] overflow-hidden">
      {/* Top Filter Bar */}
      <div className="px-4 py-2.5 bg-white dark:bg-[#1C1815] border-b border-[#E9E1D6] dark:border-[#2E2721] flex flex-wrap items-center justify-between gap-3 shrink-0">
        <div className="flex flex-wrap items-center gap-2">
          {/* View mode toggle: 看异常连接 (default) vs 全景视图 */}
          <div className="inline-flex rounded-lg bg-[#F7F3EE] dark:bg-[#25201C] p-0.5 border border-[#E9E1D6] dark:border-[#2E2721]">
            <button
              onClick={() => setViewMode('anomaly')}
              className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs font-medium border transition ${
                viewMode === 'anomaly'
                  ? 'bg-[#FFF4E4] text-[#9E4A05] border-[#FAD4A1] dark:bg-[#F08C18]/20 dark:text-[#FBB040] dark:border-[#F08C18]/40 font-semibold shadow-xs'
                  : 'border-transparent text-[#574D44] dark:text-[#A39587] hover:text-[#201B18] dark:hover:text-[#F5F0EA]'
              }`}
            >
              <AlertTriangle className="w-3.5 h-3.5 text-[#D9720B] dark:text-[#FBB040]" />
              看异常连接
              <span
                className={`px-1.5 py-0.2 rounded-full text-[10px] font-mono ${
                  anomalousEdges.length > 0
                    ? 'bg-rose-100 text-rose-700 dark:bg-rose-950/60 dark:text-rose-300'
                    : 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-300'
                }`}
              >
                {anomalousEdges.length}
              </span>
            </button>
            <button
              onClick={() => setViewMode('panorama')}
              className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs font-medium border transition ${
                viewMode === 'panorama'
                  ? 'bg-[#FFF4E4] text-[#9E4A05] border-[#FAD4A1] dark:bg-[#F08C18]/20 dark:text-[#FBB040] dark:border-[#F08C18]/40 font-semibold shadow-xs'
                  : 'border-transparent text-[#574D44] dark:text-[#A39587] hover:text-[#201B18] dark:hover:text-[#F5F0EA]'
              }`}
            >
              <Compass className="w-3.5 h-3.5" />
              全景视图
            </button>
          </div>

          {/* Namespace selector */}
          <select
            value={namespace}
            onChange={(e) => {
              const v = e.target.value;
              setNamespace(v);
              setGranularity(v === 'all' ? 'namespace' : 'workload');
            }}
            className="px-2.5 py-1.5 text-xs rounded-lg bg-[#F7F3EE] dark:bg-[#25201C] border border-[#E9E1D6] dark:border-[#2E2721] font-medium outline-none focus:ring-2 focus:ring-[#F08C18]"
          >
            <option value="all">全部命名空间</option>
            {(graph?.namespaces || []).map((ns) => (
              <option key={ns} value={ns}>
                {ns}
              </option>
            ))}
          </select>

          {/* Granularity segmented control */}
          <div className="inline-flex rounded-lg bg-[#F7F3EE] dark:bg-[#25201C] p-0.5 border border-[#E9E1D6] dark:border-[#2E2721]">
            {(['namespace', 'workload', 'pod', 'node'] as const).map((g) => (
              <button
                key={g}
                onClick={() => setGranularity(g)}
                className={`px-2.5 py-1 rounded-md text-xs font-medium transition ${
                  granularity === g
                    ? 'bg-[#FFF4E4] dark:bg-[#F08C18]/20 text-[#9E4A05] dark:text-[#FBB040] shadow-sm font-semibold'
                    : 'text-[#574D44] dark:text-[#A39587] hover:text-[#201B18] dark:hover:text-[#F5F0EA]'
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
            className="px-2.5 py-1.5 text-xs rounded-lg bg-[#F7F3EE] dark:bg-[#25201C] border border-[#E9E1D6] dark:border-[#2E2721] font-mono outline-none"
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
            className="px-2.5 py-1.5 text-xs rounded-lg bg-[#F7F3EE] dark:bg-[#25201C] border border-[#E9E1D6] dark:border-[#2E2721] font-mono outline-none"
          >
            <option value="">全部协议</option>
            <option value="TCP">TCP</option>
            <option value="UDP">UDP</option>
            <option value="ICMP">ICMP</option>
          </select>

          {/* Quick filter */}
          <div className="relative">
            <Filter className="w-3.5 h-3.5 text-[#8C7F73] absolute left-2.5 top-1/2 -translate-y-1/2" />
            <input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="过滤 IP / 端口 / 名称..."
              className="pl-8 pr-3 py-1.5 text-xs rounded-lg bg-[#F7F3EE] dark:bg-[#25201C] border border-[#E9E1D6] dark:border-[#2E2721] w-40 outline-none focus:ring-2 focus:ring-[#F08C18]"
            />
          </div>
        </div>

        <div className="flex items-center gap-2 text-xs">
          {graph && (
            <div className="hidden xl:flex items-center gap-3 px-3 py-1 rounded-lg bg-[#F7F3EE] dark:bg-[#25201C] border border-[#E9E1D6] dark:border-[#2E2721] font-mono text-[11px]">
              <span>
                全局: <b>{graph.summary.nodeCount}</b> 节点 / <b>{graph.summary.edgeCount}</b> 连线
              </span>
              <span>
                吞吐: <b className="text-[#9E4A05] dark:text-[#FBB040]">{formatBps(graph.summary.totalBytesPerSec)}</b>
              </span>
              <span>
                均值延迟: <b className="text-emerald-600 dark:text-emerald-400">{formatLatency(graph.summary.avgLatencyMs)}</b>
              </span>
            </div>
          )}
          <button
            onClick={() => setLive((v) => !v)}
            className={`inline-flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-xs font-medium border transition ${
              live
                ? 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-950/50 dark:text-emerald-300 dark:border-emerald-800'
                : 'bg-[#F7F3EE] text-[#574D44] border-[#E9E1D6] dark:bg-[#25201C] dark:text-[#D5C9BC] dark:border-[#2E2721]'
            }`}
          >
            {live ? <Pause className="w-3.5 h-3.5" /> : <Play className="w-3.5 h-3.5" />}
            {live ? 'Live' : '已暂停'}
          </button>
          <button
            onClick={load}
            className="p-1.5 rounded-lg border border-[#E9E1D6] dark:border-[#2E2721] hover:bg-[#FFF4E4] hover:border-[#FAD4A1] dark:hover:bg-[#25201C]"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
          </button>
        </div>
      </div>

      {/* Main Split Workspace: Optional Left Anomaly List + Canvas + Optional Right Edge Inspector */}
      <div className="flex-1 flex min-h-0 relative">
        {/* Left Anomaly Ranking List (Only in '看异常连接' mode) */}
        {viewMode === 'anomaly' && (
          <div className="w-[376px] shrink-0 border-r border-[#E9E1D6] dark:border-[#2E2721] bg-white dark:bg-[#1C1815] flex flex-col h-full">
            <div className="px-3.5 py-2.5 border-b border-[#E9E1D6] dark:border-[#2E2721] bg-[#FAF6F1] dark:bg-[#231D18]">
              <div className="flex items-center justify-between gap-2">
                <span className="text-xs font-bold text-[#201B18] dark:text-[#F5F0EA]">
                  异常连接（最近 {windowLabel}）
                </span>
                <span className="text-[11px] font-mono text-[#8C7F73]">{anomalousEdges.length} 条</span>
              </div>
              <p className="text-[11px] text-[#8C7F73] mt-0.5 leading-snug">
                按重传、RST 与延迟排序，右侧仅展开选中连接及上下游 1 跳
              </p>
            </div>

            <div className="flex-1 overflow-y-auto p-2.5 space-y-2">
              {anomalousEdges.length === 0 ? (
                <div className="p-4 rounded-xl border border-emerald-200 dark:border-emerald-800/60 bg-emerald-50/70 dark:bg-emerald-950/20 text-center space-y-2.5 mt-2">
                  <CheckCircle2 className="w-7 h-7 text-emerald-600 dark:text-emerald-400 mx-auto" />
                  <div className="text-xs font-semibold text-emerald-900 dark:text-emerald-200">
                    最近 {windowLabel} 未观测到明显异常连接
                  </div>
                  <p className="text-[11px] text-emerald-700/90 dark:text-emerald-300/80 leading-relaxed">
                    当前窗口内无重传激增、TCP RST 或高延迟连线。右侧画布已自动展示全景连接。
                  </p>
                  <div className="flex items-center justify-center gap-2 pt-1">
                    {windowSec < 900 && (
                      <button
                        onClick={() => setWindowSec(900)}
                        className="px-2.5 py-1 rounded-lg text-[11px] font-medium bg-white dark:bg-[#1C1815] border border-emerald-300 dark:border-emerald-700 text-emerald-800 dark:text-emerald-300 hover:bg-emerald-50"
                      >
                        扩大到最近 15m
                      </button>
                    )}
                    <button
                      onClick={() => setViewMode('panorama')}
                      className="px-2.5 py-1 rounded-lg text-[11px] font-semibold bg-[#FFF4E4] text-[#9E4A05] border border-[#FAD4A1] hover:bg-[#FDE6C4] dark:bg-[#F08C18]/20 dark:text-[#FBB040] dark:border-[#F08C18]/40 transition"
                    >
                      切换到全景视图
                    </button>
                  </div>
                </div>
              ) : (
                anomalousEdges.map((edge) => {
                  const isSelected = focusedAnomalyEdge?.id === edge.id;
                  const srcLabel = nodeLabelMap.get(edge.source)?.label || edge.source;
                  const dstLabel = nodeLabelMap.get(edge.target)?.label || edge.target;
                  const edgeWinLabel = formatWindowLabel(edge.windowSec || windowSec);
                  const saved = savedIncidentEdgeIds[edge.id];

                  return (
                    <div
                      key={edge.id}
                      onClick={() => setSelectedAnomalyEdgeId(edge.id)}
                      className={`p-3 rounded-xl border cursor-pointer transition space-y-2 ${
                        isSelected
                          ? 'bg-[#FFF8EE] dark:bg-[#2A2017] border-[#F08C18] ring-1 ring-[#F08C18]/60 shadow-sm'
                          : 'bg-[#FAF6F1]/70 dark:bg-[#161310] border-[#E9E1D6] dark:border-[#2E2721] hover:border-[#FAD4A1] dark:hover:border-[#5C4530]'
                      }`}
                    >
                      {/* Header: endpoints + status */}
                      <div className="flex items-start justify-between gap-2">
                        <div className="min-w-0">
                          <div
                            className="text-xs font-semibold font-mono text-[#201B18] dark:text-[#F5F0EA] truncate"
                            title={`${srcLabel} → ${dstLabel}`}
                          >
                            {srcLabel} <span className="text-[#F08C18]">→</span> {dstLabel}
                          </div>
                          <div className="flex flex-wrap items-center gap-1.5 mt-1 text-[10px]">
                            <span className="px-1.5 py-0.5 rounded bg-[#FFF4E4] dark:bg-[#F08C18]/20 text-[#9E4A05] dark:text-[#FBB040] border border-[#FAD4A1] dark:border-[#F08C18]/40 font-mono font-medium">
                              {edge.primaryLabel}
                            </span>
                            {edge.crossNode && (
                              <span className="px-1.5 py-0.5 rounded bg-[#FFF4E4] text-[#9E4A05] dark:bg-[#F08C18]/20 dark:text-[#FBB040]">
                                跨节点
                              </span>
                            )}
                            <span className="text-[#8C7F73] font-mono">{formatBps(edge.bytesPerSec)}</span>
                            <span className="text-[#8C7F73] font-mono">RTT {formatLatency(edge.latencyAvgMs)}</span>
                          </div>
                        </div>
                        <StatusPill status={edge.health} />
                      </div>

                      {/* Retransmits + Baseline Delta + Honest Ratio Box */}
                      <div className="p-2 rounded-lg bg-white dark:bg-[#1C1815] border border-[#EFE7DD] dark:border-[#2E2721] space-y-1 text-[11px]">
                        <div className="flex flex-wrap items-center justify-between gap-1 font-mono">
                          <span>
                            最近 {edgeWinLabel}：
                            <b className={edge.retransmits > 0 ? 'text-rose-600 dark:text-rose-400' : 'text-[#574D44]'}>
                              重传 {edge.retransmits} 次
                            </b>
                            {edge.resets > 0 && (
                              <span className="ml-1.5 text-rose-600 dark:text-rose-400 font-bold">· RST {edge.resets}</span>
                            )}
                          </span>
                          {/* Reliable baseline delta */}
                          {edge.baselineAvailable ? (
                            <span
                              className={`px-1.5 py-0.5 rounded text-[10px] font-semibold ${
                                (edge.retransmitDeltaPct || 0) >= 30 || (edge.prevRetransmits === 0 && edge.retransmits > 0)
                                  ? 'bg-rose-50 text-rose-700 border border-rose-200 dark:bg-rose-950/40 dark:text-rose-300 dark:border-rose-800'
                                  : 'bg-[#F7F3EE] text-[#574D44] dark:bg-[#25201C] dark:text-[#C4B5A5]'
                              }`}
                            >
                              {edge.prevRetransmits === 0 && edge.retransmits > 0
                                ? `前 ${edgeWinLabel} 为 0（新增）`
                                : `${(edge.retransmitDeltaPct || 0) >= 0 ? '+' : ''}${(edge.retransmitDeltaPct || 0).toFixed(0)}% vs 前 ${edgeWinLabel}`}
                            </span>
                          ) : (
                            <span className="px-1.5 py-0.5 rounded text-[10px] bg-[#F7F3EE] dark:bg-[#25201C] text-[#8C7F73]">
                              首窗无前段基线
                            </span>
                          )}
                        </div>

                        {/* Honest ratio & per-MB explanation */}
                        {(edge.estRetransmitRatePct || edge.retransmitPerMB || edge.rateNote) && (
                          <div className="flex items-start gap-1 text-[10px] text-[#6C5D4F] dark:text-[#A39587] leading-snug">
                            <Info className="w-3 h-3 shrink-0 mt-0.5 text-[#D9720B] dark:text-[#FBB040]" />
                            <span>
                              {edge.estRetransmitRatePct && edge.estRetransmitRatePct > 0 ? (
                                <b className="font-mono text-[#201B18] dark:text-[#F5F0EA] mr-1">
                                  估算重传率 ~{edge.estRetransmitRatePct.toFixed(2)}%
                                </b>
                              ) : null}
                              {edge.retransmitPerMB && edge.retransmitPerMB > 0 ? (
                                <span className="font-mono mr-1">({edge.retransmitPerMB.toFixed(1)} 次/MB)</span>
                              ) : null}
                              {edge.rateNote && <span>{edge.rateNote}</span>}
                            </span>
                          </div>
                        )}
                      </div>

                      {/* Anomaly reasons */}
                      {edge.anomalyReasons && edge.anomalyReasons.length > 0 && (
                        <div className="flex flex-wrap gap-1">
                          {edge.anomalyReasons.map((r, i) => (
                            <span
                              key={i}
                              className="px-1.5 py-0.5 rounded text-[10px] bg-amber-50 dark:bg-amber-950/30 text-amber-800 dark:text-amber-300 border border-amber-200/80 dark:border-amber-800/50"
                            >
                              {r}
                            </span>
                          ))}
                        </div>
                      )}

                      {/* Quick action row */}
                      <div className="flex items-center justify-between gap-1.5 pt-1 border-t border-[#EFE7DD] dark:border-[#2E2721]">
                        <button
                          onClick={(ev) => {
                            ev.stopPropagation();
                            jumpTraceFromEdge(edge);
                          }}
                          className="inline-flex items-center gap-1 px-2.5 py-1 rounded-lg text-[11px] font-semibold bg-[#F08C18] text-white hover:bg-[#D9720B] transition"
                        >
                          <Route className="w-3 h-3" /> TraceFlow（保留 {edgeWinLabel} 窗口）
                        </button>
                        <div className="flex items-center gap-1">
                          <button
                            onClick={(ev) => {
                              ev.stopPropagation();
                              saveEdgeIncident(edge);
                            }}
                            disabled={savingIncidentId === edge.id || saved}
                            className={`inline-flex items-center gap-1 px-2 py-1 rounded-lg text-[11px] font-medium border transition ${
                              saved
                                ? 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-300 dark:border-emerald-800'
                                : 'bg-white dark:bg-[#1C1815] text-[#574D44] dark:text-[#D5C9BC] border-[#E9E1D6] dark:border-[#2E2721] hover:border-[#F08C18]'
                            }`}
                          >
                            <BookmarkPlus className="w-3 h-3" />
                            {saved ? '已留存' : savingIncidentId === edge.id ? '保存中...' : '留存现场'}
                          </button>
                          <button
                            onClick={(ev) => {
                              ev.stopPropagation();
                              setSelectedEdge(edge);
                            }}
                            title="查看五元组连接明细"
                            className="inline-flex items-center gap-1 px-2 py-1 rounded-lg text-[11px] font-medium bg-[#F7F3EE] dark:bg-[#25201C] text-[#574D44] dark:text-[#D5C9BC] hover:bg-[#EFE7DD]"
                          >
                            <Eye className="w-3 h-3" /> 明细
                          </button>
                        </div>
                      </div>
                    </div>
                  );
                })
              )}
            </div>
          </div>
        )}

        {/* ReactFlow Graph Canvas */}
        <div className="flex-1 h-full bg-[#FBF9F6] dark:bg-[#14110F] relative">
          {/* Focused 1-hop context pill */}
          {viewMode === 'anomaly' && focusedAnomalyEdge && graph && (
            <div className="absolute top-3 right-3 z-10 flex items-center gap-2 px-3 py-1.5 rounded-xl bg-white/95 dark:bg-[#1C1815]/95 border border-[#FAD4A1] dark:border-[#F08C18]/40 shadow-sm text-[11px]">
              <span className="text-[#6C4824] dark:text-[#FBB040] font-semibold">
                焦点视图：选中连接 + 上下游 1 跳（{displayNodes.length} 节点 / {displayEdges.length} 连线）
              </span>
              <button
                onClick={() => setViewMode('panorama')}
                className="px-2 py-0.5 rounded-md bg-[#FFF4E4] dark:bg-[#F08C18]/20 text-[#9E4A05] dark:text-[#FBB040] border border-[#FAD4A1] dark:border-[#F08C18]/40 font-semibold hover:bg-[#FDE6C4] transition"
              >
                展开全景（{graph.summary.nodeCount} 节点）
              </button>
            </div>
          )}

          {nodes.length === 0 ? (
            <div className="h-full flex flex-col items-center justify-center text-[#8C7F73] text-xs">
              <p className="text-sm font-medium text-[#574D44]">当前筛选条件下暂无活跃流量边</p>
              <p className="mt-1">请等待 2~4 秒采集上报，或扩大时间窗口 / 切换到“全部命名空间”</p>
            </div>
          ) : (
            <ReactFlow
              key={effectiveFitKey}
              nodes={nodes}
              edges={edges}
              nodeTypes={nodeTypes}
              edgeTypes={edgeTypes}
              onEdgeClick={(_, edge) => setSelectedEdge((edge.data?.raw as TopologyEdge) || null)}
              onPaneClick={() => setSelectedEdge(null)}
              fitView
              fitViewOptions={{ padding: 0.2, maxZoom: 1.08 }}
              minZoom={0.12}
              maxZoom={2}
            >
              <AutoFitViewport fitKey={effectiveFitKey} nodeCount={nodes.length} />
              <Background gap={20} size={1} color="#B8A99A" className="opacity-30" />
              <Controls position="top-left" />
              <MiniMap position="bottom-right" nodeStrokeWidth={2} pannable zoomable />
            </ReactFlow>
          )}
        </div>

        {/* Edge Five-Tuple Inspector Drawer */}
        {selectedEdge && (
          <div className="w-[420px] border-l border-[#E9E1D6] dark:border-[#2E2721] bg-white dark:bg-[#1C1815] flex flex-col h-full shadow-xl z-20">
            <div className="px-4 py-3 border-b border-[#E9E1D6] dark:border-[#2E2721] flex items-center justify-between gap-2">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <StatusPill status={selectedEdge.health} />
                  {selectedEdge.crossNode && (
                    <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-[#FFF4E4] text-[#9E4A05] dark:bg-[#F08C18]/20 dark:text-[#FBB040]">
                      跨节点流量
                    </span>
                  )}
                </div>
                <div className="text-xs font-mono font-semibold text-[#201B18] dark:text-[#F5F0EA] mt-1.5 truncate">
                  {nodeLabelMap.get(selectedEdge.source)?.label || selectedEdge.source} →{' '}
                  {nodeLabelMap.get(selectedEdge.target)?.label || selectedEdge.target}
                </div>
              </div>
              <button
                onClick={() => setSelectedEdge(null)}
                className="p-1.5 rounded-lg hover:bg-[#F7F3EE] dark:hover:bg-[#25201C] text-[#8C7F73]"
              >
                <X className="w-4 h-4" />
              </button>
            </div>

            <div className="p-4 border-b border-[#E9E1D6] dark:border-[#2E2721] grid grid-cols-3 gap-2 text-xs">
              <MetricMini label="实时吞吐" value={formatBps(selectedEdge.bytesPerSec)} sub={`累计 ${formatBytes(selectedEdge.bytesTotal)}`} />
              <MetricMini label="平均 / P95 延迟" value={formatLatency(selectedEdge.latencyAvgMs)} sub={`P95 ${formatLatency(selectedEdge.latencyP95Ms)}`} />
              <MetricMini
                label={`重传 / RST (${windowLabel})`}
                value={`${selectedEdge.retransmits} / ${selectedEdge.resets}`}
                sub={
                  selectedEdge.baselineAvailable && selectedEdge.retransmitDeltaPct !== undefined
                    ? `较前 ${windowLabel} ${selectedEdge.retransmitDeltaPct >= 0 ? '+' : ''}${selectedEdge.retransmitDeltaPct.toFixed(0)}%`
                    : `${selectedEdge.flowCount} 五元组`
                }
              />
            </div>

            <div className="flex-1 overflow-y-auto p-3 space-y-2">
              <div className="text-[11px] font-semibold uppercase tracking-wider text-[#8C7F73] px-1">
                五元组连接明细 ({selectedEdge.fiveTuples.length})
              </div>
              {selectedEdge.fiveTuples.map((f) => (
                <div
                  key={f.id}
                  className="p-2.5 rounded-lg border border-[#E9E1D6] dark:border-[#2E2721] bg-[#FAF6F1] dark:bg-[#14110F] space-y-1.5 text-[11px]"
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-mono font-semibold text-[#9E4A05] dark:text-[#FBB040]">
                      {f.protocol} :{f.dstPort}
                    </span>
                    <div className="flex items-center gap-1.5">
                      <EngineBadge engine={f.engine} />
                      <StatusPill status={f.health} />
                    </div>
                  </div>
                  <div className="font-mono text-[#201B18] dark:text-[#F5F0EA] break-all">
                    {f.src.ip}:{f.srcPort} → {f.dst.ip}:{f.dstPort}
                  </div>
                  <div
                    className="text-[#8C7F73] truncate"
                    title={`${f.src.displayName} → ${f.viaService ? `[${f.viaService.displayName}] → ` : ''}${f.dst.displayName}`}
                  >
                    {f.src.displayName} → {f.viaService ? `[${f.viaService.displayName}] → ` : ''}
                    {f.dst.displayName}
                  </div>
                  {(f.retransmits > 0 || f.rateNote) && (
                    <div className="text-[10px] text-[#6C5D4F] dark:text-[#A39587] font-mono">
                      重传 {f.retransmits} / RST {f.resets}
                      {f.estRetransmitRatePct ? ` · ~${f.estRetransmitRatePct.toFixed(2)}%` : ''}
                      {f.rateNote ? ` (${f.rateNote})` : ''}
                    </div>
                  )}
                  <div className="flex items-center justify-between pt-1 border-t border-[#E9E1D6] dark:border-[#2E2721] font-mono text-[10px] text-[#8C7F73]">
                    <span>
                      {formatBytes(f.bytesTotal)} ({formatBps(f.bytesPerSec)})
                    </span>
                    <span>RTT {formatLatency(f.latencyAvgMs)}</span>
                    <div className="flex items-center gap-1">
                      <button
                        onClick={() => jumpTraceFromFlow(f, selectedEdge.id)}
                        className="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded bg-[#F08C18] text-white hover:bg-[#D9720B]"
                      >
                        <Route className="w-2.5 h-2.5" /> Trace
                      </button>
                      {f.src.kind === 'Pod' && f.src.namespace && f.src.podName && (
                        <button
                          onClick={() => onLaunchTCPDump(f.src.namespace!, f.src.podName!)}
                          className="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded bg-[#EFE7DD] dark:bg-[#25201C] text-[#574D44] dark:text-[#D5C9BC] hover:bg-[#E5D9CA]"
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
      <div className="border-t border-[#E9E1D6] dark:border-[#2E2721] bg-white dark:bg-[#1C1815] shrink-0">
        <div
          onClick={() => setDrawerExpanded((v) => !v)}
          className="px-4 py-2 flex items-center justify-between cursor-pointer hover:bg-[#FAF6F1] dark:hover:bg-[#25201C]/50"
        >
          <div className="flex items-center gap-2 text-xs font-semibold text-[#201B18] dark:text-[#F5F0EA]">
            <span>实时五元组流表 (Live Flows · 最近 {windowLabel})</span>
            <span className="px-1.5 py-0.5 rounded-full text-[10px] font-mono bg-[#FFF4E4] text-[#9E4A05] border border-[#FAD4A1] dark:bg-[#F08C18]/15 dark:text-[#FBB040] dark:border-[#F08C18]/40">
              {liveFlows.length}
            </span>
          </div>
          {drawerExpanded ? <ChevronDown className="w-4 h-4 text-[#8C7F73]" /> : <ChevronUp className="w-4 h-4 text-[#8C7F73]" />}
        </div>

        {drawerExpanded && (
          <div className="h-[210px] overflow-auto border-t border-[#F2ECE4] dark:border-[#2E2721]">
            <table className="w-full text-[11px] font-mono">
              <thead className="bg-[#FAF6F1] dark:bg-[#25201C] text-[#8C7F73] sticky top-0 z-10">
                <tr>
                  <th className="text-left px-3 py-1.5 font-medium">源端点 (Src Pod / IP:Port)</th>
                  <th className="text-left px-3 py-1.5 font-medium">目标端点 (Via SVC → Dst Pod / IP:Port)</th>
                  <th className="text-left px-3 py-1.5 font-medium">协议</th>
                  <th className="text-right px-3 py-1.5 font-medium">流量大小 / 速率</th>
                  <th className="text-right px-3 py-1.5 font-medium">平均 / 最大延迟</th>
                  <th className="text-right px-3 py-1.5 font-medium">重传 / RST（含基线）</th>
                  <th className="text-left px-3 py-1.5 font-medium">引擎</th>
                  <th className="text-right px-3 py-1.5 font-medium">排障</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[#F2ECE4] dark:divide-[#2E2721]">
                {liveFlows.slice(0, 80).map((f, idx) => {
                  const dstFull = `${f.viaService ? `[${f.viaService.serviceName}] → ` : ''}${f.dst.displayName}`;
                  const tipPos = idx === 0 ? 'top-full mt-1' : 'bottom-full mb-1';
                  return (
                    <tr key={f.id} className="hover:bg-[#FDF8F2] dark:hover:bg-[#25201C]/50">
                      <td className="px-3 py-1.5">
                        <div className="relative group inline-block max-w-[230px] align-top">
                          <div
                            title={`${f.src.displayName} (${f.src.ip}:${f.srcPort})`}
                            className="text-[#201B18] dark:text-[#F5F0EA] font-sans font-medium truncate cursor-default"
                          >
                            {f.src.displayName}
                          </div>
                          <div
                            className={`pointer-events-none hidden group-hover:block absolute left-0 ${tipPos} z-30 w-max max-w-[460px] px-2.5 py-1.5 rounded-lg bg-[#201B18] text-[#F5F0EA] text-[11px] font-mono shadow-lg border border-[#3E352D] break-all`}
                          >
                            <div>{f.src.displayName}</div>
                            <div className="text-[10px] text-[#C4B5A5] mt-0.5">
                              {f.src.ip}:{f.srcPort}
                            </div>
                          </div>
                        </div>
                        <div className="text-[10px] text-[#8C7F73]">
                          {f.src.ip}:{f.srcPort}
                        </div>
                      </td>
                      <td className="px-3 py-1.5">
                        <div className="relative group inline-block max-w-[260px] align-top">
                          <div
                            title={`${dstFull} (${f.dst.ip}:${f.dstPort})`}
                            className="text-[#201B18] dark:text-[#F5F0EA] font-sans font-medium truncate cursor-default"
                          >
                            {f.viaService ? (
                              <span className="text-[#9E4A05] dark:text-[#FBB040]">[{f.viaService.serviceName}] → </span>
                            ) : null}
                            {f.dst.displayName}
                          </div>
                          <div
                            className={`pointer-events-none hidden group-hover:block absolute left-0 ${tipPos} z-30 w-max max-w-[460px] px-2.5 py-1.5 rounded-lg bg-[#201B18] text-[#F5F0EA] text-[11px] font-mono shadow-lg border border-[#3E352D] break-all`}
                          >
                            <div>{dstFull}</div>
                            <div className="text-[10px] text-[#C4B5A5] mt-0.5">
                              {f.dst.ip}:{f.dstPort}
                            </div>
                          </div>
                        </div>
                        <div className="text-[10px] text-[#8C7F73]">
                          {f.dst.ip}:{f.dstPort}
                        </div>
                      </td>
                      <td className="px-3 py-1.5">
                        <span className="px-1.5 py-0.5 rounded bg-[#F7F3EE] dark:bg-[#25201C] text-[#574D44] dark:text-[#D5C9BC]">
                          {f.protocol}
                        </span>
                      </td>
                      <td className="px-3 py-1.5 text-right">
                        <div>{formatBytes(f.bytesTotal)}</div>
                        <div className="text-[10px] text-[#8C7F73]">{formatBps(f.bytesPerSec)}</div>
                      </td>
                      <td className="px-3 py-1.5 text-right">
                        <span className={f.latencyAvgMs > 25 ? 'text-[#D9720B] font-semibold' : 'text-emerald-600 dark:text-emerald-400'}>
                          {formatLatency(f.latencyAvgMs)}
                        </span>
                        <div className="text-[10px] text-[#8C7F73]">max {formatLatency(f.latencyMaxMs)}</div>
                      </td>
                      <td className="px-3 py-1.5 text-right">
                        <span className={f.retransmits > 0 || f.resets > 0 ? 'text-rose-600 font-semibold' : 'text-[#8C7F73]'}>
                          {f.retransmits} / {f.resets}
                        </span>
                        {f.retransmits > 0 && (
                          <div className="text-[10px] text-[#8C7F73]" title={f.rateNote || ''}>
                            {f.estRetransmitRatePct ? `~${f.estRetransmitRatePct.toFixed(2)}%` : ''}
                            {f.baselineAvailable && f.retransmitDeltaPct !== undefined
                              ? ` · ${f.retransmitDeltaPct >= 0 ? '+' : ''}${f.retransmitDeltaPct.toFixed(0)}%`
                              : ''}
                          </div>
                        )}
                      </td>
                      <td className="px-3 py-1.5">
                        <span
                          className={`text-[10px] px-1.5 py-0.5 rounded font-medium ${
                            f.engine === 'ebpf'
                              ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-300'
                              : 'bg-[#FFF4E4] text-[#9E4A05] border border-[#FAD4A1] dark:bg-[#F08C18]/20 dark:text-[#FBB040] dark:border-[#F08C18]/40'
                          }`}
                        >
                          {f.engine === 'ebpf' ? 'ebpf' : 'legacy'}
                        </span>
                      </td>
                      <td className="px-3 py-1.5 text-right">
                        <button
                          onClick={() => jumpTraceFromFlow(f)}
                          className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-[#FFF4E4] text-[#9E4A05] border border-[#FAD4A1] hover:bg-[#FDE6C4] dark:bg-[#F08C18]/15 dark:text-[#FBB040] dark:border-[#F08C18]/40 font-sans font-medium"
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
    <div className="p-2 rounded-lg bg-[#FAF6F1] dark:bg-[#14110F] border border-[#E9E1D6] dark:border-[#2E2721]">
      <div className="text-[10px] text-[#8C7F73]">{label}</div>
      <div className="text-xs font-bold font-mono text-[#201B18] dark:text-[#F5F0EA] mt-0.5">{value}</div>
      <div className="text-[10px] text-[#8C7F73] mt-0.5 truncate">{sub}</div>
    </div>
  );
}
