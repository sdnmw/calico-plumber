import React, { useEffect, useMemo, useState } from 'react';
import {
  AlertTriangle,
  ArrowRight,
  Cable,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Copy,
  Download,
  GitCompareArrows,
  Layers,
  Network,
  Play,
  RefreshCw,
  Ruler,
  Search,
  Server,
  ShieldAlert,
  SlidersHorizontal,
  Terminal,
  XCircle,
} from 'lucide-react';
import { api } from '../api/client';
import type {
  ARPEntry,
  AuditFinding,
  ClusterKernelAuditReport,
  ClusterRouteAuditReport,
  NodeKernelAudit,
  NodeRouteAudit,
  PMTUDProbeResult,
  PodBrief,
} from '../api/types';
import { EncapBadge, SectionCard, StatusPill } from '../components/common/Badges';

type AuditSubTab = 'overview' | 'routes' | 'l2';

const DRIFT_KIND_LABEL: Record<string, string> = {
  missing_block: '缺失跨节点 Block 路由',
  missing_pod: '缺失本地 Pod /32 路由',
  ghost_pod_route: '残留幽灵 Pod /32 路由',
  unexpected_blackhole: '异常 Blackhole 路由',
  missing_blackhole: '缺失本地 Blackhole 防环路由',
  encap_mismatch: '封装模式不一致',
  asymmetric_path: '跨节点去回程路径非对称',
};

const CATEGORY_LABEL: Record<string, string> = {
  nat: 'NAT 转发 / 端口映射',
  mtu: 'MTU 与封装链路',
  l2: '二层 MAC / ARP',
  routing: '三层路由转发',
  conntrack: 'NAT 连接状态',
  sysctl: '内核网络参数',
  route: '三层路由',
  bgp: 'BGP 邻居',
};

const ARP_ROLE_META: Record<string, { label: string; cls: string }> = {
  gateway: {
    label: '默认网关',
    cls: 'bg-indigo-50 text-indigo-700 border-indigo-200 dark:bg-indigo-950/50 dark:text-indigo-300 dark:border-indigo-800',
  },
  'cluster-node': {
    label: '集群节点',
    cls: 'bg-blue-50 text-blue-700 border-blue-200 dark:bg-blue-950/50 dark:text-blue-300 dark:border-blue-800',
  },
  'pod-veth': {
    label: 'Pod veth',
    cls: 'bg-teal-50 text-teal-700 border-teal-200 dark:bg-teal-950/50 dark:text-teal-300 dark:border-teal-800',
  },
  other: {
    label: '外部 / 同网段',
    cls: 'bg-slate-100 text-slate-600 border-slate-200 dark:bg-slate-800 dark:text-slate-300 dark:border-slate-700',
  },
};

function arpStateClass(state: string): string {
  switch (state) {
    case 'REACHABLE':
    case 'PERMANENT':
      return 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-300 dark:border-emerald-900';
    case 'STALE':
    case 'DELAY':
    case 'PROBE':
      return 'bg-slate-100 text-slate-600 border-slate-200 dark:bg-slate-800 dark:text-slate-300 dark:border-slate-700';
    case 'FAILED':
    case 'INCOMPLETE':
      return 'bg-rose-50 text-rose-700 border-rose-200 dark:bg-rose-950/50 dark:text-rose-300 dark:border-rose-900';
    default:
      return 'bg-slate-100 text-slate-500 border-slate-200 dark:bg-slate-800 dark:text-slate-400 dark:border-slate-700';
  }
}

export function AuditPage() {
  const [subTab, setSubTab] = useState<AuditSubTab>('overview');
  const [kernelReport, setKernelReport] = useState<ClusterKernelAuditReport | null>(null);
  const [routeReport, setRouteReport] = useState<ClusterRouteAuditReport | null>(null);
  const [pods, setPods] = useState<PodBrief[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Progressive disclosure states
  const [expandedEvidenceIds, setExpandedEvidenceIds] = useState<Record<string, boolean>>({});
  const [showRawKernelPanel, setShowRawKernelPanel] = useState(false);
  const [expandedKernelNode, setExpandedKernelNode] = useState<string | null>(null);

  // Route tab states
  const [expandedRouteNode, setExpandedRouteNode] = useState<string | null>(null);
  const [routeFilterProto, setRouteFilterProto] = useState<'all' | 'bird' | 'pod' | 'blackhole'>('all');
  const [routeSearch, setRouteSearch] = useState('');

  // L2 / ARP tab states
  const [selectedL2Node, setSelectedL2Node] = useState<string>('');
  const [arpRoleFilter, setArpRoleFilter] = useState<'important' | 'all' | 'pod-veth' | 'failed'>('important');
  const [arpSearch, setArpSearch] = useState('');

  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  // Path MTU / DF Probe state
  const [pmtudNs, setPmtudNs] = useState('');
  const [pmtudPod, setPmtudPod] = useState('');
  const [pmtudTargetIP, setPmtudTargetIP] = useState('');
  const [pmtudMaxMTU, setPmtudMaxMTU] = useState(1500);
  const [pmtudRunning, setPmtudRunning] = useState(false);
  const [pmtudResult, setPmtudResult] = useState<PMTUDProbeResult | null>(null);
  const [pmtudError, setPmtudError] = useState<string | null>(null);

  const loadAll = async () => {
    setLoading(true);
    setError(null);
    try {
      const [kRep, rRep, podList] = await Promise.all([
        api.getKernelAudit(),
        api.getRouteAudit(),
        api.listPods(''),
      ]);
      setKernelReport(kRep);
      setRouteReport(rRep);
      setPods(podList);

      if (kRep.nodes.length > 0) {
        if (!expandedKernelNode) setExpandedKernelNode(kRep.nodes[0].nodeName);
        if (!selectedL2Node) setSelectedL2Node(kRep.nodes[0].nodeName);
      }
      if (rRep.nodes.length > 0 && !expandedRouteNode) {
        const drifted = rRep.nodes.find((n) => n.driftCount > 0);
        setExpandedRouteNode(drifted ? drifted.nodeName : rRep.nodes[0].nodeName);
      }

      // Auto-prefill PMTUD source Pod & cross-node target IP
      if (!pmtudPod && podList.length > 0) {
        const runningPods = podList.filter((p) => p.phase === 'Running' && !p.hostNetwork && p.ip);
        const probePods = runningPods.filter((p) => p.name.includes('calico-netdiag-probe'));
        const src = probePods[0] || runningPods[0];
        if (src) {
          setPmtudNs(src.namespace);
          setPmtudPod(src.name);
          const remoteProbe = probePods.find((p) => p.nodeName !== src.nodeName);
          const remotePod = remoteProbe || runningPods.find((p) => p.nodeName !== src.nodeName);
          if (remotePod && !pmtudTargetIP) {
            setPmtudTargetIP(remotePod.ip);
          }
        }
      }
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadAll();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const namespaces = useMemo(() => {
    const s = new Set<string>();
    pods.forEach((p) => {
      if (p.phase === 'Running' && !p.hostNetwork && p.ip) s.add(p.namespace);
    });
    return Array.from(s).sort();
  }, [pods]);

  const podsInNs = useMemo(
    () => pods.filter((p) => p.namespace === pmtudNs && p.phase === 'Running' && !p.hostNetwork && p.ip),
    [pods, pmtudNs],
  );

  const quickPmtudTargets = useMemo(() => {
    const out: { label: string; ip: string }[] = [];
    const seen = new Set<string>();
    const srcObj = pods.find((p) => p.namespace === pmtudNs && p.name === pmtudPod);
    const probePods = pods.filter((p) => p.phase === 'Running' && !p.hostNetwork && p.ip && p.name.includes('probe'));
    for (const p of probePods) {
      if (srcObj && p.ip === srcObj.ip) continue;
      if (!seen.has(p.ip)) {
        seen.add(p.ip);
        out.push({ label: `对端探针 (${p.nodeName})`, ip: p.ip });
      }
    }
    if (kernelReport) {
      for (const n of kernelReport.nodes) {
        if (n.internalIP && !seen.has(n.internalIP)) {
          seen.add(n.internalIP);
          out.push({ label: `节点 ${n.nodeName}`, ip: n.internalIP });
        }
      }
    }
    return out.slice(0, 4);
  }, [pods, pmtudNs, pmtudPod, kernelReport]);

  const handleRunPMTUD = async () => {
    if (!pmtudNs || !pmtudPod || !pmtudTargetIP.trim()) return;
    setPmtudRunning(true);
    setPmtudError(null);
    try {
      const res = await api.runPMTUD({
        srcNamespace: pmtudNs,
        srcPod: pmtudPod,
        targetIP: pmtudTargetIP.trim(),
        maxMTU: pmtudMaxMTU,
      });
      setPmtudResult(res);
    } catch (e: unknown) {
      setPmtudError(e instanceof Error ? e.message : String(e));
    } finally {
      setPmtudRunning(false);
    }
  };

  const copyText = (id: string, text: string) => {
    navigator.clipboard?.writeText(text).catch(() => {});
    setCopiedKey(id);
    setTimeout(() => setCopiedKey(null), 1500);
  };

  const toggleEvidence = (id: string) => {
    setExpandedEvidenceIds((prev) => ({ ...prev, [id]: !prev[id] }));
  };

  // Derived network-first KPI metrics
  const bgpStats = useMemo(() => {
    const peers = kernelReport?.bgpNeighbors || [];
    const up = peers.filter((p) => {
      const s = (p.state || '').toLowerCase();
      return s.includes('established') || s.includes('up');
    }).length;
    return { up, total: peers.length };
  }, [kernelReport]);

  const l2Summary = useMemo(() => {
    const nodes = kernelReport?.nodes || [];
    let totalVeth = 0;
    let proxyArpOk = 0;
    let totalArp = 0;
    let failedArp = 0;
    let maxArpPct = 0;
    let gwAllOk = true;
    for (const n of nodes) {
      if (!n.l2) continue;
      totalVeth += n.l2.totalCaliVeths;
      proxyArpOk += n.l2.proxyARPVeths;
      totalArp += n.l2.arpCount;
      failedArp += n.l2.arpFailedCount;
      if (n.l2.arpUsagePct > maxArpPct) maxArpPct = n.l2.arpUsagePct;
      if (n.l2.gatewayARPState === 'FAILED' || n.l2.gatewayARPState === 'INCOMPLETE') gwAllOk = false;
    }
    return { totalVeth, proxyArpOk, totalArp, failedArp, maxArpPct, gwAllOk };
  }, [kernelReport]);

  const natSummary = useMemo(() => {
    const nodes = kernelReport?.nodes || [];
    const randFullyCount = nodes.filter((n) => n.conntrack.snatRandomFully).length;
    const totalDrops = nodes.reduce((acc, n) => acc + n.conntrack.drop, 0);
    const maxCtPct = nodes.reduce((m, n) => Math.max(m, n.conntrack.usagePct), 0);
    return { randFullyCount, totalNodes: nodes.length, totalDrops, maxCtPct };
  }, [kernelReport]);

  const activeL2Node: NodeKernelAudit | undefined = useMemo(() => {
    if (!kernelReport?.nodes?.length) return undefined;
    return kernelReport.nodes.find((n) => n.nodeName === selectedL2Node) || kernelReport.nodes[0];
  }, [kernelReport, selectedL2Node]);

  const filteredARPNeighbors: ARPEntry[] = useMemo(() => {
    const list = activeL2Node?.l2?.neighbors || [];
    return list.filter((nb) => {
      if (arpRoleFilter === 'important' && nb.role === 'pod-veth' && nb.state !== 'FAILED' && nb.state !== 'INCOMPLETE') {
        return false;
      }
      if (arpRoleFilter === 'pod-veth' && nb.role !== 'pod-veth') return false;
      if (arpRoleFilter === 'failed' && nb.state !== 'FAILED' && nb.state !== 'INCOMPLETE') return false;
      if (arpSearch.trim()) {
        const q = arpSearch.trim().toLowerCase();
        const hay = `${nb.ip} ${nb.mac} ${nb.dev} ${nb.state} ${nb.peerName || ''} ${nb.role || ''}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [activeL2Node, arpRoleFilter, arpSearch]);

  const totalIssueCount = (kernelReport?.findings.length || 0) + (routeReport?.totalDrifts || 0);
  const modeProfile = kernelReport?.networkMode;

  return (
    <div className="p-5 max-w-[1440px] mx-auto space-y-4">
      {/* Page Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-bold tracking-tight text-slate-900 dark:text-white flex items-center gap-2">
            <Network className="w-5 h-5 text-blue-600 dark:text-blue-400" />
            <span>网络体检与审计</span>
            {kernelReport?.encapMode && <EncapBadge mode={kernelReport.encapMode} />}
          </h1>
          <p className="text-xs text-slate-500 dark:text-slate-400 mt-0.5">
            围绕网络模式与 MTU、Calico BGP 邻居与三层路由、二层 MAC/ARP 与 NAT 转发逐层体检；检测到问题后再按需查看 Conntrack 与内核参数详情
          </p>
        </div>

        <div className="flex items-center gap-2">
          <button
            onClick={loadAll}
            disabled={loading}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 hover:bg-slate-50 dark:hover:bg-slate-800 text-slate-700 dark:text-slate-200 transition disabled:opacity-50"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
            <span>重新体检</span>
          </button>
          <a
            href={api.getBundleExportUrl()}
            download
            className="inline-flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg text-xs font-semibold bg-blue-600 hover:bg-blue-700 text-white shadow-sm transition"
          >
            <Download className="w-3.5 h-3.5" />
            <span>一键导出诊断包 (.zip)</span>
          </a>
        </div>
      </div>

      {error && (
        <div className="p-3.5 rounded-xl bg-rose-50 dark:bg-rose-950/30 border border-rose-200 dark:border-rose-900 text-rose-700 dark:text-rose-300 text-xs flex items-center gap-2">
          <XCircle className="w-4 h-4 shrink-0" />
          <span>审计数据拉取失败: {error}</span>
        </div>
      )}

      {loading && !kernelReport && !routeReport && (
        <div className="p-12 text-center text-xs text-slate-400">
          正在并发采集各节点网络模式、BGP 邻居、路由表、ARP 邻居表及 NAT 状态...
        </div>
      )}

      {/* Top 4 Network-Domain KPI Cards (Always readable in pure networking language) */}
      {kernelReport && (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
          {/* KPI 1: Calico Network Mode & MTU */}
          <button
            onClick={() => setSubTab('overview')}
            className="text-left bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 hover:border-blue-300 dark:hover:border-blue-700 rounded-xl p-3.5 transition"
          >
            <div className="flex items-center justify-between text-[11px] font-semibold text-slate-400">
              <span>网络模式与 MTU</span>
              <Layers className="w-3.5 h-3.5 text-blue-500" />
            </div>
            <div className="mt-1.5 flex items-baseline justify-between gap-2">
              <span className="text-base font-bold text-slate-900 dark:text-white truncate">
                {modeProfile?.encapMode || kernelReport.encapMode}
              </span>
              <span className="text-[11px] font-mono font-semibold text-blue-600 dark:text-blue-400 shrink-0">
                开销 {modeProfile?.encapOverheadBytes ?? 0}B
              </span>
            </div>
            <div className="text-[11px] text-slate-500 mt-1 font-mono">
              物理 {modeProfile?.physicalMTU || 1500}B → 容器 {modeProfile?.recommendedPodMTU || 1500}B
            </div>
          </button>

          {/* KPI 2: Calico BGP Neighbors & L3 Route Consistency */}
          <button
            onClick={() => setSubTab('routes')}
            className="text-left bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 hover:border-blue-300 dark:hover:border-blue-700 rounded-xl p-3.5 transition"
          >
            <div className="flex items-center justify-between text-[11px] font-semibold text-slate-400">
              <span>BGP 邻居与三层路由</span>
              <GitCompareArrows className="w-3.5 h-3.5 text-emerald-500" />
            </div>
            <div className="mt-1.5 flex items-baseline justify-between gap-2">
              <span className="text-base font-bold font-mono text-slate-900 dark:text-white">
                {bgpStats.total > 0 ? `${bgpStats.up}/${bgpStats.total} Established` : '无 BGP Peer'}
              </span>
              {routeReport && (
                <StatusPill status={routeReport.totalDrifts === 0 ? 'healthy' : routeReport.overallStatus} compact />
              )}
            </div>
            <div className="text-[11px] text-slate-500 mt-1">
              AS{modeProfile?.bgpASNumber || 64512} {modeProfile?.nodeToNodeMesh ? 'Full-Mesh' : 'RR'} · 路由偏差{' '}
              <b className={routeReport && routeReport.totalDrifts > 0 ? 'text-rose-600' : 'text-emerald-600'}>
                {routeReport?.totalDrifts ?? 0}
              </b>
            </div>
          </button>

          {/* KPI 3: L2 MAC / ARP & Calico Proxy ARP */}
          <button
            onClick={() => setSubTab('l2')}
            className="text-left bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 hover:border-blue-300 dark:hover:border-blue-700 rounded-xl p-3.5 transition"
          >
            <div className="flex items-center justify-between text-[11px] font-semibold text-slate-400">
              <span>二层 MAC / ARP 邻居</span>
              <Cable className="w-3.5 h-3.5 text-indigo-500" />
            </div>
            <div className="mt-1.5 flex items-baseline justify-between gap-2">
              <span className="text-base font-bold text-slate-900 dark:text-white">
                {l2Summary.gwAllOk && l2Summary.failedArp === 0 ? '网关与节点可达' : `${l2Summary.failedArp} 项 ARP 异常`}
              </span>
              {l2Summary.gwAllOk && l2Summary.failedArp === 0 ? (
                <CheckCircle2 className="w-4 h-4 text-emerald-500 shrink-0" />
              ) : (
                <AlertTriangle className="w-4 h-4 text-rose-500 shrink-0" />
              )}
            </div>
            <div className="text-[11px] text-slate-500 mt-1 font-mono">
              Proxy ARP {l2Summary.proxyArpOk}/{l2Summary.totalVeth} · ARP 缓存 {l2Summary.totalArp} 条
            </div>
          </button>

          {/* KPI 4: NAT Translation & Issue Drilldown */}
          <button
            onClick={() => setSubTab('overview')}
            className="text-left bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 hover:border-blue-300 dark:hover:border-blue-700 rounded-xl p-3.5 transition"
          >
            <div className="flex items-center justify-between text-[11px] font-semibold text-slate-400">
              <span>NAT 转发与异常诊断</span>
              <ShieldAlert className={`w-3.5 h-3.5 ${totalIssueCount > 0 ? 'text-amber-500' : 'text-emerald-500'}`} />
            </div>
            <div className="mt-1.5 flex items-baseline justify-between gap-2">
              <span className="text-base font-bold text-slate-900 dark:text-white">
                {totalIssueCount === 0 ? '全平面健康' : `${totalIssueCount} 项待关注`}
              </span>
              <StatusPill status={kernelReport.overallStatus} compact />
            </div>
            <div className="text-[11px] text-slate-500 mt-1">
              SNAT 随机端口 {natSummary.randFullyCount}/{natSummary.totalNodes} · {modeProfile?.kubeProxyMode || 'iptables'} DNAT
            </div>
          </button>
        </div>
      )}

      {/* 3 Sub-Navigation Tabs */}
      <div className="flex flex-wrap items-center gap-2 border-b border-slate-200 dark:border-slate-800 pb-2.5">
        <button
          onClick={() => setSubTab('overview')}
          className={`inline-flex items-center gap-2 px-3.5 py-1.5 rounded-lg text-xs font-semibold transition ${
            subTab === 'overview'
              ? 'bg-blue-600 text-white shadow-sm'
              : 'bg-white dark:bg-slate-900 text-slate-600 dark:text-slate-300 border border-slate-200 dark:border-slate-800 hover:bg-slate-50 dark:hover:bg-slate-800'
          }`}
        >
          <Layers className="w-3.5 h-3.5" />
          <span>网络模式 · MTU · NAT 体检</span>
          {kernelReport && kernelReport.findings.length > 0 && (
            <span
              className={`px-1.5 rounded-full text-[10px] font-mono ${
                subTab === 'overview'
                  ? 'bg-white/20 text-white'
                  : kernelReport.criticalCount > 0
                    ? 'bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300'
                    : 'bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300'
              }`}
            >
              {kernelReport.findings.length}
            </span>
          )}
        </button>

        <button
          onClick={() => setSubTab('routes')}
          className={`inline-flex items-center gap-2 px-3.5 py-1.5 rounded-lg text-xs font-semibold transition ${
            subTab === 'routes'
              ? 'bg-blue-600 text-white shadow-sm'
              : 'bg-white dark:bg-slate-900 text-slate-600 dark:text-slate-300 border border-slate-200 dark:border-slate-800 hover:bg-slate-50 dark:hover:bg-slate-800'
          }`}
        >
          <GitCompareArrows className="w-3.5 h-3.5" />
          <span>Calico BGP 邻居与三层路由</span>
          {routeReport && (
            <span
              className={`px-1.5 rounded-full text-[10px] font-mono ${
                subTab === 'routes'
                  ? 'bg-white/20 text-white'
                  : routeReport.totalDrifts > 0
                    ? 'bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300'
                    : 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300'
              }`}
            >
              {routeReport.totalDrifts === 0 ? '一致' : `${routeReport.totalDrifts} 偏差`}
            </span>
          )}
        </button>

        <button
          onClick={() => setSubTab('l2')}
          className={`inline-flex items-center gap-2 px-3.5 py-1.5 rounded-lg text-xs font-semibold transition ${
            subTab === 'l2'
              ? 'bg-blue-600 text-white shadow-sm'
              : 'bg-white dark:bg-slate-900 text-slate-600 dark:text-slate-300 border border-slate-200 dark:border-slate-800 hover:bg-slate-50 dark:hover:bg-slate-800'
          }`}
        >
          <Cable className="w-3.5 h-3.5" />
          <span>二层 MAC 与 ARP 邻居表</span>
          {l2Summary.failedArp > 0 && (
            <span className="px-1.5 rounded-full text-[10px] font-mono bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300">
              {l2Summary.failedArp} 异常
            </span>
          )}
        </button>
      </div>

      {/* ==================== TAB 1: NETWORK MODE · MTU · NAT & PROGRESSIVE DRILLDOWN ==================== */}
      {subTab === 'overview' && kernelReport && (
        <div className="space-y-4">
          {/* 1. Calico Network Mode & MTU Encapsulation Visual Chain */}
          <SectionCard
            title="Calico 网络模式与 MTU 封装链路"
            subtitle="不同封装模式决定外层包头开销（BGP 直连 0B / IPIP 20B / VXLAN 50B），直接影响容器与物理网口 MTU 对齐关系"
            action={
              <div className="flex items-center gap-2 text-[11px] font-mono">
                <span className="px-2 py-0.5 rounded bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-300">
                  Dataplane: {modeProfile?.dataplane || 'Iptables'}
                </span>
                <span className="px-2 py-0.5 rounded bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-300">
                  NAT Outgoing: {modeProfile?.natOutgoingPools ?? 0}/{modeProfile?.totalPools ?? 0} Pool
                </span>
              </div>
            }
          >
            <div className="p-4 space-y-3.5">
              {/* Visual 4-stage MTU & Encap Pipeline */}
              <div className="grid grid-cols-1 md:grid-cols-7 gap-2 items-center">
                <div className="md:col-span-2 p-3 rounded-xl border border-teal-200/80 dark:border-teal-900 bg-teal-50/50 dark:bg-teal-950/20">
                  <div className="text-[10px] font-semibold uppercase text-teal-600 dark:text-teal-400">1. 容器网卡层 (Pod eth0)</div>
                  <div className="text-sm font-bold font-mono text-slate-900 dark:text-white mt-0.5">
                    MTU {kernelReport.nodes[0]?.mtu.minVethMTU || modeProfile?.recommendedPodMTU || 1500} B
                  </div>
                  <div className="text-[11px] text-slate-500 mt-0.5">
                    默认网关 <span className="font-mono">169.254.1.1</span> (Proxy ARP 代答)
                  </div>
                </div>

                <div className="hidden md:flex justify-center text-slate-300 dark:text-slate-600">
                  <ArrowRight className="w-4 h-4" />
                </div>

                <div className="md:col-span-2 p-3 rounded-xl border border-blue-200/80 dark:border-blue-900 bg-blue-50/50 dark:bg-blue-950/20">
                  <div className="text-[10px] font-semibold uppercase text-blue-600 dark:text-blue-400">2. Calico 路由 / 封装层</div>
                  <div className="text-sm font-bold text-slate-900 dark:text-white mt-0.5 flex items-center gap-1.5">
                    <span>{modeProfile?.encapMode || kernelReport.encapMode}</span>
                    <span className="text-[11px] font-mono font-normal text-blue-700 dark:text-blue-300">
                      (封装头 +{modeProfile?.encapOverheadBytes ?? 0}B)
                    </span>
                  </div>
                  <div className="text-[11px] text-slate-500 mt-0.5">
                    {modeProfile?.encapOverheadBytes === 0
                      ? '纯三层 BGP 路由直连，无隧道封装接口'
                      : `外层隧道封装，要求容器 MTU ≤ ${modeProfile?.recommendedPodMTU}B`}
                  </div>
                </div>

                <div className="hidden md:flex justify-center text-slate-300 dark:text-slate-600">
                  <ArrowRight className="w-4 h-4" />
                </div>

                <div className="md:col-span-1 p-3 rounded-xl border border-slate-200 dark:border-slate-800 bg-slate-50/80 dark:bg-slate-950/40">
                  <div className="text-[10px] font-semibold uppercase text-slate-500">3. 物理网卡</div>
                  <div className="text-sm font-bold font-mono text-slate-900 dark:text-white mt-0.5">
                    {kernelReport.nodes[0]?.mtu.primaryNIC || 'eth0'} · {modeProfile?.physicalMTU || 1500}B
                  </div>
                  <div className="text-[11px] text-emerald-600 dark:text-emerald-400 font-medium mt-0.5">
                    全集群一致
                  </div>
                </div>
              </div>

              {modeProfile?.summary && (
                <div className="px-3.5 py-2.5 rounded-lg bg-slate-50 dark:bg-slate-950/60 border border-slate-200/70 dark:border-slate-800 text-xs text-slate-600 dark:text-slate-300 leading-relaxed">
                  <b className="text-slate-800 dark:text-slate-100">模式解读：</b>
                  {modeProfile.summary}
                </div>
              )}

              {/* Per-node compact Network Health Summary Matrix (MAC / Gateway / MTU / NAT — NO raw conntrack clutter) */}
              <div className="overflow-x-auto">
                <table className="w-full text-xs">
                  <thead>
                    <tr className="text-left text-[10px] uppercase text-slate-400 border-b border-slate-100 dark:border-slate-800">
                      <th className="py-1.5 pr-3">节点</th>
                      <th className="py-1.5 px-2">物理网卡 / MAC</th>
                      <th className="py-1.5 px-2">默认网关 ARP</th>
                      <th className="py-1.5 px-2">MTU 链路 (NIC → veth)</th>
                      <th className="py-1.5 px-2">出向 NAT (SNAT)</th>
                      <th className="py-1.5 pl-2 text-right">体检状态</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-100 dark:divide-slate-800/70">
                    {kernelReport.nodes.map((n) => (
                      <tr key={n.nodeName} className="hover:bg-slate-50/70 dark:hover:bg-slate-800/30">
                        <td className="py-2 pr-3">
                          <div className="font-semibold text-slate-900 dark:text-white">{n.nodeName}</div>
                          <div className="text-[11px] font-mono text-slate-400">{n.internalIP}</div>
                        </td>
                        <td className="py-2 px-2 font-mono text-[11px]">
                          <div className="text-slate-800 dark:text-slate-200">{n.l2?.primaryNIC || n.mtu.primaryNIC}</div>
                          <div className="text-slate-400">{n.l2?.primaryMAC || '—'}</div>
                        </td>
                        <td className="py-2 px-2 font-mono text-[11px]">
                          <div className="flex items-center gap-1.5">
                            <span>{n.l2?.defaultGateway || '—'}</span>
                            {n.l2?.gatewayARPState && (
                              <span className={`px-1.5 py-0.2 rounded border text-[10px] ${arpStateClass(n.l2.gatewayARPState)}`}>
                                {n.l2.gatewayARPState}
                              </span>
                            )}
                          </div>
                          {n.l2?.gatewayMAC && <div className="text-[10px] text-slate-400">{n.l2.gatewayMAC}</div>}
                        </td>
                        <td className="py-2 px-2 font-mono text-[11px]">
                          <span className="text-slate-800 dark:text-slate-200">
                            {n.mtu.primaryMTU}B
                            {n.mtu.tunnelIface ? ` → ${n.mtu.tunnelIface}(${n.mtu.tunnelMTU}B)` : ' → 直连'}
                            {n.mtu.sampledVethCount > 0 ? ` → veth(${n.mtu.minVethMTU}B)` : ''}
                          </span>
                        </td>
                        <td className="py-2 px-2 text-[11px]">
                          <div className="flex items-center gap-1.5">
                            {n.conntrack.snatRandomFully ? (
                              <span className="text-emerald-600 dark:text-emerald-400 font-medium">随机源端口 ✓</span>
                            ) : n.conntrack.natOutgoingEnabled ? (
                              <span className="text-amber-600 dark:text-amber-400 font-medium">未开随机端口</span>
                            ) : (
                              <span className="text-slate-400">无 NAT Outgoing</span>
                            )}
                          </div>
                          <div className="text-[10px] text-slate-400">
                            会话水位 {n.conntrack.usagePct.toFixed(1)}%
                            {n.conntrack.drop > 0 ? ` · 历史冲突丢包 ${n.conntrack.drop}` : ' · 零丢包'}
                          </div>
                        </td>
                        <td className="py-2 pl-2 text-right">
                          <StatusPill status={n.status} compact />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </SectionCard>

          {/* 2. Detected Issues with Progressive Kernel Evidence Inspection */}
          <SectionCard
            title="网络异常体检与根因排查"
            subtitle="先用路由 / NAT / MAC / ARP / MTU 网络语言说明业务影响；点击单条记录的「查看内核证据」可展开 Conntrack 与 Sysctl 细节"
            action={
              kernelReport.findings.length === 0 ? (
                <span className="inline-flex items-center gap-1 text-xs font-semibold text-emerald-600 dark:text-emerald-400">
                  <CheckCircle2 className="w-4 h-4" /> 全平面零异常
                </span>
              ) : (
                <span className="text-xs font-mono text-slate-500">共 {kernelReport.findings.length} 项</span>
              )
            }
          >
            {kernelReport.findings.length === 0 ? (
              <div className="px-4 py-5 flex items-center gap-3 text-emerald-700 dark:text-emerald-300 bg-emerald-50/60 dark:bg-emerald-950/20 rounded-lg text-xs">
                <CheckCircle2 className="w-5 h-5 shrink-0" />
                <span>
                  所有节点的二层 MAC/ARP、网关连通性、Calico Proxy ARP、多层 MTU 与 NAT 转发均完全健康，未检测到异常丢包或配置隐患。
                </span>
              </div>
            ) : (
              <div className="divide-y divide-slate-100 dark:divide-slate-800">
                {kernelReport.findings.map((f: AuditFinding) => {
                  const key = `${f.nodeName || 'cluster'}-${f.id}`;
                  const evidenceOpen = !!expandedEvidenceIds[key];
                  return (
                    <div key={key} className="p-3.5 space-y-2">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <div className="flex flex-wrap items-center gap-2">
                          <StatusPill status={f.severity} compact />
                          <span className="px-2 py-0.5 rounded text-[10px] font-semibold bg-blue-50 dark:bg-blue-950/50 text-blue-700 dark:text-blue-300">
                            {CATEGORY_LABEL[f.category] || f.category}
                          </span>
                          <span className="px-2 py-0.5 rounded text-[10px] font-mono bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-300">
                            {f.nodeName || '集群全局'}
                          </span>
                          <span className="text-xs font-bold text-slate-900 dark:text-white">{f.title}</span>
                        </div>

                        {f.kernelEvidence && (
                          <button
                            onClick={() => toggleEvidence(key)}
                            className="inline-flex items-center gap-1 px-2.5 py-1 rounded-md text-[11px] font-medium bg-slate-100 dark:bg-slate-800 hover:bg-blue-50 dark:hover:bg-blue-950/50 text-slate-600 dark:text-slate-300 hover:text-blue-700 dark:hover:text-blue-300 transition"
                          >
                            <Terminal className="w-3 h-3" />
                            <span>{evidenceOpen ? '收起内核证据' : '查看内核证据 (Conntrack / Sysctl)'}</span>
                            {evidenceOpen ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
                          </button>
                        )}
                      </div>

                      <p className="text-xs text-slate-600 dark:text-slate-300 leading-relaxed">{f.detail}</p>

                      {/* Progressive Drilldown: Only revealed when user clicks into underlying kernel evidence */}
                      {evidenceOpen && f.kernelEvidence && (
                        <div className="px-3 py-2 rounded-lg bg-amber-50/70 dark:bg-amber-950/20 border border-amber-200/70 dark:border-amber-900/60 text-[11px] font-mono text-amber-900 dark:text-amber-200 space-y-1">
                          <div className="text-[10px] font-sans font-semibold uppercase tracking-wide text-amber-700 dark:text-amber-400">
                            底层内核态证据（仅排查根因时参考）
                          </div>
                          <div className="break-all">{f.kernelEvidence}</div>
                        </div>
                      )}

                      {f.remediation && (
                        <div className="flex items-center justify-between gap-2 px-3 py-1.5 rounded-lg bg-slate-100/90 dark:bg-slate-950 border border-slate-200/80 dark:border-slate-800 font-mono text-[11px] text-slate-700 dark:text-slate-300">
                          <span className="truncate">建议：{f.remediation}</span>
                          <button
                            onClick={() => copyText(key, f.remediation || '')}
                            className="shrink-0 inline-flex items-center gap-1 px-2 py-0.5 rounded bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 text-[10px] hover:text-blue-600"
                          >
                            {copiedKey === key ? <Check className="w-3 h-3 text-emerald-500" /> : <Copy className="w-3 h-3" />}
                            <span>{copiedKey === key ? '已复制' : '复制'}</span>
                          </button>
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            )}
          </SectionCard>

          {/* 3. Interactive Path MTU / DF Blackhole Probe */}
          <SectionCard
            title="Path MTU / DF 大包黑洞探测（验证小包通、大包卡死）"
            subtitle="进入源 Pod 网络命名空间发送带 DF（不允许分片）标志的测试报文，验证物理交换机 / 隧道沿途真实最大 MTU"
            action={<Ruler className="w-4 h-4 text-blue-500" />}
          >
            <div className="p-4 space-y-3">
              <div className="grid grid-cols-1 md:grid-cols-12 gap-2.5 items-end">
                <div className="md:col-span-3">
                  <label className="block text-[10px] font-semibold uppercase text-slate-400 mb-1">源 Namespace</label>
                  <select
                    value={pmtudNs}
                    onChange={(e) => {
                      const nextNs = e.target.value;
                      setPmtudNs(nextNs);
                      const first = pods.find((p) => p.namespace === nextNs && p.phase === 'Running' && !p.hostNetwork && p.ip);
                      setPmtudPod(first ? first.name : '');
                    }}
                    className="w-full px-2.5 py-1.5 rounded-lg text-xs bg-slate-50 dark:bg-slate-950 border border-slate-200 dark:border-slate-700"
                  >
                    {namespaces.map((ns) => (
                      <option key={ns} value={ns}>{ns}</option>
                    ))}
                  </select>
                </div>

                <div className="md:col-span-3">
                  <label className="block text-[10px] font-semibold uppercase text-slate-400 mb-1">源 Pod</label>
                  <select
                    value={pmtudPod}
                    onChange={(e) => setPmtudPod(e.target.value)}
                    className="w-full px-2.5 py-1.5 rounded-lg text-xs font-mono bg-slate-50 dark:bg-slate-950 border border-slate-200 dark:border-slate-700"
                  >
                    {podsInNs.map((p) => (
                      <option key={p.name} value={p.name}>
                        {p.name} ({p.ip})
                      </option>
                    ))}
                  </select>
                </div>

                <div className="md:col-span-3">
                  <label className="block text-[10px] font-semibold uppercase text-slate-400 mb-1">目标 IP (Pod / Node / 网关)</label>
                  <input
                    value={pmtudTargetIP}
                    onChange={(e) => setPmtudTargetIP(e.target.value)}
                    placeholder="如 172.31.226.27"
                    className="w-full px-2.5 py-1.5 rounded-lg text-xs font-mono bg-slate-50 dark:bg-slate-950 border border-slate-200 dark:border-slate-700"
                  />
                </div>

                <div className="md:col-span-1">
                  <label className="block text-[10px] font-semibold uppercase text-slate-400 mb-1">探测上限</label>
                  <input
                    type="number"
                    value={pmtudMaxMTU}
                    min={576}
                    max={9000}
                    onChange={(e) => setPmtudMaxMTU(Number(e.target.value) || 1500)}
                    className="w-full px-2 py-1.5 rounded-lg text-xs font-mono bg-slate-50 dark:bg-slate-950 border border-slate-200 dark:border-slate-700"
                  />
                </div>

                <div className="md:col-span-2">
                  <button
                    onClick={handleRunPMTUD}
                    disabled={pmtudRunning || !pmtudPod || !pmtudTargetIP.trim()}
                    className="w-full inline-flex items-center justify-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-semibold bg-blue-600 hover:bg-blue-700 text-white transition disabled:opacity-50"
                  >
                    {pmtudRunning ? <RefreshCw className="w-3.5 h-3.5 animate-spin" /> : <Play className="w-3.5 h-3.5" />}
                    <span>{pmtudRunning ? '探测中...' : '执行 DF 探测'}</span>
                  </button>
                </div>
              </div>

              {quickPmtudTargets.length > 0 && (
                <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
                  <span className="text-slate-400">快速目标:</span>
                  {quickPmtudTargets.map((t) => (
                    <button
                      key={t.ip}
                      onClick={() => setPmtudTargetIP(t.ip)}
                      className="px-2 py-0.5 rounded-md bg-slate-100 dark:bg-slate-800 hover:bg-blue-50 dark:hover:bg-blue-950/50 text-slate-600 dark:text-slate-300 font-mono text-[10px] border border-slate-200/70 dark:border-slate-700 transition"
                    >
                      {t.label}: {t.ip}
                    </button>
                  ))}
                </div>
              )}

              {pmtudError && (
                <div className="p-2.5 rounded-lg bg-rose-50 dark:bg-rose-950/30 border border-rose-200 dark:border-rose-900 text-xs text-rose-700 dark:text-rose-300">
                  探测执行失败: {pmtudError}
                </div>
              )}

              {pmtudResult && (
                <div className="rounded-xl border border-slate-200 dark:border-slate-800 bg-slate-50/70 dark:bg-slate-950/50 p-3.5 space-y-2.5">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <StatusPill status={pmtudResult.status} />
                      <span className="text-xs font-mono font-bold text-slate-900 dark:text-white">
                        实测 Path MTU = {pmtudResult.discoveredMTU} Bytes
                      </span>
                      <span className="text-[11px] font-mono text-slate-500">
                        (Pod eth0 MTU = {pmtudResult.podMTU}B · {pmtudResult.srcIP} → {pmtudResult.targetIP})
                      </span>
                    </div>
                    {pmtudResult.blackhole && (
                      <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-semibold bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300">
                        <ShieldAlert className="w-3.5 h-3.5" /> 检测到 MTU 黑洞
                      </span>
                    )}
                  </div>
                  <p className="text-xs text-slate-700 dark:text-slate-300">{pmtudResult.diagnosis}</p>
                  <div className="flex flex-wrap gap-2">
                    {pmtudResult.attempts.map((a, i) => (
                      <div
                        key={`${a.packetSize}-${i}`}
                        className={`px-2.5 py-1.5 rounded-lg border text-[11px] font-mono ${
                          a.success
                            ? 'bg-emerald-50/80 dark:bg-emerald-950/30 border-emerald-200 dark:border-emerald-900 text-emerald-800 dark:text-emerald-200'
                            : a.result === 'FRAG_NEEDED'
                              ? 'bg-amber-50/80 dark:bg-amber-950/30 border-amber-200 dark:border-amber-900 text-amber-800 dark:text-amber-200'
                              : 'bg-rose-50/80 dark:bg-rose-950/30 border-rose-200 dark:border-rose-900 text-rose-800 dark:text-rose-200'
                        }`}
                      >
                        <div className="font-bold">{a.packetSize} B</div>
                        <div className="text-[10px] opacity-85">
                          {a.result} {a.success ? `(${a.latencyMs.toFixed(2)}ms)` : ''}
                        </div>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          </SectionCard>

          {/* 4. Collapsible Advanced Drawer: Underlying Linux Conntrack & Sysctl Raw Diagnostics */}
          <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl overflow-hidden">
            <button
              onClick={() => setShowRawKernelPanel((v) => !v)}
              className="w-full px-4 py-3 flex items-center justify-between text-left hover:bg-slate-50/80 dark:hover:bg-slate-800/40 transition"
            >
              <div className="flex items-center gap-2.5">
                <SlidersHorizontal className="w-4 h-4 text-slate-400" />
                <div>
                  <div className="text-xs font-bold text-slate-800 dark:text-slate-100">
                    高级深度排障：底层 Linux Conntrack 状态表与内核 Sysctl 原始参数
                  </div>
                  <div className="text-[11px] text-slate-400">
                    默认收起以保持视图清晰；需要深入核查连接跟踪表多核计数或单个 sysctl 配置时可点击展开
                  </div>
                </div>
              </div>
              <span className="inline-flex items-center gap-1 text-xs font-medium text-blue-600 dark:text-blue-400 shrink-0">
                <span>{showRawKernelPanel ? '收起' : `展开 (${kernelReport.nodes.length} 节点)`}</span>
                {showRawKernelPanel ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
              </span>
            </button>

            {showRawKernelPanel && (
              <div className="border-t border-slate-100 dark:border-slate-800 divide-y divide-slate-100 dark:divide-slate-800">
                {kernelReport.nodes.map((node: NodeKernelAudit) => {
                  const open = expandedKernelNode === node.nodeName;
                  const ct = node.conntrack;
                  return (
                    <div key={node.nodeName} className="p-3.5">
                      <button
                        onClick={() => setExpandedKernelNode(open ? null : node.nodeName)}
                        className="w-full text-left flex flex-wrap items-center justify-between gap-2"
                      >
                        <div className="flex items-center gap-2">
                          {open ? <ChevronDown className="w-4 h-4 text-slate-400" /> : <ChevronRight className="w-4 h-4 text-slate-400" />}
                          <span className="text-xs font-bold text-slate-900 dark:text-white">{node.nodeName}</span>
                          <span className="text-[11px] font-mono text-slate-400">{node.kernelVersion}</span>
                        </div>
                        <div className="text-[11px] font-mono text-slate-500 flex items-center gap-3">
                          <span>Conntrack {ct.count}/{ct.max} ({ct.usagePct.toFixed(1)}%)</span>
                          <span>insert_failed={ct.insertFailed} · drop={ct.drop} · early_drop={ct.earlyDrop}</span>
                        </div>
                      </button>

                      {open && (
                        <div className="mt-3 pt-3 border-t border-slate-100 dark:border-slate-800 space-y-3">
                          <div className="grid grid-cols-2 sm:grid-cols-6 gap-2 text-[11px] font-mono bg-slate-50 dark:bg-slate-950/60 p-2.5 rounded-lg border border-slate-100 dark:border-slate-800">
                            <div>
                              <div className="text-[10px] text-slate-400">Buckets</div>
                              <div className="font-semibold">{ct.buckets.toLocaleString()}</div>
                            </div>
                            <div>
                              <div className="text-[10px] text-slate-400">invalid</div>
                              <div className="font-semibold">{ct.invalid.toLocaleString()}</div>
                            </div>
                            <div>
                              <div className="text-[10px] text-slate-400">insert_failed</div>
                              <div className={`font-semibold ${ct.insertFailed > 0 ? 'text-amber-600' : ''}`}>{ct.insertFailed.toLocaleString()}</div>
                            </div>
                            <div>
                              <div className="text-[10px] text-slate-400">drop / early_drop</div>
                              <div className={`font-semibold ${ct.drop + ct.earlyDrop > 0 ? 'text-amber-600' : ''}`}>{ct.drop} / {ct.earlyDrop}</div>
                            </div>
                            <div>
                              <div className="text-[10px] text-slate-400">search_restart</div>
                              <div className="font-semibold">{ct.searchRestart.toLocaleString()}</div>
                            </div>
                            <div>
                              <div className="text-[10px] text-slate-400">nf_conntrack_acct</div>
                              <div className="font-semibold">{ct.acctEnabled ? '1 (已开)' : '0'}</div>
                            </div>
                          </div>

                          <div className="overflow-x-auto">
                            <table className="w-full text-xs">
                              <thead>
                                <tr className="text-left text-[10px] uppercase text-slate-400 border-b border-slate-100 dark:border-slate-800">
                                  <th className="py-1.5 pr-3">内核参数 (Sysctl)</th>
                                  <th className="py-1.5 px-2">当前值</th>
                                  <th className="py-1.5 px-2">推荐值</th>
                                  <th className="py-1.5 px-2">状态</th>
                                  <th className="py-1.5 pl-2">网络含义</th>
                                </tr>
                              </thead>
                              <tbody className="divide-y divide-slate-100 dark:divide-slate-800/70">
                                {node.sysctls.map((sc) => (
                                  <tr key={sc.key} className="hover:bg-slate-50/70 dark:hover:bg-slate-800/30">
                                    <td className="py-1.5 pr-3 font-mono text-[11px]">{sc.key}</td>
                                    <td className="py-1.5 px-2 font-mono text-[11px] font-semibold">{sc.value}</td>
                                    <td className="py-1.5 px-2 font-mono text-[11px] text-slate-500">{sc.expected}</td>
                                    <td className="py-1.5 px-2"><StatusPill status={sc.status} compact /></td>
                                    <td className="py-1.5 pl-2 text-[11px] text-slate-600 dark:text-slate-300">{sc.explanation}</td>
                                  </tr>
                                ))}
                              </tbody>
                            </table>
                          </div>
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        </div>
      )}

      {/* ==================== TAB 2: CALICO BGP NEIGHBORS & L3 ROUTE CONSISTENCY ==================== */}
      {subTab === 'routes' && routeReport && (
        <div className="space-y-4">
          {/* 1. Calico BGP Neighbors Table */}
          <SectionCard
            title="Calico BGP 邻居关系（BIRD Peer 状态）"
            subtitle="展示集群节点间及外部路由反射器（RR / ToR）的 BGP 邻居建立状态、AS 号与持续运行时长"
            action={
              <span className="text-xs font-mono text-slate-500">
                AS{modeProfile?.bgpASNumber || 64512} · {modeProfile?.nodeToNodeMesh ? 'Node-to-Node Full Mesh' : 'Route Reflector'}
              </span>
            }
          >
            {!kernelReport?.bgpNeighbors || kernelReport.bgpNeighbors.length === 0 ? (
              <div className="p-4 text-xs text-slate-500">
                当前集群未检测到活跃 BGP Peer（可能使用 VXLAN 无 BGP 模式或 BGP 未启用）。
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-xs">
                  <thead>
                    <tr className="text-left text-[10px] uppercase text-slate-400 border-b border-slate-100 dark:border-slate-800">
                      <th className="py-2 px-3">本端节点</th>
                      <th className="py-2 px-3">Peer 会话名</th>
                      <th className="py-2 px-3">对端邻居 (节点 / IP)</th>
                      <th className="py-2 px-3">拓扑角色</th>
                      <th className="py-2 px-3">AS 号</th>
                      <th className="py-2 px-3">会话状态</th>
                      <th className="py-2 px-3">建立时间 (Since)</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                    {kernelReport.bgpNeighbors.map((bp, idx) => (
                      <tr key={`${bp.sourceNode}-${bp.name}-${idx}`} className="hover:bg-slate-50/70 dark:hover:bg-slate-800/30">
                        <td className="py-2 px-3 font-semibold text-slate-900 dark:text-white">{bp.sourceNode}</td>
                        <td className="py-2 px-3 font-mono text-[11px] text-slate-700 dark:text-slate-300">{bp.name}</td>
                        <td className="py-2 px-3 font-mono text-[11px]">
                          <span className="font-semibold text-blue-700 dark:text-blue-300">{bp.peerNode || '外部路由器'}</span>
                          <span className="text-slate-400 ml-1.5">({bp.peerIP})</span>
                        </td>
                        <td className="py-2 px-3">
                          <span className="px-2 py-0.5 rounded text-[10px] font-medium bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-300">
                            {bp.scope}
                          </span>
                        </td>
                        <td className="py-2 px-3 font-mono text-[11px]">AS{bp.asNumber}</td>
                        <td className="py-2 px-3"><StatusPill status={bp.state} compact /></td>
                        <td className="py-2 px-3 font-mono text-[11px] text-slate-500">{bp.since || '—'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </SectionCard>

          {/* 2. Expected vs Actual Route Diff List */}
          <SectionCard
            title="Expected vs Actual 三层路由一致性审计"
            subtitle="自动比对 Calico IPAMBlock 归属、运行中 Pod 与各节点 Linux 主路由表，排查跨节点路由缺失、幽灵 Pod /32 残留及去回程非对称"
            action={
              routeReport.drifts.length === 0 ? (
                <span className="inline-flex items-center gap-1 text-xs font-semibold text-emerald-600 dark:text-emerald-400">
                  <CheckCircle2 className="w-4 h-4" /> 零偏差 · 全集群一致
                </span>
              ) : (
                <span className="text-xs font-mono text-rose-600 dark:text-rose-400">
                  {routeReport.drifts.length} 项偏差
                </span>
              )
            }
          >
            {routeReport.drifts.length === 0 ? (
              <div className="px-4 py-4 flex items-center gap-3 text-emerald-700 dark:text-emerald-300 bg-emerald-50/60 dark:bg-emerald-950/20 rounded-lg text-xs">
                <CheckCircle2 className="w-5 h-5 shrink-0" />
                <span>
                  全集群所有节点的跨节点 IPAMBlock 路由、防环路 Blackhole 路由、本地 Pod /32 主机路由及双节点去回程封装路径完全一致！
                </span>
              </div>
            ) : (
              <div className="divide-y divide-slate-100 dark:divide-slate-800">
                {routeReport.drifts.map((d, idx) => (
                  <div key={`${d.nodeName}-${d.cidr}-${idx}`} className="p-3.5 space-y-1.5">
                    <div className="flex flex-wrap items-center gap-2">
                      <StatusPill status={d.severity} compact />
                      <span className="px-2 py-0.5 rounded text-[10px] font-semibold bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-200">
                        {DRIFT_KIND_LABEL[d.kind] || d.kind}
                      </span>
                      <span className="px-2 py-0.5 rounded text-[10px] font-mono bg-blue-50 dark:bg-blue-950/50 text-blue-700 dark:text-blue-300">
                        {d.nodeName}
                      </span>
                      <span className="text-xs font-mono font-bold text-slate-900 dark:text-white">{d.cidr}</span>
                    </div>
                    <p className="text-xs text-slate-600 dark:text-slate-300">{d.detail}</p>
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-2 text-[11px] font-mono">
                      <div className="px-2.5 py-1.5 rounded bg-emerald-50/70 dark:bg-emerald-950/20 border border-emerald-200/60 dark:border-emerald-900/50 text-emerald-800 dark:text-emerald-200">
                        <span className="text-[10px] uppercase opacity-70 mr-1.5">Expected:</span>
                        {d.expected}
                      </div>
                      <div className="px-2.5 py-1.5 rounded bg-rose-50/70 dark:bg-rose-950/20 border border-rose-200/60 dark:border-rose-900/50 text-rose-800 dark:text-rose-200">
                        <span className="text-[10px] uppercase opacity-70 mr-1.5">Actual:</span>
                        {d.actual}
                      </div>
                    </div>
                    {d.remediation && (
                      <div className="px-3 py-1.5 rounded-lg bg-slate-100/90 dark:bg-slate-950 font-mono text-[11px] text-slate-700 dark:text-slate-300">
                        建议: {d.remediation}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            )}
          </SectionCard>

          {/* 3. BGP Flapping / State Transition Timeline */}
          <SectionCard
            title="BGP 会话状态变更 / 抖动时间线"
            subtitle="实时记录各节点 BIRD BGP 会话的断连与恢复翻转事件，排查间歇性路由闪断"
          >
            {!routeReport.bgpEvents || routeReport.bgpEvents.length === 0 ? (
              <div className="px-4 py-3.5 text-xs text-slate-500 flex items-center gap-2">
                <CheckCircle2 className="w-4 h-4 text-emerald-500 shrink-0" />
                <span>当前观测周期内未检测到任何 BGP 邻居断连或状态抖动事件，所有会话稳定保持在 Established。</span>
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-xs">
                  <thead>
                    <tr className="text-left text-[10px] uppercase text-slate-400 border-b border-slate-100 dark:border-slate-800">
                      <th className="py-2 px-3">时间</th>
                      <th className="py-2 px-3">节点</th>
                      <th className="py-2 px-3">BGP Peer</th>
                      <th className="py-2 px-3">状态翻转</th>
                      <th className="py-2 px-3">级别</th>
                      <th className="py-2 px-3">说明</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                    {routeReport.bgpEvents.map((ev, idx) => (
                      <tr key={`${ev.nodeName}-${ev.peerName}-${idx}`}>
                        <td className="py-2 px-3 font-mono text-[11px] text-slate-500">{new Date(ev.timestamp).toLocaleTimeString()}</td>
                        <td className="py-2 px-3 font-mono text-[11px] font-semibold">{ev.nodeName}</td>
                        <td className="py-2 px-3 font-mono text-[11px]">{ev.peerName} ({ev.peerIP})</td>
                        <td className="py-2 px-3 font-mono text-[11px]">{ev.prevState} → {ev.newState}</td>
                        <td className="py-2 px-3"><StatusPill status={ev.severity} compact /></td>
                        <td className="py-2 px-3 text-[11px] text-slate-600 dark:text-slate-300">{ev.message}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </SectionCard>

          {/* 4. Per-node Kernel Routing Table Inspector */}
          <SectionCard
            title="节点 Linux 主路由表实时透视"
            subtitle="点击节点展开真实 IPv4 路由表（支持按 BIRD / 本地 Pod /32 / Blackhole 过滤与 CIDR 检索）"
          >
            <div className="divide-y divide-slate-100 dark:divide-slate-800">
              {routeReport.nodes.map((node: NodeRouteAudit) => {
                const open = expandedRouteNode === node.nodeName;
                const filteredRoutes = (node.routes || []).filter((rt) => {
                  if (routeFilterProto === 'bird' && rt.protocol !== 'bird') return false;
                  if (routeFilterProto === 'blackhole' && rt.type !== 'blackhole') return false;
                  if (routeFilterProto === 'pod' && !(rt.dst.endsWith('/32') && rt.dev?.startsWith('cali'))) return false;
                  if (routeSearch.trim()) {
                    const q = routeSearch.trim().toLowerCase();
                    const hay = `${rt.dst} ${rt.gateway || ''} ${rt.dev || ''} ${rt.protocol} ${rt.type}`.toLowerCase();
                    if (!hay.includes(q)) return false;
                  }
                  return true;
                });

                return (
                  <div key={node.nodeName} className="p-3.5">
                    <button
                      onClick={() => setExpandedRouteNode(open ? null : node.nodeName)}
                      className="w-full text-left flex flex-wrap items-center justify-between gap-3"
                    >
                      <div className="flex items-center gap-2.5">
                        {open ? <ChevronDown className="w-4 h-4 text-slate-400" /> : <ChevronRight className="w-4 h-4 text-slate-400" />}
                        <span className="text-xs font-bold text-slate-900 dark:text-white">{node.nodeName}</span>
                        <span className="text-[11px] font-mono text-slate-400">{node.internalIP}</span>
                        <StatusPill status={node.status} compact />
                      </div>

                      <div className="flex flex-wrap items-center gap-3 text-[11px] font-mono text-slate-600 dark:text-slate-300">
                        <span>总路由 <b>{node.totalRoutes}</b></span>
                        <span>BIRD <b>{node.birdRoutes}</b></span>
                        <span>本地 Pod /32 <b>{node.localPodRoutes}/{node.expectedLocalPods}</b></span>
                        <span>Blackhole <b>{node.blackholeRoutes}</b></span>
                        <span>BGP Peer <b>{node.bgpPeersUp}/{node.bgpPeersTotal}</b></span>
                        <span className={node.driftCount > 0 ? 'text-rose-600 font-bold' : 'text-emerald-600 font-semibold'}>
                          偏差 {node.driftCount}
                        </span>
                      </div>
                    </button>

                    {open && (
                      <div className="mt-3 pt-3 border-t border-slate-100 dark:border-slate-800 space-y-2.5">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <div className="flex flex-wrap items-center gap-1.5">
                            {(
                              [
                                ['all', `全部 (${node.routes.length})`],
                                ['bird', `BIRD (${node.birdRoutes})`],
                                ['pod', `本地 Pod /32 (${node.localPodRoutes})`],
                                ['blackhole', `Blackhole (${node.blackholeRoutes})`],
                              ] as const
                            ).map(([k, label]) => (
                              <button
                                key={k}
                                onClick={() => setRouteFilterProto(k)}
                                className={`px-2.5 py-1 rounded-md text-[11px] font-medium transition ${
                                  routeFilterProto === k
                                    ? 'bg-blue-600 text-white'
                                    : 'bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-300 hover:bg-slate-200/70'
                                }`}
                              >
                                {label}
                              </button>
                            ))}
                          </div>
                          <div className="relative w-56">
                            <Search className="w-3.5 h-3.5 text-slate-400 absolute left-2.5 top-1/2 -translate-y-1/2" />
                            <input
                              value={routeSearch}
                              onChange={(e) => setRouteSearch(e.target.value)}
                              placeholder="筛选 CIDR / 下一跳 / 网卡..."
                              className="w-full pl-8 pr-2.5 py-1 rounded-lg text-[11px] font-mono bg-slate-50 dark:bg-slate-950 border border-slate-200 dark:border-slate-700"
                            />
                          </div>
                        </div>

                        <div className="max-h-[340px] overflow-y-auto border border-slate-100 dark:border-slate-800 rounded-lg">
                          <table className="w-full text-[11px] font-mono">
                            <thead className="sticky top-0 bg-slate-50 dark:bg-slate-950 text-left text-[10px] uppercase text-slate-400 border-b border-slate-100 dark:border-slate-800">
                              <tr>
                                <th className="py-1.5 px-3">目标网段 (Dst)</th>
                                <th className="py-1.5 px-3">下一跳 (Gateway)</th>
                                <th className="py-1.5 px-3">出接口 (Dev)</th>
                                <th className="py-1.5 px-3">来源协议</th>
                                <th className="py-1.5 px-3">路由类型</th>
                                <th className="py-1.5 px-3">Scope</th>
                              </tr>
                            </thead>
                            <tbody className="divide-y divide-slate-100 dark:divide-slate-800/70">
                              {filteredRoutes.map((rt, idx) => (
                                <tr key={`${rt.dst}-${rt.dev}-${idx}`} className="hover:bg-slate-50/70 dark:hover:bg-slate-800/30">
                                  <td className="py-1.5 px-3 font-semibold text-slate-900 dark:text-white">{rt.dst}</td>
                                  <td className="py-1.5 px-3 text-slate-600 dark:text-slate-300">{rt.gateway || '—'}</td>
                                  <td className="py-1.5 px-3 text-blue-700 dark:text-blue-300">{rt.dev || '—'}</td>
                                  <td className="py-1.5 px-3">
                                    <span className="px-1.5 py-0.5 rounded bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-300">
                                      {rt.protocol}
                                    </span>
                                  </td>
                                  <td className="py-1.5 px-3">
                                    <span
                                      className={`px-1.5 py-0.5 rounded ${
                                        rt.type === 'blackhole'
                                          ? 'bg-slate-800 text-white dark:bg-slate-700'
                                          : 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/50 dark:text-emerald-300'
                                      }`}
                                    >
                                      {rt.type}
                                    </span>
                                  </td>
                                  <td className="py-1.5 px-3 text-slate-500">{rt.scope || '—'}</td>
                                </tr>
                              ))}
                              {filteredRoutes.length === 0 && (
                                <tr>
                                  <td colSpan={6} className="py-6 text-center text-slate-400 font-sans">
                                    无匹配路由条目
                                  </td>
                                </tr>
                              )}
                            </tbody>
                          </table>
                        </div>
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          </SectionCard>
        </div>
      )}

      {/* ==================== TAB 3: L2 MAC · GATEWAY · ARP NEIGHBOR TABLE ==================== */}
      {subTab === 'l2' && kernelReport && (
        <div className="space-y-4">
          {/* Calico L2 / Proxy ARP Explainer Strip */}
          <div className="px-4 py-3 rounded-xl bg-indigo-50/70 dark:bg-indigo-950/30 border border-indigo-200/80 dark:border-indigo-900 text-xs text-indigo-900 dark:text-indigo-200 flex items-start gap-2.5">
            <Cable className="w-4 h-4 text-indigo-600 dark:text-indigo-400 mt-0.5 shrink-0" />
            <div>
              <b>Calico 二层工作原理提示：</b>
              Pod 内部把 <span className="font-mono font-semibold">169.254.1.1</span> 作为默认网关，宿主机侧 <span className="font-mono font-semibold">cali*</span> veth 通过开启 <b>Proxy ARP</b> 代答容器的 ARP 请求（返回虚拟 MAC <span className="font-mono">ee:ee:ee:ee:ee:ee</span>）；跨节点通信则依赖物理网卡对同网段集群节点或默认网关的正常 ARP MAC 解析。
            </div>
          </div>

          {/* Per-node L2 Identity & Gateway Cards */}
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            {kernelReport.nodes.map((n) => {
              const selected = activeL2Node?.nodeName === n.nodeName;
              const l2 = n.l2;
              return (
                <button
                  key={n.nodeName}
                  onClick={() => setSelectedL2Node(n.nodeName)}
                  className={`text-left p-4 rounded-xl border transition ${
                    selected
                      ? 'bg-blue-50/50 dark:bg-blue-950/30 border-blue-400 dark:border-blue-600 shadow-sm'
                      : 'bg-white dark:bg-slate-900 border-slate-200 dark:border-slate-800 hover:border-blue-300'
                  }`}
                >
                  <div className="flex items-center justify-between gap-2">
                    <div className="flex items-center gap-2">
                      <Server className="w-4 h-4 text-blue-600 dark:text-blue-400" />
                      <span className="text-xs font-bold text-slate-900 dark:text-white">{n.nodeName}</span>
                      <span className="text-[11px] font-mono text-slate-400">{n.internalIP}</span>
                    </div>
                    {l2 && l2.arpFailedCount > 0 ? (
                      <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-rose-100 text-rose-700">
                        {l2.arpFailedCount} 项 ARP 异常
                      </span>
                    ) : (
                      <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300">
                        二层正常
                      </span>
                    )}
                  </div>

                  <div className="mt-3 grid grid-cols-2 gap-2.5 text-[11px] font-mono">
                    <div className="p-2 rounded-lg bg-white/80 dark:bg-slate-950/60 border border-slate-100 dark:border-slate-800">
                      <div className="text-[10px] font-sans text-slate-400">主物理网卡 &amp; MAC</div>
                      <div className="font-semibold text-slate-800 dark:text-slate-100 mt-0.5">
                        {l2?.primaryNIC || n.mtu.primaryNIC} (MTU {n.mtu.primaryMTU})
                      </div>
                      <div className="text-slate-500">{l2?.primaryMAC || '—'}</div>
                    </div>

                    <div className="p-2 rounded-lg bg-white/80 dark:bg-slate-950/60 border border-slate-100 dark:border-slate-800">
                      <div className="text-[10px] font-sans text-slate-400">默认网关 &amp; ARP 状态</div>
                      <div className="font-semibold text-slate-800 dark:text-slate-100 mt-0.5 flex items-center gap-1.5">
                        <span>{l2?.defaultGateway || '—'}</span>
                        {l2?.gatewayARPState && (
                          <span className={`px-1 py-0.2 rounded border text-[9px] ${arpStateClass(l2.gatewayARPState)}`}>
                            {l2.gatewayARPState}
                          </span>
                        )}
                      </div>
                      <div className="text-slate-500">{l2?.gatewayMAC || '—'}</div>
                    </div>

                    <div className="p-2 rounded-lg bg-white/80 dark:bg-slate-950/60 border border-slate-100 dark:border-slate-800">
                      <div className="text-[10px] font-sans text-slate-400">Calico cali* Proxy ARP</div>
                      <div className="font-semibold text-emerald-700 dark:text-emerald-300 mt-0.5">
                        {l2?.proxyARPVeths ?? 0} / {l2?.totalCaliVeths ?? 0} 已开启
                      </div>
                    </div>

                    <div className="p-2 rounded-lg bg-white/80 dark:bg-slate-950/60 border border-slate-100 dark:border-slate-800">
                      <div className="text-[10px] font-sans text-slate-400">ARP 邻居表水位</div>
                      <div className="font-semibold text-slate-800 dark:text-slate-100 mt-0.5">
                        {l2?.arpCount ?? 0} / {l2?.arpGCThresh3 ?? 0} ({(l2?.arpUsagePct ?? 0).toFixed(1)}%)
                      </div>
                    </div>
                  </div>
                </button>
              );
            })}
          </div>

          {/* Selected Node ARP Neighbor Table */}
          {activeL2Node && (
            <SectionCard
              title={`节点 ${activeL2Node.nodeName} 实时 ARP 邻居表（IP ↔ MAC 映射）`}
              subtitle="默认优先展示默认网关、同网段集群节点与外部邻居；可切换查看本地 Pod veth ARP 或异常 FAILED 条目"
              action={
                <div className="flex flex-wrap items-center gap-2">
                  <div className="flex items-center gap-1">
                    {(
                      [
                        ['important', '网关与物理网段'],
                        ['all', `全部 (${activeL2Node.l2?.neighbors?.length || 0})`],
                        ['pod-veth', 'Pod veth'],
                        ['failed', '异常 FAILED'],
                      ] as const
                    ).map(([k, label]) => (
                      <button
                        key={k}
                        onClick={() => setArpRoleFilter(k)}
                        className={`px-2.5 py-1 rounded-md text-[11px] font-medium transition ${
                          arpRoleFilter === k
                            ? 'bg-blue-600 text-white'
                            : 'bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-300 hover:bg-slate-200/70'
                        }`}
                      >
                        {label}
                      </button>
                    ))}
                  </div>
                  <div className="relative w-48">
                    <Search className="w-3.5 h-3.5 text-slate-400 absolute left-2.5 top-1/2 -translate-y-1/2" />
                    <input
                      value={arpSearch}
                      onChange={(e) => setArpSearch(e.target.value)}
                      placeholder="搜索 IP / MAC / 节点..."
                      className="w-full pl-8 pr-2.5 py-1 rounded-lg text-[11px] font-mono bg-slate-50 dark:bg-slate-950 border border-slate-200 dark:border-slate-700"
                    />
                  </div>
                </div>
              }
            >
              <div className="max-h-[420px] overflow-y-auto">
                <table className="w-full text-xs">
                  <thead className="sticky top-0 bg-slate-50 dark:bg-slate-950 text-left text-[10px] uppercase text-slate-400 border-b border-slate-100 dark:border-slate-800">
                    <tr>
                      <th className="py-2 px-3">邻居 IP</th>
                      <th className="py-2 px-3">MAC 地址 (lladdr)</th>
                      <th className="py-2 px-3">网络角色</th>
                      <th className="py-2 px-3">关联对象 (节点 / Pod)</th>
                      <th className="py-2 px-3">出接口 (Dev)</th>
                      <th className="py-2 px-3">ARP 状态</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-100 dark:divide-slate-800/70 font-mono text-[11px]">
                    {filteredARPNeighbors.map((nb, idx) => {
                      const roleMeta = ARP_ROLE_META[nb.role || 'other'] || ARP_ROLE_META.other;
                      return (
                        <tr key={`${nb.ip}-${nb.dev}-${idx}`} className="hover:bg-slate-50/70 dark:hover:bg-slate-800/30">
                          <td className="py-2 px-3 font-semibold text-slate-900 dark:text-white">{nb.ip}</td>
                          <td className="py-2 px-3 text-slate-700 dark:text-slate-300">{nb.mac || '— (未解析)'}</td>
                          <td className="py-2 px-3 font-sans">
                            <span className={`px-2 py-0.5 rounded border text-[10px] font-medium ${roleMeta.cls}`}>
                              {roleMeta.label}
                            </span>
                          </td>
                          <td className="py-2 px-3 font-sans text-slate-700 dark:text-slate-300">
                            {nb.peerName || (nb.role === 'gateway' ? '默认三层网关' : '—')}
                          </td>
                          <td className="py-2 px-3 text-blue-700 dark:text-blue-300">{nb.dev || '—'}</td>
                          <td className="py-2 px-3">
                            <span className={`px-2 py-0.5 rounded border text-[10px] font-semibold ${arpStateClass(nb.state)}`}>
                              {nb.state}
                            </span>
                          </td>
                        </tr>
                      );
                    })}
                    {filteredARPNeighbors.length === 0 && (
                      <tr>
                        <td colSpan={6} className="py-8 text-center text-slate-400 font-sans text-xs">
                          当前筛选条件下无 ARP 邻居条目
                        </td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </div>
            </SectionCard>
          )}
        </div>
      )}
    </div>
  );
}
