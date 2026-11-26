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
} from 'lucide-react';
import { api } from '../api/client';
import type { GlobalHealthReport, TraceFlowRequest } from '../api/types';
import { SectionCard, StatusPill } from '../components/common/Badges';

interface HealthPageProps {
  onLaunchTrace: (req: Partial<TraceFlowRequest>) => void;
}

type SvcFilterKey = 'all' | 'abnormal' | 'broken' | 'degraded' | 'no-endpoints' | 'healthy';

export function HealthPage({ onLaunchTrace }: HealthPageProps) {
  const [report, setReport] = useState<GlobalHealthReport | null>(null);
  const [loading, setLoading] = useState(true);
  const [running, setRunning] = useState(false);
  const [svcFilter, setSvcFilter] = useState<SvcFilterKey>('all');
  const [svcSearch, setSvcSearch] = useState('');
  const [expandedSvc, setExpandedSvc] = useState<string | null>(null);
  const [highlightSection, setHighlightSection] = useState<'matrix' | 'infra' | 'services' | null>(null);

  const matrixSectionRef = useRef<HTMLDivElement>(null);
  const infraSectionRef = useRef<HTMLDivElement>(null);
  const svcSectionRef = useRef<HTMLDivElement>(null);

  const scrollToSection = (section: 'matrix' | 'infra' | 'services') => {
    const ref = section === 'matrix' ? matrixSectionRef : section === 'infra' ? infraSectionRef : svcSectionRef;
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
    api
      .getHealth()
      .then(setReport)
      .finally(() => setLoading(false));
  };

  const runNow = () => {
    setRunning(true);
    api
      .runHealthNow()
      .then(setReport)
      .finally(() => setRunning(false));
  };

  useEffect(() => {
    load();
  }, []);

  if (loading && !report) {
    return <div className="p-8 text-sm text-slate-500">正在执行集群全维度网络健康检查（Pod-Pod / Pod-Node / Pod-SVC / 全量 Service 后端）...</div>;
  }
  if (!report) return null;

  const scoreColor =
    report.overallScore >= 90
      ? 'text-emerald-600 dark:text-emerald-400 border-emerald-500'
      : report.overallScore >= 70
        ? 'text-amber-600 dark:text-amber-400 border-amber-500'
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

  // Lookup map for N x N matrix
  const cellMap = new Map((report.podToPod.cells || []).map((c) => [`${c.srcNode}|${c.dstNode}`, c]));
  const matrixNodes = report.podToPod.nodes || [];
  const infraChecks = report.infrastructure.checks || [];
  const svcChecks = report.podToService.checks || [];

  return (
    <div className="p-6 space-y-6 max-w-[1600px] mx-auto">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-[#201B18] dark:text-[#F5F0EA]">
            全局网络健康体检中心
          </h1>
          <p className="text-xs text-[#8C7F73] dark:text-[#A39587] mt-1">
            最近巡检：{new Date(report.checkedAt).toLocaleString()}（耗时 {report.durationMs} ms）· 覆盖 Pod-Pod 网格、Pod-Node、CoreDNS/SVC 及全集群 Service 后端穿透探测
          </p>
        </div>
        <button
          onClick={runNow}
          disabled={running}
          className="inline-flex items-center gap-1.5 px-4 py-2 rounded-lg bg-[#F08C18] hover:bg-[#D9720B] disabled:opacity-50 text-white text-xs font-semibold shadow-sm transition"
        >
          <RefreshCw className={`w-3.5 h-3.5 ${running ? 'animate-spin' : ''}`} />
          {running ? '全量体检中...' : '立即全量巡检'}
        </button>
      </div>

      {/* Top Health Score + 5 KPI Cards */}
      <div className="grid grid-cols-1 lg:grid-cols-6 gap-4">
        <div className="p-4 rounded-xl bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721] flex items-center gap-4">
          <div className={`w-16 h-16 rounded-full border-4 flex flex-col items-center justify-center shrink-0 ${scoreColor}`}>
            <span className="text-xl font-bold leading-none">{report.overallScore}</span>
            <span className="text-[9px] uppercase font-semibold mt-0.5">Score</span>
          </div>
          <div>
            <div className="text-xs text-[#8C7F73]">集群网络健康分</div>
            <div className="mt-1"><StatusPill status={report.overallStatus} /></div>
          </div>
        </div>

        <HealthMiniCard
          title="Pod ↔ Pod 网格"
          value={`${report.podToPod.healthyPairs}/${report.podToPod.totalPairs}`}
          sub={`均值 ${report.podToPod.avgLatencyMs}ms · 峰值 ${report.podToPod.maxLatencyMs}ms`}
          ok={report.podToPod.failedPairs === 0}
          onClick={() => scrollToSection('matrix')}
          hint={report.podToPod.failedPairs > 0 ? '查看失败明细' : undefined}
        />
        <HealthMiniCard
          title="Pod → Node 连通"
          value={`${report.podToNode.healthyChecks}/${report.podToNode.totalChecks}`}
          sub={`均值延迟 ${report.podToNode.avgLatencyMs} ms`}
          ok={report.podToNode.healthyChecks === report.podToNode.totalChecks}
          onClick={() => scrollToSection('infra')}
          hint={report.podToNode.healthyChecks < report.podToNode.totalChecks ? '查看异常明细' : undefined}
        />
        <HealthMiniCard
          title="Pod → SVC & DNS"
          value={`${report.podToService.healthyChecks}/${report.podToService.totalChecks}`}
          sub={`CoreDNS 解析均值 ${report.podToService.dnsAvgMs} ms`}
          ok={report.podToService.healthyChecks === report.podToService.totalChecks}
          onClick={() => scrollToSection('infra')}
          hint={report.podToService.healthyChecks < report.podToService.totalChecks ? '查看异常明细' : undefined}
        />
        <HealthMiniCard
          title="Service 后端健康"
          value={`${report.serviceBackends.healthyServices}/${report.serviceBackends.totalServices}`}
          sub={`异常 ${report.serviceBackends.brokenServices} · 降级 ${report.serviceBackends.degradedServices} · 无后端 ${report.serviceBackends.noEndpointServices}`}
          ok={report.serviceBackends.brokenServices === 0 && report.serviceBackends.degradedServices === 0}
          onClick={() =>
            jumpToServiceIssues(
              report.serviceBackends.brokenServices > 0 || report.serviceBackends.degradedServices > 0 || report.serviceBackends.noEndpointServices > 0
                ? 'abnormal'
                : 'all',
            )
          }
          hint={abnormalSvcCount > 0 ? `点击定位 ${abnormalSvcCount} 项异常原因` : undefined}
          subNode={
            <div className="mt-1 flex flex-wrap items-center gap-1.5 text-[11px]">
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  jumpToServiceIssues('broken');
                }}
                className={`hover:underline ${report.serviceBackends.brokenServices > 0 ? 'text-rose-600 dark:text-rose-400 font-semibold' : 'text-slate-500'}`}
              >
                异常 {report.serviceBackends.brokenServices}
              </button>
              <span className="text-slate-300 dark:text-slate-700">·</span>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  jumpToServiceIssues('degraded');
                }}
                className={`hover:underline ${report.serviceBackends.degradedServices > 0 ? 'text-amber-600 dark:text-amber-400 font-semibold' : 'text-slate-500'}`}
              >
                降级 {report.serviceBackends.degradedServices}
              </button>
              <span className="text-slate-300 dark:text-slate-700">·</span>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  jumpToServiceIssues('no-endpoints');
                }}
                className="text-slate-500 hover:underline hover:text-slate-800 dark:hover:text-slate-200"
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
          ok={report.infrastructure.calicoNodesReady === report.infrastructure.calicoNodesTotal && report.infrastructure.bgpEstablished === report.infrastructure.bgpTotal}
          onClick={() => scrollToSection('infra')}
          hint={
            report.infrastructure.calicoNodesReady < report.infrastructure.calicoNodesTotal ||
            report.infrastructure.bgpEstablished < report.infrastructure.bgpTotal
              ? '查看底座异常'
              : undefined
          }
        />
      </div>

      {/* Middle Row: N x N Pod-Pod Matrix + Infra & CoreDNS Checklist */}
      <div className="grid grid-cols-1 xl:grid-cols-2 gap-6">
        <div
          ref={matrixSectionRef}
          className={`scroll-mt-20 rounded-xl transition ${highlightSection === 'matrix' ? 'ring-2 ring-blue-500/70' : ''}`}
        >
        <SectionCard
          title="Pod ↔ Pod 全节点连通性与延迟矩阵"
          subtitle="基于每节点轻量 Probe Pod 实测，点击任意单元格一键发起该两点 TraceFlow"
        >
          <div className="p-4 overflow-x-auto">
            {matrixNodes.length > 0 ? (
              <table className="w-full text-xs font-mono border-collapse">
                <thead>
                  <tr>
                    <th className="p-2 text-left text-[11px] text-slate-400">Src \ Dst</th>
                    {matrixNodes.map((dn) => (
                      <th key={dn} className="p-2 text-center text-[11px] text-slate-600 dark:text-slate-300 truncate max-w-[140px]">
                        {dn}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {matrixNodes.map((sn) => (
                    <tr key={sn}>
                      <td className="p-2 font-semibold text-slate-700 dark:text-slate-200 truncate max-w-[150px]">{sn}</td>
                      {matrixNodes.map((dn) => {
                        const cell = cellMap.get(`${sn}|${dn}`);
                        if (!cell) {
                          return <td key={dn} className="p-1.5 text-center text-slate-400">—</td>;
                        }
                        const bg = !cell.reachable
                          ? 'bg-rose-100 text-rose-800 border-rose-300 dark:bg-rose-950/70 dark:text-rose-200 dark:border-rose-800'
                          : cell.latencyMs > 15
                            ? 'bg-amber-100 text-amber-800 border-amber-300 dark:bg-amber-950/60 dark:text-amber-200 dark:border-amber-800'
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
                              title={`${cell.srcIP} → ${cell.dstIP} (${cell.sameNode ? '同节点' : '跨节点'}) 点击 TraceFlow`}
                              className={`w-full py-2.5 px-2 rounded-lg border text-center transition hover:scale-[1.02] ${bg}`}
                            >
                              <div className="font-bold text-xs">{cell.reachable ? `${cell.latencyMs} ms` : 'FAIL'}</div>
                              <div className="text-[9px] opacity-75">{cell.sameNode ? 'Same-Node' : 'Cross-Node'}</div>
                            </button>
                          </td>
                        );
                      })}
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : (
              <div className="p-6 text-center text-xs text-slate-400">暂无 Probe Pod 数据</div>
            )}
          </div>
        </SectionCard>
        </div>

        <div
          ref={infraSectionRef}
          className={`scroll-mt-20 rounded-xl transition ${highlightSection === 'infra' ? 'ring-2 ring-blue-500/70' : ''}`}
        >
        <SectionCard title="Calico 底座、Pod-Node 与 CoreDNS 巡检明细">
          <div className="p-4 space-y-4">
            <div className="space-y-2">
              {infraChecks.map((c, idx) => (
                <div key={idx} className="flex items-start justify-between gap-3 p-2.5 rounded-lg bg-slate-50 dark:bg-slate-950/50 border border-slate-200/80 dark:border-slate-800 text-xs">
                  <div className="flex items-start gap-2">
                    {c.status === 'pass' ? (
                      <CheckCircle2 className="w-4 h-4 text-emerald-500 mt-0.5 shrink-0" />
                    ) : c.status === 'warn' ? (
                      <AlertTriangle className="w-4 h-4 text-amber-500 mt-0.5 shrink-0" />
                    ) : (
                      <XCircle className="w-4 h-4 text-rose-500 mt-0.5 shrink-0" />
                    )}
                    <div>
                      <span className="font-semibold text-slate-800 dark:text-slate-100">{c.name}</span>
                      <p className="text-[11px] text-slate-500 mt-0.5">{c.detail}</p>
                    </div>
                  </div>
                  <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-slate-200/70 dark:bg-slate-800 text-slate-600 dark:text-slate-300">
                    {c.category}
                  </span>
                </div>
              ))}
            </div>

            {/* Pod -> CoreDNS / API Server checks */}
            <div className="rounded-lg border border-slate-200 dark:border-slate-800 overflow-hidden">
              <table className="w-full text-[11px] font-mono">
                <thead className="bg-slate-50 dark:bg-slate-800/70 text-slate-500">
                  <tr>
                    <th className="text-left px-3 py-1.5">源节点</th>
                    <th className="text-left px-3 py-1.5">检测项</th>
                    <th className="text-left px-3 py-1.5">结果</th>
                    <th className="text-right px-3 py-1.5">延迟</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                  {svcChecks.map((ch, idx) => (
                    <tr key={idx}>
                      <td className="px-3 py-1.5 text-slate-700 dark:text-slate-300">{ch.srcNode}</td>
                      <td className="px-3 py-1.5">{ch.targetName} <span className="text-slate-400">({ch.detail || ch.targetAddr})</span></td>
                      <td className="px-3 py-1.5"><StatusPill status={ch.reachable ? 'pass' : 'fail'} label={ch.reachable ? 'OK' : 'FAIL'} /></td>
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

      {/* Full Service & Backend Endpoint Health Audit Table */}
      <div
        ref={svcSectionRef}
        className={`scroll-mt-20 rounded-xl transition ${highlightSection === 'services' ? 'ring-2 ring-rose-500/70' : ''}`}
      >
      <SectionCard
        title={`全集群 Service & 后端 Endpoint 健康审计 (${report.serviceBackends.totalServices})`}
        subtitle="同时实测 ClusterIP 转发与直连各后端 Pod TargetPort，快速甄别 kube-proxy 异常 vs 应用未监听 vs 无就绪端点"
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
                  {f === 'all' ? '全部' : f === 'abnormal' ? `仅异常 (${abnormalSvcCount})` : f}
                </button>
              ))}
            </div>
          </div>
        }
      >
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead className="bg-slate-50/80 dark:bg-slate-800/60 text-slate-500">
              <tr>
                <th className="text-left px-4 py-2.5 font-medium">Service</th>
                <th className="text-left px-4 py-2.5 font-medium">ClusterIP / 端口</th>
                <th className="text-left px-4 py-2.5 font-medium">ClusterIP 探测</th>
                <th className="text-left px-4 py-2.5 font-medium">后端 Ready / 实测可达</th>
                <th className="text-left px-4 py-2.5 font-medium">状态 & 诊断结论</th>
                <th className="text-right px-4 py-2.5 font-medium">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
              {filteredSvcs.map((svc) => {
                const key = `${svc.namespace}/${svc.name}`;
                const isExp = expandedSvc === key;
                const backends = svc.backends || [];
                return (
                  <React.Fragment key={key}>
                    <tr className="hover:bg-slate-50/70 dark:hover:bg-slate-800/40">
                      <td className="px-4 py-2.5">
                        <button
                          onClick={() => setExpandedSvc(isExp ? null : key)}
                          className="flex items-center gap-1.5 font-mono font-semibold text-slate-800 dark:text-slate-100 text-left"
                        >
                          {backends.length > 0 ? (
                            isExp ? <ChevronDown className="w-3.5 h-3.5 text-slate-400" /> : <ChevronRight className="w-3.5 h-3.5 text-slate-400" />
                          ) : (
                            <span className="w-3.5" />
                          )}
                          <span>{key}</span>
                        </button>
                      </td>
                      <td className="px-4 py-2.5 font-mono text-[11px]">
                        <div>{svc.clusterIP}</div>
                        <div className="text-slate-400">{(svc.ports || []).join(', ')}</div>
                      </td>
                      <td className="px-4 py-2.5 font-mono text-[11px]">
                        {svc.clusterIPReachable === undefined ? (
                          <span className="text-slate-400">—</span>
                        ) : svc.clusterIPReachable ? (
                          <span className="text-emerald-600 dark:text-emerald-400">OK ({svc.clusterIPLatencyMs}ms)</span>
                        ) : (
                          <span className="text-rose-600" title={svc.clusterIPError}>FAIL</span>
                        )}
                      </td>
                      <td className="px-4 py-2.5 font-mono text-[11px]">
                        Ready {svc.readyBackends}/{svc.totalBackends} · TCP {svc.reachableBackends}/{svc.totalBackends}
                      </td>
                      <td className="px-4 py-2.5">
                        <div className="flex items-center gap-2">
                          <StatusPill status={svc.status} />
                          <span className="text-[11px] text-slate-500 dark:text-slate-400 truncate max-w-[300px]" title={svc.diagnosis}>
                            {svc.diagnosis}
                          </span>
                        </div>
                      </td>
                      <td className="px-4 py-2.5 text-right">
                        {svc.clusterIP && svc.clusterIP !== 'None' && (
                          <button
                            onClick={() =>
                              onLaunchTrace({
                                targetType: 'service',
                                dstNamespace: svc.namespace,
                                dstName: svc.name,
                              })
                            }
                            className="inline-flex items-center gap-1 px-2 py-1 rounded-md bg-[#FFF4E4] text-[#9E4A05] border border-[#FAD4A1] hover:bg-[#FDE6C4] dark:bg-[#F08C18]/15 dark:text-[#FBB040] dark:border-[#F08C18]/40 text-[11px] font-medium"
                          >
                            <Route className="w-3 h-3" /> TraceFlow
                          </button>
                        )}
                      </td>
                    </tr>

                    {isExp && backends.length > 0 && (
                      <tr className="bg-slate-50/80 dark:bg-slate-950/60">
                        <td colSpan={6} className="px-10 py-3">
                          <div className="space-y-1.5">
                            {backends.map((be, bi) => (
                              <div
                                key={`${be.podIP}-${bi}`}
                                className="flex flex-wrap items-center justify-between gap-2 px-3 py-1.5 rounded-lg bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 text-[11px] font-mono"
                              >
                                <div className="flex items-center gap-3">
                                  <span className="font-semibold text-slate-800 dark:text-slate-100">
                                    {be.podName ? `${be.podNamespace}/${be.podName}` : be.podIP}
                                  </span>
                                  <span>{be.podIP}:{be.targetPort}/{be.protocol}</span>
                                  {be.nodeName && <span className="text-slate-400">Node: {be.nodeName}</span>}
                                </div>
                                <div className="flex items-center gap-2">
                                  <StatusPill status={be.k8sReady ? 'Ready' : 'NotReady'} />
                                  <StatusPill status={be.probeOk ? 'pass' : 'fail'} label={be.probeOk ? `TCP ${be.latencyMs}ms` : 'TCP FAIL'} />
                                </div>
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
      </SectionCard>
      </div>
    </div>
  );
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
