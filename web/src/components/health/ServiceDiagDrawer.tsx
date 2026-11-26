import React, { useEffect, useState } from 'react';
import {
  X,
  RefreshCw,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  Shield,
  Radio,
  Route,
  GitCompare,
  Layers,
  Server,
} from 'lucide-react';
import { api } from '../../api/client';
import type {
  CompareSourcesResponse,
  PodBrief,
  PolicyAttributionItem,
  ServiceInspectResponse,
  TraceFlowRequest,
} from '../../api/types';
import { StatusPill } from '../common/Badges';

export type ServiceDiagMode = 'empty' | 'listening' | 'compare';

interface ServiceDiagDrawerProps {
  namespace: string;
  serviceName: string;
  focusPort?: number;
  initialMode: ServiceDiagMode;
  onClose: () => void;
  onLaunchTrace: (req: Partial<TraceFlowRequest>) => void;
}

export function ServiceDiagDrawer({
  namespace,
  serviceName,
  focusPort,
  initialMode,
  onClose,
  onLaunchTrace,
}: ServiceDiagDrawerProps) {
  const [mode, setMode] = useState<ServiceDiagMode>(initialMode);
  const [inspectData, setInspectData] = useState<ServiceInspectResponse | null>(null);
  const [inspectLoading, setInspectLoading] = useState(false);
  const [inspectErr, setInspectErr] = useState('');

  // Dual-source comparison state
  const [pods, setPods] = useState<PodBrief[]>([]);
  const [bizNs, setBizNs] = useState('');
  const [bizPod, setBizPod] = useState('');
  const [cmpPort, setCmpPort] = useState(focusPort || 0);
  const [cmpLoading, setCmpLoading] = useState(false);
  const [cmpErr, setCmpErr] = useState('');
  const [cmpData, setCmpData] = useState<CompareSourcesResponse | null>(null);

  const loadInspect = () => {
    setInspectLoading(true);
    setInspectErr('');
    api
      .inspectService(namespace, serviceName, focusPort)
      .then((res) => {
        setInspectData(res);
        if (!cmpPort && res.ports?.length > 0) {
          setCmpPort(res.focusPort || res.ports[0].port);
        }
      })
      .catch((e) => setInspectErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setInspectLoading(false));
  };

  useEffect(() => {
    setMode(initialMode);
    loadInspect();
    api.listPods().then((list) => {
      const running = list.filter((p) => p.phase === 'Running' && p.ip && !p.hostNetwork);
      setPods(running);
      // Pick a realistic non-probe business pod by default
      const def =
        running.find((p) => p.namespace === namespace && !p.name.includes('probe')) ||
        running.find((p) => p.namespace !== 'calico-netdiag' && p.namespace !== 'kube-system' && !p.name.includes('probe')) ||
        running.find((p) => !p.name.includes('probe')) ||
        running[0];
      if (def) {
        setBizNs(def.namespace);
        setBizPod(def.name);
      }
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [namespace, serviceName, focusPort, initialMode]);

  const namespaces = Array.from(new Set(pods.map((p) => p.namespace))).sort();
  const podsInBizNs = pods.filter((p) => p.namespace === bizNs);

  const runCompare = () => {
    if (!bizNs || !bizPod) return;
    setCmpLoading(true);
    setCmpErr('');
    api
      .compareSources({
        srcNamespace: bizNs,
        srcPod: bizPod,
        targetNamespace: namespace,
        targetService: serviceName,
        targetPort: cmpPort || focusPort || inspectData?.ports?.[0]?.port || 0,
        protocol: 'TCP',
      })
      .then(setCmpData)
      .catch((e) => setCmpErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setCmpLoading(false));
  };

  const titleMap: Record<ServiceDiagMode, string> = {
    empty: '后端为空归因（Selector 与 EndpointSlice）',
    listening: '目标端口与容器真实监听状态核对',
    compare: '双来源路径与 Calico/K8s 策略对比',
  };

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-[#14110F]/45 backdrop-blur-[1px]" onClick={onClose}>
      <div
        className="w-full max-w-3xl bg-white dark:bg-[#1C1815] h-full shadow-2xl border-l border-[#E9E1D6] dark:border-[#2E2721] flex flex-col overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="px-5 py-4 border-b border-[#E9E1D6] dark:border-[#2E2721] flex items-start justify-between gap-3 bg-gradient-to-r from-[#FFF9F1] to-[#F4F9FD] dark:from-[#25201C] dark:to-[#1C1815]">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <span className="px-2 py-0.5 text-[11px] font-mono font-semibold rounded bg-[#EDF5FC] text-[#155A96] border border-[#C4DCF0] dark:bg-[#155A96]/25 dark:text-[#7EB8E8] dark:border-[#4B84B8]/40">
                Service
              </span>
              <span className="font-mono text-sm font-bold text-[#201B18] dark:text-[#F5F0EA] truncate">
                {namespace}/{serviceName}
                {focusPort ? `:${focusPort}` : ''}
              </span>
            </div>
            <p className="text-xs text-[#574D44] dark:text-[#A39587] mt-1">{titleMap[mode]}</p>
          </div>
          <div className="flex items-center gap-1.5 shrink-0">
            <button
              onClick={loadInspect}
              className="p-1.5 rounded-lg text-[#8C7F73] hover:bg-white/80 dark:hover:bg-[#25201C]"
              title="重新实时核对"
            >
              <RefreshCw className={`w-4 h-4 ${inspectLoading ? 'animate-spin' : ''}`} />
            </button>
            <button
              onClick={onClose}
              className="p-1.5 rounded-lg text-[#8C7F73] hover:bg-white/80 dark:hover:bg-[#25201C]"
            >
              <X className="w-4 h-4" />
            </button>
          </div>
        </div>

        {/* Mode Switcher */}
        <div className="px-5 py-2.5 border-b border-[#E9E1D6] dark:border-[#2E2721] bg-[#FAF6F1] dark:bg-[#14110F] flex flex-wrap items-center gap-2">
          {(
            [
              { id: 'listening', label: '核对目标端口与监听状态', icon: Radio },
              { id: 'empty', label: '查看后端与 Selector 匹配', icon: Layers },
              { id: 'compare', label: '对比双来源路径与策略', icon: GitCompare },
            ] as const
          ).map((t) => {
            const Icon = t.icon;
            return (
              <button
                key={t.id}
                onClick={() => setMode(t.id)}
                className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-semibold transition ${
                  mode === t.id
                    ? 'bg-[#F08C18] text-white shadow-xs'
                    : 'bg-white dark:bg-[#1C1815] text-[#574D44] dark:text-[#D5C9BC] border border-[#E9E1D6] dark:border-[#2E2721] hover:border-[#F08C18]'
                }`}
              >
                <Icon className="w-3.5 h-3.5" />
                {t.label}
              </button>
            );
          })}
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto p-5 space-y-4">
          {inspectErr && (
            <div className="p-3 rounded-xl bg-rose-50 dark:bg-rose-950/30 border border-rose-200 dark:border-rose-900 text-xs text-rose-700 dark:text-rose-300">
              核对失败：{inspectErr}
            </div>
          )}

          {/* MODE 1 & 2: EMPTY BACKENDS OR LISTENING PORT VERIFICATION */}
          {(mode === 'empty' || mode === 'listening') && (
            <>
              {inspectLoading && !inspectData ? (
                <div className="py-12 text-center text-xs text-[#8C7F73]">
                  正在读取 Service Selector、EndpointSlice 及目标 Pod 内部真实监听端口 (/proc/net/tcp)...
                </div>
              ) : inspectData ? (
                <div className="space-y-4">
                  {/* Verdict Card */}
                  <div className="p-4 rounded-xl bg-[#FFF4E4]/70 dark:bg-[#F08C18]/10 border border-[#FAD4A1] dark:border-[#F08C18]/40 space-y-2">
                    <div className="flex items-center justify-between gap-2">
                      <span className="text-xs font-bold text-[#9E4A05] dark:text-[#FBB040]">诊断结论</span>
                      <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-emerald-100 text-emerald-800 dark:bg-emerald-950/60 dark:text-emerald-300">
                        {inspectData.evidenceSource || '实际观测到'}
                      </span>
                    </div>
                    <p className="text-xs font-medium text-[#201B18] dark:text-[#F5F0EA] leading-relaxed">
                      {inspectData.conclusion}
                    </p>
                    <p className="text-[11px] text-[#574D44] dark:text-[#D5C9BC]">
                      <b>建议下一步：</b>{inspectData.nextStepHint}
                    </p>
                  </div>

                  {/* Service Selector & Port Mapping */}
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-3 text-xs">
                    <div className="p-3 rounded-xl bg-[#FAF6F1] dark:bg-[#14110F] border border-[#E9E1D6] dark:border-[#2E2721]">
                      <div className="text-[11px] font-semibold text-[#8C7F73] mb-1.5">Service Selector 标签条件</div>
                      {inspectData.selector && Object.keys(inspectData.selector).length > 0 ? (
                        <div className="flex flex-wrap gap-1.5 font-mono text-[11px]">
                          {Object.entries(inspectData.selector).map(([k, v]) => (
                            <span
                              key={k}
                              className="px-2 py-0.5 rounded bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721] text-[#201B18] dark:text-[#F5F0EA]"
                            >
                              {k}={v}
                            </span>
                          ))}
                        </div>
                      ) : (
                        <span className="text-[#8C7F73]">无 Selector（手工维护 Endpoint 或 ExternalName）</span>
                      )}
                    </div>
                    <div className="p-3 rounded-xl bg-[#FAF6F1] dark:bg-[#14110F] border border-[#E9E1D6] dark:border-[#2E2721]">
                      <div className="text-[11px] font-semibold text-[#8C7F73] mb-1.5">Service 端口映射 (Port → TargetPort)</div>
                      <div className="flex flex-wrap gap-1.5 font-mono text-[11px]">
                        {(inspectData.ports || []).map((pm, idx) => (
                          <span
                            key={idx}
                            className={`px-2 py-0.5 rounded border ${
                              focusPort && pm.port === focusPort
                                ? 'bg-[#FFF4E4] border-[#F08C18] text-[#9E4A05] font-semibold dark:bg-[#F08C18]/20 dark:text-[#FBB040]'
                                : 'bg-white dark:bg-[#1C1815] border-[#E9E1D6] dark:border-[#2E2721] text-[#574D44] dark:text-[#D5C9BC]'
                            }`}
                          >
                            {pm.name ? `${pm.name}: ` : ''}
                            {pm.port} → {pm.targetPort}/{pm.protocol}
                          </span>
                        ))}
                      </div>
                    </div>
                  </div>

                  {/* Matching Pods & Live Listening Sockets */}
                  <div className="space-y-2">
                    <div className="text-xs font-bold text-[#201B18] dark:text-[#F5F0EA] flex items-center justify-between">
                      <span>Selector 匹配的 Pod ({(inspectData.matchingPods || []).length}) 与容器真实监听状态</span>
                      <span className="text-[10px] font-normal text-[#8C7F73]">直接读取 Pod Netns /proc/net/tcp（无需容器装 ss/netstat）</span>
                    </div>

                    {(inspectData.matchingPods || []).length === 0 ? (
                      <div className="p-4 rounded-xl border border-rose-200 dark:border-rose-900/60 bg-rose-50/50 dark:bg-rose-950/20 text-xs text-rose-700 dark:text-rose-300">
                        命名空间 <code className="font-mono">{namespace}</code> 内未找到任何匹配该 Selector 的 Pod。
                      </div>
                    ) : (
                      <div className="space-y-2.5">
                        {(inspectData.matchingPods || []).map((mp) => (
                          <div
                            key={mp.name}
                            className="p-3.5 rounded-xl border border-[#E9E1D6] dark:border-[#2E2721] bg-white dark:bg-[#1C1815] space-y-2 text-xs"
                          >
                            <div className="flex flex-wrap items-center justify-between gap-2">
                              <div className="flex items-center gap-2 font-mono">
                                <Server className="w-3.5 h-3.5 text-[#155A96] dark:text-[#7EB8E8]" />
                                <span className="font-semibold text-[#201B18] dark:text-[#F5F0EA]">{mp.name}</span>
                                <span className="text-[#8C7F73]">{mp.ip || '无 IP'}</span>
                                <span className="text-[11px] text-[#8C7F73]">@ {mp.nodeName}</span>
                              </div>
                              <div className="flex items-center gap-1.5">
                                <StatusPill status={mp.ready ? 'Ready' : 'NotReady'} compact />
                                {mp.targetPortMatch === 'listening_all' && (
                                  <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-emerald-50 text-emerald-700 border border-emerald-200 dark:bg-emerald-950/50 dark:text-emerald-300">
                                    ✓ 目标端口已监听
                                  </span>
                                )}
                                {mp.targetPortMatch === 'listening_loopback_only' && (
                                  <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-amber-50 text-amber-800 border border-amber-300 dark:bg-amber-950/50 dark:text-amber-300">
                                    ⚠ 仅监听 127.0.0.1
                                  </span>
                                )}
                                {mp.targetPortMatch === 'not_listening' && (
                                  <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-rose-50 text-rose-700 border border-rose-200 dark:bg-rose-950/50 dark:text-rose-300">
                                    ✕ 未监听目标端口
                                  </span>
                                )}
                              </div>
                            </div>

                            {mp.notReadyReason && (
                              <div className="text-[11px] text-rose-600 dark:text-rose-400">
                                未就绪原因：{mp.notReadyReason}（重启 {mp.restartCount} 次）
                              </div>
                            )}

                            <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 text-[11px]">
                              <div className="p-2 rounded-lg bg-[#FAF6F1] dark:bg-[#14110F]">
                                <div className="text-[10px] text-[#8C7F73] mb-1">YAML 声明 containerPort</div>
                                <div className="font-mono text-[#201B18] dark:text-[#F5F0EA]">
                                  {mp.containerPorts && mp.containerPorts.length > 0 ? mp.containerPorts.join(', ') : '未在 Spec 中显式声明'}
                                </div>
                              </div>
                              <div className="p-2 rounded-lg bg-[#FAF6F1] dark:bg-[#14110F]">
                                <div className="text-[10px] text-[#8C7F73] mb-1">容器内实测 LISTEN Socket（实际观测到）</div>
                                {mp.socketInspectErr ? (
                                  <div className="text-amber-700 dark:text-amber-400 font-mono">{mp.socketInspectErr}</div>
                                ) : mp.listeningSockets && mp.listeningSockets.length > 0 ? (
                                  <div className="flex flex-wrap gap-1 font-mono">
                                    {mp.listeningSockets.map((sk, si) => (
                                      <span
                                        key={si}
                                        className={`px-1.5 py-0.5 rounded border text-[10px] ${
                                          sk.loopback
                                            ? 'bg-amber-50 text-amber-800 border-amber-200 dark:bg-amber-950/50 dark:text-amber-300'
                                            : 'bg-[#EDF5FC] text-[#155A96] border-[#C4DCF0] dark:bg-[#155A96]/25 dark:text-[#7EB8E8]'
                                        }`}
                                      >
                                        {sk.protocol} {sk.localAddr}:{sk.port}
                                      </span>
                                    ))}
                                  </div>
                                ) : (
                                  <span className="text-rose-600 dark:text-rose-400 font-medium">未观测到任何监听端口</span>
                                )}
                              </div>
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>

                  {/* Raw EndpointSlices */}
                  <div className="space-y-2">
                    <div className="text-xs font-bold text-[#201B18] dark:text-[#F5F0EA]">
                      EndpointSlice 原始记录 ({(inspectData.endpointSlices || []).length})
                    </div>
                    {(inspectData.endpointSlices || []).length === 0 ? (
                      <div className="p-3 rounded-xl bg-[#FAF6F1] dark:bg-[#14110F] border border-[#E9E1D6] dark:border-[#2E2721] text-xs text-[#8C7F73]">
                        未找到对应的 EndpointSlice 对象。
                      </div>
                    ) : (
                      <div className="space-y-2">
                        {(inspectData.endpointSlices || []).map((es) => (
                          <div
                            key={es.name}
                            className="p-3 rounded-xl bg-[#FAF6F1] dark:bg-[#14110F] border border-[#E9E1D6] dark:border-[#2E2721] text-[11px] font-mono space-y-1.5"
                          >
                            <div className="flex items-center justify-between">
                              <span className="font-semibold text-[#201B18] dark:text-[#F5F0EA]">{es.name}</span>
                              <span className="text-[#8C7F73]">Ports: {es.ports?.join(', ') || '—'}</span>
                            </div>
                            {(es.endpoints || []).length === 0 ? (
                              <div className="text-rose-600 dark:text-rose-400 font-sans">endpoints 列表为空（0 个后端地址）</div>
                            ) : (
                              <div className="space-y-1">
                                {(es.endpoints || []).map((ep, ei) => (
                                  <div key={ei} className="flex flex-wrap items-center justify-between gap-2 bg-white dark:bg-[#1C1815] px-2.5 py-1 rounded border border-[#E9E1D6] dark:border-[#2E2721]">
                                    <span>
                                      {(ep.addresses || []).join(', ')} {ep.targetPod ? `(${ep.targetPod})` : ''}
                                    </span>
                                    <span className="text-[10px] text-[#8C7F73]">
                                      ready={String(ep.ready ?? true)} · serving={String(ep.serving ?? true)} · terminating={String(ep.terminating ?? false)}
                                    </span>
                                  </div>
                                ))}
                              </div>
                            )}
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                </div>
              ) : null}
            </>
          )}

          {/* MODE 3: DUAL-SOURCE PATH & CALICO POLICY COMPARISON */}
          {mode === 'compare' && (
            <div className="space-y-4">
              <div className="p-3.5 rounded-xl bg-[#FAF6F1] dark:bg-[#14110F] border border-[#E9E1D6] dark:border-[#2E2721] space-y-3">
                <div className="text-xs font-semibold text-[#201B18] dark:text-[#F5F0EA]">
                  选择要与「巡检探针 Pod」对比的业务源 Pod
                </div>
                <div className="grid grid-cols-1 sm:grid-cols-12 gap-2 items-end">
                  <div className="sm:col-span-3">
                    <label className="block text-[10px] text-[#8C7F73] mb-1">业务 Namespace</label>
                    <select
                      value={bizNs}
                      onChange={(e) => {
                        const n = e.target.value;
                        setBizNs(n);
                        const first = pods.find((p) => p.namespace === n);
                        setBizPod(first ? first.name : '');
                      }}
                      className="w-full px-2.5 py-1.5 text-xs rounded-lg bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721]"
                    >
                      {namespaces.map((n) => (
                        <option key={n} value={n}>{n}</option>
                      ))}
                    </select>
                  </div>
                  <div className="sm:col-span-5">
                    <label className="block text-[10px] text-[#8C7F73] mb-1">业务源 Pod</label>
                    <select
                      value={bizPod}
                      onChange={(e) => setBizPod(e.target.value)}
                      className="w-full px-2.5 py-1.5 text-xs font-mono rounded-lg bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721]"
                    >
                      {podsInBizNs.map((p) => (
                        <option key={p.name} value={p.name}>
                          {p.name} ({p.ip})
                        </option>
                      ))}
                    </select>
                  </div>
                  <div className="sm:col-span-2">
                    <label className="block text-[10px] text-[#8C7F73] mb-1">目标端口</label>
                    <input
                      type="number"
                      value={cmpPort}
                      onChange={(e) => setCmpPort(Number(e.target.value))}
                      className="w-full px-2 py-1.5 text-xs font-mono rounded-lg bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721]"
                    />
                  </div>
                  <div className="sm:col-span-2">
                    <button
                      onClick={runCompare}
                      disabled={cmpLoading || !bizPod}
                      className="w-full inline-flex items-center justify-center gap-1 px-3 py-1.5 rounded-lg bg-[#F08C18] hover:bg-[#D9720B] disabled:opacity-50 text-white text-xs font-semibold"
                    >
                      {cmpLoading ? <RefreshCw className="w-3.5 h-3.5 animate-spin" /> : <GitCompare className="w-3.5 h-3.5" />}
                      <span>对比</span>
                    </button>
                  </div>
                </div>
                {cmpErr && <div className="text-xs text-rose-600">{cmpErr}</div>}
              </div>

              {cmpData && (
                <div className="space-y-4">
                  {/* Overall Comparison Conclusion */}
                  <div className="p-4 rounded-xl bg-[#FFF4E4]/75 dark:bg-[#F08C18]/10 border border-[#FAD4A1] dark:border-[#F08C18]/40 space-y-1.5">
                    <div className="text-xs font-bold text-[#9E4A05] dark:text-[#FBB040]">对比诊断结论</div>
                    <p className="text-xs text-[#201B18] dark:text-[#F5F0EA] leading-relaxed">{cmpData.conclusion}</p>
                    <p className="text-[11px] text-[#574D44] dark:text-[#D5C9BC]">
                      <b>建议下一步：</b>{cmpData.recommendedNext}
                    </p>
                    <div className="pt-1.5 flex flex-wrap items-center gap-2">
                      <button
                        onClick={() => {
                          onClose();
                          onLaunchTrace({
                            srcNamespace: cmpData.businessOutcome.podNamespace,
                            srcPod: cmpData.businessOutcome.podName,
                            targetType: 'service',
                            dstNamespace: namespace,
                            dstName: serviceName,
                            protocol: 'TCP',
                            dstPort: cmpData.targetPort,
                          });
                        }}
                        className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg bg-[#F08C18] hover:bg-[#D9720B] text-white text-xs font-semibold"
                      >
                        <Route className="w-3.5 h-3.5" /> 对业务 Pod 发起 TraceFlow 实测验证
                      </button>
                    </div>
                  </div>

                  {/* Side-by-Side Observed Outcomes */}
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                    {[
                      { title: '来源 A：巡检探针 Pod', out: cmpData.probeOutcome },
                      { title: '来源 B：指定业务 Pod', out: cmpData.businessOutcome },
                    ].map(({ title, out }) => (
                      <div
                        key={title}
                        className="p-3.5 rounded-xl bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721] space-y-2.5 text-xs"
                      >
                        <div className="flex items-center justify-between">
                          <span className="font-bold text-[#201B18] dark:text-[#F5F0EA]">{title}</span>
                          <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-emerald-100 text-emerald-800 dark:bg-emerald-950/60 dark:text-emerald-300">
                            {out.evidenceKind || '实际观测到'}
                          </span>
                        </div>
                        <div className="font-mono text-[11px] text-[#574D44] dark:text-[#D5C9BC]">
                          {out.podNamespace}/{out.podName} ({out.podIP} @ {out.nodeName})
                        </div>

                        <div className="grid grid-cols-2 gap-2 text-[11px] font-mono">
                          <div className="p-2 rounded-lg bg-[#FAF6F1] dark:bg-[#14110F]">
                            <div className="text-[10px] font-sans text-[#8C7F73]">访问 ClusterIP:{cmpData.targetPort}</div>
                            <div className={`mt-0.5 font-semibold flex items-center gap-1 ${out.clusterIPOk ? 'text-emerald-600' : 'text-rose-600'}`}>
                              {out.clusterIPOk ? <CheckCircle2 className="w-3.5 h-3.5" /> : <XCircle className="w-3.5 h-3.5" />}
                              <span>{out.clusterIPOk ? `可达 (${out.clusterIPLatencyMs}ms)` : out.clusterIPFailure || '失败'}</span>
                            </div>
                          </div>
                          <div className="p-2 rounded-lg bg-[#FAF6F1] dark:bg-[#14110F]">
                            <div className="text-[10px] font-sans text-[#8C7F73]">直连后端:{cmpData.backendPort}</div>
                            <div className={`mt-0.5 font-semibold flex items-center gap-1 ${out.directPodOk ? 'text-emerald-600' : 'text-rose-600'}`}>
                              {out.directPodOk ? <CheckCircle2 className="w-3.5 h-3.5" /> : <XCircle className="w-3.5 h-3.5" />}
                              <span>{out.directPodOk ? `可达 (${out.directPodLatencyMs}ms)` : out.directPodFailure || '失败'}</span>
                            </div>
                          </div>
                        </div>

                        {/* Egress Policies */}
                        <PolicyListBlock
                          title="源 Pod 出站策略 (Egress)"
                          policies={out.egressPolicies || []}
                        />
                      </div>
                    ))}
                  </div>

                  {/* Target Backend Ingress Policies */}
                  <div className="p-3.5 rounded-xl bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721] space-y-2 text-xs">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <span className="font-bold text-[#201B18] dark:text-[#F5F0EA]">
                        目标后端 Pod 入站策略 (Ingress) · {cmpData.backendPod || cmpData.backendIP || '—'}
                      </span>
                      <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-[#EDF5FC] text-[#155A96] border border-[#C4DCF0] dark:bg-[#155A96]/25 dark:text-[#7EB8E8]">
                        按配置推算
                      </span>
                    </div>
                    <PolicyListBlock
                      title="命中该后端的 K8s / Calico 入站规则"
                      policies={cmpData.targetIngressPolicies || []}
                    />
                  </div>
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function PolicyListBlock({ title, policies }: { title: string; policies: PolicyAttributionItem[] }) {
  return (
    <div className="space-y-1.5 pt-1 border-t border-[#F2ECE4] dark:border-[#2E2721]">
      <div className="flex items-center justify-between text-[11px]">
        <span className="font-semibold text-[#574D44] dark:text-[#D5C9BC] flex items-center gap-1">
          <Shield className="w-3 h-3 text-[#F08C18]" /> {title}
        </span>
        <span className="text-[10px] px-1.5 py-0.5 rounded bg-[#EDF5FC] text-[#155A96] dark:bg-[#155A96]/25 dark:text-[#7EB8E8]">
          按配置推算
        </span>
      </div>
      {policies.length === 0 ? (
        <div className="text-[11px] text-emerald-700 dark:text-emerald-400">未命中限制性策略（默认放行）</div>
      ) : (
        <div className="space-y-1.5">
          {policies.map((pol, idx) => (
            <div
              key={`${pol.name}-${idx}`}
              className={`p-2 rounded-lg border text-[11px] space-y-1 ${
                pol.effectHint === 'default_deny'
                  ? 'bg-rose-50/70 border-rose-200 dark:bg-rose-950/20 dark:border-rose-900'
                  : 'bg-[#FFF4E4]/60 border-[#FAD4A1] dark:bg-[#F08C18]/10 dark:border-[#F08C18]/40'
              }`}
            >
              <div className="flex flex-wrap items-center justify-between gap-1">
                <span className="font-mono font-semibold text-[#201B18] dark:text-[#F5F0EA]">
                  {pol.namespace ? `${pol.namespace}/` : ''}{pol.name}
                </span>
                <span className="text-[10px] text-[#8C7F73]">
                  {pol.source}{pol.tier ? ` · Tier:${pol.tier}` : ''}
                </span>
              </div>
              <div className="font-mono text-[10px] text-[#574D44] dark:text-[#D5C9BC]">
                Selector: {pol.selector}
              </div>
              <ul className="list-disc pl-4 space-y-0.5 font-mono text-[10px] text-[#574D44] dark:text-[#D5C9BC]">
                {(pol.ruleSummaries || []).slice(0, 4).map((rs, ri) => (
                  <li key={ri} className="break-all">{rs}</li>
                ))}
              </ul>
              {pol.effectHint === 'default_deny' && (
                <div className="inline-flex items-center gap-1 text-[10px] font-semibold text-rose-700 dark:text-rose-300">
                  <AlertTriangle className="w-3 h-3" /> 疑似默认拒绝规则，需结合 TraceFlow 确认
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
