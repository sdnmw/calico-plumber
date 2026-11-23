import React, { useEffect, useState } from 'react';
import { Search, RefreshCw, Route, Terminal, Activity, Globe, ChevronDown, ChevronRight } from 'lucide-react';
import { api } from '../api/client';
import type { IPAMSnapshot, IPSlotDetail, SearchResultItem, TraceFlowRequest } from '../api/types';
import { EncapBadge, SectionCard, StatusPill } from '../components/common/Badges';

interface IpamSearchPageProps {
  onInspectPod: (ns: string, name: string) => void;
  onLaunchTrace: (req: Partial<TraceFlowRequest>) => void;
  onLaunchTroubleshoot: (ns: string, pod: string, tool: 'tcpdump' | 'tcp' | 'http') => void;
}

export function IpamSearchPage({
  onInspectPod,
  onLaunchTrace,
  onLaunchTroubleshoot,
}: IpamSearchPageProps) {
  const [ipam, setIpam] = useState<IPAMSnapshot | null>(null);
  const [namespaces, setNamespaces] = useState<string[]>([]);
  const [query, setQuery] = useState('');
  const [nsFilter, setNsFilter] = useState('');
  const [kindFilter, setKindFilter] = useState('');
  const [results, setResults] = useState<SearchResultItem[]>([]);
  const [expandedRow, setExpandedRow] = useState<string | null>(null);
  const [hoveredSlot, setHoveredSlot] = useState<IPSlotDetail | null>(null);
  const [loading, setLoading] = useState(true);

  const loadIPAM = () => {
    setLoading(true);
    Promise.all([api.getIPAM(), api.getNamespaces()])
      .then(([snap, nss]) => {
        setIpam(snap);
        setNamespaces(nss);
      })
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    loadIPAM();
  }, []);

  useEffect(() => {
    const t = setTimeout(() => {
      api.search(query, nsFilter, kindFilter, 120).then((r) => setResults(r.results || []));
    }, 120);
    return () => clearTimeout(t);
  }, [query, nsFilter, kindFilter]);

  const slotColor = (s: IPSlotDetail['status']) => {
    switch (s) {
      case 'pod':
        return 'bg-emerald-500 hover:bg-emerald-400';
      case 'tunnel':
        return 'bg-violet-500 hover:bg-violet-400';
      case 'borrowed':
        return 'bg-amber-500 hover:bg-amber-400';
      case 'orphan':
        return 'bg-rose-500 hover:bg-rose-400';
      default:
        return 'bg-slate-200 dark:bg-slate-800 hover:bg-slate-300 dark:hover:bg-slate-700';
    }
  };

  // Group blocks by affinity node
  const blocksByNode = (ipam?.blocks || []).reduce<Record<string, typeof ipam extends null ? never : IPAMSnapshot['blocks']>>((acc, b) => {
    const k = b.affinityNode || 'Unassigned';
    acc[k] = acc[k] || [];
    acc[k].push(b);
    return acc;
  }, {});

  return (
    <div className="p-6 space-y-6 max-w-[1600px] mx-auto">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-slate-900 dark:text-white">
            IPPool / IPAM 分配全景与资产检索
          </h1>
          <p className="text-xs text-slate-500 dark:text-slate-400 mt-1">
            直观掌握各节点 IPAM Block 位图分配（活跃 Pod / 隧道 IP / 跨节点借用 / 孤儿残留），支持按 IP、Pod、Deployment、Service 毫秒级定位排障
          </p>
        </div>
        <button
          onClick={loadIPAM}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 hover:bg-slate-50 dark:hover:bg-slate-800"
        >
          <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
          刷新 IPAM
        </button>
      </div>

      {/* IPPool Summary Strip */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {(ipam?.pools || []).map((pool) => (
          <SectionCard key={pool.name} className="p-4">
            <div className="flex items-center justify-between">
              <div>
                <div className="flex items-center gap-2">
                  <h3 className="font-semibold text-sm text-slate-900 dark:text-white">{pool.name}</h3>
                  <span className="font-mono text-xs px-2 py-0.5 rounded bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-200">
                    {pool.cidr}
                  </span>
                  <span className="font-mono text-xs text-slate-400">blockSize=/{pool.blockSize}</span>
                </div>
                <p className="text-[11px] text-slate-500 mt-1">
                  NodeSelector: <code className="font-mono">{pool.nodeSelector}</code> · NAT Outgoing: <b>{pool.natOutgoing ? 'Enabled' : 'Disabled'}</b>
                </p>
              </div>
              <EncapBadge mode={pool.encapMode} />
            </div>

            <div className="mt-3">
              <div className="flex justify-between text-xs mb-1">
                <span className="text-slate-500">
                  已用 <b>{pool.allocatedIPs}</b> / {pool.totalIPs} IPs（{pool.allocatedBlocks} / {pool.totalBlocks} Blocks）
                </span>
                <span className="font-mono font-semibold">{pool.utilizationPercent}%</span>
              </div>
              <div className="h-2 rounded-full bg-slate-100 dark:bg-slate-800 overflow-hidden">
                <div className="h-full bg-blue-600 rounded-full" style={{ width: `${Math.max(pool.utilizationPercent, 1.5)}%` }} />
              </div>
            </div>
          </SectionCard>
        ))}
      </div>

      {/* Per-Node IPAM Block Heatmap */}
      <SectionCard
        title="节点 IPAM Block 分配热力图"
        subtitle="悬停任意 IP 格查看归属 Pod / 工作负载，点击直接打开 Pod 数据面详情与一键排障"
        action={
          <div className="flex items-center gap-3 text-[11px] text-slate-500">
            <LegendDot color="bg-emerald-500" label="活跃 Pod" />
            <LegendDot color="bg-violet-500" label="隧道 IP" />
            <LegendDot color="bg-amber-500" label="跨节点借用" />
            <LegendDot color="bg-rose-500" label="孤儿残留" />
            <LegendDot color="bg-slate-300 dark:bg-slate-700" label="空闲" />
          </div>
        }
      >
        {/* Sticky hovered slot info ribbon */}
        <div className="px-4 py-2 bg-slate-50/80 dark:bg-slate-950/60 border-b border-slate-100 dark:border-slate-800 text-xs font-mono flex items-center justify-between min-h-[34px]">
          {hoveredSlot ? (
            <div className="flex flex-wrap items-center gap-3">
              <span className="font-semibold text-blue-600 dark:text-blue-400">{hoveredSlot.ip}</span>
              <StatusPill status={hoveredSlot.status} />
              {hoveredSlot.podName && (
                <span className="text-slate-700 dark:text-slate-200">
                  {hoveredSlot.namespace}/{hoveredSlot.podName}
                </span>
              )}
              {hoveredSlot.workloadName && (
                <span className="text-slate-400">({hoveredSlot.workloadKind}/{hoveredSlot.workloadName})</span>
              )}
              {hoveredSlot.podNode && <span className="text-slate-400">Node: {hoveredSlot.podNode}</span>}
            </div>
          ) : (
            <span className="text-slate-400 font-sans">将鼠标悬停在下方任意 IP 色块上预览详情，点击 Pod 色块打开排障抽屉</span>
          )}
        </div>

        <div className="p-4 grid grid-cols-1 xl:grid-cols-2 gap-4">
          {Object.entries(blocksByNode).map(([nodeName, blocks]) => (
            <div key={nodeName} className="rounded-xl border border-slate-200/80 dark:border-slate-800 p-4 space-y-3 bg-slate-50/40 dark:bg-slate-950/40">
              <div className="flex items-center justify-between">
                <div className="font-mono text-xs font-bold text-slate-800 dark:text-slate-100">
                  Node: {nodeName}
                </div>
                <span className="text-[11px] text-slate-500">{blocks.length} 个亲和 Block</span>
              </div>

              {blocks.map((blk) => (
                <div key={blk.name} className="rounded-lg bg-white dark:bg-slate-900 border border-slate-200/80 dark:border-slate-800 p-3">
                  <div className="flex items-center justify-between text-xs mb-2">
                    <div className="flex items-center gap-2 font-mono">
                      <span className="font-semibold text-slate-800 dark:text-slate-100">{blk.cidr}</span>
                      <span className="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 dark:bg-slate-800 text-slate-500">
                        {blk.affinityState}
                      </span>
                    </div>
                    <span className="text-[11px] font-mono text-slate-500">
                      {blk.allocatedSlots}/{blk.totalSlots} ({blk.utilizationPercent}%)
                    </span>
                  </div>
                  <div className="grid grid-cols-16 gap-1" style={{ gridTemplateColumns: 'repeat(16, minmax(0, 1fr))' }}>
                    {blk.slots.map((slot) => {
                      const matchesSearch =
                        query.trim().length > 0 &&
                        (slot.ip.includes(query.trim()) ||
                          (slot.podName && slot.podName.toLowerCase().includes(query.trim().toLowerCase())) ||
                          (slot.workloadName && slot.workloadName.toLowerCase().includes(query.trim().toLowerCase())));
                      return (
                        <button
                          key={slot.index}
                          type="button"
                          onMouseEnter={() => setHoveredSlot(slot)}
                          onClick={() => {
                            if (slot.namespace && slot.podName) {
                              onInspectPod(slot.namespace, slot.podName);
                            }
                          }}
                          className={`h-5 rounded-[3px] transition-transform ${slotColor(slot.status)} ${
                            matchesSearch ? 'ring-2 ring-blue-600 scale-110 z-10' : ''
                          } ${slot.podName ? 'cursor-pointer' : 'cursor-default'}`}
                          title={`${slot.ip} (${slot.status})${slot.podName ? ` · ${slot.namespace}/${slot.podName}` : ''}`}
                        />
                      );
                    })}
                  </div>
                </div>
              ))}
            </div>
          ))}
        </div>
      </SectionCard>

      {/* Unified Omni-Search & Asset Directory */}
      <SectionCard
        title="全维度资产检索（IP / Pod / Deployment / StatefulSet / DaemonSet / Service / Node）"
        subtitle="支持精确 IP、网段前缀、工作负载名称、Service ClusterIP/NodePort 及标签键值搜索"
      >
        <div className="p-4 border-b border-slate-100 dark:border-slate-800 flex flex-wrap items-center gap-3">
          <div className="relative flex-1 min-w-[260px]">
            <Search className="w-4 h-4 text-slate-400 absolute left-3 top-1/2 -translate-y-1/2" />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="输入 172.31.226.68 / api-gateway / coredns / 10.250.252.10 / app=nginx ..."
              className="w-full pl-9 pr-3 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
          <select
            value={nsFilter}
            onChange={(e) => setNsFilter(e.target.value)}
            className="px-3 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 outline-none"
          >
            <option value="">全部 Namespace</option>
            {namespaces.map((ns) => (
              <option key={ns} value={ns}>{ns}</option>
            ))}
          </select>
          <div className="inline-flex rounded-lg bg-slate-100 dark:bg-slate-800 p-0.5">
            {[
              { id: '', label: '全部' },
              { id: 'pod', label: 'Pod' },
              { id: 'deployment', label: 'Deploy' },
              { id: 'service', label: 'Service' },
              { id: 'node', label: 'Node' },
            ].map((k) => (
              <button
                key={k.id}
                onClick={() => setKindFilter(k.id)}
                className={`px-2.5 py-1 rounded-md text-xs font-medium ${
                  kindFilter === k.id
                    ? 'bg-white dark:bg-slate-900 text-blue-600 dark:text-blue-400 shadow-sm'
                    : 'text-slate-500 hover:text-slate-800 dark:hover:text-slate-200'
                }`}
              >
                {k.label}
              </button>
            ))}
          </div>
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead className="bg-slate-50/80 dark:bg-slate-800/60 text-slate-500">
              <tr>
                <th className="text-left px-4 py-2.5 font-medium">类型 / 名称</th>
                <th className="text-left px-4 py-2.5 font-medium">IP / ClusterIP</th>
                <th className="text-left px-4 py-2.5 font-medium">节点 / IPAM Block</th>
                <th className="text-left px-4 py-2.5 font-medium">所属工作负载 / 端口</th>
                <th className="text-left px-4 py-2.5 font-medium">状态</th>
                <th className="text-right px-4 py-2.5 font-medium">排障操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
              {results.map((item, idx) => {
                const rowKey = `${item.kind}-${item.namespace}-${item.name}-${idx}`;
                const hasPods = (item.pods?.length || 0) > 0;
                const isExpanded = expandedRow === rowKey;
                return (
                  <React.Fragment key={rowKey}>
                    <tr className="hover:bg-slate-50/70 dark:hover:bg-slate-800/40">
                      <td className="px-4 py-2.5">
                        <div className="flex items-center gap-2">
                          {hasPods && (
                            <button
                              onClick={() => setExpandedRow(isExpanded ? null : rowKey)}
                              className="p-0.5 text-slate-400 hover:text-slate-700"
                            >
                              {isExpanded ? <ChevronDown className="w-3.5 h-3.5" /> : <ChevronRight className="w-3.5 h-3.5" />}
                            </button>
                          )}
                          <span className="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-300">
                            {item.kind}
                          </span>
                          <button
                            onClick={() => {
                              if (item.kind === 'Pod' && item.namespace) onInspectPod(item.namespace, item.name);
                              else if (hasPods) setExpandedRow(isExpanded ? null : rowKey);
                            }}
                            className="font-mono font-semibold text-slate-800 dark:text-slate-100 hover:text-blue-600 dark:hover:text-blue-400 text-left"
                          >
                            {item.namespace ? `${item.namespace}/${item.name}` : item.name}
                          </button>
                        </div>
                      </td>
                      <td className="px-4 py-2.5 font-mono text-slate-700 dark:text-slate-200">
                        {item.ip || item.clusterIP || (hasPods ? `${item.pods!.length} Pods` : '—')}
                      </td>
                      <td className="px-4 py-2.5 font-mono text-[11px]">
                        <div className="text-slate-700 dark:text-slate-300">{item.nodeName || '—'}</div>
                        {item.ipamBlock && (
                          <div className="text-slate-400">
                            {item.ipamBlock} {item.borrowed && <span className="text-amber-600">(Borrowed)</span>}
                          </div>
                        )}
                      </td>
                      <td className="px-4 py-2.5 text-[11px] font-mono text-slate-500">
                        {item.workloadName ? `${item.workloadKind}/${item.workloadName}` : item.ports?.join(', ') || '—'}
                      </td>
                      <td className="px-4 py-2.5">
                        <StatusPill status={item.status || 'Active'} />
                      </td>
                      <td className="px-4 py-2.5 text-right">
                        {item.kind === 'Pod' && item.namespace ? (
                          <div className="inline-flex items-center gap-1">
                            <ActionChip icon={<Route className="w-3 h-3" />} label="Trace" primary onClick={() => onLaunchTrace({ srcNamespace: item.namespace, srcPod: item.name })} />
                            <ActionChip icon={<Terminal className="w-3 h-3" />} label="抓包" onClick={() => onLaunchTroubleshoot(item.namespace!, item.name, 'tcpdump')} />
                            <ActionChip icon={<Activity className="w-3 h-3" />} label="TCP" onClick={() => onLaunchTroubleshoot(item.namespace!, item.name, 'tcp')} />
                            <ActionChip icon={<Globe className="w-3 h-3" />} label="HTTP" onClick={() => onLaunchTroubleshoot(item.namespace!, item.name, 'http')} />
                          </div>
                        ) : item.kind === 'Service' && item.namespace ? (
                          <ActionChip
                            icon={<Route className="w-3 h-3" />}
                            label="Trace 到此 SVC"
                            primary
                            onClick={() => onLaunchTrace({ targetType: 'service', dstNamespace: item.namespace, dstName: item.name })}
                          />
                        ) : null}
                      </td>
                    </tr>

                    {/* Expanded Workload / Service Backing Pods */}
                    {isExpanded && item.pods && (
                      <tr className="bg-slate-50/80 dark:bg-slate-950/60">
                        <td colSpan={6} className="px-8 py-2.5">
                          <div className="text-[11px] font-semibold text-slate-500 mb-1.5">关联 Pods ({item.pods.length})</div>
                          <div className="space-y-1.5">
                            {item.pods.map((p) => (
                              <div key={p.name} className="flex items-center justify-between px-3 py-1.5 rounded-lg bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 text-[11px] font-mono">
                                <div className="flex items-center gap-3">
                                  <button onClick={() => onInspectPod(p.namespace, p.name)} className="font-semibold text-blue-600 dark:text-blue-400 hover:underline">
                                    {p.name}
                                  </button>
                                  <span>{p.ip}</span>
                                  <span className="text-slate-400">Node: {p.nodeName}</span>
                                  {p.ipamBlock && <span className="text-slate-400">Block: {p.ipamBlock}</span>}
                                </div>
                                <div className="flex items-center gap-1">
                                  <ActionChip icon={<Route className="w-3 h-3" />} label="Trace" primary onClick={() => onLaunchTrace({ srcNamespace: p.namespace, srcPod: p.name })} />
                                  <ActionChip icon={<Terminal className="w-3 h-3" />} label="抓包" onClick={() => onLaunchTroubleshoot(p.namespace, p.name, 'tcpdump')} />
                                  <ActionChip icon={<Activity className="w-3 h-3" />} label="TCP" onClick={() => onLaunchTroubleshoot(p.namespace, p.name, 'tcp')} />
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
  );
}

function LegendDot({ color, label }: { color: string; label: string }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className={`w-2.5 h-2.5 rounded-[2px] ${color}`} />
      <span>{label}</span>
    </span>
  );
}

function ActionChip({
  icon,
  label,
  primary,
  onClick,
}: {
  icon: React.ReactNode;
  label: string;
  primary?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className={`inline-flex items-center gap-1 px-2 py-1 rounded-md text-[11px] font-medium font-sans transition ${
        primary
          ? 'bg-blue-600 text-white hover:bg-blue-700'
          : 'bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-200 hover:bg-slate-200 dark:hover:bg-slate-700'
      }`}
    >
      {icon}
      {label}
    </button>
  );
}
