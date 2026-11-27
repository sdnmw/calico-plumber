import React, { useEffect, useRef, useState } from 'react';
import {
  RefreshCw,
  ChevronDown,
  ChevronRight,
  Route,
  CheckCircle2,
  AlertTriangle,
  XCircle,
  ArrowDownRight,
  BookmarkPlus,
  History,
  Play,
  Radio,
  Layers,
  GitCompare,
  Clock,
} from 'lucide-react';
import { api } from '../api/client';
import type {
  GlobalHealthReport,
  IncidentSnapshot,
  ServiceHealthItem,
  ServicePortHealth,
  TraceFlowRequest,
} from '../api/types';
import { SectionCard, StatusPill } from '../components/common/Badges';
import { ServiceDiagDrawer, type ServiceDiagMode } from '../components/health/ServiceDiagDrawer';

interface HealthPageProps {
  onLaunchTrace: (req: Partial<TraceFlowRequest>) => void;
}

type SvcFilterKey = 'all' | 'abnormal' | 'broken' | 'degraded' | 'no-endpoints' | 'healthy';

export function HealthPage({ onLaunchTrace }: HealthPageProps) {
  const [report, setReport] = useState<GlobalHealthReport | null>(null);
  const [incidents, setIncidents] = useState<IncidentSnapshot[]>([]);
  const [loading, setLoading] = useState(true);
  const [running, setRunning] = useState(false);
  const [svcFilter, setSvcFilter] = useState<SvcFilterKey>('all');
  const [svcSearch, setSvcSearch] = useState('');
  const [expandedSvc, setExpandedSvc] = useState<string | null>(null);
  const [highlightSection, setHighlightSection] = useState<'matrix' | 'infra' | 'services' | 'incidents' | null>(null);

  // Evidence-driven Service Diagnostic Drawer
  const [diagDrawer, setDiagDrawer] = useState<{
    namespace: string;
    serviceName: string;
    focusPort?: number;
    mode: ServiceDiagMode;
  } | null>(null);

  // Incident retest & save states
  const [retestingId, setRetestingId] = useState<string | null>(null);
  const [expandedIncidentId, setExpandedIncidentId] = useState<string | null>(null);
  const [savedNotice, setSavedNotice] = useState<string | null>(null);

  const matrixSectionRef = useRef<HTMLDivElement>(null);
  const infraSectionRef = useRef<HTMLDivElement>(null);
  const svcSectionRef = useRef<HTMLDivElement>(null);
  const incSectionRef = useRef<HTMLDivElement>(null);

  const scrollToSection = (section: 'matrix' | 'infra' | 'services' | 'incidents') => {
    const ref =
      section === 'matrix'
        ? matrixSectionRef
        : section === 'infra'
          ? infraSectionRef
          : section === 'incidents'
            ? incSectionRef
            : svcSectionRef;
    ref.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
    setHighlightSection(section);
    window.setTimeout(() => {
      setHighlightSection((cur) => (cur === section ? null : cur));
    }, 1800);
  };

  const jumpToServiceIssues = (filter: SvcFilterKey = 'abnormal') => {
    setSvcFilter(filter);
    setSvcSearch('');
    const list = report?.serviceBackends.services || [];
    const target = list.find((s) => {
      if (filter === 'abnormal') return s.status === 'broken' || s.status === 'degraded' || s.status === 'no-endpoints';
      return s.status === filter;
    });
    if (target) {
      setExpandedSvc(`${target.namespace}/${target.name}`);
    }
    scrollToSection('services');
  };

  const load = () => {
    setLoading(true);
    Promise.all([api.getHealth(), api.listIncidents().catch(() => [])])
      .then(([rep, incs]) => {
        setReport(rep);
        setIncidents(incs);
      })
      .finally(() => setLoading(false));
  };

  const runNow = () => {
    setRunning(true);
    api
      .runHealthNow()
      .then(async (rep) => {
        setReport(rep);
        const incs = await api.listIncidents().catch(() => []);
        setIncidents(incs);
      })
      .finally(() => setRunning(false));
  };

  useEffect(() => {
    load();
  }, []);

  const handleSavePortIncident = async (svc: ServiceHealthItem, pc: ServicePortHealth) => {
    const failedBackend = pc.backends.find((b) => !b.probeOk) || pc.backends[0];
    const [srcNs, srcPod] = (pc.probeSourcePod || '').includes('/')
      ? (pc.probeSourcePod || '').split('/', 2)
      : ['', pc.probeSourcePod || ''];
    try {
      const created = await api.createIncident({
        title: `${svc.namespace}/${svc.name}:${pc.servicePort}/${pc.protocol} ${pc.failureSummary}`,
        kind: 'service_port',
        srcNamespace: srcNs,
        srcPod,
        targetNamespace: svc.namespace,
        targetService: svc.name,
        targetClusterIP: svc.clusterIP,
        targetBackendPod: failedBackend?.podName,
        targetBackendIP: failedBackend?.podIP,
        servicePort: pc.servicePort,
        backendPort: pc.resolvedTargetPort,
        protocol: pc.protocol,
        failureKind: pc.failureKind,
        failureSummary: pc.failureSummary,
        initialLatencyMs: pc.clusterIPLatencyMs || failedBackend?.latencyMs || 0,
      });
      setIncidents((prev) => [created, ...prev.filter((i) => i.id !== created.id)]);
      setExpandedIncidentId(created.id);
      setSavedNotice(`已保存现场 ${created.id}，可在下方点击「用相同条件复测」`);
      setTimeout(() => setSavedNotice(null), 4000);
      scrollToSection('incidents');
    } catch {
      // ignore
    }
  };

  const handleRetestIncident = async (id: string) => {
    setRetestingId(id);
    try {
      const updated = await api.retestIncident(id);
      setIncidents((prev) => prev.map((it) => (it.id === id ? updated : it)));
      setExpandedIncidentId(id);
    } finally {
      setRetestingId(null);
    }
  };

  const triggerEvidenceAction = (
    action: ServicePortHealth['recommendedAction'] | undefined,
    svc: ServiceHealthItem,
    focusPort?: number,
    probeSourcePod?: string,
  ) => {
    const port = focusPort || svc.portChecks?.[0]?.servicePort || 0;
    switch (action) {
      case 'inspect_empty_backends':
        setDiagDrawer({ namespace: svc.namespace, serviceName: svc.name, focusPort: port, mode: 'empty' });
        return;
      case 'check_port_listening':
        setDiagDrawer({ namespace: svc.namespace, serviceName: svc.name, focusPort: port, mode: 'listening' });
        return;
      case 'compare_policy_sources':
        setDiagDrawer({ namespace: svc.namespace, serviceName: svc.name, focusPort: port, mode: 'compare' });
        return;
      case 'trace_service_dnat':
      default: {
        const [srcNs, srcPod] = (probeSourcePod || svc.probeSourcePod || '').includes('/')
          ? (probeSourcePod || svc.probeSourcePod || '').split('/', 2)
          : ['', ''];
        onLaunchTrace({
          ...(srcNs && srcPod ? { srcNamespace: srcNs, srcPod } : {}),
          targetType: 'service',
          dstNamespace: svc.namespace,
          dstName: svc.name,
          protocol: 'TCP',
          dstPort: port || undefined,
        });
      }
    }
  };

  if (loading && !report) {
    return (
      <div className="p-8 text-sm text-[#8C7F73]">
        正在执行集群全维度网络健康检查（跨节点探针 / Pod-Node / CoreDNS / 全量 Service 逐端口探测）...
      </div>
    );
  }
  if (!report) return null;

  const scoreColor =
    report.overallStatus === 'healthy'
      ? 'text-emerald-600 dark:text-emerald-400 border-emerald-500'
      : report.overallStatus === 'degraded'
        ? 'text-[#D9720B] dark:text-[#FBB040] border-[#F08C18]'
        : 'text-rose-600 dark:text-rose-400 border-rose-500';

  const filteredSvcs = (report.serviceBackends.services || []).filter((s) => {
    if (svcFilter === 'abnormal') {
      if (s.status !== 'broken' && s.status !== 'degraded' && s.status !== 'no-endpoints') return false;
    } else if (svcFilter !== 'all' && s.status !== svcFilter) {
      return false;
    }
    if (svcSearch) {
      const q = svcSearch.toLowerCase();
      return (
        s.name.toLowerCase().includes(q) ||
        s.namespace.toLowerCase().includes(q) ||
        (s.clusterIP || '').includes(q)
      );
    }
    return true;
  });

  const abnormalSvcCount =
    report.serviceBackends.brokenServices +
    report.serviceBackends.degradedServices +
    report.serviceBackends.noEndpointServices;

  const cellMap = new Map((report.podToPod.cells || []).map((c) => [`${c.srcNode}|${c.dstNode}`, c]));
  const matrixNodes = report.podToPod.nodes || [];
  const infraChecks = report.infrastructure.checks || [];
  const svcChecks = report.podToService.checks || [];

  const crossTotal = report.podToPod.crossNodeTotal ?? Math.max(0, report.podToPod.totalPairs - matrixNodes.length);
  const crossHealthy =
    report.podToPod.crossNodeHealthy ??
    (report.podToPod.cells || []).filter((c) => !c.selfCheck && c.srcIP !== c.dstIP && c.reachable).length;
  const crossFailed = report.podToPod.crossNodeFailed ?? Math.max(0, crossTotal - crossHealthy);
  const needsAttention =
    crossFailed > 0 || report.serviceBackends.brokenServices > 0 || report.serviceBackends.degradedServices > 0;

  return (
    <div className="p-6 space-y-5 max-w-[1600px] mx-auto">
      {/* Page Title + Actions */}
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-[#201B18] dark:text-[#F5F0EA]">
            全局网络健康体检中心
          </h1>
          <p className="text-xs text-[#8C7F73] dark:text-[#A39587] mt-0.5">
            最近巡检：{new Date(report.checkedAt).toLocaleString()}（耗时 {report.durationMs} ms）
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => scrollToSection('incidents')}
            className="inline-flex items-center gap-1.5 px-3 py-2 rounded-lg bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721] hover:border-[#F08C18] text-xs font-semibold text-[#574D44] dark:text-[#D5C9BC] transition"
          >
            <History className="w-3.5 h-3.5 text-[#F08C18]" />
            <span>故障现场与复测</span>
            <span className="px-1.5 py-0.2 rounded-full text-[10px] font-mono bg-[#FFF4E4] text-[#9E4A05] dark:bg-[#F08C18]/20 dark:text-[#FBB040]">
              {incidents.length}
            </span>
          </button>
          <button
            onClick={runNow}
            disabled={running}
            className="inline-flex items-center gap-1.5 px-4 py-2 rounded-lg bg-[#F08C18] hover:bg-[#D9720B] disabled:opacity-50 text-white text-xs font-semibold shadow-sm transition"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${running ? 'animate-spin' : ''}`} />
            {running ? '全量体检中...' : '立即全量巡检'}
          </button>
        </div>
      </div>

      {/* 1. Plain-Language Clickable Executive Verdict Banner (Instant On-Call Decision) */}
      <div
        className={`p-4 rounded-2xl border flex flex-wrap items-center justify-between gap-4 shadow-xs ${
          needsAttention
            ? 'bg-gradient-to-r from-[#FFF7EC] via-white to-[#FFF3F3] dark:from-[#2A1D12] dark:via-[#1C1815] dark:to-[#291516] border-[#FAD4A1] dark:border-[#F08C18]/50'
            : 'bg-gradient-to-r from-emerald-50/80 to-white dark:from-emerald-950/30 dark:to-[#1C1815] border-emerald-200 dark:border-emerald-900/60'
        }`}
      >
        <div className="flex items-start gap-3 min-w-0 flex-1">
          {needsAttention ? (
            <AlertTriangle className="w-5 h-5 text-[#D9720B] dark:text-[#FBB040] mt-0.5 shrink-0" />
          ) : (
            <CheckCircle2 className="w-5 h-5 text-emerald-600 dark:text-emerald-400 mt-0.5 shrink-0" />
          )}
          <div className="space-y-1.5 min-w-0">
            <div className="flex flex-wrap items-center gap-1.5 text-sm font-semibold text-[#201B18] dark:text-[#F5F0EA] leading-relaxed">
              <button
                type="button"
                onClick={() => scrollToSection('matrix')}
                className={`underline decoration-dotted underline-offset-4 hover:text-[#D9720B] ${
                  crossFailed > 0 ? 'text-rose-600 dark:text-rose-400' : ''
                }`}
              >
                {matrixNodes.length} 个节点的跨节点探针
                {crossTotal > 0
                  ? crossFailed === 0
                    ? `双向连通（${crossHealthy}/${crossTotal}）`
                    : `存在异常（${crossHealthy}/${crossTotal} 可达）`
                  : '在线'}
              </button>
              <span>。</span>
              <button
                type="button"
                onClick={() => jumpToServiceIssues('all')}
                className="underline decoration-dotted underline-offset-4 hover:text-[#D9720B]"
              >
                {report.serviceBackends.totalServices} 个 Service
              </button>
              <span>中，</span>
              <button
                type="button"
                onClick={() => jumpToServiceIssues('broken')}
                className={`px-1.5 py-0.2 rounded font-bold transition ${
                  report.serviceBackends.brokenServices > 0
                    ? 'bg-rose-100 text-rose-700 hover:bg-rose-200 dark:bg-rose-950/70 dark:text-rose-300'
                    : 'text-emerald-700 dark:text-emerald-400 hover:underline'
                }`}
              >
                {report.serviceBackends.brokenServices} 个探测失败
              </button>
              <span>、</span>
              <button
                type="button"
                onClick={() => jumpToServiceIssues('degraded')}
                className={`px-1.5 py-0.2 rounded font-bold transition ${
                  report.serviceBackends.degradedServices > 0
                    ? 'bg-[#FFF4E4] text-[#9E4A05] hover:bg-[#FDE6C4] dark:bg-[#F08C18]/20 dark:text-[#FBB040]'
                    : 'text-[#574D44] dark:text-[#A39587] hover:underline'
                }`}
              >
                {report.serviceBackends.degradedServices} 个降级
              </button>
              <span>、</span>
              <button
                type="button"
                onClick={() => jumpToServiceIssues('no-endpoints')}
                className="px-1.5 py-0.2 rounded font-semibold bg-[#F7F3EE] dark:bg-[#25201C] text-[#574D44] dark:text-[#D5C9BC] hover:bg-[#EFE7DD]"
              >
                {report.serviceBackends.noEndpointServices} 个无后端
              </button>
              <span>。</span>
            </div>
            <div className="text-[11px] text-[#8C7F73] dark:text-[#A39587]">
              {needsAttention
                ? '存在需要确认的 Service 端口探测异常：点击右侧按钮直接定位到具体端口、探测来源与失败表现（连接被拒绝 vs 等待超时）。'
                : '跨节点连通性与已探测的 Service 端口均正常，当前无需立即介入处理。'}
            </div>
          </div>
        </div>

        <div className="flex items-center gap-3 shrink-0">
          {(report.serviceBackends.brokenServices > 0 || report.serviceBackends.degradedServices > 0) && (
            <button
              onClick={() => jumpToServiceIssues(report.serviceBackends.brokenServices > 0 ? 'broken' : 'degraded')}
              className="inline-flex items-center gap-1.5 px-3.5 py-2 rounded-xl bg-rose-600 hover:bg-rose-700 text-white text-xs font-bold shadow-sm transition"
            >
              <span>查看失败服务</span>
              <ArrowDownRight className="w-3.5 h-3.5" />
            </button>
          )}
          <div className="flex items-center gap-2.5 pl-3 border-l border-[#E9E1D6] dark:border-[#2E2721]">
            <div className={`w-12 h-12 rounded-full border-[3px] flex flex-col items-center justify-center shrink-0 ${scoreColor}`}>
              <span className="text-base font-bold leading-none">{report.overallScore}</span>
              <span className="text-[8px] uppercase font-semibold mt-0.5">Score</span>
            </div>
            <StatusPill status={report.overallStatus} />
          </div>
        </div>
      </div>

      {/* 5 Focused Dimension KPI Cards */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-3.5">
        <HealthMiniCard
          title="跨节点探针双向连通"
          value={crossTotal > 0 ? `${crossHealthy}/${crossTotal}` : `${report.podToPod.healthyPairs}/${report.podToPod.totalPairs}`}
          sub={`跨节点均值 ${report.podToPod.avgLatencyMs}ms · 对角格为探针自身检查`}
          ok={crossFailed === 0}
          onClick={() => scrollToSection('matrix')}
          hint={crossFailed > 0 ? '查看跨节点失败明细' : undefined}
        />
        <HealthMiniCard
          title="Pod → Node 连通"
          value={`${report.podToNode.healthyChecks}/${report.podToNode.totalChecks}`}
          sub={`均值延迟 ${report.podToNode.avgLatencyMs} ms`}
          ok={report.podToNode.healthyChecks === report.podToNode.totalChecks}
          onClick={() => scrollToSection('infra')}
        />
        <HealthMiniCard
          title="Pod → 核心 SVC & DNS"
          value={`${report.podToService.healthyChecks}/${report.podToService.totalChecks}`}
          sub={`CoreDNS 解析均值 ${report.podToService.dnsAvgMs} ms`}
          ok={report.podToService.healthyChecks === report.podToService.totalChecks}
          onClick={() => scrollToSection('infra')}
        />
        <HealthMiniCard
          title="Service 逐端口探测"
          value={`${report.serviceBackends.healthyServices}/${report.serviceBackends.totalServices}`}
          sub={`失败 ${report.serviceBackends.brokenServices} · 降级 ${report.serviceBackends.degradedServices} · 无后端 ${report.serviceBackends.noEndpointServices}`}
          ok={report.serviceBackends.brokenServices === 0 && report.serviceBackends.degradedServices === 0}
          onClick={() => jumpToServiceIssues(abnormalSvcCount > 0 ? 'abnormal' : 'all')}
          hint={report.serviceBackends.brokenServices > 0 ? `点击展开 ${report.serviceBackends.brokenServices} 个失败服务` : undefined}
          subNode={
            <div className="mt-1 flex flex-wrap items-center gap-1.5 text-[11px]">
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  jumpToServiceIssues('broken');
                }}
                className={`hover:underline ${report.serviceBackends.brokenServices > 0 ? 'text-rose-600 dark:text-rose-400 font-semibold' : 'text-[#8C7F73]'}`}
              >
                失败 {report.serviceBackends.brokenServices}
              </button>
              <span className="text-[#D5C9BC] dark:text-[#3E352D]">·</span>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  jumpToServiceIssues('degraded');
                }}
                className={`hover:underline ${report.serviceBackends.degradedServices > 0 ? 'text-[#D9720B] dark:text-[#FBB040] font-semibold' : 'text-[#8C7F73]'}`}
              >
                降级 {report.serviceBackends.degradedServices}
              </button>
              <span className="text-[#D5C9BC] dark:text-[#3E352D]">·</span>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  jumpToServiceIssues('no-endpoints');
                }}
                className="text-[#8C7F73] hover:underline"
              >
                无后端 {report.serviceBackends.noEndpointServices}
              </button>
            </div>
          }
        />
        <HealthMiniCard
          title="Calico / BGP 底座"
          value={`${report.infrastructure.calicoNodesReady}/${report.infrastructure.calicoNodesTotal} Node`}
          sub={`BGP ${report.infrastructure.bgpEstablished}/${report.infrastructure.bgpTotal} · 孤儿 IP ${report.infrastructure.orphanIPCount}`}
          ok={
            report.infrastructure.calicoNodesReady === report.infrastructure.calicoNodesTotal &&
            report.infrastructure.bgpEstablished === report.infrastructure.bgpTotal
          }
          onClick={() => scrollToSection('infra')}
        />
      </div>

      {/* Middle Row: Pod Probe Matrix (Self-Check clearly separated from Cross-Node) + Infra Checklist */}
      <div className="grid grid-cols-1 xl:grid-cols-2 gap-6">
        <div
          ref={matrixSectionRef}
          className={`scroll-mt-20 rounded-xl transition ${highlightSection === 'matrix' ? 'ring-2 ring-[#F08C18]' : ''}`}
        >
          <SectionCard
            title={`跨节点探针双向连通矩阵（跨节点 ${crossHealthy}/${crossTotal}）`}
            subtitle="非对角格为跨节点两探针实测；对角格源/目标 IP 相同，仅代表「探针自身检查」（若需证明同节点两不同 Pod 互通，请在 TraceFlow 中选择该节点两个不同 Pod）"
          >
            <div className="p-4 overflow-x-auto">
              {matrixNodes.length > 0 ? (
                <table className="w-full text-xs font-mono border-collapse">
                  <thead>
                    <tr>
                      <th className="p-2 text-left text-[11px] text-[#8C7F73]">Src \ Dst</th>
                      {matrixNodes.map((dn) => (
                        <th key={dn} className="p-2 text-center text-[11px] text-[#574D44] dark:text-[#D5C9BC] truncate max-w-[160px]">
                          {dn}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {matrixNodes.map((sn) => (
                      <tr key={sn}>
                        <td className="p-2 font-semibold text-[#201B18] dark:text-[#F5F0EA] truncate max-w-[160px]">{sn}</td>
                        {matrixNodes.map((dn) => {
                          const cell = cellMap.get(`${sn}|${dn}`);
                          if (!cell) {
                            return <td key={dn} className="p-1.5 text-center text-[#8C7F73]">—</td>;
                          }
                          const isSelf = Boolean(cell.selfCheck || (cell.sameNode && cell.srcIP === cell.dstIP));
                          if (isSelf) {
                            return (
                              <td key={dn} className="p-1.5">
                                <div
                                  title={`源与目标为同一探针 Pod (${cell.srcIP})，仅代表探针进程自身检查`}
                                  className="w-full py-2.5 px-2 rounded-lg border border-dashed border-[#DFCFC0] dark:border-[#3A322A] bg-[#FAF6F1] dark:bg-[#25201C]/70 text-center text-[#574D44] dark:text-[#A39587]"
                                >
                                  <div className="font-sans font-semibold text-[11px]">探针自身检查</div>
                                  <div className="text-[10px] opacity-80 mt-0.5">
                                    {cell.reachable ? `在线 (${cell.latencyMs} ms)` : '异常'}
                                  </div>
                                </div>
                              </td>
                            );
                          }
                          const bg = !cell.reachable
                            ? 'bg-rose-100 text-rose-800 border-rose-300 dark:bg-rose-950/70 dark:text-rose-200 dark:border-rose-800'
                            : cell.latencyMs > 15
                              ? 'bg-[#FFF4E4] text-[#9E4A05] border-[#FAD4A1] dark:bg-[#F08C18]/20 dark:text-[#FBB040] dark:border-[#F08C18]/40'
                              : 'bg-emerald-100/90 text-emerald-800 border-emerald-300 dark:bg-emerald-950/60 dark:text-emerald-200 dark:border-emerald-800';
                          return (
                            <td key={dn} className="p-1.5">
                              <button
                                onClick={() =>
                                  onLaunchTrace({
                                    srcNamespace: 'calico-netdiag',
                                    srcPod: cell.srcPod,
                                    targetType: 'pod',
                                    dstNamespace: 'calico-netdiag',
                                    dstName: cell.dstPod,
                                    protocol: 'TCP',
                                    dstPort: 8099,
                                  })
                                }
                                title={`跨节点 ${cell.srcIP} → ${cell.dstIP}，点击发起 TraceFlow`}
                                className={`w-full py-2.5 px-2 rounded-lg border text-center transition hover:scale-[1.02] ${bg}`}
                              >
                                <div className="font-bold text-xs">{cell.reachable ? `${cell.latencyMs} ms` : 'FAIL'}</div>
                                <div className="text-[9px] font-sans opacity-80 mt-0.5">跨节点双向</div>
                              </button>
                            </td>
                          );
                        })}
                      </tr>
                    ))}
                  </tbody>
                </table>
              ) : (
                <div className="p-6 text-center text-xs text-[#8C7F73]">暂无 Probe Pod 数据</div>
              )}
            </div>
          </SectionCard>
        </div>

        <div
          ref={infraSectionRef}
          className={`scroll-mt-20 rounded-xl transition ${highlightSection === 'infra' ? 'ring-2 ring-[#F08C18]' : ''}`}
        >
          <SectionCard title="Calico 底座、Pod-Node 与 CoreDNS 巡检明细">
            <div className="p-4 space-y-4">
              <div className="space-y-2">
                {infraChecks.map((c, idx) => (
                  <div
                    key={idx}
                    className="flex items-start justify-between gap-3 p-2.5 rounded-lg bg-[#FAF6F1] dark:bg-[#14110F] border border-[#E9E1D6] dark:border-[#2E2721] text-xs"
                  >
                    <div className="flex items-start gap-2">
                      {c.status === 'pass' ? (
                        <CheckCircle2 className="w-4 h-4 text-emerald-500 mt-0.5 shrink-0" />
                      ) : c.status === 'warn' ? (
                        <AlertTriangle className="w-4 h-4 text-[#F08C18] mt-0.5 shrink-0" />
                      ) : (
                        <XCircle className="w-4 h-4 text-rose-500 mt-0.5 shrink-0" />
                      )}
                      <div>
                        <span className="font-semibold text-[#201B18] dark:text-[#F5F0EA]">{c.name}</span>
                        <p className="text-[11px] text-[#8C7F73] mt-0.5">{c.detail}</p>
                      </div>
                    </div>
                    <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-white dark:bg-[#25201C] border border-[#E9E1D6] dark:border-[#2E2721] text-[#574D44] dark:text-[#D5C9BC]">
                      {c.category}
                    </span>
                  </div>
                ))}
              </div>

              <div className="rounded-lg border border-[#E9E1D6] dark:border-[#2E2721] overflow-hidden">
                <table className="w-full text-[11px] font-mono">
                  <thead className="bg-[#FAF6F1] dark:bg-[#25201C] text-[#8C7F73]">
                    <tr>
                      <th className="text-left px-3 py-1.5">源节点</th>
                      <th className="text-left px-3 py-1.5">检测项</th>
                      <th className="text-left px-3 py-1.5">结果</th>
                      <th className="text-right px-3 py-1.5">延迟</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[#F2ECE4] dark:divide-[#2E2721]">
                    {svcChecks.map((ch, idx) => (
                      <tr key={idx}>
                        <td className="px-3 py-1.5 text-[#574D44] dark:text-[#D5C9BC]">{ch.srcNode}</td>
                        <td className="px-3 py-1.5">
                          {ch.targetName} <span className="text-[#8C7F73]">({ch.detail || ch.targetAddr})</span>
                        </td>
                        <td className="px-3 py-1.5">
                          <StatusPill status={ch.reachable ? 'pass' : 'fail'} label={ch.reachable ? 'OK' : 'FAIL'} />
                        </td>
                        <td className="px-3 py-1.5 text-right">{ch.latencyMs} ms</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </SectionCard>
        </div>
      </div>

      {/* 2. Per-Port Service Health Audit Table with Evidence-Driven Next-Step Action */}
      <div
        ref={svcSectionRef}
        className={`scroll-mt-20 rounded-xl transition ${highlightSection === 'services' ? 'ring-2 ring-rose-500/70' : ''}`}
      >
        <SectionCard
          title={`全集群 Service 逐端口健康审计 (${report.serviceBackends.totalServices})`}
          subtitle="点击任意 Service 展开逐端口探测结果，右侧按异常原因提供对应核验入口"
          action={
            <div className="flex flex-wrap items-center gap-2">
              <input
                value={svcSearch}
                onChange={(e) => setSvcSearch(e.target.value)}
                placeholder="搜索 Service / IP..."
                className="px-2.5 py-1 text-xs rounded-lg bg-[#F7F3EE] dark:bg-[#25201C] border border-[#E9E1D6] dark:border-[#2E2721] outline-none"
              />
              <div className="inline-flex rounded-lg bg-[#F7F3EE] dark:bg-[#25201C] p-0.5 text-[11px] border border-[#E9E1D6] dark:border-[#2E2721]">
                {(['all', 'abnormal', 'broken', 'degraded', 'no-endpoints', 'healthy'] as const).map((f) => (
                  <button
                    key={f}
                    onClick={() => setSvcFilter(f)}
                    className={`px-2 py-0.5 rounded font-medium ${
                      svcFilter === f
                        ? 'bg-[#FFF4E4] dark:bg-[#F08C18]/20 text-[#9E4A05] dark:text-[#FBB040] font-semibold shadow-sm'
                        : 'text-[#8C7F73]'
                    }`}
                  >
                    {f === 'all'
                      ? '全部'
                      : f === 'abnormal'
                        ? `仅异常 (${abnormalSvcCount})`
                        : f === 'broken'
                          ? `失败 (${report.serviceBackends.brokenServices})`
                          : f === 'degraded'
                            ? `降级 (${report.serviceBackends.degradedServices})`
                            : f === 'no-endpoints'
                              ? `无后端 (${report.serviceBackends.noEndpointServices})`
                              : '健康'}
                  </button>
                ))}
              </div>
            </div>
          }
        >
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead className="bg-[#FAF6F1] dark:bg-[#25201C]/70 text-[#8C7F73] border-b border-[#F2ECE4] dark:border-[#2E2721]">
                <tr>
                  <th className="text-left px-4 py-2.5 font-medium">Service</th>
                  <th className="text-left px-4 py-2.5 font-medium">ClusterIP / 声明端口</th>
                  <th className="text-left px-4 py-2.5 font-medium">逐端口状态摘要</th>
                  <th className="text-left px-4 py-2.5 font-medium">诊断结论</th>
                  <th className="text-right px-4 py-2.5 font-medium">推荐下一步</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[#F2ECE4] dark:divide-[#2E2721]">
                {filteredSvcs.map((svc) => {
                  const key = `${svc.namespace}/${svc.name}`;
                  const isExp = expandedSvc === key;
                  const portChecks = svc.portChecks || [];
                  const brokenPort = portChecks.find((pc) => pc.status === 'broken' || pc.status === 'degraded');
                  const focusPort = brokenPort?.servicePort || portChecks[0]?.servicePort;
                  const btnMeta = getActionButtonMeta(svc.recommendedAction, svc.status);

                  return (
                    <React.Fragment key={key}>
                      <tr className="hover:bg-[#FDF8F2] dark:hover:bg-[#25201C]/40">
                        <td className="px-4 py-2.5">
                          <button
                            onClick={() => setExpandedSvc(isExp ? null : key)}
                            className="flex items-center gap-1.5 font-mono font-semibold text-[#201B18] dark:text-[#F5F0EA] text-left hover:text-[#D9720B]"
                          >
                            {isExp ? (
                              <ChevronDown className="w-3.5 h-3.5 text-[#8C7F73]" />
                            ) : (
                              <ChevronRight className="w-3.5 h-3.5 text-[#8C7F73]" />
                            )}
                            <span>{key}</span>
                          </button>
                        </td>
                        <td className="px-4 py-2.5 font-mono text-[11px]">
                          <div className="text-[#201B18] dark:text-[#F5F0EA]">{svc.clusterIP || 'None'}</div>
                          <div className="text-[#8C7F73]">{(svc.ports || []).join(', ') || '—'}</div>
                        </td>
                        <td className="px-4 py-2.5">
                          <div className="flex flex-wrap items-center gap-1.5">
                            <StatusPill status={svc.status} compact />
                            {portChecks.map((pc, pi) => (
                              <span
                                key={pi}
                                className={`px-1.5 py-0.5 rounded font-mono text-[10px] border ${
                                  pc.status === 'broken'
                                    ? 'bg-rose-50 text-rose-700 border-rose-200 dark:bg-rose-950/50 dark:text-rose-300 dark:border-rose-900'
                                    : pc.status === 'degraded'
                                      ? 'bg-[#FFF4E4] text-[#9E4A05] border-[#FAD4A1] dark:bg-[#F08C18]/20 dark:text-[#FBB040]'
                                      : pc.status === 'untested' || pc.status === 'no-endpoints'
                                        ? 'bg-[#F7F3EE] text-[#8C7F73] border-[#E9E1D6] dark:bg-[#25201C] dark:text-[#A39587]'
                                        : 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-300'
                                }`}
                              >
                                {pc.servicePort}/{pc.protocol}:{' '}
                                {pc.status === 'healthy'
                                  ? '正常'
                                  : pc.status === 'broken'
                                    ? '失败'
                                    : pc.status === 'degraded'
                                      ? '降级'
                                      : pc.status === 'no-endpoints'
                                        ? '无后端'
                                        : '未测'}
                              </span>
                            ))}
                          </div>
                        </td>
                        <td className="px-4 py-2.5">
                          <span className="text-[11px] text-[#574D44] dark:text-[#D5C9BC] block max-w-[360px]" title={svc.diagnosis}>
                            {svc.diagnosis}
                          </span>
                        </td>
                        <td className="px-4 py-2.5 text-right whitespace-nowrap">
                          <button
                            onClick={() => triggerEvidenceAction(svc.recommendedAction, svc, focusPort)}
                            className={`inline-flex items-center gap-1 px-2.5 py-1 rounded-lg text-[11px] font-semibold transition ${btnMeta.cls}`}
                          >
                            {btnMeta.icon}
                            <span>{btnMeta.label}</span>
                          </button>
                        </td>
                      </tr>

                      {/* Expanded Per-Port Exact Diagnosis Table */}
                      {isExp && (
                        <tr className="bg-[#FAF6F1] dark:bg-[#14110F]">
                          <td colSpan={5} className="px-6 py-3.5">
                            <div className="rounded-xl border border-[#E9E1D6] dark:border-[#2E2721] bg-white dark:bg-[#1C1815] overflow-hidden">
                              <div className="px-3.5 py-2 bg-[#F7F3EE] dark:bg-[#25201C] border-b border-[#E9E1D6] dark:border-[#2E2721] flex flex-wrap items-center justify-between gap-2 text-[11px]">
                                <span className="font-semibold text-[#201B18] dark:text-[#F5F0EA]">
                                  端口级探测明细（共 {portChecks.length} 个声明端口）
                                </span>
                                <span className="font-mono text-[#8C7F73]">
                                  探测来源：{svc.probeSource || '—'}
                                </span>
                              </div>
                              <div className="overflow-x-auto">
                                <table className="w-full text-[11px]">
                                  <thead className="bg-[#FAF6F1] dark:bg-[#25201C]/50 text-[#8C7F73] border-b border-[#F2ECE4] dark:border-[#2E2721]">
                                    <tr>
                                      <th className="text-left px-3 py-2 font-medium">Service 端口</th>
                                      <th className="text-left px-3 py-2 font-medium">映射后端端口</th>
                                      <th className="text-left px-3 py-2 font-medium">探测来源</th>
                                      <th className="text-left px-3 py-2 font-medium">ClusterIP 结果</th>
                                      <th className="text-left px-3 py-2 font-medium">直连后端结果</th>
                                      <th className="text-left px-3 py-2 font-medium">失败表现</th>
                                      <th className="text-right px-3 py-2 font-medium">操作</th>
                                    </tr>
                                  </thead>
                                  <tbody className="divide-y divide-[#F2ECE4] dark:divide-[#2E2721]">
                                    {portChecks.map((pc, pi) => {
                                      const pBtn = getActionButtonMeta(pc.recommendedAction, pc.status);
                                      return (
                                        <React.Fragment key={pi}>
                                          <tr className="hover:bg-[#FDF8F2] dark:hover:bg-[#25201C]/30">
                                            <td className="px-3 py-2 font-mono font-semibold text-[#201B18] dark:text-[#F5F0EA]">
                                              {pc.servicePort}/{pc.protocol}
                                              {pc.portName && <span className="text-[10px] text-[#8C7F73] ml-1">({pc.portName})</span>}
                                            </td>
                                            <td className="px-3 py-2 font-mono text-[#574D44] dark:text-[#D5C9BC]">
                                              {pc.resolvedTargetPort > 0 ? `${pc.resolvedTargetPort}/${pc.protocol}` : pc.targetPort}
                                            </td>
                                            <td className="px-3 py-2 font-mono text-[10px] text-[#8C7F73]">
                                              {pc.probeSource}
                                            </td>
                                            <td className="px-3 py-2 font-mono">
                                              {pc.clusterIPState === 'ok' ? (
                                                <span className="text-emerald-600 dark:text-emerald-400 font-semibold">
                                                  可达 ({pc.clusterIPLatencyMs}ms)
                                                </span>
                                              ) : pc.clusterIPState === 'failed' ? (
                                                <span className="text-rose-600 dark:text-rose-400 font-semibold" title={pc.clusterIPError}>
                                                  失败
                                                </span>
                                              ) : pc.clusterIPState === 'headless' ? (
                                                <span className="text-[#8C7F73]">Headless (无 ClusterIP)</span>
                                              ) : (
                                                <span className="text-[#8C7F73]">未测</span>
                                              )}
                                            </td>
                                            <td className="px-3 py-2 font-mono">
                                              {pc.totalBackends === 0 ? (
                                                <span className="text-[#8C7F73]">0 个后端</span>
                                              ) : pc.probeState === 'untested' ? (
                                                <span className="text-[#8C7F73]">未测 (Ready {pc.readyBackends}/{pc.totalBackends})</span>
                                              ) : (
                                                <span
                                                  className={
                                                    pc.reachableBackends === 0
                                                      ? 'text-rose-600 dark:text-rose-400 font-semibold'
                                                      : pc.reachableBackends < pc.testedBackends
                                                        ? 'text-[#D9720B] font-semibold'
                                                        : 'text-emerald-600 dark:text-emerald-400 font-semibold'
                                                  }
                                                >
                                                  {pc.reachableBackends}/{pc.testedBackends} 可达 (Ready {pc.readyBackends}/{pc.totalBackends})
                                                </span>
                                              )}
                                            </td>
                                            <td className="px-3 py-2">
                                              <span
                                                className={
                                                  pc.status === 'broken'
                                                    ? 'text-rose-700 dark:text-rose-300 font-semibold'
                                                    : pc.status === 'degraded'
                                                      ? 'text-[#9E4A05] dark:text-[#FBB040] font-semibold'
                                                      : pc.status === 'untested'
                                                        ? 'text-[#8C7F73]'
                                                        : 'text-emerald-700 dark:text-emerald-400'
                                                }
                                              >
                                                {pc.failureSummary || '—'}
                                              </span>
                                            </td>
                                            <td className="px-3 py-2 text-right whitespace-nowrap">
                                              <div className="inline-flex items-center gap-1.5">
                                                <button
                                                  onClick={() => triggerEvidenceAction(pc.recommendedAction, svc, pc.servicePort, pc.probeSourcePod)}
                                                  className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-semibold transition ${pBtn.cls}`}
                                                >
                                                  {pBtn.label}
                                                </button>
                                                {(pc.status === 'broken' || pc.status === 'degraded') && (
                                                  <button
                                                    onClick={() => handleSavePortIncident(svc, pc)}
                                                    className="inline-flex items-center gap-0.5 px-2 py-0.5 rounded text-[10px] font-medium bg-[#F7F3EE] dark:bg-[#25201C] text-[#574D44] dark:text-[#D5C9BC] border border-[#E9E1D6] dark:border-[#2E2721] hover:border-[#F08C18]"
                                                    title="保存为可复测故障现场"
                                                  >
                                                    <BookmarkPlus className="w-3 h-3" /> 留存现场
                                                  </button>
                                                )}
                                              </div>
                                            </td>
                                          </tr>
                                          {/* Individual backends under this port */}
                                          {pc.backends.length > 0 && (
                                            <tr className="bg-[#FCF9F4] dark:bg-[#161310]">
                                              <td colSpan={7} className="px-6 py-2">
                                                <div className="flex flex-wrap gap-2">
                                                  {pc.backends.map((be, bi) => (
                                                    <div
                                                      key={`${be.podIP}-${be.targetPort}-${bi}`}
                                                      className="inline-flex items-center gap-2 px-2.5 py-1 rounded-lg bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721] font-mono text-[10px]"
                                                    >
                                                      <span className="font-semibold text-[#201B18] dark:text-[#F5F0EA]">
                                                        {be.podName ? `${be.podNamespace}/${be.podName}` : be.podIP}
                                                      </span>
                                                      <span className="text-[#8C7F73]">
                                                        {be.podIP}:{be.targetPort}
                                                        {be.nodeName ? ` @ ${be.nodeName}` : ''}
                                                      </span>
                                                      <StatusPill status={be.k8sReady ? 'Ready' : 'NotReady'} compact />
                                                      {be.probeState === 'untested' ? (
                                                        <span className="text-[#8C7F73]">未测</span>
                                                      ) : be.probeOk ? (
                                                        <span className="text-emerald-600 dark:text-emerald-400 font-semibold">
                                                          ✓ {be.latencyMs}ms
                                                        </span>
                                                      ) : (
                                                        <span className="text-rose-600 dark:text-rose-400 font-semibold" title={be.error}>
                                                          ✕ {be.failureLabel || 'FAIL'}
                                                        </span>
                                                      )}
                                                    </div>
                                                  ))}
                                                </div>
                                              </td>
                                            </tr>
                                          )}
                                        </React.Fragment>
                                      );
                                    })}
                                  </tbody>
                                </table>
                              </div>
                            </div>
                          </td>
                        </tr>
                      )}
                    </React.Fragment>
                  );
                })}
              </tbody>
            </table>
          </div>
        </SectionCard>
      </div>

      {/* 3. Intermittent Incident Snapshots, Correlated Timeline & One-Click Retest Diff */}
      <div
        ref={incSectionRef}
        className={`scroll-mt-20 rounded-xl transition ${highlightSection === 'incidents' ? 'ring-2 ring-[#F08C18]' : ''}`}
      >
        <SectionCard
          title={`偶发故障现场留存与复测对比 (${incidents.length})`}
          subtitle="记录失败端口的首次/最近出现时间与周边变更时间轴，支持同条件复测对比"
          action={
            savedNotice ? (
              <span className="text-xs font-semibold text-emerald-600 dark:text-emerald-400">{savedNotice}</span>
            ) : null
          }
        >
          {incidents.length === 0 ? (
            <div className="p-6 text-center text-xs text-[#8C7F73]">
              当前暂无留存的故障现场；巡检检测到失败端口时会自动记录，也可在任意异常端口行点击「留存现场」保存。
            </div>
          ) : (
            <div className="divide-y divide-[#F2ECE4] dark:divide-[#2E2721]">
              {incidents.map((inc) => {
                const isOpen = expandedIncidentId === inc.id;
                const latestRetest = inc.retests?.[0];
                return (
                  <div key={inc.id} className="p-4 space-y-3">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <div className="space-y-1 min-w-0">
                        <div className="flex flex-wrap items-center gap-2">
                          <span
                            className={`px-2 py-0.5 rounded-full text-[10px] font-bold border ${
                              inc.status === 'active'
                                ? 'bg-rose-50 text-rose-700 border-rose-200 dark:bg-rose-950/60 dark:text-rose-300'
                                : inc.status === 'intermittent'
                                  ? 'bg-[#FFF4E4] text-[#9E4A05] border-[#FAD4A1] dark:bg-[#F08C18]/20 dark:text-[#FBB040]'
                                  : 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-950/50 dark:text-emerald-300'
                            }`}
                          >
                            {inc.status === 'active' ? '持续异常' : inc.status === 'intermittent' ? '偶发已恢复' : '已恢复'}
                          </span>
                          <span className="font-mono text-xs font-bold text-[#201B18] dark:text-[#F5F0EA]">{inc.title}</span>
                          <span className="font-mono text-[10px] text-[#8C7F73]">({inc.id})</span>
                        </div>
                        <div className="flex flex-wrap items-center gap-3 text-[11px] text-[#8C7F73] font-mono">
                          <span className="inline-flex items-center gap-1">
                            <Clock className="w-3 h-3" /> 首次: {new Date(inc.firstSeenAt).toLocaleTimeString()}
                          </span>
                          <span>最近: {new Date(inc.lastSeenAt).toLocaleTimeString()}</span>
                          <span>累计失败 {inc.failureCount} 次</span>
                          {inc.srcPod && <span>源: {inc.srcNamespace}/{inc.srcPod}</span>}
                          {inc.targetBackendIP && <span>后端: {inc.targetBackendPod || inc.targetBackendIP}:{inc.backendPort || inc.servicePort}</span>}
                        </div>
                      </div>

                      <div className="flex items-center gap-2 shrink-0">
                        <button
                          onClick={() => setExpandedIncidentId(isOpen ? null : inc.id)}
                          className="px-2.5 py-1.5 rounded-lg text-xs font-medium bg-[#F7F3EE] dark:bg-[#25201C] text-[#574D44] dark:text-[#D5C9BC] border border-[#E9E1D6] dark:border-[#2E2721] hover:border-[#F08C18]"
                        >
                          {isOpen ? '收起时间轴' : `周边时间轴 (${inc.timeline?.length || 0})`}
                        </button>
                        <button
                          onClick={() => handleRetestIncident(inc.id)}
                          disabled={retestingId === inc.id}
                          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-semibold bg-[#F08C18] hover:bg-[#D9720B] disabled:opacity-50 text-white shadow-xs transition"
                        >
                          {retestingId === inc.id ? (
                            <RefreshCw className="w-3.5 h-3.5 animate-spin" />
                          ) : (
                            <Play className="w-3.5 h-3.5" />
                          )}
                          <span>{retestingId === inc.id ? '复测中...' : '用相同条件复测'}</span>
                        </button>
                      </div>
                    </div>

                    {/* Latest Retest Before vs After Diff Card */}
                    {latestRetest && (
                      <div
                        className={`p-3 rounded-xl border text-xs space-y-2 ${
                          latestRetest.verdict === 'recovered'
                            ? 'bg-emerald-50/70 dark:bg-emerald-950/20 border-emerald-200 dark:border-emerald-900'
                            : 'bg-[#FFF4E4]/60 dark:bg-[#F08C18]/10 border-[#FAD4A1] dark:border-[#F08C18]/40'
                        }`}
                      >
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <span className="font-bold text-[#201B18] dark:text-[#F5F0EA]">
                            最近复测结果：{latestRetest.verdictText}
                          </span>
                          <span className="font-mono text-[10px] text-[#8C7F73]">
                            {new Date(latestRetest.testedAt).toLocaleTimeString()} · 来源: {latestRetest.probeSource}
                          </span>
                        </div>
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-2 text-[11px] font-mono">
                          <div className="p-2 rounded-lg bg-white/80 dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721]">
                            <div className="text-[10px] font-sans text-[#8C7F73]">故障当时 (Before)</div>
                            <div className="text-rose-700 dark:text-rose-300 font-semibold mt-0.5">
                              {latestRetest.diff.failureBefore}
                            </div>
                          </div>
                          <div className="p-2 rounded-lg bg-white/80 dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721]">
                            <div className="text-[10px] font-sans text-[#8C7F73]">复测现在 (After)</div>
                            <div
                              className={`font-semibold mt-0.5 ${
                                latestRetest.verdict === 'recovered' ? 'text-emerald-700 dark:text-emerald-300' : 'text-rose-700 dark:text-rose-300'
                              }`}
                            >
                              {latestRetest.diff.failureAfter}
                              {latestRetest.diff.latencyAfter > 0 ? ` (${latestRetest.diff.latencyAfter} ms)` : ''}
                            </div>
                          </div>
                        </div>
                        <ul className="list-disc pl-4 text-[11px] text-[#574D44] dark:text-[#D5C9BC] space-y-0.5">
                          {latestRetest.diff.changesSummary.map((line, li) => (
                            <li key={li}>{line}</li>
                          ))}
                        </ul>
                      </div>
                    )}

                    {/* Expanded Correlated Cluster Change Timeline */}
                    {isOpen && (
                      <div className="p-3 rounded-xl bg-[#FAF6F1] dark:bg-[#14110F] border border-[#E9E1D6] dark:border-[#2E2721] space-y-2">
                        <div className="text-[11px] font-bold text-[#574D44] dark:text-[#D5C9BC]">
                          相邻时间窗口集群变更时间轴（后端更替 / Pod 重建 / 策略变更 / BGP 会话变化）
                        </div>
                        {!inc.timeline || inc.timeline.length === 0 ? (
                          <div className="text-[11px] text-[#8C7F73]">
                            该故障前后 10 分钟内未观测到关联的 Pod 重建、EndpointSlice 变更、策略变更或 BGP 会话抖动。
                          </div>
                        ) : (
                          <div className="space-y-1.5 max-h-[220px] overflow-y-auto">
                            {inc.timeline.map((ev, ei) => (
                              <div
                                key={ei}
                                className="flex flex-wrap items-center gap-2 px-2.5 py-1.5 rounded-lg bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721] text-[11px]"
                              >
                                <span className="font-mono text-[10px] text-[#8C7F73]">
                                  {new Date(ev.timestamp).toLocaleTimeString()}
                                </span>
                                <span className="px-1.5 py-0.2 rounded text-[10px] font-semibold bg-[#FFF4E4] text-[#9E4A05] border border-[#FAD4A1] dark:bg-[#F08C18]/20 dark:text-[#FBB040] dark:border-[#F08C18]/40">
                                  {ev.category === 'pod_rebuild'
                                    ? 'Pod 变更'
                                    : ev.category === 'endpoint_change'
                                      ? '后端更替'
                                      : ev.category === 'policy_change'
                                        ? '策略变更'
                                        : ev.category === 'bgp_change'
                                          ? 'BGP 变化'
                                          : 'K8s Event'}
                                </span>
                                <span className="font-mono font-semibold text-[#201B18] dark:text-[#F5F0EA]">{ev.object}</span>
                                <span className="text-[#574D44] dark:text-[#D5C9BC]">{ev.summary}</span>
                              </div>
                            ))}
                          </div>
                        )}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </SectionCard>
      </div>

      {/* Evidence-Driven Service Diagnosis Slide-Over Drawer */}
      {diagDrawer && (
        <ServiceDiagDrawer
          namespace={diagDrawer.namespace}
          serviceName={diagDrawer.serviceName}
          focusPort={diagDrawer.focusPort}
          initialMode={diagDrawer.mode}
          onClose={() => setDiagDrawer(null)}
          onLaunchTrace={onLaunchTrace}
        />
      )}
    </div>
  );
}

function getActionButtonMeta(
  action: ServicePortHealth['recommendedAction'] | undefined,
  status: string,
): { label: string; cls: string; icon: React.ReactNode } {
  if (action === 'inspect_empty_backends' || status === 'no-endpoints') {
    return {
      label: '查看后端为什么为空',
      cls: 'bg-[#F6EFE8] text-[#6C4824] border border-[#DFCFC0] hover:bg-[#EBE0D3] dark:bg-[#6C4824]/30 dark:text-[#DFC4A6] dark:border-[#6C4824]/60',
      icon: <Layers className="w-3 h-3" />,
    };
  }
  if (action === 'check_port_listening') {
    return {
      label: '核对目标端口与监听状态',
      cls: 'bg-[#F08C18] text-white hover:bg-[#D9720B] shadow-xs',
      icon: <Radio className="w-3 h-3" />,
    };
  }
  if (action === 'trace_service_dnat') {
    return {
      label: '追踪这次 Service 转发',
      cls: 'bg-rose-600 text-white hover:bg-rose-700 shadow-xs',
      icon: <Route className="w-3 h-3" />,
    };
  }
  if (action === 'compare_policy_sources') {
    return {
      label: '对比双来源路径与策略',
      cls: 'bg-[#FFF4E4] text-[#9E4A05] border border-[#FAD4A1] hover:bg-[#FDE6C4] dark:bg-[#F08C18]/20 dark:text-[#FBB040] dark:border-[#F08C18]/40',
      icon: <GitCompare className="w-3 h-3" />,
    };
  }
  return {
    label: 'TraceFlow',
    cls: 'bg-[#FFF4E4] text-[#9E4A05] border border-[#FAD4A1] hover:bg-[#FDE6C4] dark:bg-[#F08C18]/15 dark:text-[#FBB040]',
    icon: <Route className="w-3 h-3" />,
  };
}

function HealthMiniCard({
  title,
  value,
  sub,
  ok,
  onClick,
  hint,
  subNode,
}: {
  title: string;
  value: string;
  sub: string;
  ok: boolean;
  onClick?: () => void;
  hint?: string;
  subNode?: React.ReactNode;
}) {
  return (
    <div
      onClick={onClick}
      className={`p-4 rounded-xl bg-white dark:bg-[#1C1815] border transition ${
        !ok
          ? 'border-rose-200 dark:border-rose-900/70 hover:border-rose-400 dark:hover:border-rose-600'
          : 'border-[#E9E1D6] dark:border-[#2E2721] hover:border-[#F08C18] dark:hover:border-[#F08C18]/70'
      } ${onClick ? 'cursor-pointer hover:shadow-sm' : ''}`}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs text-[#8C7F73]">{title}</span>
        <span className={`w-2 h-2 rounded-full ${ok ? 'bg-emerald-500' : 'bg-rose-500 animate-pulse'}`} />
      </div>
      <div className="mt-1.5 text-lg font-bold font-mono text-[#201B18] dark:text-[#F5F0EA]">{value}</div>
      {subNode || (
        <div className="mt-1 text-[11px] text-[#8C7F73] truncate" title={sub}>
          {sub}
        </div>
      )}
      {hint && (
        <div className="mt-1.5 inline-flex items-center gap-0.5 text-[10px] font-semibold text-rose-600 dark:text-rose-400">
          <span>{hint}</span>
          <ArrowDownRight className="w-3 h-3" />
        </div>
      )}
    </div>
  );
}
