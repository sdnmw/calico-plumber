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
        return 'bg-[#ECE4DA] dark:bg-[#2A241E] hover:bg-[#DFD4C6] dark:hover:bg-[#3A322A]';
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
          <h1 className="text-xl font-bold tracking-tight text-[#201B18] dark:text-[#F5F0EA]">
            IPPool / IPAM 分配全景与资产检索
          </h1>
          <p className="text-xs text-[#8C7F73] dark:text-[#A39587] mt-1">
            直观掌握各节点 IPAM Block 位图分配（活跃 Pod / 隧道 IP / 跨节点借用 / 孤儿残留），支持按 IP、Pod、Deployment、Service 毫秒级定位排障
          </p>
        </div>
        <button
          onClick={loadIPAM}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721] text-[#574D44] dark:text-[#D5C9BC] hover:bg-[#FFF4E4] hover:text-[#9E4A05] hover:border-[#FAD4A1] dark:hover:bg-[#25201C]"
        >
          <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
          刷新 IPAM
        </button>
      </div>

      {/* IPPool Summary + Per-Node Block Occupancy Strip */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {(ipam?.pools || []).map((pool) => (
          <SectionCard key={pool.name} className="p-4 flex flex-col justify-between">
            <div className="flex items-center justify-between">
              <div>
                <div className="flex items-center gap-2">
                  <h3 className="font-semibold text-sm text-[#201B18] dark:text-[#F5F0EA]">{pool.name}</h3>
                  <span className="font-mono text-xs px-2 py-0.5 rounded bg-[#F7F3EE] dark:bg-[#25201C] text-[#574D44] dark:text-[#D5C9BC] border border-[#E9E1D6] dark:border-[#2E2721]">
                    {pool.cidr}
                  </span>
                  <span className="font-mono text-xs text-[#8C7F73]">blockSize=/{pool.blockSize}</span>
                </div>
                <p className="text-[11px] text-[#8C7F73] mt-1">
                  NodeSelector: <code className="font-mono">{pool.nodeSelector}</code> · NAT Outgoing: <b className="text-[#201B18] dark:text-[#F5F0EA]">{pool.natOutgoing ? 'Enabled' : 'Disabled'}</b>
                </p>
              </div>
              <EncapBadge mode={pool.encapMode} />
            </div>

            <div className="mt-3">
              <div className="flex justify-between text-xs mb-1">
                <span className="text-[#574D44] dark:text-[#A39587]">
                  已用 <b>{pool.allocatedIPs}</b> / {pool.totalIPs} IPs（{pool.allocatedBlocks} / {pool.totalBlocks} Blocks）
                </span>
                <span className="font-mono font-bold text-[#9E4A05] dark:text-[#FBB040]">{pool.utilizationPercent}%</span>
              </div>
              <div className="h-2 rounded-full bg-[#ECE3D7] dark:bg-[#2E2721] overflow-hidden">
                <div className="h-full bg-gradient-to-r from-[#F08C18] to-[#D9720B] rounded-full" style={{ width: `${Math.max(pool.utilizationPercent, 2)}%` }} />
              </div>
            </div>
          </SectionCard>
        ))}

        {/* Companion Card: Per-Node IPAM Block Occupancy */}
        <SectionCard className="p-4">
          <div className="flex items-center justify-between mb-2.5">
            <h3 className="font-semibold text-sm text-[#201B18] dark:text-[#F5F0EA]">各节点 IPAM Block 占用</h3>
            <span className="text-[11px] font-mono text-[#8C7F73]">
              {Object.keys(blocksByNode).length} 节点 · {(ipam?.blocks || []).length} Blocks
            </span>
          </div>
          {Object.keys(blocksByNode).length === 0 ? (
            <div className="text-xs text-[#8C7F73] py-4 text-center">暂无节点 IPAM Block 数据</div>
          ) : (
            <div className="space-y-2.5">
              {Object.entries(blocksByNode).map(([nodeName, blocks]) => {
                const used = blocks.reduce((s, b) => s + b.allocatedSlots, 0);
                const total = blocks.reduce((s, b) => s + b.totalSlots, 0);
                const pct = total > 0 ? Math.round((used / total) * 1000) / 10 : 0;
                const barCls = pct >= 85 ? 'bg-rose-500' : pct >= 65 ? 'bg-[#F08C18]' : 'bg-emerald-500';
                return (
                  <div key={nodeName} className="space-y-1">
                    <div className="flex items-center justify-between gap-2 text-xs">
                      <span className="font-mono font-semibold text-[#201B18] dark:text-[#F5F0EA] truncate" title={nodeName}>
                        {nodeName}
                      </span>
                      <div className="flex items-center gap-2 shrink-0 font-mono text-[11px]">
                        <span className="text-[#8C7F73]">{blocks.length} Blocks</span>
                        <span className="font-semibold text-[#201B18] dark:text-[#F5F0EA]">
                          {used}/{total} ({pct}%)
                        </span>
                      </div>
                    </div>
                    <div className="h-1.5 rounded-full bg-[#ECE3D7] dark:bg-[#2E2721] overflow-hidden">
                      <div className={`h-full rounded-full ${barCls}`} style={{ width: `${Math.max(pct, 2)}%` }} />
                    </div>
                    <div className="flex flex-wrap gap-1.5 pt-0.5">
                      {blocks.map((blk) => (
                        <span
                          key={blk.name}
                          className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-mono bg-[#F7F3EE] dark:bg-[#25201C] text-[#574D44] dark:text-[#D5C9BC] border border-[#E9E1D6] dark:border-[#2E2721]"
                        >
                          <span>{blk.cidr}</span>
                          <span className={blk.utilizationPercent >= 80 ? 'text-[#D9720B] dark:text-[#FBB040] font-semibold' : 'text-[#8C7F73]'}>
                            {blk.allocatedSlots}/{blk.totalSlots}
                          </span>
                        </span>
                      ))}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </SectionCard>
      </div>

      {/* Per-Node IPAM Block Heatmap */}
      <SectionCard
        title="节点 IPAM Block 分配热力图"
        subtitle="悬停任意 IP 格查看归属 Pod / 工作负载，点击直接打开 Pod 数据面详情与一键排障"
        action={
          <div className="flex items-center gap-3 text-[11px] text-[#574D44] dark:text-[#A39587]">
            <LegendDot color="bg-emerald-500" label="活跃 Pod" />
            <LegendDot color="bg-violet-500" label="隧道 IP" />
            <LegendDot color="bg-[#F08C18]" label="跨节点借用" />
            <LegendDot color="bg-rose-500" label="孤儿残留" />
            <LegendDot color="bg-[#D9CEC0] dark:bg-[#3A322A]" label="空闲" />
          </div>
        }
      >
        {/* Sticky hovered slot info ribbon */}
        <div className="px-4 py-2 bg-[#FAF6F1] dark:bg-[#14110F] border-b border-[#F2ECE4] dark:border-[#2E2721] text-xs font-mono flex items-center justify-between min-h-[34px]">
          {hoveredSlot ? (
            <div className="flex flex-wrap items-center gap-3">
              <span className="font-semibold text-[#D9720B] dark:text-[#FBB040]">{hoveredSlot.ip}</span>
              <StatusPill status={hoveredSlot.status} />
              {hoveredSlot.podName && (
                <span className="text-[#201B18] dark:text-[#F5F0EA]">
                  {hoveredSlot.namespace}/{hoveredSlot.podName}
                </span>
              )}
              {hoveredSlot.workloadName && (
                <span className="text-[#8C7F73]">({hoveredSlot.workloadKind}/{hoveredSlot.workloadName})</span>
              )}
              {hoveredSlot.podNode && <span className="text-[#8C7F73]">Node: {hoveredSlot.podNode}</span>}
            </div>
          ) : (
            <span className="text-[#8C7F73] font-sans">将鼠标悬停在下方任意 IP 色块上预览详情，点击 Pod 色块打开排障抽屉</span>
          )}
        </div>

        <div className="p-4 grid grid-cols-1 xl:grid-cols-2 gap-4">
          {Object.entries(blocksByNode).map(([nodeName, blocks]) => (
            <div key={nodeName} className="rounded-xl border border-[#E9E1D6] dark:border-[#2E2721] p-4 space-y-3 bg-[#FAF6F1]/70 dark:bg-[#14110F]/60">
              <div className="flex items-center justify-between">
                <div className="font-mono text-xs font-bold text-[#201B18] dark:text-[#F5F0EA]">
                  Node: {nodeName}
                </div>
                <span className="text-[11px] text-[#8C7F73]">{blocks.length} 个亲和 Block</span>
              </div>

              {blocks.map((blk) => (
                <div key={blk.name} className="rounded-lg bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721] p-3">
                  <div className="flex items-center justify-between text-xs mb-2">
                    <div className="flex items-center gap-2 font-mono">
                      <span className="font-semibold text-[#201B18] dark:text-[#F5F0EA]">{blk.cidr}</span>
                      <span className="text-[10px] px-1.5 py-0.5 rounded bg-[#F7F3EE] dark:bg-[#25201C] text-[#8C7F73]">
                        {blk.affinityState}
                      </span>
                    </div>
                    <span className="text-[11px] font-mono text-[#8C7F73]">
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
                            matchesSearch ? 'ring-2 ring-[#F08C18] scale-110 z-10' : ''
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
        <div className="p-4 border-b border-[#F2ECE4] dark:border-[#2E2721] flex flex-wrap items-center gap-3">
          <div className="relative flex-1 min-w-[260px]">
            <Search className="w-4 h-4 text-[#8C7F73] absolute left-3 top-1/2 -translate-y-1/2" />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="输入 172.31.226.68 / api-gateway / coredns / 10.250.252.10 / app=nginx ..."
              className="w-full pl-9 pr-3 py-2 text-xs rounded-lg bg-[#F7F3EE] dark:bg-[#25201C] border border-[#E9E1D6] dark:border-[#2E2721] outline-none focus:ring-2 focus:ring-[#F08C18]"
            />
          </div>
          <select
            value={nsFilter}
            onChange={(e) => setNsFilter(e.target.value)}
            className="px-3 py-2 text-xs rounded-lg bg-[#F7F3EE] dark:bg-[#25201C] border border-[#E9E1D6] dark:border-[#2E2721] outline-none"
          >
            <option value="">全部 Namespace</option>
            {namespaces.map((ns) => (
              <option key={ns} value={ns}>{ns}</option>
            ))}
          </select>
          <div className="inline-flex rounded-lg bg-[#F7F3EE] dark:bg-[#25201C] p-0.5 border border-[#E9E1D6] dark:border-[#2E2721]">
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
                    ? 'bg-[#FFF4E4] dark:bg-[#F08C18]/20 text-[#9E4A05] dark:text-[#FBB040] font-semibold shadow-sm'
                    : 'text-[#574D44] dark:text-[#A39587] hover:text-[#201B18] dark:hover:text-[#F5F0EA]'
                }`}
              >
                {k.label}
              </button>
            ))}
          </div>
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead className="bg-[#FAF6F1] dark:bg-[#25201C]/70 text-[#8C7F73]">
              <tr>
                <th className="text-left px-4 py-2.5 font-medium">类型 / 名称</th>
                <th className="text-left px-4 py-2.5 font-medium">IP / ClusterIP</th>
                <th className="text-left px-4 py-2.5 font-medium">节点 / IPAM Block</th>
                <th className="text-left px-4 py-2.5 font-medium">所属工作负载 / 端口</th>
                <th className="text-left px-4 py-2.5 font-medium">状态</th>
                <th className="text-right px-4 py-2.5 font-medium">排障操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#F2ECE4] dark:divide-[#2E2721]">
              {results.map((item, idx) => {
                const rowKey = `${item.kind}-${item.namespace}-${item.name}-${idx}`;
                const hasPods = (item.pods?.length || 0) > 0;
                const isExpanded = expandedRow === rowKey;
                return (
                  <React.Fragment key={rowKey}>
                    <tr className="hover:bg-[#FDF8F2] dark:hover:bg-[#25201C]/40">
                      <td className="px-4 py-2.5">
                        <div className="flex items-center gap-2">
                          {hasPods && (
                            <button
                              onClick={() => setExpandedRow(isExpanded ? null : rowKey)}
                              className="p-0.5 text-[#8C7F73] hover:text-[#201B18]"
                            >
                              {isExpanded ? <ChevronDown className="w-3.5 h-3.5" /> : <ChevronRight className="w-3.5 h-3.5" />}
                            </button>
                          )}
                          <span className="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-[#F7F3EE] dark:bg-[#25201C] text-[#574D44] dark:text-[#D5C9BC]">
                            {item.kind}
                          </span>
                          <button
                            onClick={() => {
                              if (item.kind === 'Pod' && item.namespace) onInspectPod(item.namespace, item.name);
                              else if (hasPods) setExpandedRow(isExpanded ? null : rowKey);
                            }}
                            className="font-mono font-semibold text-[#201B18] dark:text-[#F5F0EA] hover:text-[#D9720B] dark:hover:text-[#FBB040] text-left"
                          >
                            {item.namespace ? `${item.namespace}/${item.name}` : item.name}
                          </button>
                        </div>
                      </td>
                      <td className="px-4 py-2.5 font-mono text-[#574D44] dark:text-[#D5C9BC]">
                        {item.ip || item.clusterIP || (hasPods ? `${item.pods!.length} Pods` : '—')}
                      </td>
                      <td className="px-4 py-2.5 font-mono text-[11px]">
                        <div className="text-[#574D44] dark:text-[#D5C9BC]">{item.nodeName || '—'}</div>
                        {item.ipamBlock && (
                          <div className="text-[#8C7F73]">
                            {item.ipamBlock} {item.borrowed && <span className="text-[#D9720B]">(Borrowed)</span>}
                          </div>
                        )}
                      </td>
                      <td className="px-4 py-2.5 text-[11px] font-mono text-[#8C7F73]">
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
                      <tr className="bg-[#FAF6F1] dark:bg-[#14110F]">
                        <td colSpan={6} className="px-8 py-2.5">
                          <div className="text-[11px] font-semibold text-[#8C7F73] mb-1.5">关联 Pods ({item.pods.length})</div>
                          <div className="space-y-1.5">
                            {item.pods.map((p) => (
                              <div key={p.name} className="flex items-center justify-between px-3 py-1.5 rounded-lg bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721] text-[11px] font-mono">
                                <div className="flex items-center gap-3">
                                  <button onClick={() => onInspectPod(p.namespace, p.name)} className="font-semibold text-[#D9720B] dark:text-[#FBB040] hover:underline">
                                    {p.name}
                                  </button>
                                  <span>{p.ip}</span>
                                  <span className="text-[#8C7F73]">Node: {p.nodeName}</span>
                                  {p.ipamBlock && <span className="text-[#8C7F73]">Block: {p.ipamBlock}</span>}
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
          ? 'bg-[#F08C18] text-white hover:bg-[#D9720B]'
          : 'bg-[#F7F3EE] dark:bg-[#25201C] text-[#574D44] dark:text-[#D5C9BC] hover:bg-[#EFE7DD] dark:hover:bg-[#332B24]'
      }`}
    >
      {icon}
      {label}
    </button>
  );
}
