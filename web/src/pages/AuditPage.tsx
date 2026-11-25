import React, { useEffect, useMemo, useState } from 'react';
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Copy,
  Check,
  Cpu,
  Download,
  GitCompareArrows,
  Play,
  RefreshCw,
  Ruler,
  Search,
  ShieldAlert,
  XCircle,
} from 'lucide-react';
import { api } from '../api/client';
import type {
  ClusterKernelAuditReport,
  ClusterRouteAuditReport,
  NodeKernelAudit,
  NodeRouteAudit,
  PMTUDProbeResult,
  PodBrief,
} from '../api/types';
import { EncapBadge, SectionCard, StatusPill } from '../components/common/Badges';

type AuditSubTab = 'kernel' | 'routes';

const DRIFT_KIND_LABEL: Record<string, string> = {
  missing_block: '缺失跨节点 Block 路由',
  missing_pod: '缺失本地 Pod /32 路由',
  ghost_pod_route: '残留幽灵 Pod /32 路由',
  unexpected_blackhole: '异常 Blackhole 路由',
  missing_blackhole: '缺失本地 Blackhole 防环路由',
  encap_mismatch: '封装模式不一致',
  asymmetric_path: '跨节点去回程路径非对称',
};

export function AuditPage() {
  const [subTab, setSubTab] = useState<AuditSubTab>('kernel');
  const [kernelReport, setKernelReport] = useState<ClusterKernelAuditReport | null>(null);
  const [routeReport, setRouteReport] = useState<ClusterRouteAuditReport | null>(null);
  const [pods, setPods] = useState<PodBrief[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Expanded nodes
  const [expandedKernelNode, setExpandedKernelNode] = useState<string | null>(null);
  const [expandedRouteNode, setExpandedRouteNode] = useState<string | null>(null);
  const [routeFilterProto, setRouteFilterProto] = useState<'all' | 'bird' | 'pod' | 'blackhole'>('all');
  const [routeSearch, setRouteSearch] = useState('');
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

      if (kRep.nodes.length > 0 && !expandedKernelNode) {
        const risky = kRep.nodes.find((n) => n.status !== 'healthy');
        setExpandedKernelNode(risky ? risky.nodeName : kRep.nodes[0].nodeName);
      }
      if (rRep.nodes.length > 0 && !expandedRouteNode) {
        const drifted = rRep.nodes.find((n) => n.driftCount > 0);
        setExpandedRouteNode(drifted ? drifted.nodeName : rRep.nodes[0].nodeName);
      }

      // Auto-prefill PMTUD source Pod & cross-node target IP for 1-click testing
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

  const maxConntrackNode = useMemo(() => {
    if (!kernelReport || kernelReport.nodes.length === 0) return null;
    return kernelReport.nodes.reduce((best, cur) =>
      cur.conntrack.usagePct > best.conntrack.usagePct ? cur : best,
    );
  }, [kernelReport]);

  const mtuSummary = useMemo(() => {
    if (!kernelReport) return { ok: true, text: '—' };
    const bad = kernelReport.nodes.filter((n) => n.mtu.status !== 'pass' && n.status !== 'unreachable');
    const mtus = Array.from(
      new Set(kernelReport.nodes.map((n) => n.mtu.primaryMTU).filter((m) => m > 0)),
    );
    if (bad.length > 0 || mtus.length > 1) {
      return { ok: false, text: mtus.length > 1 ? `不一致 (${mtus.join('/')})` : `${bad.length} 节点异常` };
    }
    return { ok: true, text: mtus.length === 1 ? `全集群 ${mtus[0]}B 一致` : '正常' };
  }, [kernelReport]);

  return (
    <div className="p-5 max-w-[1440px] mx-auto space-y-4">
      {/* Page Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-bold tracking-tight text-slate-900 dark:text-white flex items-center gap-2">
            <Cpu className="w-5 h-5 text-blue-600 dark:text-blue-400" />
            <span>内核体检与路由一致性审计</span>
            {kernelReport?.encapMode && <EncapBadge mode={kernelReport.encapMode} />}
          </h1>
          <p className="text-xs text-slate-500 dark:text-slate-400 mt-0.5">
            排查 Linux Conntrack/NAT 竞态、关键 Sysctl/MTU 黑洞隐患，并实时比对 Calico Expected vs Actual 内核路由表与 BGP 会话抖动
          </p>
        </div>

        <div className="flex items-center gap-2">
          <button
            onClick={loadAll}
            disabled={loading}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 hover:bg-slate-50 dark:hover:bg-slate-800 text-slate-700 dark:text-slate-200 transition disabled:opacity-50"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
            <span>重新审计</span>
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

      {/* Sub-navigation Tabs */}
      <div className="flex flex-wrap items-center gap-2 border-b border-slate-200 dark:border-slate-800 pb-2.5">
        <button
          onClick={() => setSubTab('kernel')}
          className={`inline-flex items-center gap-2 px-3.5 py-1.5 rounded-lg text-xs font-semibold transition ${
            subTab === 'kernel'
              ? 'bg-blue-600 text-white shadow-sm'
              : 'bg-white dark:bg-slate-900 text-slate-600 dark:text-slate-300 border border-slate-200 dark:border-slate-800 hover:bg-slate-50 dark:hover:bg-slate-800'
          }`}
        >
          <Activity className="w-3.5 h-3.5" />
          <span>内核 / Conntrack / MTU 体检</span>
          {kernelReport && (kernelReport.criticalCount > 0 || kernelReport.warningCount > 0) && (
            <span
              className={`px-1.5 py-0.2 rounded-full text-[10px] font-mono ${
                subTab === 'kernel'
                  ? 'bg-white/20 text-white'
                  : kernelReport.criticalCount > 0
                    ? 'bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300'
                    : 'bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300'
              }`}
            >
              {kernelReport.criticalCount + kernelReport.warningCount}
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
          <span>路由与 BGP 一致性审计</span>
          {routeReport && (
            <span
              className={`px-1.5 py-0.2 rounded-full text-[10px] font-mono ${
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
      </div>

      {error && (
        <div className="p-3.5 rounded-xl bg-rose-50 dark:bg-rose-950/30 border border-rose-200 dark:border-rose-900 text-rose-700 dark:text-rose-300 text-xs flex items-center gap-2">
          <XCircle className="w-4 h-4 shrink-0" />
          <span>审计数据拉取失败: {error}</span>
        </div>
      )}

      {loading && !kernelReport && !routeReport && (
        <div className="p-12 text-center text-xs text-slate-400">
          正在并发采集各节点内核 Conntrack、Sysctl、MTU 与路由表...
        </div>
      )}

      {/* ==================== TAB 1: KERNEL / CONNTRACK / MTU ==================== */}
      {subTab === 'kernel' && kernelReport && (
        <div className="space-y-4">
          {/* KPI Summary Row */}
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
            <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl p-3.5">
              <div className="text-[11px] font-semibold text-slate-400 uppercase">集群内核体检评分</div>
              <div className="mt-1.5 flex items-baseline justify-between">
                <span className="text-2xl font-bold font-mono text-slate-900 dark:text-white">
                  {kernelReport.averageScore}
                  <span className="text-xs font-normal text-slate-400 ml-1">/ 100</span>
                </span>
                <StatusPill status={kernelReport.overallStatus} compact />
              </div>
              <div className="text-[11px] text-slate-500 mt-1">
                覆盖 {kernelReport.nodes.length} 个集群节点实时采集
              </div>
            </div>

            <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl p-3.5">
              <div className="text-[11px] font-semibold text-slate-400 uppercase">隐患项统计</div>
              <div className="mt-1.5 flex items-center gap-3 font-mono">
                <span className="text-2xl font-bold text-rose-600 dark:text-rose-400">
                  {kernelReport.criticalCount}
                  <span className="text-[11px] font-sans font-normal text-slate-500 ml-1">严重</span>
                </span>
                <span className="text-2xl font-bold text-amber-600 dark:text-amber-400">
                  {kernelReport.warningCount}
                  <span className="text-[11px] font-sans font-normal text-slate-500 ml-1">警告</span>
                </span>
              </div>
              <div className="text-[11px] text-slate-500 mt-1">
                包含单节点参数及跨节点一致性漂移
              </div>
            </div>

            <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl p-3.5">
              <div className="text-[11px] font-semibold text-slate-400 uppercase">Conntrack 最高水位</div>
              <div className="mt-1.5 flex items-baseline justify-between">
                <span className="text-2xl font-bold font-mono text-slate-900 dark:text-white">
                  {maxConntrackNode ? `${maxConntrackNode.conntrack.usagePct.toFixed(2)}%` : '—'}
                </span>
                <span className="text-[11px] font-mono text-slate-500">
                  {maxConntrackNode ? `${maxConntrackNode.conntrack.count} / ${maxConntrackNode.conntrack.max}` : ''}
                </span>
              </div>
              <div className="text-[11px] text-slate-500 mt-1 truncate">
                最高节点: {maxConntrackNode?.nodeName || '—'}
              </div>
            </div>

            <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl p-3.5">
              <div className="text-[11px] font-semibold text-slate-400 uppercase">多层 MTU 一致性</div>
              <div className="mt-1.5 flex items-center justify-between">
                <span className="text-base font-bold text-slate-900 dark:text-white">{mtuSummary.text}</span>
                {mtuSummary.ok ? (
                  <CheckCircle2 className="w-5 h-5 text-emerald-500" />
                ) : (
                  <AlertTriangle className="w-5 h-5 text-amber-500" />
                )}
              </div>
              <div className="text-[11px] text-slate-500 mt-1">
                物理 NIC ↔ Tunnel ↔ cali* veth 链路校验
              </div>
            </div>
          </div>

          {/* Findings Section */}
          <SectionCard
            title="隐患诊断与修复建议"
            subtitle="自动识别 Conntrack 满表/竞态、SNAT --random-fully 缺失、rp_filter 严格模式、跨节点 MTU 漂移等偶发丢包元凶"
            action={
              kernelReport.findings.length === 0 ? (
                <span className="inline-flex items-center gap-1 text-xs font-semibold text-emerald-600 dark:text-emerald-400">
                  <CheckCircle2 className="w-4 h-4" /> 零高危隐患
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
                  全集群所有节点的 Conntrack 水位、NAT SNAT 规则、核心内核网络参数及物理/隧道/Pod MTU 均通过体检，未发现潜在网络丢包隐患。
                </span>
              </div>
            ) : (
              <div className="divide-y divide-slate-100 dark:divide-slate-800">
                {kernelReport.findings.map((f) => (
                  <div key={`${f.nodeName || 'cluster'}-${f.id}`} className="p-3.5 space-y-1.5">
                    <div className="flex flex-wrap items-center gap-2">
                      <StatusPill status={f.severity} compact />
                      <span className="px-2 py-0.5 rounded text-[10px] font-mono bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-300">
                        {f.nodeName || '集群全局'}
                      </span>
                      <span className="px-2 py-0.5 rounded text-[10px] font-mono bg-blue-50 dark:bg-blue-950/50 text-blue-700 dark:text-blue-300">
                        {f.category}
                      </span>
                      <span className="text-xs font-semibold text-slate-900 dark:text-white">{f.title}</span>
                    </div>
                    <p className="text-xs text-slate-600 dark:text-slate-300 leading-relaxed">{f.detail}</p>
                    {f.remediation && (
                      <div className="flex items-center justify-between gap-2 px-3 py-1.5 rounded-lg bg-slate-100/90 dark:bg-slate-950 border border-slate-200/80 dark:border-slate-800 font-mono text-[11px] text-slate-700 dark:text-slate-300">
                        <span className="truncate">{f.remediation}</span>
                        <button
                          onClick={() => copyText(f.id, f.remediation || '')}
                          className="shrink-0 inline-flex items-center gap-1 px-2 py-0.5 rounded bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 text-[10px] hover:text-blue-600"
                        >
                          {copiedKey === f.id ? <Check className="w-3 h-3 text-emerald-500" /> : <Copy className="w-3 h-3" />}
                          <span>{copiedKey === f.id ? '已复制' : '复制'}</span>
                        </button>
                      </div>
                    )}
                  </div>
                ))}
              </div>
            )}
          </SectionCard>

          {/* Per-node Kernel / Conntrack / Sysctl Accordion Cards */}
          <SectionCard
            title="节点 Conntrack / NAT / MTU 与核心 Sysctl 明细"
            subtitle="点击节点展开完整 Linux 内核网络参数逐项审计表"
          >
            <div className="divide-y divide-slate-100 dark:divide-slate-800">
              {kernelReport.nodes.map((node: NodeKernelAudit) => {
                const open = expandedKernelNode === node.nodeName;
                const ct = node.conntrack;
                const ctColor =
                  ct.usagePct >= 80
                    ? 'bg-rose-500'
                    : ct.usagePct >= 60
                      ? 'bg-amber-500'
                      : 'bg-emerald-500';
                return (
                  <div key={node.nodeName} className="p-3.5">
                    <button
                      onClick={() => setExpandedKernelNode(open ? null : node.nodeName)}
                      className="w-full text-left flex flex-wrap items-center justify-between gap-3"
                    >
                      <div className="flex items-center gap-2.5 min-w-0">
                        {open ? (
                          <ChevronDown className="w-4 h-4 text-slate-400 shrink-0" />
                        ) : (
                          <ChevronRight className="w-4 h-4 text-slate-400 shrink-0" />
                        )}
                        <div>
                          <div className="flex items-center gap-2">
                            <span className="text-xs font-bold text-slate-900 dark:text-white">{node.nodeName}</span>
                            <span className="text-[11px] font-mono text-slate-400">{node.internalIP}</span>
                            <StatusPill status={node.status} compact />
                            <span className="text-[11px] font-mono font-semibold text-slate-700 dark:text-slate-300">
                              {node.score} 分
                            </span>
                          </div>
                          <div className="text-[11px] font-mono text-slate-400 mt-0.5">
                            Kernel {node.kernelVersion || '—'} · NIC {node.mtu.primaryNIC || '—'} ({node.mtu.primaryMTU || '—'}B)
                            {node.mtu.tunnelIface ? ` · ${node.mtu.tunnelIface} (${node.mtu.tunnelMTU}B)` : ''}
                            {node.mtu.sampledVethCount > 0
                              ? ` · cali* veth(${node.mtu.sampledVethCount})=${node.mtu.minVethMTU}${
                                  node.mtu.maxVethMTU !== node.mtu.minVethMTU ? `..${node.mtu.maxVethMTU}` : ''
                                }B`
                              : ''}
                          </div>
                        </div>
                      </div>

                      {/* Right quick metrics */}
                      <div className="flex flex-wrap items-center gap-4 text-[11px] font-mono">
                        <div className="w-36">
                          <div className="flex justify-between text-[10px] text-slate-500 mb-0.5">
                            <span>Conntrack</span>
                            <span>{ct.usagePct.toFixed(1)}%</span>
                          </div>
                          <div className="h-1.5 rounded-full bg-slate-100 dark:bg-slate-800 overflow-hidden">
                            <div
                              className={`h-full rounded-full ${ctColor}`}
                              style={{ width: `${Math.min(100, Math.max(2, ct.usagePct))}%` }}
                            />
                          </div>
                          <div className="text-[10px] text-slate-400 mt-0.5">
                            {ct.count.toLocaleString()} / {ct.max.toLocaleString()}
                          </div>
                        </div>

                        <div className="text-right">
                          <div className="text-[10px] text-slate-400">insert_failed / drop / early</div>
                          <div
                            className={`font-semibold ${
                              ct.insertFailed > 0 || ct.drop > 0 || ct.earlyDrop > 0
                                ? 'text-rose-600 dark:text-rose-400'
                                : 'text-emerald-600 dark:text-emerald-400'
                            }`}
                          >
                            {ct.insertFailed} / {ct.drop} / {ct.earlyDrop}
                          </div>
                        </div>

                        <div className="text-right">
                          <div className="text-[10px] text-slate-400">SNAT --random-fully</div>
                          <div>
                            {ct.snatRandomFully ? (
                              <span className="text-emerald-600 dark:text-emerald-400 font-semibold">已开启</span>
                            ) : ct.natOutgoingEnabled ? (
                              <span className="text-amber-600 dark:text-amber-400 font-semibold">未开启</span>
                            ) : (
                              <span className="text-slate-400">无 NAT Outgoing</span>
                            )}
                          </div>
                        </div>
                      </div>
                    </button>

                    {open && (
                      <div className="mt-3 pt-3 border-t border-slate-100 dark:border-slate-800 space-y-3">
                        {/* Detailed Conntrack Error Strip */}
                        <div className="grid grid-cols-2 sm:grid-cols-6 gap-2 text-[11px] font-mono bg-slate-50 dark:bg-slate-950/60 p-2.5 rounded-lg border border-slate-100 dark:border-slate-800">
                          <div>
                            <div className="text-[10px] text-slate-400">Buckets</div>
                            <div className="font-semibold text-slate-800 dark:text-slate-200">{ct.buckets.toLocaleString()}</div>
                          </div>
                          <div>
                            <div className="text-[10px] text-slate-400">invalid</div>
                            <div className="font-semibold text-slate-800 dark:text-slate-200">{ct.invalid.toLocaleString()}</div>
                          </div>
                          <div>
                            <div className="text-[10px] text-slate-400">insert_failed</div>
                            <div className={`font-semibold ${ct.insertFailed > 0 ? 'text-rose-600' : 'text-slate-800 dark:text-slate-200'}`}>
                              {ct.insertFailed.toLocaleString()}
                            </div>
                          </div>
                          <div>
                            <div className="text-[10px] text-slate-400">drop / early_drop</div>
                            <div className={`font-semibold ${ct.drop + ct.earlyDrop > 0 ? 'text-rose-600' : 'text-slate-800 dark:text-slate-200'}`}>
                              {ct.drop.toLocaleString()} / {ct.earlyDrop.toLocaleString()}
                            </div>
                          </div>
                          <div>
                            <div className="text-[10px] text-slate-400">search_restart</div>
                            <div className="font-semibold text-slate-800 dark:text-slate-200">{ct.searchRestart.toLocaleString()}</div>
                          </div>
                          <div>
                            <div className="text-[10px] text-slate-400">nf_conntrack_acct</div>
                            <div className="font-semibold text-slate-800 dark:text-slate-200">{ct.acctEnabled ? '1 (已开)' : '0'}</div>
                          </div>
                        </div>

                        {/* Sysctl Table */}
                        <div className="overflow-x-auto">
                          <table className="w-full text-xs">
                            <thead>
                              <tr className="text-left text-[10px] uppercase text-slate-400 border-b border-slate-100 dark:border-slate-800">
                                <th className="py-1.5 pr-3">内核参数 (Sysctl)</th>
                                <th className="py-1.5 px-2">当前值</th>
                                <th className="py-1.5 px-2">推荐值</th>
                                <th className="py-1.5 px-2">状态</th>
                                <th className="py-1.5 pl-2">网络影响说明</th>
                              </tr>
                            </thead>
                            <tbody className="divide-y divide-slate-100 dark:divide-slate-800/70">
                              {node.sysctls.map((sc) => (
                                <tr key={sc.key} className="hover:bg-slate-50/70 dark:hover:bg-slate-800/30">
                                  <td className="py-1.5 pr-3 font-mono text-[11px] text-slate-800 dark:text-slate-200">{sc.key}</td>
                                  <td className="py-1.5 px-2 font-mono text-[11px] font-semibold text-slate-900 dark:text-white">{sc.value}</td>
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
          </SectionCard>

          {/* Interactive Path MTU / DF Blackhole Probe Card */}
          <SectionCard
            title="Path MTU / DF 大包黑洞探测（专治小包通、大包卡死）"
            subtitle="真实进入源 Pod Netns 构造带 DF（Don't Fragment）标志的 IPv4 ICMP 报文，二分探测端到端最大不分片 MTU，精准揪出 IPIP/VXLAN/物理交换机 MTU 黑洞"
            action={<Ruler className="w-4 h-4 text-blue-500" />}
          >
            <div className="p-4 space-y-3.5">
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
                  <label className="block text-[10px] font-semibold uppercase text-slate-400 mb-1">目标 IP (Pod / Node / 外部)</label>
                  <input
                    value={pmtudTargetIP}
                    onChange={(e) => setPmtudTargetIP(e.target.value)}
                    placeholder="如 172.31.226.27"
                    className="w-full px-2.5 py-1.5 rounded-lg text-xs font-mono bg-slate-50 dark:bg-slate-950 border border-slate-200 dark:border-slate-700"
                  />
                </div>

                <div className="md:col-span-1">
                  <label className="block text-[10px] font-semibold uppercase text-slate-400 mb-1">上限 MTU</label>
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

              {/* Quick target suggestions */}
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
                <div className="rounded-xl border border-slate-200 dark:border-slate-800 bg-slate-50/70 dark:bg-slate-950/50 p-3.5 space-y-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <StatusPill status={pmtudResult.status} />
                      <span className="text-xs font-mono font-bold text-slate-900 dark:text-white">
                        实测 Path MTU = {pmtudResult.discoveredMTU} Bytes
                      </span>
                      <span className="text-[11px] font-mono text-slate-500">
                        (源 Pod eth0 MTU = {pmtudResult.podMTU}B · {pmtudResult.srcIP} → {pmtudResult.targetIP})
                      </span>
                    </div>
                    {pmtudResult.blackhole && (
                      <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-semibold bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300">
                        <ShieldAlert className="w-3.5 h-3.5" /> 检测到 MTU 黑洞
                      </span>
                    )}
                  </div>
                  <p className="text-xs text-slate-700 dark:text-slate-300">{pmtudResult.diagnosis}</p>

                  {/* Attempt Ladder */}
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
        </div>
      )}

      {/* ==================== TAB 2: ROUTE & BGP CONSISTENCY AUDIT ==================== */}
      {subTab === 'routes' && routeReport && (
        <div className="space-y-4">
          {/* KPI Summary Row */}
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
            <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl p-3.5">
              <div className="text-[11px] font-semibold text-slate-400 uppercase">路由一致性总览</div>
              <div className="mt-1.5 flex items-baseline justify-between">
                <span className="text-2xl font-bold font-mono text-slate-900 dark:text-white">
                  {routeReport.totalDrifts}
                  <span className="text-xs font-sans font-normal text-slate-400 ml-1">项偏差</span>
                </span>
                <StatusPill status={routeReport.overallStatus} compact />
              </div>
              <div className="text-[11px] text-slate-500 mt-1">
                已审计 {routeReport.nodes.length} 节点主路由表 vs Calico IPAM
              </div>
            </div>

            <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl p-3.5">
              <div className="text-[11px] font-semibold text-slate-400 uppercase">缺失路由</div>
              <div className="mt-1.5 text-2xl font-bold font-mono text-slate-900 dark:text-white">
                <span className={routeReport.missingCount > 0 ? 'text-rose-600 dark:text-rose-400' : 'text-emerald-600 dark:text-emerald-400'}>
                  {routeReport.missingCount}
                </span>
              </div>
              <div className="text-[11px] text-slate-500 mt-1">
                跨节点 Block / 本地 Pod /32 / Blackhole
              </div>
            </div>

            <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl p-3.5">
              <div className="text-[11px] font-semibold text-slate-400 uppercase">残留幽灵 Pod 路由</div>
              <div className="mt-1.5 text-2xl font-bold font-mono">
                <span className={routeReport.ghostPodCount > 0 ? 'text-amber-600 dark:text-amber-400' : 'text-emerald-600 dark:text-emerald-400'}>
                  {routeReport.ghostPodCount}
                </span>
              </div>
              <div className="text-[11px] text-slate-500 mt-1">
                无对应活跃 Pod 的失效 cali* /32 路由
              </div>
            </div>

            <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl p-3.5">
              <div className="text-[11px] font-semibold text-slate-400 uppercase">非对称路径 / BGP 抖动</div>
              <div className="mt-1.5 flex items-center gap-3 font-mono">
                <span className={`text-2xl font-bold ${routeReport.asymmetricCount > 0 ? 'text-rose-600' : 'text-emerald-600 dark:text-emerald-400'}`}>
                  {routeReport.asymmetricCount}
                  <span className="text-[11px] font-sans font-normal text-slate-500 ml-1">非对称</span>
                </span>
                <span className="text-2xl font-bold text-slate-700 dark:text-slate-200">
                  {routeReport.bgpEvents?.length || 0}
                  <span className="text-[11px] font-sans font-normal text-slate-500 ml-1">BGP事件</span>
                </span>
              </div>
              <div className="text-[11px] text-slate-500 mt-1">
                双节点去回程封装比对与 Peer 状态变更记录
              </div>
            </div>
          </div>

          {/* Expected vs Actual Route Diff List */}
          <SectionCard
            title="Expected vs Actual 路由偏差清单"
            subtitle="对比 Calico IPAMBlock 归属、运行中 Pod 与各节点 Linux 内核主路由表，精准识别路由缺失、残留幽灵路由及去回程非对称"
            action={
              routeReport.drifts.length === 0 ? (
                <span className="inline-flex items-center gap-1 text-xs font-semibold text-emerald-600 dark:text-emerald-400">
                  <CheckCircle2 className="w-4 h-4" /> 零偏差 · 全集群一致
                </span>
              ) : (
                <span className="text-xs font-mono text-rose-600 dark:text-rose-400">
                  {routeReport.drifts.length} 项异常
                </span>
              )
            }
          >
            {routeReport.drifts.length === 0 ? (
              <div className="px-4 py-5 flex items-center gap-3 text-emerald-700 dark:text-emerald-300 bg-emerald-50/60 dark:bg-emerald-950/20 rounded-lg text-xs">
                <CheckCircle2 className="w-5 h-5 shrink-0" />
                <span>
                  所有节点的跨节点 IPAMBlock 路由、防环路 Blackhole 路由、本地 Pod /32 主机路由及双节点往返封装路径均完全一致！
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

          {/* BGP Flapping / State Transition Timeline */}
          <SectionCard
            title="BGP Peer 状态变更 / 抖动时间线"
            subtitle="实时监听各节点 BIRD BGP 会话状态翻转（Established ↔ Active/Idle/Connect），辅助定位间歇性路由震荡"
          >
            {!routeReport.bgpEvents || routeReport.bgpEvents.length === 0 ? (
              <div className="px-4 py-4 text-xs text-slate-500 flex items-center gap-2">
                <CheckCircle2 className="w-4 h-4 text-emerald-500 shrink-0" />
                <span>当前观测周期内未检测到任何 BGP Peer 会话断连或状态抖动事件，所有 BGP 会话保持平稳。</span>
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
                      <th className="py-2 px-3">详情</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                    {routeReport.bgpEvents.map((ev, idx) => (
                      <tr key={`${ev.nodeName}-${ev.peerName}-${idx}`}>
                        <td className="py-2 px-3 font-mono text-[11px] text-slate-500">
                          {new Date(ev.timestamp).toLocaleTimeString()}
                        </td>
                        <td className="py-2 px-3 font-mono text-[11px] font-semibold">{ev.nodeName}</td>
                        <td className="py-2 px-3 font-mono text-[11px]">
                          {ev.peerName} ({ev.peerIP})
                        </td>
                        <td className="py-2 px-3 font-mono text-[11px]">
                          {ev.prevState} → {ev.newState}
                        </td>
                        <td className="py-2 px-3"><StatusPill status={ev.severity} compact /></td>
                        <td className="py-2 px-3 text-[11px] text-slate-600 dark:text-slate-300">{ev.message}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </SectionCard>

          {/* Per-node Kernel Routing Table Inspector */}
          <SectionCard
            title="节点 Linux 内核主路由表实时透视"
            subtitle="点击节点展开该节点真实 netlink IPv4 路由表，支持按协议/类型与 CIDR 关键词即时过滤"
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
                        {open ? (
                          <ChevronDown className="w-4 h-4 text-slate-400" />
                        ) : (
                          <ChevronRight className="w-4 h-4 text-slate-400" />
                        )}
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
                        {/* Filter controls */}
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

                        {/* Route Table */}
                        <div className="max-h-[340px] overflow-y-auto border border-slate-100 dark:border-slate-800 rounded-lg">
                          <table className="w-full text-[11px] font-mono">
                            <thead className="sticky top-0 bg-slate-50 dark:bg-slate-950 text-left text-[10px] uppercase text-slate-400 border-b border-slate-100 dark:border-slate-800">
                              <tr>
                                <th className="py-1.5 px-3">目标网段 (Dst)</th>
                                <th className="py-1.5 px-3">下一跳 (Gateway)</th>
                                <th className="py-1.5 px-3">出接口 (Dev)</th>
                                <th className="py-1.5 px-3">协议</th>
                                <th className="py-1.5 px-3">类型</th>
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
    </div>
  );
}
