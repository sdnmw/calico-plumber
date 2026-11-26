import React, { useEffect, useState } from 'react';
import {
  Activity,
  Cpu,
  Globe,
  Layers,
  Network,
  RefreshCw,
  Server,
  Shield,
  ArrowRight,
  GitBranch,
} from 'lucide-react';
import { api } from '../api/client';
import type { ClusterNetworkOverview } from '../api/types';
import { EncapBadge, EngineBadge, SectionCard, StatusPill } from '../components/common/Badges';
import { formatBps, formatLatency, formatRelTime } from '../utils/format';

export function OverviewPage({ onNavigate }: { onNavigate: (tab: string) => void }) {
  const [data, setData] = useState<ClusterNetworkOverview | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const load = () => {
    setLoading(true);
    api
      .getOverview()
      .then((d) => {
        setData(d);
        setError('');
      })
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    load();
    const timer = setInterval(load, 8000);
    return () => clearInterval(timer);
  }, []);

  if (loading && !data) {
    return <div className="p-8 text-sm text-slate-500">正在加载 Calico 集群网络全景...</div>;
  }
  if (error && !data) {
    return <div className="p-8 text-sm text-rose-600">加载失败：{error}</div>;
  }
  if (!data) return null;

  return (
    <div className="p-6 space-y-6 max-w-[1600px] mx-auto">
      {/* Header Row */}
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="text-xl font-bold tracking-tight text-slate-900 dark:text-white">
            Calico 集群总览
          </h1>
          <div className="flex flex-wrap items-center gap-2">
            <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-medium bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-[0_1px_2px_rgba(15,23,42,0.03)] text-slate-700 dark:text-slate-200">
              <Server className="w-3.5 h-3.5 text-blue-500" />
              <span className="text-slate-400">Kubernetes</span>
              <span className="font-mono font-semibold">{data.k8sVersion}</span>
            </span>
            <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-medium bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-[0_1px_2px_rgba(15,23,42,0.03)] text-slate-700 dark:text-slate-200">
              <Shield className="w-3.5 h-3.5 text-indigo-500" />
              <span className="text-slate-400">{data.calicoVariant}</span>
              <span className="font-mono font-semibold">{data.calicoVersion || '—'}</span>
            </span>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <EncapBadge mode={data.primaryEncapMode} />
          <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-200 border border-slate-200 dark:border-slate-700 font-mono">
            kube-proxy: {data.kubeProxyMode}
          </span>
          <button
            onClick={load}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 hover:bg-slate-50 dark:hover:bg-slate-800 transition"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
            刷新
          </button>
        </div>
      </div>

      {/* Primary KPI Strip */}
      <div className="grid grid-cols-2 lg:grid-cols-5 gap-4">
        <KpiCard
          icon={<Layers className="w-4 h-4 text-blue-600 dark:text-blue-400" />}
          label="Calico 网络模式"
          value={data.primaryEncapMode}
          sub={`Dataplane: ${data.linuxDataplane} · MTU ${data.mtu}`}
          onClick={() => onNavigate('ipam')}
        />
        <KpiCard
          icon={<Network className="w-4 h-4 text-violet-600 dark:text-violet-400" />}
          label="kube-proxy 数据面"
          value={data.kubeProxyMode.toUpperCase()}
          sub={`Services: ${data.serviceCount} · Policies: ${data.networkPolicyCount}`}
          onClick={() => onNavigate('health')}
        />
        <KpiCard
          icon={<Activity className="w-4 h-4 text-emerald-600 dark:text-emerald-400" />}
          label="实时活跃五元组"
          value={String(data.globalTraffic.activeFlows)}
          sub={`吞吐 ${formatBps(data.globalTraffic.totalBytesPerSec)}`}
          onClick={() => onNavigate('topology')}
        />
        <KpiCard
          icon={<Globe className="w-4 h-4 text-amber-600 dark:text-amber-400" />}
          label="平均连接延迟 (RTT)"
          value={formatLatency(data.globalTraffic.avgLatencyMs)}
          sub={`重传 ${data.globalTraffic.retransmitsPerMin} /min`}
          onClick={() => onNavigate('topology')}
        />
        <KpiCard
          icon={<Cpu className="w-4 h-4 text-teal-600 dark:text-teal-400" />}
          label="节点遥测引擎分布"
          value={`${data.globalTraffic.ebpfNodes} eBPF · ${data.globalTraffic.conntrackNodes} Legacy`}
          sub={`总节点 ${data.nodes.length} · 总 Pod ${data.podCount}`}
        />
      </div>

      {/* Node Dataplane & Adaptive Telemetry Matrix */}
      <SectionCard title="集群节点与自适应流量采集引擎">
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead className="bg-slate-50/80 dark:bg-slate-800/60 text-slate-500 dark:text-slate-400">
              <tr>
                <th className="text-left px-4 py-2.5 font-medium">节点 / 角色</th>
                <th className="text-left px-4 py-2.5 font-medium">IP / 内核版本</th>
                <th className="text-left px-4 py-2.5 font-medium">活跃采集引擎</th>
                <th className="text-left px-4 py-2.5 font-medium">kube-proxy</th>
                <th className="text-left px-4 py-2.5 font-medium">物理 / 隧道网卡</th>
                <th className="text-left px-4 py-2.5 font-medium">Pod / IPAM Block</th>
                <th className="text-left px-4 py-2.5 font-medium">活跃流 / 心跳</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
              {data.nodes.map((n) => (
                <tr key={n.name} className="hover:bg-slate-50/70 dark:hover:bg-slate-800/40">
                  <td className="px-4 py-3">
                    <div className="flex items-center gap-2">
                      <span className={`w-2 h-2 rounded-full ${n.ready && n.agentReady ? 'bg-emerald-500' : 'bg-amber-500'}`} />
                      <div>
                        <div className="font-semibold font-mono text-slate-800 dark:text-slate-100">{n.name}</div>
                        <div className="text-[11px] text-slate-400">{n.roles.join(', ')} · {n.architecture}</div>
                      </div>
                    </div>
                  </td>
                  <td className="px-4 py-3 font-mono">
                    <div className="text-slate-700 dark:text-slate-200">{n.internalIP}</div>
                    <div className="text-[11px] text-slate-400 truncate max-w-[210px]" title={n.kernelVersion}>
                      {n.kernelVersion}
                    </div>
                  </td>
                  <td className="px-4 py-3">
                    <div className="space-y-1">
                      <EngineBadge engine={n.telemetryEngine} reason={n.telemetryReason} />
                      <div className="text-[10px] text-slate-400 max-w-[230px] truncate" title={n.telemetryReason}>
                        BTF: {n.btfAvailable ? 'Yes' : 'No'} · {n.telemetryReason || '—'}
                      </div>
                    </div>
                  </td>
                  <td className="px-4 py-3 font-mono text-slate-700 dark:text-slate-300">
                    {n.kubeProxyMode}
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex flex-wrap gap-1">
                      {(n.tunnelInterfaces || []).map((tif) => (
                        <span
                          key={tif.name}
                          className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-mono border ${
                            tif.type === 'vxlan' || tif.type === 'ipip'
                              ? 'bg-violet-50 text-violet-700 border-violet-200 dark:bg-violet-950/40 dark:text-violet-300 dark:border-violet-800'
                              : 'bg-slate-100 text-slate-600 border-slate-200 dark:bg-slate-800 dark:text-slate-300 dark:border-slate-700'
                          }`}
                        >
                          {tif.name} ({tif.mtu})
                        </span>
                      ))}
                      {(!n.tunnelInterfaces || n.tunnelInterfaces.length === 0) && (
                        <span className="text-slate-400 font-mono">{n.primaryNIC || '—'}</span>
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-3 font-mono">
                    <div className="text-slate-800 dark:text-slate-200">{n.podCount} Pods</div>
                    <div className="text-[11px] text-slate-400">{n.blockCount} Blocks · {n.allocatedIPs} IPs</div>
                  </td>
                  <td className="px-4 py-3 font-mono">
                    <div className="text-slate-800 dark:text-slate-200">{n.activeFlowsCount} flows</div>
                    <div className="text-[11px] text-slate-400">{formatRelTime(n.lastHeartbeat)}</div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </SectionCard>

      {/* Bottom 2-column Grid: IPPools & BGP Mesh */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <SectionCard
          title="Calico IPPool 分配概览"
          action={
            <button
              onClick={() => onNavigate('ipam')}
              className="inline-flex items-center gap-1 text-xs font-medium text-blue-600 dark:text-blue-400 hover:underline"
            >
              进入 IPAM <ArrowRight className="w-3.5 h-3.5" />
            </button>
          }
        >
          <div className="p-4 space-y-3">
            {data.ipPools.map((pool) => (
              <div key={pool.name} className="p-3.5 rounded-xl bg-slate-50 dark:bg-slate-950/60 border border-slate-200/80 dark:border-slate-800">
                <div className="flex items-center justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <span className="font-semibold text-sm text-slate-800 dark:text-slate-100">{pool.name}</span>
                    <span className="font-mono text-xs text-slate-500">{pool.cidr} (/{pool.blockSize})</span>
                  </div>
                  <EncapBadge mode={pool.encapMode} />
                </div>
                <div className="mt-3">
                  <div className="flex justify-between text-[11px] text-slate-500 mb-1">
                    <span>已分配 {pool.allocatedIPs} / {pool.totalIPs} IPs ({pool.allocatedBlocks}/{pool.totalBlocks} Blocks)</span>
                    <span className="font-mono font-semibold text-slate-700 dark:text-slate-200">{pool.utilizationPercent}%</span>
                  </div>
                  <div className="h-2 rounded-full bg-slate-200 dark:bg-slate-800 overflow-hidden">
                    <div
                      className={`h-full rounded-full transition-all ${
                        pool.utilizationPercent > 85 ? 'bg-rose-500' : pool.utilizationPercent > 65 ? 'bg-amber-500' : 'bg-blue-600'
                      }`}
                      style={{ width: `${Math.max(pool.utilizationPercent, 1.5)}%` }}
                    />
                  </div>
                </div>
                <div className="mt-2.5 flex flex-wrap gap-3 text-[11px] text-slate-500">
                  <span>活跃 Pod: <b className="text-emerald-600 dark:text-emerald-400">{pool.activePodIPs}</b></span>
                  <span>隧道 IP: <b className="text-violet-600 dark:text-violet-400">{pool.tunnelIPs}</b></span>
                  <span>借用 IP: <b className="text-amber-600 dark:text-amber-400">{pool.borrowedIPs}</b></span>
                  <span>孤儿 IP: <b className="text-rose-600 dark:text-rose-400">{pool.orphanIPs}</b></span>
                  <span>NAT Outgoing: <b>{pool.natOutgoing ? 'Enabled' : 'Disabled'}</b></span>
                </div>
              </div>
            ))}
          </div>
        </SectionCard>

        <SectionCard
          title="BIRD BGP 路由邻居状态"
          action={
            <div className="flex flex-wrap items-center justify-end gap-1.5">
              <span
                className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md text-[11px] font-medium border ${
                  data.bgpEnabled
                    ? 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-300 dark:border-emerald-800'
                    : 'bg-slate-100 text-slate-500 border-slate-200 dark:bg-slate-800 dark:text-slate-400 dark:border-slate-700'
                }`}
              >
                <span className={`w-1.5 h-1.5 rounded-full ${data.bgpEnabled ? 'bg-emerald-500' : 'bg-slate-400'}`} />
                BGP {data.bgpEnabled ? 'Enabled' : 'Disabled'}
              </span>
              <span className="inline-flex items-center px-2 py-0.5 rounded-md text-[11px] font-mono font-semibold bg-blue-50 text-blue-700 border border-blue-200 dark:bg-blue-950/40 dark:text-blue-300 dark:border-blue-800">
                AS {data.bgpAsNumber || 64512}
              </span>
              <span
                className={`inline-flex items-center px-2 py-0.5 rounded-md text-[11px] font-medium border ${
                  data.nodeToNodeMesh
                    ? 'bg-indigo-50 text-indigo-700 border-indigo-200 dark:bg-indigo-950/40 dark:text-indigo-300 dark:border-indigo-800'
                    : 'bg-slate-100 text-slate-600 border-slate-200 dark:bg-slate-800 dark:text-slate-300 dark:border-slate-700'
                }`}
              >
                Node-to-Node Mesh: {data.nodeToNodeMesh ? 'On' : 'Off'}
              </span>
              <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-[11px] font-mono bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-300 border border-slate-200/80 dark:border-slate-700">
                <GitBranch className="w-3 h-3" /> {data.bgpPeers?.length || 0} 会话
              </span>
            </div>
          }
        >
          <div className="overflow-x-auto">
            {data.bgpPeers && data.bgpPeers.length > 0 ? (
              <table className="w-full text-xs">
                <thead className="bg-slate-50/80 dark:bg-slate-800/60 text-slate-500">
                  <tr>
                    <th className="text-left px-4 py-2 font-medium">源节点</th>
                    <th className="text-left px-4 py-2 font-medium">Peer / 对端节点</th>
                    <th className="text-left px-4 py-2 font-medium">类型</th>
                    <th className="text-left px-4 py-2 font-medium">状态</th>
                    <th className="text-left px-4 py-2 font-medium">建立时间</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                  {data.bgpPeers.map((peer, idx) => (
                    <tr key={`${peer.sourceNode}-${peer.name}-${idx}`}>
                      <td className="px-4 py-2.5 font-mono text-slate-700 dark:text-slate-200">{peer.sourceNode}</td>
                      <td className="px-4 py-2.5 font-mono">
                        <div className="text-slate-800 dark:text-slate-100">{peer.peerIP || peer.name}</div>
                        {peer.peerNode && <div className="text-[10px] text-slate-400">{peer.peerNode}</div>}
                      </td>
                      <td className="px-4 py-2.5 text-slate-500">{peer.scope}</td>
                      <td className="px-4 py-2.5"><StatusPill status={peer.state} /></td>
                      <td className="px-4 py-2.5 font-mono text-slate-500">{peer.since || '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : (
              <div className="p-8 text-center text-xs text-slate-400">
                当前未检测到活跃 BIRD BGP 会话（VXLAN 纯隧道模式或单节点集群下无需 BGP Mesh）
              </div>
            )}
          </div>
        </SectionCard>
      </div>
    </div>
  );
}

function KpiCard({
  icon,
  label,
  value,
  sub,
  onClick,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  sub: string;
  onClick?: () => void;
}) {
  return (
    <div
      onClick={onClick}
      className={`p-4 rounded-xl bg-white dark:bg-slate-900/90 border border-slate-200/90 dark:border-slate-800 shadow-[0_1px_2px_rgba(15,23,42,0.04)] ${
        onClick ? 'cursor-pointer hover:border-blue-400 dark:hover:border-blue-600 transition' : ''
      }`}
    >
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-slate-500 dark:text-slate-400">{label}</span>
        <div className="p-1.5 rounded-lg bg-slate-50 dark:bg-slate-800">{icon}</div>
      </div>
      <div className="mt-2 text-lg font-bold tracking-tight text-slate-900 dark:text-white truncate">{value}</div>
      <div className="mt-1 text-[11px] text-slate-500 dark:text-slate-400 truncate">{sub}</div>
    </div>
  );
}
