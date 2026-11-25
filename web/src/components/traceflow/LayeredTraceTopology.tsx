import React from 'react';
import {
  ArrowDown,
  ArrowRight,
  ArrowUp,
  Box,
  Cpu,
  Globe,
  HardDrive,
  Layers,
  Network,
  Server,
  Shield,
  Shuffle,
  Terminal,
  Waypoints,
} from 'lucide-react';
import type { NodeNetworkStatus, TraceFlowResult, TraceHop } from '../../api/types';
import { StatusPill } from '../common/Badges';

interface LayeredTraceTopologyProps {
  result: TraceFlowResult;
  nodes: NodeNetworkStatus[];
  selectedHop: TraceHop | null;
  onSelectHop: (hop: TraceHop) => void;
}

/**
 * Layered Node-Stack TraceFlow Topology (Bottom -> Top physical architecture):
 *   Bottom : Node Hardware/VM Baseplate
 *   Layer 2: Physical NIC (ens4 / eth0)
 *   Layer 3: Host OS
 *              ├─ Kernel Space (Tunnel vNIC / vSwitch Role ↔ Netfilter Policy / NAT / L3 Route ↔ Host Virtual NIC cali* veth / lo)
 *              └─ User Space   (Control Plane: calico-node Felix/BIRD, kube-proxy; + hostNetwork services)
 *   Top    : Container / Pod Netns (Pod User-Space App + Container Virtual NIC eth0, directly linked to Kernel veth!)
 */
export function LayeredTraceTopology({
  result,
  nodes,
  selectedHop,
  onSelectHop,
}: LayeredTraceTopologyProps) {
  const hops = result.forwardHops || [];
  const srcHops = hops.filter((h) => h.lane === 'source-node');
  const fabricHop = hops.find((h) => h.lane === 'fabric');
  const dstHops = hops.filter((h) => h.lane === 'dest-node');
  const externalHop = hops.find((h) => h.lane === 'external');

  const isExternal = Boolean(externalHop) || result.destination.kind === 'External';
  const isCrossNode = result.crossNode && dstHops.length > 0;

  const findNode = (name?: string) => nodes.find((n) => n.name === name);
  const srcNodeMeta = findNode(result.source.nodeName);
  const dstNodeName =
    dstHops.find((h) => h.nodeName)?.nodeName ||
    result.resolvedBackend?.nodeName ||
    result.destination.nodeName;
  const dstNodeMeta = findNode(dstNodeName);

  // Source hops
  const srcPodHop = srcHops.find((h) => h.component === 'pod_nic');
  const srcVethHop = srcHops.find((h) => h.component === 'host_veth' && h.title.includes('Calico Host veth'));
  const srcEgressPolicyHop = srcHops.find((h) => h.component === 'egress_policy');
  const kubeProxyHop = srcHops.find((h) => h.component === 'kube_proxy_nat');
  const routeHop = srcHops.find((h) => h.component === 'route_lookup');
  const srcTunnelHop = srcHops.find((h) => h.component === 'tunnel_encap');
  const srcNicHop = srcHops.find((h) => h.component === 'host_nic');

  // Same-node dest hops
  const sameNodeIngressPolicy = !isCrossNode ? srcHops.find((h) => h.component === 'ingress_policy') : undefined;
  const sameNodeDstVeth = !isCrossNode
    ? srcHops.find((h) => h.component === 'host_veth' && h.title.includes('Dest Calico veth'))
    : undefined;
  const sameNodeDstTarget = !isCrossNode ? srcHops.find((h) => h.component === 'dst_pod') : undefined;
  const isSameNodeHostLocal =
    !isCrossNode &&
    !isExternal &&
    Boolean(sameNodeDstTarget) &&
    !sameNodeDstVeth &&
    (sameNodeDstTarget?.interface === 'lo' ||
      sameNodeDstTarget?.interface === 'hostNetwork' ||
      sameNodeDstTarget?.title.includes('Host Local') ||
      sameNodeDstTarget?.title.includes('hostNetwork'));

  // Cross-node dest hops
  const dstNicHop = dstHops.find((h) => h.component === 'host_nic');
  const dstTunnelHop = dstHops.find((h) => h.component === 'tunnel_decap');
  const dstIngressPolicy = dstHops.find((h) => h.component === 'ingress_policy');
  const dstVethHop = dstHops.find((h) => h.component === 'host_veth');
  const dstTargetHop = dstHops.find((h) => h.component === 'dst_pod');
  const isCrossNodeHostNet =
    isCrossNode &&
    Boolean(dstTargetHop) &&
    !dstVethHop &&
    (dstTargetHop?.interface === 'hostNetwork' ||
      dstTargetHop?.title.includes('hostNetwork') ||
      dstTargetHop?.title.includes('Host Stack'));

  const tunnelIfaceHint = (node?: NodeNetworkStatus, tunnelHop?: TraceHop) => {
    if (tunnelHop) {
      return tunnelHop.interface || tunnelHop.subtitle || result.encapMode;
    }
    const mode = (result.encapMode || '').toUpperCase();
    if (mode.includes('VXLAN')) return 'vxlan.calico (VXLAN 虚拟隧道网卡)';
    if (mode.includes('IPIP')) return 'tunl0 (IPIP 虚拟隧道网卡)';
    const t = node?.tunnelInterfaces?.find((x) => x.type === 'vxlan' || x.type === 'ipip');
    if (t) return `${t.name}（未启用封装 · 当前 BGP L3 直连）`;
    return 'Calico L3 虚拟路由直转（无 OVS / Linux Bridge · 无隧道）';
  };

  /* ============================================================================
   * LAYOUT 1: SAME-NODE SINGLE NODE STACK (INTERNAL KERNEL U-TURN)
   * ==========================================================================*/
  if (!isCrossNode && !isExternal) {
    return (
      <div className="space-y-3">
        <LayerLegend />

        <div className="rounded-2xl border-2 border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900 overflow-hidden shadow-sm">
          {/* OS ENVELOPE HEADER */}
          <OSHeaderBar
            osImage={srcNodeMeta?.osImage || 'Linux Host OS'}
            kernelVersion={srcNodeMeta?.kernelVersion || 'Linux Kernel'}
            kubeProxyMode={result.kubeProxyMode}
            encapMode={result.encapMode}
          />

          {/* UPPER REGION: USER SPACE (CONTAINERS IN POD NETNS + HOST USER SPACE) */}
          <div className="p-4 bg-gradient-to-b from-sky-50/50 via-indigo-50/20 to-white dark:from-sky-950/20 dark:via-indigo-950/10 dark:to-slate-900 border-b border-dashed border-slate-300 dark:border-slate-700">
            <div className="flex flex-wrap items-center justify-between gap-2 mb-3">
              <div className="flex items-center gap-2">
                <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-md bg-sky-100 dark:bg-sky-950/70 text-sky-800 dark:text-sky-300 text-[11px] font-bold">
                  <Box className="w-3.5 h-3.5" />
                  容器层 (Pod Netns · 容器虚拟网卡)
                </span>
                <span className="text-slate-300 dark:text-slate-600">|</span>
                <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-md bg-indigo-100 dark:bg-indigo-950/70 text-indigo-800 dark:text-indigo-300 text-[11px] font-bold">
                  <Terminal className="w-3.5 h-3.5" />
                  OS 用户态 (Host User Space)
                </span>
              </div>
              <div className="flex flex-wrap items-center gap-1.5">
                <ControlPlanePill name="calico-node (Felix/BIRD)" detail="下发策略/路由" compact />
                <ControlPlanePill name={`kube-proxy (${result.kubeProxyMode})`} detail="下发 DNAT" compact />
              </div>
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-11 gap-3 items-stretch">
              {/* Left: Source Container (Pod Netns) */}
              <div className="lg:col-span-5 flex flex-col">
                <ContainerBox
                  role="源容器 · Container (Pod Netns)"
                  name={result.source.displayName}
                  ip={result.source.ip}
                  port={result.srcPort}
                  direction="egress"
                >
                  {srcPodHop && (
                    <StackHopNode
                      hop={srcPodHop}
                      selected={selectedHop?.index === srcPodHop.index}
                      onClick={() => onSelectHop(srcPodHop)}
                      tag="容器虚拟网卡 (eth0)"
                    />
                  )}
                </ContainerBox>
              </div>

              <div className="hidden lg:flex lg:col-span-1 items-center justify-center">
                <div className="h-full border-r border-dashed border-slate-200 dark:border-slate-800" />
              </div>

              {/* Right: Destination Container OR Host OS User Space Process (hostNetwork) */}
              <div className="lg:col-span-5 flex flex-col">
                {!isSameNodeHostLocal && sameNodeDstTarget ? (
                  <ContainerBox
                    role="目标容器 · Container (Pod Netns)"
                    name={result.resolvedBackend?.displayName || result.destination.displayName}
                    ip={result.resolvedBackend?.ip || result.destination.ip}
                    port={result.resolvedBackend?.port || result.dstPort}
                    direction="ingress"
                  >
                    <StackHopNode
                      hop={sameNodeDstTarget}
                      selected={selectedHop?.index === sameNodeDstTarget.index}
                      onClick={() => onSelectHop(sameNodeDstTarget)}
                      tag="容器虚拟网卡 (eth0)"
                    />
                  </ContainerBox>
                ) : (
                  <div className="flex-1 rounded-xl border-2 border-indigo-200 dark:border-indigo-800/80 bg-indigo-50/40 dark:bg-indigo-950/25 p-3 shadow-xs space-y-2 flex flex-col justify-between">
                    <div className="flex items-center justify-between gap-2">
                      <div className="min-w-0">
                        <div className="text-[10px] font-semibold uppercase tracking-wider text-indigo-600 dark:text-indigo-400">
                          OS 用户态进程 · Host User Space (hostNetwork)
                        </div>
                        <div className="text-xs font-bold font-mono text-slate-800 dark:text-slate-100 truncate">
                          {result.resolvedBackend?.displayName || result.destination.displayName}
                        </div>
                      </div>
                      <span className="shrink-0 px-2 py-0.5 rounded bg-indigo-100 dark:bg-indigo-900/60 font-mono text-[10px] text-indigo-700 dark:text-indigo-300">
                        无独立容器 Netns
                      </span>
                    </div>
                    <div className="flex items-center gap-1.5 text-[10px] font-mono text-indigo-600/80 dark:text-indigo-300/80">
                      <ArrowUp className="w-3 h-3 text-emerald-500" />
                      <span>经内核回环 lo 直接递交宿主机用户态服务 Socket</span>
                    </div>
                    {sameNodeDstTarget && (
                      <StackHopNode
                        hop={sameNodeDstTarget}
                        selected={selectedHop?.index === sameNodeDstTarget.index}
                        onClick={() => onSelectHop(sameNodeDstTarget)}
                        tag="宿主机用户态服务"
                      />
                    )}
                  </div>
                )}
              </div>
            </div>
          </div>

          {/* USER SPACE / KERNEL SPACE + NETNS BOUNDARY STRIP */}
          <div className="px-4 py-1.5 bg-slate-100/80 dark:bg-slate-950/80 border-b border-slate-200 dark:border-slate-800 grid grid-cols-1 lg:grid-cols-11 gap-3 items-center text-[10px] font-mono">
            <div className="lg:col-span-5 flex justify-center">
              <FlowConnector
                direction="down"
                label={`veth pair 跨 Netns (eth0 → ${srcVethHop?.interface || 'cali*'})`}
                status={srcVethHop?.status || 'OBSERVED'}
              />
            </div>
            <div className="hidden lg:block lg:col-span-1 text-center text-[9px] text-slate-400 uppercase tracking-wider">
              User / Kernel
            </div>
            <div className="lg:col-span-5 flex justify-center">
              {!isSameNodeHostLocal && sameNodeDstVeth ? (
                <FlowConnector
                  direction="up"
                  label={`veth pair 跨 Netns (${sameNodeDstVeth.interface || 'cali*'} → eth0)`}
                  status={sameNodeDstTarget?.status || sameNodeDstVeth.status}
                />
              ) : (
                <FlowConnector
                  direction="up"
                  label="内核 lo 回环 → 宿主机用户态 Socket"
                  status={sameNodeDstTarget?.status || 'DELIVERED'}
                />
              )}
            </div>
          </div>

          {/* MIDDLE REGION: OS KERNEL SPACE */}
          <div className="p-4 bg-emerald-50/25 dark:bg-emerald-950/15 space-y-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex items-center gap-1.5 text-xs font-bold text-emerald-900 dark:text-emerald-300">
                <Layers className="w-3.5 h-3.5 text-emerald-600 dark:text-emerald-400" />
                OS 内核态 · Linux Kernel Space（虚拟网卡 · Netfilter 策略/NAT · 虚拟路由/交换层）
              </div>
              <span className="px-2 py-0.5 rounded bg-emerald-100/80 dark:bg-emerald-950/70 text-emerald-800 dark:text-emerald-300 font-mono text-[10px]">
                同节点内核态 U 型折返转发
              </span>
            </div>

            {/* Sub-band 1: Host Virtual NICs (cali* veth / lo) */}
            <div className="rounded-xl border border-emerald-200/80 dark:border-emerald-900/60 bg-white/90 dark:bg-slate-900/80 p-3">
              <div className="text-[10px] font-semibold uppercase tracking-wider text-teal-700 dark:text-teal-400 mb-2 flex items-center gap-1.5">
                <Waypoints className="w-3.5 h-3.5" />
                宿主机虚拟网卡层 (Host Virtual NICs: cali* veth / lo)
              </div>
              <div className="grid grid-cols-1 lg:grid-cols-11 gap-3 items-center">
                <div className="lg:col-span-5">
                  {srcVethHop && (
                    <StackHopNode
                      hop={srcVethHop}
                      selected={selectedHop?.index === srcVethHop.index}
                      onClick={() => onSelectHop(srcVethHop)}
                      tag="源侧宿主机 veth"
                    />
                  )}
                </div>
                <div className="hidden lg:flex lg:col-span-1 justify-center text-[10px] font-mono text-slate-400">
                  ↓ / ↑
                </div>
                <div className="lg:col-span-5">
                  {!isSameNodeHostLocal && sameNodeDstVeth ? (
                    <StackHopNode
                      hop={sameNodeDstVeth}
                      selected={selectedHop?.index === sameNodeDstVeth.index}
                      onClick={() => onSelectHop(sameNodeDstVeth)}
                      tag="目标侧宿主机 veth"
                    />
                  ) : (
                    <div className="rounded-xl border border-emerald-300/80 dark:border-emerald-800 bg-emerald-50/70 dark:bg-emerald-950/40 px-3 py-2.5 flex items-center justify-between gap-2">
                      <div>
                        <div className="text-xs font-semibold text-emerald-900 dark:text-emerald-200 flex items-center gap-1.5">
                          <ArrowUp className="w-3.5 h-3.5 text-emerald-600" />
                          虚拟回环网卡 lo (Loopback vNIC)
                        </div>
                        <div className="text-[10px] font-mono text-emerald-700/80 dark:text-emerald-300/80 mt-0.5">
                          Host Local 路由将报文直送回环接口 lo，上行进入宿主机用户态
                        </div>
                      </div>
                      <StatusPill status={sameNodeDstTarget?.status || 'PASSED'} />
                    </div>
                  )}
                </div>
              </div>
            </div>

            {/* Vertical arrows between vNICs and Netfilter */}
            <div className="grid grid-cols-1 lg:grid-cols-11 gap-3 items-center">
              <div className="lg:col-span-5 flex justify-center">
                <FlowConnector direction="down" label="进入 Netfilter / Calico Egress 链" status={srcEgressPolicyHop?.status || 'PASSED'} />
              </div>
              <div className="hidden lg:block lg:col-span-1" />
              <div className="lg:col-span-5 flex justify-center">
                {!isSameNodeHostLocal && sameNodeIngressPolicy ? (
                  <FlowConnector direction="up" label="通过 Calico Ingress 链送入目标 veth" status={sameNodeDstVeth?.status || sameNodeIngressPolicy.status} />
                ) : (
                  <FlowConnector direction="up" label="经本地 INPUT 链进入 lo 回环" status={sameNodeDstTarget?.status || 'DELIVERED'} />
                )}
              </div>
            </div>

            {/* Sub-band 2: Netfilter Policy + NAT + Virtual Switch / L3 Router */}
            <div className="rounded-xl border border-emerald-200/80 dark:border-emerald-900/60 bg-white/90 dark:bg-slate-900/80 p-3 space-y-2.5">
              <div className="text-[10px] font-semibold uppercase tracking-wider text-blue-700 dark:text-blue-400 flex items-center gap-1.5">
                <Network className="w-3.5 h-3.5" />
                内核协议栈 / Netfilter 策略 · Service DNAT · 虚拟交换与 L3 路由转发层
              </div>

              {/* Left Egress Policy -> Right Ingress Policy */}
              <div className="grid grid-cols-1 lg:grid-cols-11 gap-3 items-center">
                <div className="lg:col-span-5">
                  {srcEgressPolicyHop && (
                    <StackHopNode
                      hop={srcEgressPolicyHop}
                      selected={selectedHop?.index === srcEgressPolicyHop.index}
                      onClick={() => onSelectHop(srcEgressPolicyHop)}
                      tag="Netfilter Egress"
                    />
                  )}
                </div>
                <div className="hidden lg:flex lg:col-span-1 justify-center">
                  <ArrowRight className="w-4 h-4 text-emerald-500" />
                </div>
                <div className="lg:col-span-5">
                  {!isSameNodeHostLocal && sameNodeIngressPolicy ? (
                    <StackHopNode
                      hop={sameNodeIngressPolicy}
                      selected={selectedHop?.index === sameNodeIngressPolicy.index}
                      onClick={() => onSelectHop(sameNodeIngressPolicy)}
                      tag="Netfilter Ingress"
                    />
                  ) : (
                    <div className="rounded-xl border border-dashed border-slate-200 dark:border-slate-700 px-3 py-2.5 text-[10px] font-mono text-slate-500 dark:text-slate-400">
                      目标为宿主机本地栈（INPUT 链），跳过 Pod 专属 cali-tw-* Ingress Policy 链
                    </div>
                  )}
                </div>
              </div>

              {/* Bottom Kernel U-turn Bus: DNAT -> Kernel FIB Route -> Virtual Switch / L3 Router Role */}
              <div className="pt-2.5 border-t border-dashed border-slate-200 dark:border-slate-800">
                <div className="text-[10px] font-mono text-slate-400 mb-2 flex items-center gap-1.5">
                  <Shuffle className="w-3 h-3 text-amber-500" />
                  内核路由/虚拟交换折返总线（PREROUTING DNAT → 内核 FIB 路由查表 → 虚拟交换/L3 路由折返上行 ↑）
                </div>
                <div className="flex flex-wrap items-stretch gap-2.5">
                  {kubeProxyHop && (
                    <>
                      <div className="flex-1 min-w-[230px]">
                        <StackHopNode
                          hop={kubeProxyHop}
                          selected={selectedHop?.index === kubeProxyHop.index}
                          onClick={() => onSelectHop(kubeProxyHop)}
                          tag="Netfilter DNAT"
                        />
                      </div>
                      <div className="flex items-center">
                        <ArrowRight className="w-4 h-4 text-amber-500 shrink-0" />
                      </div>
                    </>
                  )}
                  {routeHop && (
                    <div className="flex-1 min-w-[230px]">
                      <StackHopNode
                        hop={routeHop}
                        selected={selectedHop?.index === routeHop.index}
                        onClick={() => onSelectHop(routeHop)}
                        tag="内核 FIB 路由表"
                      />
                    </div>
                  )}
                  <div className="flex items-center">
                    <ArrowRight className="w-4 h-4 text-teal-500 shrink-0" />
                  </div>
                  <div className="px-3 py-2 rounded-xl bg-teal-50/70 dark:bg-teal-950/30 border border-dashed border-teal-300 dark:border-teal-800 text-[10px] font-mono text-teal-900 dark:text-teal-200 flex flex-col justify-center">
                    <div className="font-bold">虚拟交换 / 转发角色</div>
                    <div className="text-teal-700 dark:text-teal-300 mt-0.5">
                      {isSameNodeHostLocal
                        ? 'Local Table → lo 回环上行 ↑'
                        : 'Calico L3 虚拟路由直转（无 OVS/Bridge）↑'}
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>

          {/* BOTTOM LAYER: PHYSICAL NIC (BYPASSED) + NODE BASEPLATE */}
          <div className="px-4 py-3 bg-amber-50/30 dark:bg-slate-900/90 border-t border-slate-200 dark:border-slate-800">
            <div className="rounded-xl border border-dashed border-slate-300 dark:border-slate-700 bg-white/70 dark:bg-slate-950/50 px-3.5 py-2.5 flex flex-wrap items-center justify-between gap-2 opacity-75">
              <div className="flex items-center gap-2.5">
                <HardDrive className="w-4 h-4 text-slate-400" />
                <div>
                  <div className="text-xs font-semibold text-slate-600 dark:text-slate-300">
                    物理网卡层 · Physical NIC ({srcNodeMeta?.primaryNIC || 'ens4'}{srcNodeMeta?.nicMTU ? ` · MTU ${srcNodeMeta.nicMTU}` : ''})
                  </div>
                  <div className="text-[10px] font-mono text-slate-400">
                    {isSameNodeHostLocal
                      ? '同节点经内核 lo 回环直达宿主机用户态，未经过底层物理网卡'
                      : '同节点 Pod 间由内核 L3 路由直接转发，未经过底层物理网卡'}
                  </div>
                </div>
              </div>
              <span className="px-2 py-0.5 rounded-full text-[10px] font-mono bg-slate-200/80 dark:bg-slate-800 text-slate-500 dark:text-slate-400">
                BYPASSED · 未经过物理口
              </span>
            </div>
          </div>

          <NodeBaseplate
            roleLabel="底层节点 · NODE (同节点闭环)"
            nodeName={result.source.nodeName || 'Source Node'}
            meta={srcNodeMeta}
          />
        </div>
      </div>
    );
  }

  /* ============================================================================
   * LAYOUT 2 & 3: CROSS-NODE (2 NODE TOWERS + BOTTOM FABRIC) OR EXTERNAL (L-SHAPE)
   * ==========================================================================*/
  return (
    <div className="space-y-3">
      <LayerLegend />

      <div className={`grid grid-cols-1 ${isCrossNode ? 'xl:grid-cols-2' : 'xl:grid-cols-12'} gap-5 items-stretch`}>
        {/* ==================== LEFT TOWER: SOURCE NODE (TOP -> BOTTOM) ==================== */}
        <div
          className={`${
            isCrossNode ? '' : 'xl:col-span-7'
          } rounded-2xl border-2 border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900 overflow-hidden shadow-sm flex flex-col`}
        >
          <OSHeaderBar
            osImage={srcNodeMeta?.osImage || 'Linux Host OS'}
            kernelVersion={srcNodeMeta?.kernelVersion || 'Linux Kernel'}
            kubeProxyMode={result.kubeProxyMode}
            encapMode={result.encapMode}
          />

          {/* TOP: CONTAINER LAYER + HOST USER SPACE SIDE-BY-SIDE (DIRECTLY ABOVE KERNEL!) */}
          <div className="p-3.5 bg-gradient-to-b from-sky-50/55 via-indigo-50/15 to-white dark:from-sky-950/20 dark:via-indigo-950/10 dark:to-slate-900 border-b border-dashed border-slate-300 dark:border-slate-700">
            <div className="grid grid-cols-1 md:grid-cols-12 gap-2.5 items-stretch">
              <div className="md:col-span-8">
                <ContainerBox
                  role="容器层 · Source Container (Pod Netns)"
                  name={result.source.displayName}
                  ip={result.source.ip}
                  port={result.srcPort}
                  direction="egress"
                >
                  {srcPodHop && (
                    <StackHopNode
                      hop={srcPodHop}
                      selected={selectedHop?.index === srcPodHop.index}
                      onClick={() => onSelectHop(srcPodHop)}
                      tag="容器虚拟网卡 (eth0)"
                    />
                  )}
                </ContainerBox>
              </div>
              <div className="md:col-span-4 rounded-xl border border-indigo-200/80 dark:border-indigo-900/60 bg-indigo-50/40 dark:bg-indigo-950/20 p-2.5 flex flex-col justify-between gap-2">
                <div className="text-[10px] font-bold text-indigo-800 dark:text-indigo-300 flex items-center gap-1">
                  <Terminal className="w-3 h-3" />
                  OS 用户态 (控制面)
                </div>
                <div className="space-y-1.5">
                  <ControlPlanePill name="calico-node" detail="Felix/BIRD" compact />
                  <ControlPlanePill name="kube-proxy" detail={result.kubeProxyMode} compact />
                </div>
                <div className="text-[9px] font-mono text-indigo-600/70 dark:text-indigo-400/70">
                  ↓ 下发策略/NAT/路由至内核
                </div>
              </div>
            </div>
          </div>

          {/* Direct unobstructed veth pair boundary between Container eth0 and Kernel cali* veth */}
          <div className="py-1.5 px-3 bg-slate-100/80 dark:bg-slate-950/80 border-b border-slate-200 dark:border-slate-800 flex items-center justify-between text-[10px] font-mono">
            <span className="text-[9px] uppercase tracking-wider text-slate-400">User / Kernel · Netns 边界</span>
            <FlowConnector
              direction="down"
              label={`veth pair (eth0 → ${srcVethHop?.interface || 'cali*'})`}
              status={srcVethHop?.status || 'OBSERVED'}
            />
            <span className="w-12 hidden sm:inline" />
          </div>

          {/* MIDDLE: OS KERNEL SPACE (veth -> Netfilter/NAT/Route -> Tunnel/vSwitch) */}
          <div className="p-3.5 bg-emerald-50/25 dark:bg-emerald-950/15 space-y-2 flex-1 flex flex-col justify-between">
            <div className="flex items-center justify-between text-[11px] font-bold text-emerald-900 dark:text-emerald-300">
              <span className="flex items-center gap-1.5">
                <Layers className="w-3.5 h-3.5 text-emerald-600" />
                OS 内核态 · Kernel Space（数据面下行 ↓）
              </span>
              <span className="font-mono text-[9px] font-normal text-emerald-700/75 dark:text-emerald-400/75">
                veth → Netfilter → FIB → vSwitch/Tunnel
              </span>
            </div>

            <div className="space-y-1.5">
              {/* 1. Host veth */}
              {srcVethHop && (
                <StackHopNode
                  hop={srcVethHop}
                  selected={selectedHop?.index === srcVethHop.index}
                  onClick={() => onSelectHop(srcVethHop)}
                  tag="宿主机虚拟网卡 (cali* veth)"
                />
              )}
              <div className="flex justify-center">
                <FlowConnector direction="down" status={srcEgressPolicyHop?.status || 'PASSED'} />
              </div>

              {/* 2. Netfilter Egress Policy + optional kube-proxy DNAT */}
              <div className={`grid grid-cols-1 ${kubeProxyHop ? 'sm:grid-cols-2' : ''} gap-2`}>
                {srcEgressPolicyHop && (
                  <StackHopNode
                    hop={srcEgressPolicyHop}
                    selected={selectedHop?.index === srcEgressPolicyHop.index}
                    onClick={() => onSelectHop(srcEgressPolicyHop)}
                    tag="Netfilter Egress"
                  />
                )}
                {kubeProxyHop && (
                  <StackHopNode
                    hop={kubeProxyHop}
                    selected={selectedHop?.index === kubeProxyHop.index}
                    onClick={() => onSelectHop(kubeProxyHop)}
                    tag="Netfilter DNAT"
                  />
                )}
              </div>
              <div className="flex justify-center">
                <FlowConnector direction="down" status={routeHop?.status || 'PASSED'} />
              </div>

              {/* 3. Kernel FIB Route Lookup */}
              {routeHop && (
                <StackHopNode
                  hop={routeHop}
                  selected={selectedHop?.index === routeHop.index}
                  onClick={() => onSelectHop(routeHop)}
                  tag="内核 L3 路由表"
                />
              )}
              <div className="flex justify-center">
                <FlowConnector direction="down" status={srcTunnelHop?.status || srcNicHop?.status || 'PASSED'} />
              </div>

              {/* 4. Virtual Switch / Tunnel vNIC layer (tunl0 / vxlan.calico / Calico L3 Direct) */}
              {srcTunnelHop ? (
                <StackHopNode
                  hop={srcTunnelHop}
                  selected={selectedHop?.index === srcTunnelHop.index}
                  onClick={() => onSelectHop(srcTunnelHop)}
                  tag="虚拟隧道网卡 (tunl0 / vxlan)"
                />
              ) : (
                <VirtualSwitchRoleBanner
                  label="虚拟交换 / 隧道虚拟网卡层"
                  detail={tunnelIfaceHint(srcNodeMeta, srcTunnelHop)}
                  snatActive={Boolean(srcNicHop?.outerHeader?.startsWith('SNAT') || routeHop?.subtitle?.includes('SNAT'))}
                />
              )}
            </div>
          </div>

          {/* Connector down into Physical NIC */}
          <div className="py-1 px-3 bg-amber-50/50 dark:bg-amber-950/20 border-t border-slate-200 dark:border-slate-800 flex justify-center">
            <FlowConnector direction="down" label="经驱动队列发往物理网卡" status={srcNicHop?.status || 'OBSERVED'} />
          </div>

          {/* BOTTOM: PHYSICAL NIC LAYER */}
          <div className="p-3 bg-amber-50/25 dark:bg-slate-900/90 border-t border-slate-200 dark:border-slate-800">
            <div className="text-[10px] font-semibold uppercase tracking-wider text-amber-800 dark:text-amber-400 mb-1.5 flex items-center justify-between">
              <span className="flex items-center gap-1">
                <HardDrive className="w-3.5 h-3.5" />
                物理网卡层 · Physical NIC
              </span>
              {srcNodeMeta?.nicMTU && <span className="font-mono text-slate-500">MTU {srcNodeMeta.nicMTU}</span>}
            </div>
            {srcNicHop && (
              <StackHopNode
                hop={srcNicHop}
                selected={selectedHop?.index === srcNicHop.index}
                onClick={() => onSelectHop(srcNicHop)}
                tag="物理网卡出口"
              />
            )}
          </div>

          {/* NODE BASEPLATE */}
          <NodeBaseplate
            roleLabel="底层节点 · SOURCE NODE"
            nodeName={result.source.nodeName || 'Source Node'}
            meta={srcNodeMeta}
          />
        </div>

        {/* ==================== RIGHT TOWER: DEST NODE (BOTTOM -> TOP) OR EXTERNAL L-BRANCH ==================== */}
        {isCrossNode ? (
          <div className="rounded-2xl border-2 border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900 overflow-hidden shadow-sm flex flex-col">
            <OSHeaderBar
              osImage={dstNodeMeta?.osImage || 'Linux Host OS'}
              kernelVersion={dstNodeMeta?.kernelVersion || 'Linux Kernel'}
              kubeProxyMode={dstNodeMeta?.kubeProxyMode || result.kubeProxyMode}
              encapMode={result.encapMode}
            />

            {/* TOP: DEST CONTAINER LAYER + OS USER SPACE (If hostNetwork, target card lives in OS User Space!) */}
            <div className="p-3.5 bg-gradient-to-b from-sky-50/55 via-indigo-50/15 to-white dark:from-sky-950/20 dark:via-indigo-950/10 dark:to-slate-900 border-b border-dashed border-slate-300 dark:border-slate-700">
              {!isCrossNodeHostNet ? (
                <div className="grid grid-cols-1 md:grid-cols-12 gap-2.5 items-stretch">
                  <div className="md:col-span-8">
                    {dstTargetHop && (
                      <ContainerBox
                        role="容器层 · Destination Container (Pod Netns)"
                        name={result.resolvedBackend?.displayName || result.destination.displayName}
                        ip={result.resolvedBackend?.ip || result.destination.ip}
                        port={result.resolvedBackend?.port || result.dstPort}
                        direction="ingress"
                      >
                        <StackHopNode
                          hop={dstTargetHop}
                          selected={selectedHop?.index === dstTargetHop.index}
                          onClick={() => onSelectHop(dstTargetHop)}
                          tag="容器虚拟网卡 (eth0)"
                        />
                      </ContainerBox>
                    )}
                  </div>
                  <div className="md:col-span-4 rounded-xl border border-indigo-200/80 dark:border-indigo-900/60 bg-indigo-50/40 dark:bg-indigo-950/20 p-2.5 flex flex-col justify-between gap-2">
                    <div className="text-[10px] font-bold text-indigo-800 dark:text-indigo-300 flex items-center gap-1">
                      <Terminal className="w-3 h-3" />
                      OS 用户态 (控制面)
                    </div>
                    <ControlPlanePill name="calico-node" detail="Felix/BIRD" compact />
                    <div className="text-[9px] font-mono text-indigo-600/70 dark:text-indigo-400/70">
                      ↓ 下发 Ingress Policy
                    </div>
                  </div>
                </div>
              ) : (
                <div className="grid grid-cols-1 md:grid-cols-12 gap-2.5 items-stretch">
                  <div className="md:col-span-4 rounded-xl border border-dashed border-slate-300 dark:border-slate-700 bg-white/60 dark:bg-slate-900/40 p-2.5 flex flex-col justify-center text-center">
                    <span className="text-[10px] font-semibold text-slate-500">容器层 (跳过)</span>
                    <span className="text-[9px] text-slate-400 mt-1">目标使用 hostNetwork / 宿主机 IP</span>
                  </div>
                  <div className="md:col-span-8 rounded-xl border-2 border-indigo-200 dark:border-indigo-800/80 bg-indigo-50/45 dark:bg-indigo-950/25 p-3 space-y-2">
                    <div className="flex items-center justify-between gap-2">
                      <span className="text-[10px] font-bold uppercase tracking-wider text-indigo-700 dark:text-indigo-300 flex items-center gap-1">
                        <Terminal className="w-3 h-3" />
                        OS 用户态 · Host User Space (hostNetwork)
                      </span>
                      <span className="text-[9px] font-mono text-emerald-600 dark:text-emerald-400">↑ 内核直交</span>
                    </div>
                    {dstTargetHop && (
                      <StackHopNode
                        hop={dstTargetHop}
                        selected={selectedHop?.index === dstTargetHop.index}
                        onClick={() => onSelectHop(dstTargetHop)}
                        tag="宿主机用户态服务"
                      />
                    )}
                  </div>
                </div>
              )}
            </div>

            {/* Direct unobstructed upward boundary between Kernel and Container/Host User Space */}
            <div className="py-1.5 px-3 bg-slate-100/80 dark:bg-slate-950/80 border-b border-slate-200 dark:border-slate-800 flex items-center justify-between text-[10px] font-mono">
              <span className="text-[9px] uppercase tracking-wider text-slate-400">User / Kernel · Netns 边界</span>
              {!isCrossNodeHostNet ? (
                <FlowConnector
                  direction="up"
                  label={`veth pair (${dstVethHop?.interface || 'cali*'} → eth0)`}
                  status={dstTargetHop?.status || dstVethHop?.status || 'DELIVERED'}
                />
              ) : (
                <FlowConnector
                  direction="up"
                  label="内核本地 INPUT 链 → 宿主机用户态 Socket"
                  status={dstTargetHop?.status || 'DELIVERED'}
                />
              )}
              <span className="w-12 hidden sm:inline" />
            </div>

            {/* MIDDLE: OS KERNEL SPACE (DATA FLOWS UPWARD ↑: Tunnel/vSwitch -> Ingress Policy -> Dest veth) */}
            <div className="p-3.5 bg-emerald-50/25 dark:bg-emerald-950/15 space-y-2 flex-1 flex flex-col justify-between">
              <div className="flex items-center justify-between text-[11px] font-bold text-emerald-900 dark:text-emerald-300">
                <span className="flex items-center gap-1.5">
                  <Layers className="w-3.5 h-3.5 text-emerald-600" />
                  OS 内核态 · Kernel Space（数据面上行 ↑）
                </span>
                <span className="font-mono text-[9px] font-normal text-emerald-700/75 dark:text-emerald-400/75">
                  {isCrossNodeHostNet ? 'NIC → Local INPUT → User Space' : 'vSwitch/Tunnel → Netfilter → veth'}
                </span>
              </div>

              {!isCrossNodeHostNet ? (
                <div className="space-y-1.5">
                  {/* Top of Kernel: Dest Calico veth */}
                  {dstVethHop && (
                    <StackHopNode
                      hop={dstVethHop}
                      selected={selectedHop?.index === dstVethHop.index}
                      onClick={() => onSelectHop(dstVethHop)}
                      tag="目标宿主机虚拟网卡 (cali* veth)"
                    />
                  )}
                  <div className="flex justify-center">
                    <FlowConnector direction="up" status={dstVethHop?.status || 'PASSED'} />
                  </div>

                  {/* Mid of Kernel: Calico Ingress Policy */}
                  {dstIngressPolicy && (
                    <StackHopNode
                      hop={dstIngressPolicy}
                      selected={selectedHop?.index === dstIngressPolicy.index}
                      onClick={() => onSelectHop(dstIngressPolicy)}
                      tag="Netfilter Ingress 策略"
                    />
                  )}
                  <div className="flex justify-center">
                    <FlowConnector direction="up" status={dstIngressPolicy?.status || 'PASSED'} />
                  </div>

                  {/* Bottom of Kernel: Tunnel Decap or Virtual Switch / L3 Router role */}
                  {dstTunnelHop ? (
                    <StackHopNode
                      hop={dstTunnelHop}
                      selected={selectedHop?.index === dstTunnelHop.index}
                      onClick={() => onSelectHop(dstTunnelHop)}
                      tag="虚拟隧道网卡解封装"
                    />
                  ) : (
                    <VirtualSwitchRoleBanner
                      label="虚拟交换 / 隧道虚拟网卡层"
                      detail={tunnelIfaceHint(dstNodeMeta, dstTunnelHop)}
                      upward
                    />
                  )}
                </div>
              ) : (
                <div className="flex-1 flex flex-col justify-between gap-2.5 py-1">
                  <div className="flex justify-center">
                    <FlowConnector
                      direction="up"
                      label="Local 路由表命中 → 上行送达宿主机用户态"
                      status={dstTargetHop?.status || 'DELIVERED'}
                    />
                  </div>
                  <div className="rounded-xl border border-emerald-200/80 dark:border-emerald-800/70 bg-white/90 dark:bg-slate-900/80 p-3.5 text-xs space-y-1.5">
                    <div className="font-semibold text-slate-800 dark:text-slate-100 flex items-center justify-between gap-2">
                      <span>内核本地协议栈 (Local Delivery · INPUT 链)</span>
                      <StatusPill status={dstTargetHop?.status || 'PASSED'} />
                    </div>
                    <p className="text-[10px] font-mono text-slate-500 dark:text-slate-400 leading-relaxed">
                      目标 IP 为本机宿主机地址，报文由物理网卡进入内核后直接通过本地路由表与 INPUT 链交付宿主机用户态进程，无需经过容器虚拟网卡与 cali-tw-* 策略链
                    </p>
                  </div>
                  <div className="flex justify-center">
                    <FlowConnector direction="up" status={dstNicHop?.status || 'PASSED'} />
                  </div>
                  {dstTunnelHop ? (
                    <StackHopNode
                      hop={dstTunnelHop}
                      selected={selectedHop?.index === dstTunnelHop.index}
                      onClick={() => onSelectHop(dstTunnelHop)}
                      tag="虚拟隧道网卡解封装"
                    />
                  ) : (
                    <VirtualSwitchRoleBanner
                      label="虚拟交换 / 隧道虚拟网卡层"
                      detail={tunnelIfaceHint(dstNodeMeta, dstTunnelHop)}
                      upward
                    />
                  )}
                </div>
              )}
            </div>

            {/* Connector UP from Physical NIC into Kernel */}
            <div className="py-1 px-3 bg-amber-50/50 dark:bg-amber-950/20 border-t border-slate-200 dark:border-slate-800 flex justify-center">
              <FlowConnector direction="up" label="物理网卡收包并上送内核协议栈" status={dstNicHop?.status || 'OBSERVED'} />
            </div>

            {/* BOTTOM: PHYSICAL NIC LAYER */}
            <div className="p-3 bg-amber-50/25 dark:bg-slate-900/90 border-t border-slate-200 dark:border-slate-800">
              <div className="text-[10px] font-semibold uppercase tracking-wider text-amber-800 dark:text-amber-400 mb-1.5 flex items-center justify-between">
                <span className="flex items-center gap-1">
                  <HardDrive className="w-3.5 h-3.5" />
                  物理网卡层 · Physical NIC
                </span>
                {dstNodeMeta?.nicMTU && <span className="font-mono text-slate-500">MTU {dstNodeMeta.nicMTU}</span>}
              </div>
              {dstNicHop && (
                <StackHopNode
                  hop={dstNicHop}
                  selected={selectedHop?.index === dstNicHop.index}
                  onClick={() => onSelectHop(dstNicHop)}
                  tag="物理网卡入口"
                />
              )}
            </div>

            {/* NODE BASEPLATE */}
            <NodeBaseplate
              roleLabel="底层节点 · DESTINATION NODE"
              nodeName={dstNodeName || 'Dest Node'}
              meta={dstNodeMeta}
            />
          </div>
        ) : (
          /* ==================== EXTERNAL L-BRANCH (ALIGNED TO BOTTOM PHYSICAL NIC!) ==================== */
          <div className="xl:col-span-5 flex flex-col justify-end">
            <div className="rounded-2xl border-2 border-dashed border-violet-300 dark:border-violet-800 bg-gradient-to-br from-violet-50/50 via-white to-violet-50/30 dark:from-violet-950/25 dark:via-slate-900/80 dark:to-violet-950/15 p-4 space-y-3.5 shadow-sm">
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2 text-xs font-bold text-violet-900 dark:text-violet-200">
                  <Globe className="w-4 h-4 text-violet-600 dark:text-violet-400" />
                  集群外部物理网络 · External Network
                </div>
                <span className="px-2 py-0.5 rounded-md bg-violet-100 dark:bg-violet-950/70 text-violet-700 dark:text-violet-300 font-mono text-[10px]">
                  自左侧物理网卡出集群 →
                </span>
              </div>

              <div className="flex items-center gap-2 text-[10px] font-mono text-violet-700 dark:text-violet-300 bg-violet-100/60 dark:bg-violet-950/50 px-2.5 py-1.5 rounded-lg">
                <ArrowRight className="w-3.5 h-3.5 shrink-0" />
                <span>
                  {srcNicHop?.interface || srcNodeMeta?.primaryNIC || 'ens4'} 经 Calico SNAT (cali-nat-outgoing) 发出
                </span>
              </div>

              {fabricHop && (
                <StackHopNode
                  hop={fabricHop}
                  selected={selectedHop?.index === fabricHop.index}
                  onClick={() => onSelectHop(fabricHop)}
                  tag="物理交换 / 默认网关"
                />
              )}

              <div className="flex justify-center">
                <FlowConnector direction="down" label="物理三层路由送达外部端点" status={externalHop?.status || 'DELIVERED'} />
              </div>

              {externalHop && (
                <StackHopNode
                  hop={externalHop}
                  selected={selectedHop?.index === externalHop.index}
                  onClick={() => onSelectHop(externalHop)}
                  tag="外部目标主机 / 服务"
                />
              )}

              <div className="rounded-xl bg-slate-900 dark:bg-black text-slate-200 px-3.5 py-2 text-[11px] font-mono flex items-center justify-between">
                <span className="flex items-center gap-1.5">
                  <Globe className="w-3.5 h-3.5 text-violet-400" />
                  External Endpoint
                </span>
                <span className="text-violet-300">
                  {result.destination.ip}
                  {result.dstPort ? `:${result.dstPort}` : ''}
                </span>
              </div>
            </div>
          </div>
        )}
      </div>

      {/* ==================== BOTTOM U-BRIDGE FOR CROSS-NODE PHYSICAL FABRIC ==================== */}
      {isCrossNode && fabricHop && (
        <div className="relative pt-1">
          <div className="hidden xl:grid grid-cols-12 items-center px-16 -mb-1">
            <div className="col-span-5 flex items-end">
              <div className="w-1/2" />
              <div className={`w-1/2 h-5 border-l-2 border-b-2 rounded-bl-xl ${pipeBorderColor(fabricHop.status)}`} />
            </div>
            <div className="col-span-2 flex justify-center items-end pb-0.5">
              <span className="inline-flex items-center gap-1 text-[10px] font-mono text-violet-600 dark:text-violet-400 font-semibold">
                底层物理网络传输 <ArrowRight className="w-3.5 h-3.5" />
              </span>
            </div>
            <div className="col-span-5 flex items-end">
              <div className={`w-1/2 h-5 border-r-2 border-b-2 rounded-br-xl ${pipeBorderColor(dstNicHop?.status || fabricHop.status)}`} />
              <div className="w-1/2" />
            </div>
          </div>

          <div className="rounded-2xl border-2 border-dashed border-violet-300/90 dark:border-violet-800/80 bg-gradient-to-r from-violet-50/50 via-white to-violet-50/50 dark:from-violet-950/25 dark:via-slate-900/80 dark:to-violet-950/25 p-3.5 flex flex-col md:flex-row items-center justify-between gap-4">
            <div className="flex items-center gap-2.5 text-xs">
              <span className="inline-flex items-center justify-center w-8 h-8 rounded-xl bg-violet-100 dark:bg-violet-950/70 text-violet-600 dark:text-violet-300 shrink-0">
                <Globe className="w-4 h-4" />
              </span>
              <div>
                <div className="font-semibold text-slate-800 dark:text-slate-100">
                  底层物理网络 / 隧道织物（Physical Underlay & Switch Fabric）
                </div>
                <div className="text-[11px] font-mono text-slate-500 dark:text-slate-400">
                  {srcNodeMeta?.internalIP || result.source.nodeName} ({srcNicHop?.interface || srcNodeMeta?.primaryNIC || 'ens4'})
                  {' ──▶ '}
                  {dstNodeMeta?.internalIP || dstNodeName} ({dstNicHop?.interface || dstNodeMeta?.primaryNIC || 'ens4'})
                </div>
              </div>
            </div>

            <div className="w-full md:w-[340px] shrink-0">
              <StackHopNode
                hop={fabricHop}
                selected={selectedHop?.index === fabricHop.index}
                onClick={() => onSelectHop(fabricHop)}
                tag="底层网络 / 隧道织物"
              />
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

/* ==================== Shared Visual Building Blocks ==================== */

function LayerLegend() {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 px-3 py-2 rounded-xl bg-slate-100/80 dark:bg-slate-800/60 border border-slate-200/80 dark:border-slate-700/70 text-[11px]">
      <div className="flex flex-wrap items-center gap-1.5 text-slate-600 dark:text-slate-300">
        <span className="font-semibold text-slate-700 dark:text-slate-200">分层结构（自上而下）：</span>
        <span className="px-2 py-0.5 rounded bg-sky-100/90 dark:bg-sky-950/70 text-sky-800 dark:text-sky-300 font-medium">
          ① 容器层 (Pod eth0)
        </span>
        <span className="text-slate-400">/</span>
        <span className="px-2 py-0.5 rounded bg-indigo-100/90 dark:bg-indigo-950/70 text-indigo-800 dark:text-indigo-300 font-medium">
          ② OS 用户态 (控制面 · hostNetwork)
        </span>
        <span className="text-slate-400">→</span>
        <span className="px-2 py-0.5 rounded bg-emerald-100/90 dark:bg-emerald-950/70 text-emerald-800 dark:text-emerald-300 font-medium">
          ③ OS 内核态 (veth · Netfilter/路由 · 虚拟隧道网卡)
        </span>
        <span className="text-slate-400">→</span>
        <span className="px-2 py-0.5 rounded bg-amber-100/90 dark:bg-amber-950/70 text-amber-800 dark:text-amber-300 font-medium">
          ④ 物理网卡 & Node 底座
        </span>
      </div>
      <span className="text-[10px] font-mono text-slate-400">点击任意跳点卡片查看抓包与规则明细</span>
    </div>
  );
}

function OSHeaderBar({
  osImage,
  kernelVersion,
  kubeProxyMode,
  encapMode,
}: {
  osImage: string;
  kernelVersion: string;
  kubeProxyMode: string;
  encapMode: string;
}) {
  return (
    <div className="px-3.5 py-2 bg-slate-100/90 dark:bg-slate-800/90 border-b border-slate-200 dark:border-slate-700 flex flex-wrap items-center justify-between gap-2 text-[11px]">
      <div className="flex items-center gap-1.5 font-semibold text-slate-700 dark:text-slate-200 min-w-0">
        <Cpu className="w-3.5 h-3.5 text-indigo-500 shrink-0" />
        <span className="truncate">宿主机 OS · {osImage}</span>
      </div>
      <div className="flex flex-wrap items-center gap-1.5 font-mono text-[10px] text-slate-500 dark:text-slate-400">
        <span className="px-1.5 py-0.5 rounded bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700">
          Kernel {kernelVersion}
        </span>
        <span className="px-1.5 py-0.5 rounded bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700">
          {encapMode} · {kubeProxyMode}
        </span>
      </div>
    </div>
  );
}

function ContainerBox({
  role,
  name,
  ip,
  port,
  direction,
  children,
}: {
  role: string;
  name: string;
  ip?: string;
  port?: number;
  direction: 'egress' | 'ingress';
  children: React.ReactNode;
}) {
  return (
    <div className="h-full rounded-xl border-2 border-sky-200 dark:border-sky-800/80 bg-white dark:bg-slate-900 p-3 shadow-xs space-y-2 flex flex-col justify-between">
      <div className="flex items-center justify-between gap-2">
        <div className="min-w-0">
          <div className="text-[10px] font-bold uppercase tracking-wider text-sky-600 dark:text-sky-400">
            {role}
          </div>
          <div className="text-xs font-bold font-mono text-slate-800 dark:text-slate-100 truncate" title={name}>
            {name}
          </div>
        </div>
        {ip && (
          <span className="shrink-0 px-2 py-0.5 rounded bg-sky-50 dark:bg-sky-950/60 border border-sky-200/70 dark:border-sky-800 font-mono text-[10px] text-sky-800 dark:text-sky-300">
            {ip}
            {port ? `:${port}` : ''}
          </span>
        )}
      </div>
      <div className="flex items-center gap-1.5 text-[10px] font-mono text-slate-400">
        {direction === 'egress' ? (
          <>
            <ArrowDown className="w-3 h-3 text-sky-500" />
            <span>容器进程 Socket → 容器虚拟网卡 eth0</span>
          </>
        ) : (
          <>
            <ArrowUp className="w-3 h-3 text-emerald-500" />
            <span>容器虚拟网卡 eth0 → 容器进程 Socket</span>
          </>
        )}
      </div>
      {children}
    </div>
  );
}

function ControlPlanePill({
  name,
  detail,
  compact = false,
}: {
  name: string;
  detail: string;
  compact?: boolean;
}) {
  return (
    <div
      className={`inline-flex flex-wrap items-center gap-x-1.5 gap-y-0.5 rounded-lg bg-white/90 dark:bg-slate-900/90 border border-indigo-200/80 dark:border-indigo-800/60 ${
        compact ? 'px-2 py-1 text-[10px]' : 'px-2.5 py-1.5 text-[11px]'
      }`}
    >
      <span className="w-1.5 h-1.5 rounded-full bg-indigo-500 shrink-0" />
      <span className="font-mono font-semibold text-slate-700 dark:text-slate-200 whitespace-nowrap">{name}</span>
      <span className="text-slate-400">·</span>
      <span className="text-slate-500 dark:text-slate-400 whitespace-nowrap">{detail}</span>
    </div>
  );
}

function VirtualSwitchRoleBanner({
  label,
  detail,
  snatActive = false,
  upward = false,
}: {
  label: string;
  detail: string;
  snatActive?: boolean;
  upward?: boolean;
}) {
  return (
    <div className="rounded-lg border border-dashed border-teal-300/90 dark:border-teal-800/70 bg-teal-50/60 dark:bg-teal-950/25 px-3 py-2 flex flex-wrap items-center justify-between gap-2 text-[10px] font-mono">
      <div className="flex items-center gap-1.5 text-teal-800 dark:text-teal-300 min-w-0">
        <Waypoints className="w-3.5 h-3.5 shrink-0" />
        <span className="font-semibold shrink-0">{label}:</span>
        <span className="text-slate-600 dark:text-slate-300 truncate" title={detail}>
          {detail}
        </span>
      </div>
      <div className="flex items-center gap-1.5 shrink-0">
        {snatActive && (
          <span className="px-1.5 py-0.5 rounded bg-violet-100 dark:bg-violet-950/60 text-violet-700 dark:text-violet-300 font-semibold">
            POSTROUTING SNAT
          </span>
        )}
        <span className="text-teal-600 dark:text-teal-400">{upward ? '↑ 直通' : '↓ 直通'}</span>
      </div>
    </div>
  );
}

function NodeBaseplate({
  roleLabel,
  nodeName,
  meta,
}: {
  roleLabel: string;
  nodeName: string;
  meta?: NodeNetworkStatus;
}) {
  return (
    <div className="px-4 py-2.5 bg-slate-900 dark:bg-black text-slate-100 flex flex-wrap items-center justify-between gap-2 text-[11px]">
      <div className="flex items-center gap-2 min-w-0">
        <Server className="w-4 h-4 text-blue-400 shrink-0" />
        <span className="px-1.5 py-0.5 rounded bg-slate-800 text-blue-300 font-mono text-[10px] uppercase shrink-0">
          {roleLabel}
        </span>
        <span className="font-mono font-bold text-white truncate">{nodeName}</span>
        {meta?.internalIP && <span className="font-mono text-slate-400 shrink-0">({meta.internalIP})</span>}
      </div>
      <div className="flex flex-wrap items-center gap-2 font-mono text-[10px] text-slate-400">
        {meta?.containerRuntime && <span>{meta.containerRuntime.split('://')[0]}</span>}
        {meta?.podCIDR && <span>PodCIDR {meta.podCIDR}</span>}
        {meta?.telemetryEngine && (
          <span className="px-1.5 py-0.5 rounded bg-emerald-950/80 border border-emerald-800 text-emerald-300">
            {meta.telemetryEngine === 'ebpf' ? 'eBPF' : 'Legacy'}
          </span>
        )}
      </div>
    </div>
  );
}

function FlowConnector({
  direction,
  label,
  status,
}: {
  direction: 'down' | 'up';
  label?: string;
  status: string;
}) {
  const s = (status || '').toUpperCase();
  const colorCls =
    s === 'DROPPED' || s === 'TIMEOUT'
      ? 'text-rose-500'
      : s === 'SKIPPED'
        ? 'text-slate-300 dark:text-slate-600'
        : s === 'NAT' || s === 'RESET' || s === 'ENCAPSULATED' || s === 'DECAPSULATED'
          ? 'text-amber-500'
          : 'text-emerald-500';

  const Icon = direction === 'down' ? ArrowDown : ArrowUp;
  return (
    <div className={`inline-flex items-center gap-1 text-[10px] font-mono ${colorCls}`}>
      <Icon className="w-3.5 h-3.5 shrink-0" />
      {label && <span className="text-slate-500 dark:text-slate-400">{label}</span>}
    </div>
  );
}

function pipeBorderColor(status: string): string {
  const s = (status || '').toUpperCase();
  if (s === 'DROPPED' || s === 'TIMEOUT') return 'border-rose-400 border-dashed';
  if (s === 'SKIPPED') return 'border-slate-300 dark:border-slate-700 border-dashed';
  return 'border-violet-400 dark:border-violet-600';
}

function StackHopNode({
  hop,
  selected,
  onClick,
  tag,
}: {
  hop: TraceHop;
  selected: boolean;
  onClick: () => void;
  tag: string;
}) {
  const isBad = hop.status === 'DROPPED' || hop.status === 'TIMEOUT';
  const isSkipped = hop.status === 'SKIPPED';
  const isWarn =
    hop.status === 'RESET' ||
    hop.status === 'NAT' ||
    hop.status === 'ENCAPSULATED' ||
    hop.status === 'DECAPSULATED';

  const cls = isBad
    ? 'border-rose-400 bg-rose-50/90 dark:bg-rose-950/40'
    : isSkipped
      ? 'border-slate-200 dark:border-slate-800 border-dashed bg-slate-50/60 dark:bg-slate-900/50 opacity-70'
      : isWarn
        ? 'border-amber-300 dark:border-amber-800 bg-white dark:bg-slate-900'
        : 'border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900';

  const Icon =
    hop.component.includes('policy')
      ? Shield
      : hop.component.includes('nat')
        ? Shuffle
        : hop.component.includes('tunnel')
          ? Layers
          : hop.component.includes('route')
            ? Network
            : hop.component.includes('pod')
              ? Box
              : Server;

  return (
    <button
      onClick={onClick}
      className={`w-full text-left rounded-xl border px-3 py-2.5 transition shadow-xs hover:shadow-md ${cls} ${
        selected ? 'ring-2 ring-blue-500 border-blue-400' : ''
      }`}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-1.5 min-w-0">
          <span className="inline-flex items-center justify-center w-5 h-5 rounded-md bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-300 shrink-0">
            <Icon className="w-3 h-3" />
          </span>
          <span className="text-[10px] font-mono text-slate-400 shrink-0">#{hop.index}</span>
          <span className="text-xs font-semibold text-slate-800 dark:text-slate-100 truncate" title={hop.title}>
            {hop.title}
          </span>
        </div>
        <StatusPill status={hop.status} />
      </div>

      <div className="mt-1 flex items-center justify-between gap-2 text-[10px] font-mono">
        <span className="text-slate-500 dark:text-slate-400 truncate" title={hop.subtitle || hop.interface}>
          {hop.subtitle || hop.interface || '—'}
        </span>
        <span className="shrink-0 text-[9px] px-1.5 py-0.5 rounded bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400">
          {tag}
        </span>
      </div>

      {hop.outerHeader && (
        <div
          className="mt-1.5 px-2 py-0.5 rounded bg-violet-50 dark:bg-violet-950/60 text-violet-700 dark:text-violet-300 font-mono text-[10px] truncate"
          title={hop.outerHeader}
        >
          {hop.outerHeader}
        </div>
      )}

      <div className="mt-1.5 flex items-center justify-between text-[10px] font-mono text-slate-400">
        <span>{hop.observed ? 'Wire Observed' : isSkipped ? 'Not Reached' : 'Evaluated'}</span>
        <span>{hop.deltaMs ? `+${hop.deltaMs}ms` : ''}</span>
      </div>
    </button>
  );
}
