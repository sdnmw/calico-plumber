import React, { useEffect, useState } from 'react';
import {
  Activity,
  Cpu,
  Globe,
  Layers,
  Network,
  RefreshCw,
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
        <div className="flex flex-wrap items-center gap-2.5">
          <h1 className="text-xl font-bold tracking-tight text-[#201B18] dark:text-[#F5F0EA]">
            Calico 集群总览
          </h1>
          <div className="flex flex-wrap items-center gap-2">
            <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-medium bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721] shadow-[0_1px_2px_rgba(32,27,24,0.03)] text-[#201B18] dark:text-[#F5F0EA]">
              <span className="w-2 h-2 rounded-full bg-[#155A96] dark:bg-[#7EB8E8]" />
              <span className="text-[#8C7F73] dark:text-[#A39587]">Kubernetes</span>
              <span className="font-mono font-semibold">{data.k8sVersion}</span>
            </span>
            <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-medium bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721] shadow-[0_1px_2px_rgba(32,27,24,0.03)] text-[#201B18] dark:text-[#F5F0EA]">
              <span className="w-2 h-2 rounded-full bg-[#F08C18]" />
              <span className="text-[#8C7F73] dark:text-[#A39587]">{data.calicoVariant}</span>
              <span className="font-mono font-semibold">{data.calicoVersion || '—'}</span>
            </span>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <EncapBadge mode={data.primaryEncapMode} />
          <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium bg-[#F6EFE8] dark:bg-[#6C4824]/25 text-[#6C4824] dark:text-[#DFC4A6] border border-[#DFCFC0] dark:border-[#6C4824]/50 font-mono">
            kube-proxy: {data.kubeProxyMode}
          </span>
          <button
            onClick={load}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721] text-[#574D44] dark:text-[#D5C9BC] hover:bg-[#FFF4E4] hover:text-[#9E4A05] hover:border-[#FAD4A1] dark:hover:bg-[#25201C] dark:hover:text-[#FBB040] transition"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
            刷新
          </button>
        </div>
      </div>

      {/* Primary KPI Strip */}
      <div className="grid grid-cols-2 lg:grid-cols-5 gap-4">
        <KpiCard
          icon={<Layers className="w-4 h-4 text-[#155A96] dark:text-[#7EB8E8]" />}
          iconBg="bg-[#EDF5FC] border-[#C4DCF0] dark:bg-[#155A96]/25 dark:border-[#4B84B8]/40"
          label="Calico 网络模式"
          value={data.primaryEncapMode}
          sub={`Dataplane: ${data.linuxDataplane} · MTU ${data.mtu}`}
          onClick={() => onNavigate('ipam')}
        />
        <KpiCard
          icon={<Network className="w-4 h-4 text-[#6C4824] dark:text-[#DFC4A6]" />}
          iconBg="bg-[#F6EFE8] border-[#DFCFC0] dark:bg-[#6C4824]/25 dark:border-[#6C4824]/50"
          label="kube-proxy 数据面"
          value={data.kubeProxyMode.toUpperCase()}
          sub={`Services: ${data.serviceCount} · Policies: ${data.networkPolicyCount}`}
          onClick={() => onNavigate('health')}
        />
        <KpiCard
          icon={<Activity className="w-4 h-4 text-[#047857] dark:text-emerald-400" />}
          iconBg="bg-emerald-50 border-emerald-200/80 dark:bg-emerald-950/50 dark:border-emerald-800/60"
          label="实时活跃五元组"
          value={String(data.globalTraffic.activeFlows)}
          sub={`吞吐 ${formatBps(data.globalTraffic.totalBytesPerSec)}`}
          onClick={() => onNavigate('topology')}
        />
        <KpiCard
          icon={<Globe className="w-4 h-4 text-[#D9720B] dark:text-[#FBB040]" />}
          iconBg="bg-[#FFF4E4] border-[#FAD4A1] dark:bg-[#F08C18]/20 dark:border-[#F08C18]/40"
          label="平均连接延迟 (RTT)"
          value={formatLatency(data.globalTraffic.avgLatencyMs)}
          sub={`重传 ${data.globalTraffic.retransmitsPerMin} /min`}
          onClick={() => onNavigate('topology')}
        />
        <KpiCard
          icon={<Cpu className="w-4 h-4 text-[#155A96] dark:text-[#7EB8E8]" />}
          iconBg="bg-[#EDF5FC] border-[#C4DCF0] dark:bg-[#155A96]/25 dark:border-[#4B84B8]/40"
          label="节点遥测引擎分布"
          value={`${data.globalTraffic.ebpfNodes} eBPF · ${data.globalTraffic.conntrackNodes} Legacy`}
          sub={`总节点 ${data.nodes.length} · 总 Pod ${data.podCount}`}
        />
      </div>

      {/* Node Dataplane & Adaptive Telemetry Matrix */}
      <SectionCard title="集群节点与自适应流量采集引擎">
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead className="bg-[#FAF6F1] dark:bg-[#25201C]/70 text-[#8C7F73] dark:text-[#A39587] border-b border-[#F2ECE4] dark:border-[#2E2721]">
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
            <tbody className="divide-y divide-[#F2ECE4] dark:divide-[#2E2721]">
              {data.nodes.map((n) => (
                <tr key={n.name} className="hover:bg-[#FDF8F2] dark:hover:bg-[#25201C]/50 transition-colors">
                  <td className="px-4 py-3">
                    <div className="flex items-center gap-2">
                      <span className={`w-2 h-2 rounded-full ${n.ready && n.agentReady ? 'bg-emerald-500' : 'bg-[#F08C18]'}`} />
                      <div>
                        <div className="font-semibold font-mono text-[#201B18] dark:text-[#F5F0EA]">{n.name}</div>
                        <div className="text-[11px] text-[#8C7F73] dark:text-[#A39587]">{n.roles.join(', ')} · {n.architecture}</div>
                      </div>
                    </div>
                  </td>
                  <td className="px-4 py-3 font-mono">
                    <div className="text-[#201B18] dark:text-[#F5F0EA]">{n.internalIP}</div>
                    <div className="text-[11px] text-[#8C7F73] dark:text-[#A39587] truncate max-w-[210px]" title={n.kernelVersion}>
                      {n.kernelVersion}
                    </div>
                  </td>
                  <td className="px-4 py-3">
                    <div className="space-y-1">
                      <EngineBadge engine={n.telemetryEngine} reason={n.telemetryReason} />
                      <div className="text-[10px] text-[#8C7F73] dark:text-[#A39587] max-w-[230px] truncate" title={n.telemetryReason}>
                        BTF: {n.btfAvailable ? 'Yes' : 'No'} · {n.telemetryReason || '—'}
                      </div>
                    </div>
                  </td>
                  <td className="px-4 py-3 font-mono text-[#574D44] dark:text-[#D5C9BC]">
                    {n.kubeProxyMode}
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex flex-wrap gap-1">
                      {(n.tunnelInterfaces || []).map((tif) => (
                        <span
                          key={tif.name}
                          className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-[10px] font-mono border ${
                            tif.type === 'vxlan' || tif.type === 'ipip'
                              ? 'bg-[#FFF4E4] text-[#9E4A05] border-[#FAD4A1] dark:bg-[#F08C18]/15 dark:text-[#FBB040] dark:border-[#F08C18]/40'
                              : 'bg-[#EDF5FC] text-[#155A96] border-[#C4DCF0] dark:bg-[#155A96]/25 dark:text-[#7EB8E8] dark:border-[#4B84B8]/40'
                          }`}
                        >
                          {tif.name} ({tif.mtu})
                        </span>
                      ))}
                      {(!n.tunnelInterfaces || n.tunnelInterfaces.length === 0) && (
                        <span className="text-[#8C7F73] font-mono">{n.primaryNIC || '—'}</span>
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-3 font-mono">
                    <div className="text-[#201B18] dark:text-[#F5F0EA]">{n.podCount} Pods</div>
                    <div className="text-[11px] text-[#8C7F73] dark:text-[#A39587]">{n.blockCount} Blocks · {n.allocatedIPs} IPs</div>
                  </td>
                  <td className="px-4 py-3 font-mono">
                    <div className="text-[#201B18] dark:text-[#F5F0EA]">{n.activeFlowsCount} flows</div>
                    <div className="text-[11px] text-[#8C7F73] dark:text-[#A39587]">{formatRelTime(n.lastHeartbeat)}</div>
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
              className="inline-flex items-center gap-1 text-xs font-semibold text-[#D9720B] dark:text-[#FBB040] hover:underline"
            >
              进入 IPAM <ArrowRight className="w-3.5 h-3.5" />
            </button>
          }
        >
          <div className="p-4 space-y-3">
            {data.ipPools.map((pool) => (
              <div
                key={pool.name}
                className="p-3.5 rounded-xl bg-gradient-to-b from-[#FCF9F4] to-[#F7F1E9] dark:from-[#25201C] dark:to-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721]"
              >
                <div className="flex items-center justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <span className="font-semibold text-sm text-[#201B18] dark:text-[#F5F0EA]">{pool.name}</span>
                    <span className="font-mono text-xs text-[#8C7F73] dark:text-[#A39587]">{pool.cidr} (/{pool.blockSize})</span>
                  </div>
                  <EncapBadge mode={pool.encapMode} />
                </div>
                <div className="mt-3">
                  <div className="flex justify-between text-[11px] text-[#574D44] dark:text-[#A39587] mb-1.5">
                    <span>已分配 {pool.allocatedIPs} / {pool.totalIPs} IPs ({pool.allocatedBlocks}/{pool.totalBlocks} Blocks)</span>
                    <span className="font-mono font-bold text-[#9E4A05] dark:text-[#FBB040]">{pool.utilizationPercent}%</span>
                  </div>
                  <div className="h-2 rounded-full bg-[#ECE3D7] dark:bg-[#2E2721] overflow-hidden">
                    <div
                      className={`h-full rounded-full transition-all ${
                        pool.utilizationPercent > 85
                          ? 'bg-rose-500'
                          : pool.utilizationPercent > 65
                            ? 'bg-amber-500'
                            : 'bg-gradient-to-r from-[#F08C18] to-[#155A96]'
                      }`}
                      style={{ width: `${Math.max(pool.utilizationPercent, 2)}%` }}
                    />
                  </div>
                </div>
                <div className="mt-2.5 flex flex-wrap gap-3 text-[11px] text-[#574D44] dark:text-[#A39587]">
                  <span>活跃 Pod: <b className="text-emerald-600 dark:text-emerald-400">{pool.activePodIPs}</b></span>
                  <span>隧道 IP: <b className="text-[#155A96] dark:text-[#7EB8E8]">{pool.tunnelIPs}</b></span>
                  <span>借用 IP: <b className="text-[#D9720B] dark:text-[#FBB040]">{pool.borrowedIPs}</b></span>
                  <span>孤儿 IP: <b className="text-rose-600 dark:text-rose-400">{pool.orphanIPs}</b></span>
                  <span>NAT Outgoing: <b className="text-[#201B18] dark:text-[#F5F0EA]">{pool.natOutgoing ? 'Enabled' : 'Disabled'}</b></span>
                </div>
              </div>
            ))}
          </div>
        </SectionCard>

        <SectionCard
          title="BIRD BGP 邻居"
          action={
            <div className="flex flex-wrap items-center justify-end gap-1.5">
              <span
                className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md text-[11px] font-medium border ${
                  data.bgpEnabled
                    ? 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-300 dark:border-emerald-800'
                    : 'bg-[#F7F3EE] text-[#8C7F73] border-[#E9E1D6] dark:bg-[#25201C] dark:text-[#A39587] dark:border-[#2E2721]'
                }`}
              >
                <span className={`w-1.5 h-1.5 rounded-full ${data.bgpEnabled ? 'bg-emerald-500' : 'bg-slate-400'}`} />
                BGP {data.bgpEnabled ? 'Enabled' : 'Disabled'}
              </span>
              <span className="inline-flex items-center px-2 py-0.5 rounded-md text-[11px] font-mono font-semibold bg-[#EDF5FC] text-[#155A96] border border-[#C4DCF0] dark:bg-[#155A96]/25 dark:text-[#7EB8E8] dark:border-[#4B84B8]/40">
                AS {data.bgpAsNumber || 64512}
              </span>
              <span
                className={`inline-flex items-center px-2 py-0.5 rounded-md text-[11px] font-medium border ${
                  data.nodeToNodeMesh
                    ? 'bg-[#FFF4E4] text-[#9E4A05] border-[#FAD4A1] dark:bg-[#F08C18]/15 dark:text-[#FBB040] dark:border-[#F08C18]/40'
                    : 'bg-[#F7F3EE] text-[#574D44] border-[#E9E1D6] dark:bg-[#25201C] dark:text-[#A39587] dark:border-[#2E2721]'
                }`}
              >
                Mesh: {data.nodeToNodeMesh ? 'On' : 'Off'}
              </span>
              <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-[11px] font-mono bg-[#F6EFE8] dark:bg-[#6C4824]/25 text-[#6C4824] dark:text-[#DFC4A6] border border-[#DFCFC0] dark:border-[#6C4824]/50">
                <GitBranch className="w-3 h-3" /> {data.bgpPeers?.length || 0} 会话
              </span>
            </div>
          }
        >
          <div className="overflow-x-auto">
            {data.bgpPeers && data.bgpPeers.length > 0 ? (
              <table className="w-full text-xs">
                <thead className="bg-[#FAF6F1] dark:bg-[#25201C]/70 text-[#8C7F73] dark:text-[#A39587] border-b border-[#F2ECE4] dark:border-[#2E2721]">
                  <tr>
                    <th className="text-left px-4 py-2 font-medium">源节点</th>
                    <th className="text-left px-4 py-2 font-medium">Peer / 对端节点</th>
                    <th className="text-left px-4 py-2 font-medium">类型</th>
                    <th className="text-left px-4 py-2 font-medium">状态</th>
                    <th className="text-left px-4 py-2 font-medium">建立时间</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[#F2ECE4] dark:divide-[#2E2721]">
                  {data.bgpPeers.map((peer, idx) => (
                    <tr key={`${peer.sourceNode}-${peer.name}-${idx}`} className="hover:bg-[#FDF8F2] dark:hover:bg-[#25201C]/40">
                      <td className="px-4 py-2.5 font-mono text-[#201B18] dark:text-[#F5F0EA]">{peer.sourceNode}</td>
                      <td className="px-4 py-2.5 font-mono">
                        <div className="text-[#201B18] dark:text-[#F5F0EA]">{peer.peerIP || peer.name}</div>
                        {peer.peerNode && <div className="text-[10px] text-[#8C7F73] dark:text-[#A39587]">{peer.peerNode}</div>}
                      </td>
                      <td className="px-4 py-2.5 text-[#574D44] dark:text-[#A39587]">{peer.scope}</td>
                      <td className="px-4 py-2.5"><StatusPill status={peer.state} /></td>
                      <td className="px-4 py-2.5 font-mono text-[#8C7F73] dark:text-[#A39587]">{peer.since || '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : (
              <div className="p-8 text-center text-xs text-[#8C7F73]">
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
  iconBg,
  label,
  value,
  sub,
  onClick,
}: {
  icon: React.ReactNode;
  iconBg: string;
  label: string;
  value: string;
  sub: string;
  onClick?: () => void;
}) {
  return (
    <div
      onClick={onClick}
      className={`p-4 rounded-xl bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721] shadow-[0_1px_2px_rgba(32,27,24,0.03)] ${
        onClick ? 'cursor-pointer hover:border-[#F08C18] dark:hover:border-[#F08C18]/70 hover:shadow-[0_4px_14px_rgba(240,140,24,0.10)] transition' : ''
      }`}
    >
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-[#8C7F73] dark:text-[#A39587]">{label}</span>
        <div className={`w-7 h-7 rounded-lg border flex items-center justify-center ${iconBg}`}>{icon}</div>
      </div>
      <div className="mt-2 text-lg font-bold tracking-tight text-[#201B18] dark:text-[#F5F0EA] truncate">{value}</div>
      <div className="mt-1 text-[11px] text-[#8C7F73] dark:text-[#A39587] truncate">{sub}</div>
    </div>
  );
}
