import React, { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { X, Route, Terminal, Activity, Globe, Shield, Network, Server, RefreshCw } from 'lucide-react';
import { api } from '../../api/client';
import type { PodNetworkDetail, TraceFlowRequest } from '../../api/types';
import { EncapBadge, StatusPill } from '../common/Badges';

interface PodInspectorDrawerProps {
  namespace: string;
  podName: string;
  onClose: () => void;
  onLaunchTrace: (req: Partial<TraceFlowRequest>) => void;
  onLaunchTroubleshoot: (ns: string, pod: string, tool: 'tcpdump' | 'tcp' | 'http') => void;
}

export function PodInspectorDrawer({
  namespace,
  podName,
  onClose,
  onLaunchTrace,
  onLaunchTroubleshoot,
}: PodInspectorDrawerProps) {
  const [detail, setDetail] = useState<PodNetworkDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const load = () => {
    setLoading(true);
    setError('');
    api
      .getPodDetail(namespace, podName)
      .then(setDetail)
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    load();
  }, [namespace, podName]);

  return createPortal(
    <div className="fixed inset-0 z-[100] m-0 flex justify-end bg-[#14110F]/45 backdrop-blur-[1px]" onClick={onClose}>
      <div
        className="w-full max-w-xl bg-white dark:bg-[#1C1815] h-full shadow-2xl border-l border-[#E9E1D6] dark:border-[#2E2721] flex flex-col overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="px-5 py-4 border-b border-[#E9E1D6] dark:border-[#2E2721] flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="px-2 py-0.5 text-[11px] font-medium rounded bg-[#FFF4E4] text-[#9E4A05] border border-[#FAD4A1] dark:bg-[#F08C18]/20 dark:text-[#FBB040] dark:border-[#F08C18]/40">
                {namespace}
              </span>
              {detail && <StatusPill status={detail.pod.ready ? 'Ready' : detail.pod.phase} />}
            </div>
            <h2 className="text-base font-semibold text-[#201B18] dark:text-[#F5F0EA] mt-1 truncate font-mono">
              {podName}
            </h2>
          </div>
          <div className="flex items-center gap-1">
            <button
              onClick={load}
              className="p-1.5 rounded-lg text-[#8C7F73] hover:bg-[#F7F3EE] dark:hover:bg-[#25201C]"
              title="刷新 Pod 数据面详情"
            >
              <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
            </button>
            <button
              onClick={onClose}
              className="p-1.5 rounded-lg text-[#8C7F73] hover:bg-[#F7F3EE] dark:hover:bg-[#25201C]"
            >
              <X className="w-4 h-4" />
            </button>
          </div>
        </div>

        {/* One-Click Diagnostic Action Bar */}
        <div className="px-5 py-3 bg-[#FAF6F1] dark:bg-[#14110F] border-b border-[#E9E1D6] dark:border-[#2E2721] grid grid-cols-4 gap-2">
          <button
            onClick={() => {
              onClose();
              onLaunchTrace({ srcNamespace: namespace, srcPod: podName });
            }}
            className="flex flex-col items-center gap-1 px-2 py-2 rounded-lg bg-[#F08C18] hover:bg-[#D9720B] text-white text-xs font-medium transition shadow-sm"
          >
            <Route className="w-4 h-4" />
            <span>TraceFlow</span>
          </button>
          <button
            onClick={() => {
              onClose();
              onLaunchTroubleshoot(namespace, podName, 'tcpdump');
            }}
            className="flex flex-col items-center gap-1 px-2 py-2 rounded-lg bg-white dark:bg-slate-800 hover:bg-slate-100 dark:hover:bg-slate-700 border border-slate-200 dark:border-slate-700 text-slate-700 dark:text-slate-200 text-xs font-medium transition"
          >
            <Terminal className="w-4 h-4 text-violet-600 dark:text-violet-400" />
            <span>TCPdump</span>
          </button>
          <button
            onClick={() => {
              onClose();
              onLaunchTroubleshoot(namespace, podName, 'tcp');
            }}
            className="flex flex-col items-center gap-1 px-2 py-2 rounded-lg bg-white dark:bg-slate-800 hover:bg-slate-100 dark:hover:bg-slate-700 border border-slate-200 dark:border-slate-700 text-slate-700 dark:text-slate-200 text-xs font-medium transition"
          >
            <Activity className="w-4 h-4 text-emerald-600 dark:text-emerald-400" />
            <span>TCP 握手</span>
          </button>
          <button
            onClick={() => {
              onClose();
              onLaunchTroubleshoot(namespace, podName, 'http');
            }}
            className="flex flex-col items-center gap-1 px-2 py-2 rounded-lg bg-white dark:bg-slate-800 hover:bg-slate-100 dark:hover:bg-slate-700 border border-slate-200 dark:border-slate-700 text-slate-700 dark:text-slate-200 text-xs font-medium transition"
          >
            <Globe className="w-4 h-4 text-amber-600 dark:text-amber-400" />
            <span>HTTP 探测</span>
          </button>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto p-5 space-y-5">
          {loading && !detail && (
            <div className="text-center py-10 text-sm text-slate-500">正在从节点 Agent 实时采集 Pod Netns 与路由信息...</div>
          )}
          {error && (
            <div className="p-3 rounded-lg bg-rose-50 dark:bg-rose-950/50 border border-rose-200 dark:border-rose-800 text-xs text-rose-700 dark:text-rose-300">
              {error}
            </div>
          )}

          {detail && (
            <>
              {/* Identity & IPAM Grid */}
              <div className="grid grid-cols-2 gap-3 text-xs">
                <InfoBox label="Pod IP" value={detail.pod.ip || '—'} mono />
                <InfoBox label="所属节点" value={detail.pod.nodeName || '—'} mono />
                <InfoBox
                  label="所属工作负载"
                  value={detail.pod.workloadKind ? `${detail.pod.workloadKind}/${detail.pod.workloadName}` : '—'}
                />
                <InfoBox
                  label="IPPool / IPAMBlock"
                  value={detail.pod.ipamBlock ? `${detail.pod.ipPool} · ${detail.pod.ipamBlock}` : detail.pod.hostNetwork ? 'HostNetwork' : '—'}
                  mono
                />
                <InfoBox label="宿主机 Calico veth" value={detail.hostVeth || (detail.pod.hostNetwork ? 'hostNetwork' : '—')} mono />
                <InfoBox
                  label="Pod 网卡 / MAC / MTU"
                  value={detail.podIface ? `${detail.podIface} · ${detail.podMAC || '—'} · MTU ${detail.podMTU}` : '—'}
                  mono
                />
              </div>

              <div className="flex items-center gap-2 flex-wrap">
                <EncapBadge mode={detail.encapMode} />
                {detail.pod.borrowed && (
                  <StatusPill status="borrowed" label="跨节点借用 IP (Borrowed)" />
                )}
              </div>

              {/* Host & Pod Routes */}
              <div className="space-y-2">
                <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-500 flex items-center gap-1.5">
                  <Network className="w-3.5 h-3.5" /> 宿主机 & Pod 内部路由
                </h4>
                {detail.hostRouteToPod && (
                  <div className="p-2.5 rounded-lg bg-slate-50 dark:bg-slate-950 border border-slate-200 dark:border-slate-800 font-mono text-[11px] text-slate-700 dark:text-slate-300">
                    <span className="text-slate-400 mr-2">[Host FIB]</span>
                    {detail.hostRouteToPod}
                  </div>
                )}
                {detail.podRoutes && detail.podRoutes.length > 0 ? (
                  <div className="rounded-lg border border-slate-200 dark:border-slate-800 overflow-hidden">
                    <table className="w-full text-[11px] font-mono">
                      <thead className="bg-slate-50 dark:bg-slate-800/70 text-slate-500">
                        <tr>
                          <th className="text-left px-2.5 py-1.5 font-medium">目标网段</th>
                          <th className="text-left px-2.5 py-1.5 font-medium">网关</th>
                          <th className="text-left px-2.5 py-1.5 font-medium">出接口</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                        {detail.podRoutes.map((r, i) => (
                          <tr key={i}>
                            <td className="px-2.5 py-1.5">{r.dst}</td>
                            <td className="px-2.5 py-1.5 text-slate-500">{r.gateway || 'direct'}</td>
                            <td className="px-2.5 py-1.5 text-[#9E4A05] dark:text-[#FBB040]">{r.dev}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  detail.dataplaneError && (
                    <p className="text-xs text-amber-600 dark:text-amber-400">Dataplane 提示：{detail.dataplaneError}</p>
                  )
                )}
              </div>

              {/* Associated Services */}
              <div className="space-y-2">
                <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-500 flex items-center gap-1.5">
                  <Server className="w-3.5 h-3.5" /> 关联 Service ({detail.services?.length || 0})
                </h4>
                {detail.services && detail.services.length > 0 ? (
                  <div className="space-y-1.5">
                    {detail.services.map((s) => (
                      <div
                        key={s.name}
                        className="p-2.5 rounded-lg border border-slate-200 dark:border-slate-800 flex items-center justify-between text-xs"
                      >
                        <div>
                          <span className="font-medium text-slate-800 dark:text-slate-200">{s.name}</span>
                          <span className="ml-2 font-mono text-slate-500">{s.clusterIP}</span>
                        </div>
                        <span className="font-mono text-[11px] text-slate-500">{s.ports.join(', ')}</span>
                      </div>
                    ))}
                  </div>
                ) : (
                  <p className="text-xs text-slate-400">暂无匹配此 Pod 的 Service</p>
                )}
              </div>

              {/* Matching NetworkPolicies */}
              <div className="space-y-2">
                <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-500 flex items-center gap-1.5">
                  <Shield className="w-3.5 h-3.5" /> 命中的 NetworkPolicy ({detail.networkPolicies?.length || 0})
                </h4>
                {detail.networkPolicies && detail.networkPolicies.length > 0 ? (
                  <div className="space-y-1.5">
                    {detail.networkPolicies.map((np, idx) => (
                      <div
                        key={idx}
                        className="p-2.5 rounded-lg border border-amber-200/80 dark:border-amber-900 bg-amber-50/40 dark:bg-amber-950/20 text-xs"
                      >
                        <div className="flex items-center justify-between">
                          <span className="font-semibold text-slate-800 dark:text-slate-100">{np.name}</span>
                          <span className="text-[10px] px-1.5 py-0.5 rounded bg-amber-100 dark:bg-amber-900/60 text-amber-800 dark:text-amber-200">
                            {np.source}
                          </span>
                        </div>
                        <p className="text-[11px] text-slate-500 dark:text-slate-400 mt-1">{np.summary}</p>
                      </div>
                    ))}
                  </div>
                ) : (
                  <p className="text-xs text-emerald-600 dark:text-emerald-400">默认放行（未命中任何限制性 K8s/Calico NetworkPolicy）</p>
                )}
              </div>
            </>
          )}
        </div>
      </div>
    </div>,
    document.body
  );
}

function InfoBox({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="p-2.5 rounded-lg bg-slate-50 dark:bg-slate-950/70 border border-slate-200/80 dark:border-slate-800">
      <div className="text-[11px] text-slate-400">{label}</div>
      <div className={`mt-0.5 text-slate-800 dark:text-slate-100 font-medium truncate ${mono ? 'font-mono text-[11px]' : ''}`} title={value}>
        {value}
      </div>
    </div>
  );
}
