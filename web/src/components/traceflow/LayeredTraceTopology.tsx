import React from 'react';
import {
  ArrowDown,
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
  animate?: boolean;
}

/**
 * Compact vertical-stack TraceFlow topology with synchronized animated packet flow:
 *   Left  (Source Node): Container eth0 ↓ OS Kernel (veth / Netfilter / FIB / Tunnel) ↓ Physical NIC
 *   Bottom (Fabric)    : Physical Underlay / Overlay U-Bridge (Left → Right)
 *   Right (Dest Node)  : Physical NIC ↑ OS Kernel (Tunnel / Ingress / veth or Local) ↑ Container eth0 / Host User Space
 */
export function LayeredTraceTopology({
  result,
  nodes,
  selectedHop,
  onSelectHop,
  animate = true,
}: LayeredTraceTopologyProps) {
  const hops = result.forwardHops || [];
  const srcHops = hops.filter((h) => h.lane === 'source-node');
  const fabricHop = hops.find((h) => h.lane === 'fabric');
  const dstHops = hops.filter((h) => h.lane === 'dest-node');
  const externalHop = hops.find((h) => h.lane === 'external');

  const isExternal = Boolean(externalHop) || result.destination.kind === 'External';
  const isCrossNode = result.crossNode && dstHops.length > 0;

  // Determine where a genuine drop/timeout occurred so downstream conduits don't animate past a drop
  const srcDropped = srcHops.some((h) => h.status === 'DROPPED' || h.status === 'TIMEOUT');
  const fabricDropped = fabricHop?.status === 'DROPPED' || fabricHop?.status === 'TIMEOUT';
  const dstAllSkipped = dstHops.length > 0 && dstHops.every((h) => h.status === 'SKIPPED');
  const animSrc = animate;
  const animFabric = animate && !srcDropped && fabricHop?.status !== 'SKIPPED';
  const animDst = animate && !srcDropped && !fabricDropped && !dstAllSkipped;
  const protoTag = result.protocol || 'PKT';

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
    if (tunnelHop) return tunnelHop.interface || tunnelHop.subtitle || result.encapMode;
    const mode = (result.encapMode || '').toUpperCase();
    if (mode.includes('VXLAN')) return 'vxlan.calico (VXLAN vNIC)';
    if (mode.includes('IPIP')) return 'tunl0 (IPIP vNIC)';
    const t = node?.tunnelInterfaces?.find((x) => x.type === 'vxlan' || x.type === 'ipip');
    if (t) return `${t.name} (直连未封装)`;
    return 'Calico L3 虚拟路由直转 (无 Bridge/OVS/隧道)';
  };

  /* ============================================================================
   * LAYOUT 1: SAME-NODE COMPACT SINGLE NODE STACK (DOWN -> KERNEL U-TURN -> UP)
   * ==========================================================================*/
  if (!isCrossNode && !isExternal) {
    const sameRightActive =
      animate &&
      sameNodeDstTarget?.status !== 'SKIPPED' &&
      sameNodeDstVeth?.status !== 'SKIPPED';
    const sameTurnActive = animate && routeHop?.status !== 'SKIPPED';

    return (
      <div className="rounded-xl border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900 overflow-hidden shadow-xs">
        <OSHeaderBar
          osImage={srcNodeMeta?.osImage || 'Linux Host OS'}
          kernelVersion={srcNodeMeta?.kernelVersion || 'Linux'}
          kubeProxyMode={result.kubeProxyMode}
          encapMode={result.encapMode}
          extraControls={
            <div className="hidden md:flex items-center gap-1.5">
              <ControlPlanePill name="calico-node" detail="Felix/BIRD" />
              <ControlPlanePill name="kube-proxy" detail={result.kubeProxyMode} />
            </div>
          }
        />

        {/* Animated Left(Down) + Right(Up) Flow Wrapper */}
        <div className="relative flex">
          <VerticalFlowRail direction="down" active={animSrc} tone="sky" label={`${protoTag} ↓`} />

          <div className="flex-1 min-w-0">
            {/* LAYER 1: CONTAINER (POD NETNS) + OS USER SPACE */}
            <div className="px-2.5 py-1.5 bg-gradient-to-b from-sky-50/45 to-white dark:from-sky-950/20 dark:to-slate-900 border-b border-dashed border-slate-200 dark:border-slate-800">
              <div className="grid grid-cols-1 lg:grid-cols-11 gap-2 items-stretch">
                <div className="lg:col-span-5">
                  <ContainerBox
                    role="容器层 · Source Pod"
                    ip={result.source.ip}
                    port={result.srcPort}
                  >
                    {srcPodHop && (
                      <CompactHopRow
                        hop={srcPodHop}
                        selected={selectedHop?.index === srcPodHop.index}
                        onClick={() => onSelectHop(srcPodHop)}
                        tag="容器 eth0"
                      />
                    )}
                  </ContainerBox>
                </div>

                <div className="hidden lg:flex lg:col-span-1 items-center justify-center">
                  <div className="h-full border-r border-dashed border-slate-200 dark:border-slate-800" />
                </div>

                <div className="lg:col-span-5">
                  {!isSameNodeHostLocal && sameNodeDstTarget ? (
                    <ContainerBox
                      role="容器层 · Destination Pod"
                      ip={result.resolvedBackend?.ip || result.destination.ip}
                      port={result.resolvedBackend?.port || result.dstPort}
                    >
                      <CompactHopRow
                        hop={sameNodeDstTarget}
                        selected={selectedHop?.index === sameNodeDstTarget.index}
                        onClick={() => onSelectHop(sameNodeDstTarget)}
                        tag="容器 eth0"
                      />
                    </ContainerBox>
                  ) : (
                    <div className="h-full rounded-lg border border-indigo-200 dark:border-indigo-800 bg-indigo-50/45 dark:bg-indigo-950/25 px-2 py-1.5 space-y-1">
                      <div className="flex items-center justify-between gap-2 text-[10px]">
                        <span className="font-bold text-indigo-700 dark:text-indigo-300 flex items-center gap-1">
                          <Terminal className="w-3 h-3" />
                          OS 用户态 · hostNetwork (无容器 Netns)
                        </span>
                        <span className="font-mono text-emerald-600 dark:text-emerald-400">↑ 经 lo 直达</span>
                      </div>
                      {sameNodeDstTarget && (
                        <CompactHopRow
                          hop={sameNodeDstTarget}
                          selected={selectedHop?.index === sameNodeDstTarget.index}
                          onClick={() => onSelectHop(sameNodeDstTarget)}
                          tag="宿主机用户态"
                        />
                      )}
                    </div>
                  )}
                </div>
              </div>
            </div>

            {/* USER / KERNEL + NETNS BOUNDARY */}
            <div className="px-2.5 py-0.5 bg-slate-100/80 dark:bg-slate-950/80 border-b border-slate-200 dark:border-slate-800 grid grid-cols-1 lg:grid-cols-11 gap-2 items-center text-[10px] font-mono">
              <div className="lg:col-span-5 flex justify-center">
                <FlowConnector
                  direction="down"
                  label={`veth pair (eth0 → ${srcVethHop?.interface || 'cali*'})`}
                  status={srcVethHop?.status || 'OBSERVED'}
                  animate={animSrc}
                />
              </div>
              <div className="hidden lg:block lg:col-span-1 text-center text-[9px] text-slate-400">User / Kernel</div>
              <div className="lg:col-span-5 flex justify-center">
                {!isSameNodeHostLocal && sameNodeDstVeth ? (
                  <FlowConnector
                    direction="up"
                    label={`veth pair (${sameNodeDstVeth.interface || 'cali*'} → eth0)`}
                    status={sameNodeDstTarget?.status || sameNodeDstVeth.status}
                    animate={sameRightActive}
                  />
                ) : (
                  <FlowConnector
                    direction="up"
                    label="内核 lo 回环 → 宿主机用户态 Socket"
                    status={sameNodeDstTarget?.status || 'DELIVERED'}
                    animate={sameRightActive}
                  />
                )}
              </div>
            </div>

            {/* LAYER 2: OS KERNEL SPACE (COMPACT U-TURN) */}
            <div className="px-2.5 py-1.5 bg-emerald-50/25 dark:bg-emerald-950/15 space-y-1">
              <div className="flex items-center justify-between text-[10px]">
                <span className="font-bold text-emerald-900 dark:text-emerald-300 flex items-center gap-1">
                  <Layers className="w-3 h-3 text-emerald-600" />
                  OS 内核态 · Linux Kernel Space（虚拟网卡 · Netfilter 策略/DNAT · L3 路由折返）
                </span>
                <span className="font-mono text-[9px] text-emerald-700/80 dark:text-emerald-400/80">同节点内核闭环</span>
              </div>

              {/* Kernel Row 1: Host Virtual NICs (cali* / lo) */}
              <div className="grid grid-cols-1 lg:grid-cols-11 gap-2 items-center">
                <div className="lg:col-span-5">
                  {srcVethHop && (
                    <CompactHopRow
                      hop={srcVethHop}
                      selected={selectedHop?.index === srcVethHop.index}
                      onClick={() => onSelectHop(srcVethHop)}
                      tag="宿主机 veth"
                    />
                  )}
                </div>
                <div className="hidden lg:flex lg:col-span-1 justify-center text-[10px] font-mono text-slate-400">↓ / ↑</div>
                <div className="lg:col-span-5">
                  {!isSameNodeHostLocal && sameNodeDstVeth ? (
                    <CompactHopRow
                      hop={sameNodeDstVeth}
                      selected={selectedHop?.index === sameNodeDstVeth.index}
                      onClick={() => onSelectHop(sameNodeDstVeth)}
                      tag="目标 veth"
                    />
                  ) : (
                    <div className="rounded-lg border border-emerald-300/80 dark:border-emerald-800 bg-emerald-50/70 dark:bg-emerald-950/40 px-2.5 py-1.5 flex items-center justify-between gap-2 text-[11px]">
                      <span className="font-semibold text-emerald-900 dark:text-emerald-200 flex items-center gap-1 truncate">
                        <ArrowUp className="w-3 h-3 text-emerald-600 shrink-0" />
                        虚拟回环网卡 lo
                        <span className="font-mono text-[10px] font-normal text-emerald-700/80 dark:text-emerald-300/80 truncate">
                          (Host Local 直交宿主机栈)
                        </span>
                      </span>
                      <StatusPill status={sameNodeDstTarget?.status || 'PASSED'} compact />
                    </div>
                  )}
                </div>
              </div>

              {/* Kernel Row 2: Netfilter Policies + DNAT + FIB Route U-turn */}
              <div className="rounded-lg border border-emerald-200/80 dark:border-emerald-900/60 bg-white/85 dark:bg-slate-900/75 p-1.5 space-y-1.5">
                <div className="grid grid-cols-1 lg:grid-cols-11 gap-2 items-center">
                  <div className="lg:col-span-5">
                    {srcEgressPolicyHop && (
                      <CompactHopRow
                        hop={srcEgressPolicyHop}
                        selected={selectedHop?.index === srcEgressPolicyHop.index}
                        onClick={() => onSelectHop(srcEgressPolicyHop)}
                        tag="Egress 策略"
                      />
                    )}
                  </div>
                  <div className="hidden lg:flex lg:col-span-1 justify-center">
                    <AnimatedRightArrow active={sameTurnActive} status={srcEgressPolicyHop?.status || 'PASSED'} />
                  </div>
                  <div className="lg:col-span-5">
                    {!isSameNodeHostLocal && sameNodeIngressPolicy ? (
                      <CompactHopRow
                        hop={sameNodeIngressPolicy}
                        selected={selectedHop?.index === sameNodeIngressPolicy.index}
                        onClick={() => onSelectHop(sameNodeIngressPolicy)}
                        tag="Ingress 策略"
                      />
                    ) : (
                      <div className="rounded-lg border border-dashed border-slate-200 dark:border-slate-700 px-2.5 py-1.5 text-[10px] font-mono text-slate-500 dark:text-slate-400">
                        宿主机本地 INPUT 链（跳过目标 Pod cali-tw-* 链）
                      </div>
                    )}
                  </div>
                </div>

                {/* Animated Kernel U-Turn Bus */}
                <div className="space-y-1 pt-1 border-t border-dashed border-slate-200 dark:border-slate-800">
                  <HorizontalFlowRail active={sameTurnActive} label="内核折返总线 · PREROUTING DNAT → FIB 路由 → 上行折返" />
                  <div className="flex flex-wrap items-center gap-1.5">
                    {kubeProxyHop && (
                      <>
                        <div className="flex-1 min-w-[200px]">
                          <CompactHopRow
                            hop={kubeProxyHop}
                            selected={selectedHop?.index === kubeProxyHop.index}
                            onClick={() => onSelectHop(kubeProxyHop)}
                            tag="Service DNAT"
                          />
                        </div>
                        <AnimatedRightArrow active={sameTurnActive} status={kubeProxyHop.status} />
                      </>
                    )}
                    {routeHop && (
                      <div className="flex-1 min-w-[200px]">
                        <CompactHopRow
                          hop={routeHop}
                          selected={selectedHop?.index === routeHop.index}
                          onClick={() => onSelectHop(routeHop)}
                          tag="内核 L3 路由"
                        />
                      </div>
                    )}
                    <AnimatedRightArrow active={sameTurnActive} status={routeHop?.status || 'PASSED'} />
                    <div className="px-2 py-1.5 rounded-lg bg-teal-50/70 dark:bg-teal-950/30 border border-dashed border-teal-300 dark:border-teal-800 text-[10px] font-mono text-teal-800 dark:text-teal-300 whitespace-nowrap">
                      {isSameNodeHostLocal ? 'Local Table → lo 折返 ↑' : 'Calico L3 虚拟路由直转 ↑'}
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <VerticalFlowRail direction="up" active={sameRightActive} tone="emerald" label={`${protoTag} ↑`} />
        </div>

        {/* LAYER 3: PHYSICAL NIC (BYPASSED) + NODE BASEPLATE */}
        <div className="px-3 py-1 bg-slate-100/80 dark:bg-slate-900/90 border-t border-slate-200 dark:border-slate-800 flex flex-wrap items-center justify-between gap-2 text-[10px] font-mono text-slate-500">
          <span className="flex items-center gap-1.5">
            <HardDrive className="w-3 h-3 text-slate-400" />
            <b className="text-slate-600 dark:text-slate-300">物理网卡 ({srcNodeMeta?.primaryNIC || 'ens4'})</b>
            <span>· {isSameNodeHostLocal ? '经 lo 回环直达，未经过物理口' : '同节点内核直转，未经过物理口'}</span>
          </span>
          <span className="px-1.5 py-0 rounded bg-slate-200/80 dark:bg-slate-800 text-[9px]">BYPASSED</span>
        </div>

        <NodeBaseplate roleLabel="NODE (同节点)" nodeName={result.source.nodeName || 'Source Node'} meta={srcNodeMeta} />
      </div>
    );
  }

  /* ============================================================================
   * LAYOUT 2 & 3: COMPACT CROSS-NODE (2 TOWERS + ANIMATED U-BRIDGE) OR EXTERNAL
   * ==========================================================================*/
  return (
    <div className="space-y-1.5">
      <div className={`grid grid-cols-1 ${isCrossNode ? 'xl:grid-cols-2' : 'xl:grid-cols-12'} gap-3 items-stretch`}>
        {/* ==================== LEFT TOWER: SOURCE NODE (TOP -> BOTTOM ↓) ==================== */}
        <div
          className={`${
            isCrossNode ? '' : 'xl:col-span-7'
          } rounded-xl border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900 overflow-hidden shadow-xs flex flex-col`}
        >
          <OSHeaderBar
            osImage={srcNodeMeta?.osImage || 'Linux Host OS'}
            kernelVersion={srcNodeMeta?.kernelVersion || 'Linux'}
            kubeProxyMode={result.kubeProxyMode}
            encapMode={result.encapMode}
          />

          <div className="flex flex-1 min-h-0">
            {/* Synchronized Downward Packet Rail (Top -> Bottom) */}
            <VerticalFlowRail
              direction="down"
              active={animSrc}
              tone="sky"
              label={`${protoTag} ↓`}
              externalMode={isExternal}
            />

            <div className="flex-1 min-w-0 flex flex-col">
              {/* LAYER 1: CONTAINER + OS USER SPACE */}
              <div className="px-2 py-1.5 bg-gradient-to-b from-sky-50/45 to-white dark:from-sky-950/20 dark:to-slate-900 border-b border-dashed border-slate-200 dark:border-slate-800">
                <div className="grid grid-cols-1 md:grid-cols-12 gap-1.5 items-stretch">
                  <div className="md:col-span-8">
                    <ContainerBox role="容器层 · Source Pod" ip={result.source.ip} port={result.srcPort}>
                      {srcPodHop && (
                        <CompactHopRow
                          hop={srcPodHop}
                          selected={selectedHop?.index === srcPodHop.index}
                          onClick={() => onSelectHop(srcPodHop)}
                          tag="容器 eth0"
                        />
                      )}
                    </ContainerBox>
                  </div>
                  <div className="md:col-span-4 rounded-lg border border-indigo-200/80 dark:border-indigo-900/60 bg-indigo-50/40 dark:bg-indigo-950/20 px-2 py-1 flex flex-col justify-between gap-1">
                    <div className="text-[10px] font-bold text-indigo-800 dark:text-indigo-300 flex items-center gap-1">
                      <Terminal className="w-3 h-3 shrink-0" />
                      OS 用户态
                    </div>
                    <div className="flex flex-col gap-0.5">
                      <ControlPlanePill name="calico-node" detail="Felix/BIRD" />
                      <ControlPlanePill name="kube-proxy" detail={result.kubeProxyMode} />
                    </div>
                  </div>
                </div>
              </div>

              {/* NETNS / USER-KERNEL BOUNDARY */}
              <div className="py-0.5 px-2 bg-slate-100/80 dark:bg-slate-950/80 border-b border-slate-200 dark:border-slate-800 flex items-center justify-between text-[10px] font-mono">
                <span className="text-[9px] text-slate-400">Netns · User/Kernel</span>
                <FlowConnector
                  direction="down"
                  label={`veth pair (eth0 → ${srcVethHop?.interface || 'cali*'})`}
                  status={srcVethHop?.status || 'OBSERVED'}
                  animate={animSrc}
                />
                <span className="w-8 hidden sm:inline" />
              </div>

              {/* LAYER 2: OS KERNEL SPACE (DOWNWARD ↓) */}
              <div className="px-2 py-1.5 bg-emerald-50/25 dark:bg-emerald-950/15 space-y-1 flex-1 flex flex-col justify-between">
                <div className="flex items-center justify-between text-[10px]">
                  <span className="font-bold text-emerald-900 dark:text-emerald-300 flex items-center gap-1">
                    <Layers className="w-3 h-3 text-emerald-600" />
                    OS 内核态 · Kernel Space (下行 ↓)
                  </span>
                  <span className="font-mono text-[9px] text-emerald-700/75 dark:text-emerald-400/75">
                    veth → Netfilter/FIB → vSwitch/Tunnel
                  </span>
                </div>

                <div className="space-y-0.5">
                  {srcVethHop && (
                    <CompactHopRow
                      hop={srcVethHop}
                      selected={selectedHop?.index === srcVethHop.index}
                      onClick={() => onSelectHop(srcVethHop)}
                      tag="宿主机 veth"
                    />
                  )}
                  <AnimatedMicroArrow direction="down" status={srcEgressPolicyHop?.status || 'PASSED'} animate={animSrc} />

                  <div className={`grid grid-cols-1 ${kubeProxyHop ? 'sm:grid-cols-3' : 'sm:grid-cols-2'} gap-1.5`}>
                    {srcEgressPolicyHop && (
                      <CompactHopRow
                        hop={srcEgressPolicyHop}
                        selected={selectedHop?.index === srcEgressPolicyHop.index}
                        onClick={() => onSelectHop(srcEgressPolicyHop)}
                        tag="Egress"
                        narrow
                      />
                    )}
                    {kubeProxyHop && (
                      <CompactHopRow
                        hop={kubeProxyHop}
                        selected={selectedHop?.index === kubeProxyHop.index}
                        onClick={() => onSelectHop(kubeProxyHop)}
                        tag="DNAT"
                        narrow
                      />
                    )}
                    {routeHop && (
                      <CompactHopRow
                        hop={routeHop}
                        selected={selectedHop?.index === routeHop.index}
                        onClick={() => onSelectHop(routeHop)}
                        tag="FIB 路由"
                        narrow
                      />
                    )}
                  </div>
                  <AnimatedMicroArrow
                    direction="down"
                    status={srcTunnelHop?.status || srcNicHop?.status || 'PASSED'}
                    animate={animSrc && !srcDropped}
                  />

                  {srcTunnelHop ? (
                    <CompactHopRow
                      hop={srcTunnelHop}
                      selected={selectedHop?.index === srcTunnelHop.index}
                      onClick={() => onSelectHop(srcTunnelHop)}
                      tag="隧道虚拟网卡"
                    />
                  ) : (
                    <VirtualSwitchStrip
                      detail={tunnelIfaceHint(srcNodeMeta, srcTunnelHop)}
                      snatActive={Boolean(srcNicHop?.outerHeader?.startsWith('SNAT') || routeHop?.subtitle?.includes('SNAT'))}
                    />
                  )}
                </div>
              </div>

              {/* LAYER 3: PHYSICAL NIC */}
              <div className="px-2 py-1.5 bg-amber-50/30 dark:bg-slate-900/90 border-t border-slate-200 dark:border-slate-800 space-y-1">
                <div className="flex items-center justify-between text-[10px]">
                  <span className="font-bold text-amber-800 dark:text-amber-400 flex items-center gap-1">
                    <HardDrive className="w-3 h-3" />
                    物理网卡层 · Physical NIC
                  </span>
                  {srcNodeMeta?.nicMTU && <span className="font-mono text-[9px] text-slate-500">MTU {srcNodeMeta.nicMTU}</span>}
                </div>
                {srcNicHop && (
                  <CompactHopRow
                    hop={srcNicHop}
                    selected={selectedHop?.index === srcNicHop.index}
                    onClick={() => onSelectHop(srcNicHop)}
                    tag="物理网卡出口"
                  />
                )}
              </div>
            </div>
          </div>

          <NodeBaseplate roleLabel="SOURCE NODE" nodeName={result.source.nodeName || 'Source Node'} meta={srcNodeMeta} />
        </div>

        {/* ==================== RIGHT TOWER: DEST NODE (BOTTOM -> TOP ↑) OR EXTERNAL L-BRANCH ==================== */}
        {isCrossNode ? (
          <div className="rounded-xl border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900 overflow-hidden shadow-xs flex flex-col">
            <OSHeaderBar
              osImage={dstNodeMeta?.osImage || 'Linux Host OS'}
              kernelVersion={dstNodeMeta?.kernelVersion || 'Linux'}
              kubeProxyMode={dstNodeMeta?.kubeProxyMode || result.kubeProxyMode}
              encapMode={result.encapMode}
            />

            <div className="flex flex-1 min-h-0">
              <div className="flex-1 min-w-0 flex flex-col">
                {/* LAYER 1: DEST CONTAINER OR HOST USER SPACE */}
                <div className="px-2 py-1.5 bg-gradient-to-b from-sky-50/45 to-white dark:from-sky-950/20 dark:to-slate-900 border-b border-dashed border-slate-200 dark:border-slate-800">
                  {!isCrossNodeHostNet ? (
                    <div className="grid grid-cols-1 md:grid-cols-12 gap-1.5 items-stretch">
                      <div className="md:col-span-8">
                        {dstTargetHop && (
                          <ContainerBox
                            role="容器层 · Destination Pod"
                            ip={result.resolvedBackend?.ip || result.destination.ip}
                            port={result.resolvedBackend?.port || result.dstPort}
                          >
                            <CompactHopRow
                              hop={dstTargetHop}
                              selected={selectedHop?.index === dstTargetHop.index}
                              onClick={() => onSelectHop(dstTargetHop)}
                              tag="容器 eth0"
                            />
                          </ContainerBox>
                        )}
                      </div>
                      <div className="md:col-span-4 rounded-lg border border-indigo-200/80 dark:border-indigo-900/60 bg-indigo-50/40 dark:bg-indigo-950/20 px-2 py-1 flex flex-col justify-between gap-1">
                        <div className="text-[10px] font-bold text-indigo-800 dark:text-indigo-300 flex items-center gap-1">
                          <Terminal className="w-3 h-3 shrink-0" />
                          OS 用户态
                        </div>
                        <ControlPlanePill name="calico-node" detail="Felix/BIRD" />
                        <span className="text-[9px] font-mono text-indigo-600/75 dark:text-indigo-400/75">↓ Ingress Policy</span>
                      </div>
                    </div>
                  ) : (
                    <div className="grid grid-cols-1 md:grid-cols-12 gap-1.5 items-stretch">
                      <div className="md:col-span-4 rounded-lg border border-dashed border-slate-300 dark:border-slate-700 bg-slate-50/70 dark:bg-slate-900/40 px-2 py-1 flex flex-col justify-center text-center">
                        <span className="text-[10px] font-semibold text-slate-500">容器层 (跳过)</span>
                        <span className="text-[9px] text-slate-400">hostNetwork / Node</span>
                      </div>
                      <div className="md:col-span-8 rounded-lg border border-indigo-200 dark:border-indigo-800 bg-indigo-50/45 dark:bg-indigo-950/25 px-2 py-1 space-y-1">
                        <div className="flex items-center justify-between text-[10px]">
                          <span className="font-bold text-indigo-700 dark:text-indigo-300 flex items-center gap-1">
                            <Terminal className="w-3 h-3" />
                            OS 用户态 · hostNetwork
                          </span>
                          <span className="font-mono text-emerald-600 dark:text-emerald-400">↑ 内核直交</span>
                        </div>
                        {dstTargetHop && (
                          <CompactHopRow
                            hop={dstTargetHop}
                            selected={selectedHop?.index === dstTargetHop.index}
                            onClick={() => onSelectHop(dstTargetHop)}
                            tag="宿主机用户态"
                          />
                        )}
                      </div>
                    </div>
                  )}
                </div>

                {/* NETNS / USER-KERNEL BOUNDARY (UPWARD ↑) */}
                <div className="py-0.5 px-2 bg-slate-100/80 dark:bg-slate-950/80 border-b border-slate-200 dark:border-slate-800 flex items-center justify-between text-[10px] font-mono">
                  <span className="text-[9px] text-slate-400">Netns · User/Kernel</span>
                  {!isCrossNodeHostNet ? (
                    <FlowConnector
                      direction="up"
                      label={`veth pair (${dstVethHop?.interface || 'cali*'} → eth0)`}
                      status={dstTargetHop?.status || dstVethHop?.status || 'DELIVERED'}
                      animate={animDst && dstTargetHop?.status !== 'SKIPPED'}
                    />
                  ) : (
                    <FlowConnector
                      direction="up"
                      label="内核 INPUT 链 → 宿主机用户态 Socket"
                      status={dstTargetHop?.status || 'DELIVERED'}
                      animate={animDst && dstTargetHop?.status !== 'SKIPPED'}
                    />
                  )}
                  <span className="w-8 hidden sm:inline" />
                </div>

                {/* LAYER 2: OS KERNEL SPACE (UPWARD ↑) */}
                <div className="px-2 py-1.5 bg-emerald-50/25 dark:bg-emerald-950/15 space-y-1 flex-1 flex flex-col justify-between">
                  <div className="flex items-center justify-between text-[10px]">
                    <span className="font-bold text-emerald-900 dark:text-emerald-300 flex items-center gap-1">
                      <Layers className="w-3 h-3 text-emerald-600" />
                      OS 内核态 · Kernel Space (上行 ↑)
                    </span>
                    <span className="font-mono text-[9px] text-emerald-700/75 dark:text-emerald-400/75">
                      {isCrossNodeHostNet ? 'NIC → Local INPUT → User' : 'vSwitch/Tunnel → Ingress → veth'}
                    </span>
                  </div>

                  {!isCrossNodeHostNet ? (
                    <div className="space-y-0.5">
                      {dstVethHop && (
                        <CompactHopRow
                          hop={dstVethHop}
                          selected={selectedHop?.index === dstVethHop.index}
                          onClick={() => onSelectHop(dstVethHop)}
                          tag="目标 veth"
                        />
                      )}
                      <AnimatedMicroArrow
                        direction="up"
                        status={dstVethHop?.status || 'PASSED'}
                        animate={animDst && dstVethHop?.status !== 'SKIPPED'}
                      />
                      {dstIngressPolicy && (
                        <CompactHopRow
                          hop={dstIngressPolicy}
                          selected={selectedHop?.index === dstIngressPolicy.index}
                          onClick={() => onSelectHop(dstIngressPolicy)}
                          tag="Ingress 策略"
                        />
                      )}
                      <AnimatedMicroArrow
                        direction="up"
                        status={dstIngressPolicy?.status || 'PASSED'}
                        animate={animDst && dstIngressPolicy?.status !== 'SKIPPED'}
                      />
                      {dstTunnelHop ? (
                        <CompactHopRow
                          hop={dstTunnelHop}
                          selected={selectedHop?.index === dstTunnelHop.index}
                          onClick={() => onSelectHop(dstTunnelHop)}
                          tag="隧道解封装"
                        />
                      ) : (
                        <VirtualSwitchStrip detail={tunnelIfaceHint(dstNodeMeta, dstTunnelHop)} upward />
                      )}
                    </div>
                  ) : (
                    <div className="space-y-1 my-auto">
                      <AnimatedMicroArrow
                        direction="up"
                        status={dstTargetHop?.status || 'DELIVERED'}
                        animate={animDst}
                      />
                      <div className="rounded-lg border border-emerald-200/80 dark:border-emerald-800/70 bg-white/90 dark:bg-slate-900/80 px-2.5 py-1.5 flex items-center justify-between gap-2 text-[11px]">
                        <div className="min-w-0">
                          <div className="font-semibold text-slate-800 dark:text-slate-100">内核本地协议栈 (Local Delivery · INPUT)</div>
                          <div className="text-[10px] font-mono text-slate-500 truncate">本机宿主机地址直交用户态，跳过 Pod veth</div>
                        </div>
                        <StatusPill status={dstTargetHop?.status || 'PASSED'} compact />
                      </div>
                      <AnimatedMicroArrow direction="up" status={dstNicHop?.status || 'PASSED'} animate={animDst} />
                      {dstTunnelHop ? (
                        <CompactHopRow
                          hop={dstTunnelHop}
                          selected={selectedHop?.index === dstTunnelHop.index}
                          onClick={() => onSelectHop(dstTunnelHop)}
                          tag="隧道解封装"
                        />
                      ) : (
                        <VirtualSwitchStrip detail={tunnelIfaceHint(dstNodeMeta, dstTunnelHop)} upward />
                      )}
                    </div>
                  )}
                </div>

                {/* LAYER 3: PHYSICAL NIC */}
                <div className="px-2 py-1.5 bg-amber-50/30 dark:bg-slate-900/90 border-t border-slate-200 dark:border-slate-800 space-y-1">
                  <div className="flex items-center justify-between text-[10px]">
                    <span className="font-bold text-amber-800 dark:text-amber-400 flex items-center gap-1">
                      <HardDrive className="w-3 h-3" />
                      物理网卡层 · Physical NIC
                    </span>
                    {dstNodeMeta?.nicMTU && <span className="font-mono text-[9px] text-slate-500">MTU {dstNodeMeta.nicMTU}</span>}
                  </div>
                  {dstNicHop && (
                    <CompactHopRow
                      hop={dstNicHop}
                      selected={selectedHop?.index === dstNicHop.index}
                      onClick={() => onSelectHop(dstNicHop)}
                      tag="物理网卡入口"
                    />
                  )}
                </div>
              </div>

              {/* Synchronized Upward Packet Rail (Bottom -> Top) */}
              <VerticalFlowRail direction="up" active={animDst} tone="emerald" label={`${protoTag} ↑`} />
            </div>

            <NodeBaseplate roleLabel="DEST NODE" nodeName={dstNodeName || 'Dest Node'} meta={dstNodeMeta} />
          </div>
        ) : (
          /* ==================== COMPACT EXTERNAL L-BRANCH WITH ANIMATED PACKET FLOW ==================== */
          <div className="xl:col-span-5 flex flex-col justify-end">
            <div className="rounded-xl border border-dashed border-violet-300 dark:border-violet-800 bg-violet-50/30 dark:bg-violet-950/15 p-2.5 space-y-1.5 shadow-xs">
              <div className="flex items-center justify-between gap-2 text-[11px]">
                <span className="font-bold text-violet-900 dark:text-violet-200 flex items-center gap-1.5">
                  <Globe className="w-3.5 h-3.5 text-violet-600" />
                  集群外部物理网络 · External Network
                </span>
                <span className="font-mono text-[9px] px-1.5 py-0.5 rounded bg-violet-100 dark:bg-violet-950/70 text-violet-700 dark:text-violet-300">
                  {srcNicHop?.interface || 'ens4'} SNAT 出集群 →
                </span>
              </div>

              <HorizontalFlowRail
                active={animFabric}
                externalMode
                label={`SNAT 报文自 ${srcNicHop?.interface || 'ens4'} 流出 → 外部网关`}
              />

              {fabricHop && (
                <CompactHopRow
                  hop={fabricHop}
                  selected={selectedHop?.index === fabricHop.index}
                  onClick={() => onSelectHop(fabricHop)}
                  tag="外部网关"
                />
              )}
              <AnimatedMicroArrow
                direction="down"
                status={externalHop?.status || 'DELIVERED'}
                animate={animFabric && externalHop?.status !== 'SKIPPED'}
              />
              {externalHop && (
                <CompactHopRow
                  hop={externalHop}
                  selected={selectedHop?.index === externalHop.index}
                  onClick={() => onSelectHop(externalHop)}
                  tag="外部目标"
                />
              )}

              <div className="rounded-lg bg-slate-900 dark:bg-black text-slate-200 px-3 py-1.5 text-[10px] font-mono flex items-center justify-between">
                <span className="flex items-center gap-1.5">
                  <Globe className="w-3 h-3 text-violet-400" />
                  External Target
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

      {/* ==================== ANIMATED SVG U-BRIDGE CONNECTING LEFT NIC -> FABRIC -> RIGHT NIC ==================== */}
      {isCrossNode && fabricHop && (
        <div className="relative">
          {/* Native SVG U-Pipe with synchronized <animateMotion> packet orb (Left NIC down -> Right -> Right NIC up!) */}
          <div className="hidden xl:block w-full h-6 -mt-1 -mb-0.5">
            <svg viewBox="0 0 1000 26" preserveAspectRatio="none" className="w-full h-full overflow-visible">
              <defs>
                <filter id="tf-orb-glow" x="-50%" y="-50%" width="200%" height="200%">
                  <feGaussianBlur stdDeviation="2.5" result="blur" />
                  <feMerge>
                    <feMergeNode in="blur" />
                    <feMergeNode in="SourceGraphic" />
                  </feMerge>
                </filter>
              </defs>
              {/* Static faint backing rail */}
              <path
                d="M 245 0 L 245 14 Q 245 21 258 21 L 742 21 Q 755 21 755 14 L 755 0"
                fill="none"
                stroke={fabricDropped ? '#fda4af' : '#c4b5fd'}
                strokeOpacity="0.45"
                strokeWidth="3"
              />
              {/* Animated marching dashed path */}
              <path
                id="tf-u-bridge-path"
                d="M 245 0 L 245 14 Q 245 21 258 21 L 742 21 Q 755 21 755 14 L 755 0"
                fill="none"
                stroke={fabricDropped ? '#f43f5e' : '#7c3aed'}
                strokeWidth="2"
                strokeDasharray="6 6"
                className={animFabric ? 'animate-flow-dash' : ''}
              />
              {/* Synchronized glowing packet orb traveling 34%->66% of the 3s loop (seamlessly bridging Left↓ and Right↑!) */}
              {animFabric && (
                <circle r="4.5" fill={fabricDropped ? '#f43f5e' : '#0ea5e9'} filter="url(#tf-orb-glow)">
                  <animateMotion
                    dur="3s"
                    repeatCount="indefinite"
                    keyPoints="0;0;1;1"
                    keyTimes="0;0.33;0.67;1"
                    calcMode="linear"
                    path="M 245 0 L 245 14 Q 245 21 258 21 L 742 21 Q 755 21 755 14 L 755 0"
                  />
                  <animate
                    attributeName="opacity"
                    dur="3s"
                    repeatCount="indefinite"
                    values="0;0;1;1;0;0"
                    keyTimes="0;0.32;0.36;0.64;0.68;1"
                  />
                </circle>
              )}
            </svg>
          </div>

          <div className="rounded-xl border border-dashed border-violet-300/90 dark:border-violet-800 bg-violet-50/40 dark:bg-violet-950/20 px-3 py-1.5 flex flex-col md:flex-row items-center justify-between gap-2.5">
            <div className="flex items-center gap-2 text-[11px] min-w-0">
              <Globe className="w-3.5 h-3.5 text-violet-600 dark:text-violet-400 shrink-0" />
              <span className="font-semibold text-slate-800 dark:text-slate-100 shrink-0">底层物理网络 / 隧道织物</span>
              <span className="font-mono text-[10px] text-slate-500 dark:text-slate-400 truncate">
                {srcNodeMeta?.internalIP || result.source.nodeName} ({srcNicHop?.interface || srcNodeMeta?.primaryNIC || 'ens4'})
                {' ──▶ '}
                {dstNodeMeta?.internalIP || dstNodeName} ({dstNicHop?.interface || dstNodeMeta?.primaryNIC || 'ens4'})
              </span>
            </div>
            <div className="w-full md:w-[360px] shrink-0">
              <CompactHopRow
                hop={fabricHop}
                selected={selectedHop?.index === fabricHop.index}
                onClick={() => onSelectHop(fabricHop)}
                tag="物理/隧道织物"
              />
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

/* ==================== Animated Packet Conduit Subcomponents ==================== */

function VerticalFlowRail({
  direction,
  active,
  tone,
  label,
  externalMode = false,
}: {
  direction: 'down' | 'up';
  active: boolean;
  tone: 'sky' | 'emerald';
  label: string;
  externalMode?: boolean;
}) {
  const isDown = direction === 'down';
  const railBg =
    tone === 'sky'
      ? 'bg-sky-50/70 dark:bg-sky-950/25 border-r border-sky-200/70 dark:border-sky-900/50'
      : 'bg-emerald-50/70 dark:bg-emerald-950/25 border-l border-emerald-200/70 dark:border-emerald-900/50';
  const trackStroke = tone === 'sky' ? '#38bdf8' : '#10b981';
  const animCls = !active
    ? 'hidden'
    : externalMode
      ? 'tf-ext-down'
      : isDown
        ? 'tf-packet-down'
        : 'tf-packet-up';
  const cometGrad =
    tone === 'sky'
      ? 'bg-gradient-to-b from-sky-400/0 via-sky-500 to-blue-600 shadow-[0_0_8px_rgba(14,165,233,0.9)]'
      : 'bg-gradient-to-t from-emerald-400/0 via-emerald-500 to-teal-500 shadow-[0_0_8px_rgba(16,185,129,0.9)]';

  return (
    <div className={`w-6 shrink-0 relative flex flex-col items-center justify-between py-2 select-none ${railBg}`}>
      <span
        className={`text-[8px] font-mono font-bold leading-none ${
          tone === 'sky' ? 'text-sky-600 dark:text-sky-400' : 'text-emerald-600 dark:text-emerald-400'
        }`}
      >
        {isDown ? '↓' : '↑'}
      </span>

      {/* Dashed SVG guide line running full height */}
      <div className="relative flex-1 w-full my-1">
        <svg className="absolute inset-0 w-full h-full" preserveAspectRatio="none">
          <line
            x1="50%"
            y1="0%"
            x2="50%"
            y2="100%"
            stroke={active ? trackStroke : '#94a3b8'}
            strokeOpacity={active ? '0.55' : '0.3'}
            strokeWidth="2"
            strokeDasharray="3 3"
            className={active ? (isDown ? 'tf-dash-down' : 'tf-dash-up') : ''}
          />
        </svg>

        {/* Glowing traveling packet comet */}
        {active && (
          <div
            className={`absolute left-1/2 w-2.5 h-6 rounded-full ${cometGrad} ${animCls}`}
            title={label}
          >
            <div
              className={`absolute left-1/2 -translate-x-1/2 w-2 h-2 rounded-full bg-white ${
                isDown ? 'bottom-0.5' : 'top-0.5'
              }`}
            />
          </div>
        )}
      </div>

      <span
        className={`text-[8px] font-mono font-bold leading-none ${
          tone === 'sky' ? 'text-sky-600 dark:text-sky-400' : 'text-emerald-600 dark:text-emerald-400'
        }`}
      >
        {isDown ? '↓' : '↑'}
      </span>
    </div>
  );
}

function HorizontalFlowRail({
  active,
  label,
  externalMode = false,
}: {
  active: boolean;
  label: string;
  externalMode?: boolean;
}) {
  return (
    <div className="relative h-4 flex items-center gap-2">
      <div className="relative flex-1 h-2 flex items-center">
        <div className="w-full h-[2px] border-t-2 border-dashed border-violet-300 dark:border-violet-700" />
        {active && (
          <div
            className={`absolute top-1/2 h-2 w-6 rounded-full bg-gradient-to-r from-violet-400/0 via-sky-500 to-violet-600 shadow-[0_0_8px_rgba(139,92,246,0.85)] ${
              externalMode ? 'tf-ext-right' : 'tf-packet-right'
            }`}
          >
            <div className="absolute right-0.5 top-1/2 -translate-y-1/2 w-1.5 h-1.5 rounded-full bg-white" />
          </div>
        )}
      </div>
      <span className="text-[9px] font-mono text-slate-400 shrink-0">{label}</span>
    </div>
  );
}

function AnimatedMicroArrow({
  direction,
  status,
  animate,
}: {
  direction: 'down' | 'up';
  status: string;
  animate: boolean;
}) {
  const isDown = direction === 'down';
  const s = (status || '').toUpperCase();
  const isSkipped = s === 'SKIPPED';
  const isBad = s === 'DROPPED' || s === 'TIMEOUT';
  const color = isBad ? '#f43f5e' : isSkipped ? '#cbd5e1' : isDown ? '#0ea5e9' : '#10b981';

  return (
    <div className="flex justify-center items-center h-3.5 leading-none">
      <svg width="24" height="14" viewBox="0 0 24 14" className="overflow-visible">
        <line
          x1="12"
          y1="1"
          x2="12"
          y2="13"
          stroke={color}
          strokeOpacity="0.45"
          strokeWidth="1.5"
          strokeDasharray="2.5 2.5"
          className={animate && !isSkipped ? (isDown ? 'tf-dash-down' : 'tf-dash-up') : ''}
        />
        {isDown ? (
          <path d="M 8.5 9.5 L 12 13 L 15.5 9.5" fill="none" stroke={color} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
        ) : (
          <path d="M 8.5 4.5 L 12 1 L 15.5 4.5" fill="none" stroke={color} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
        )}
        {animate && !isSkipped && (
          <circle
            cx="12"
            cy="7"
            r="2.2"
            fill={color}
            className={isDown ? 'tf-bead-down' : 'tf-bead-up'}
          />
        )}
      </svg>
    </div>
  );
}

function AnimatedRightArrow({ active, status }: { active: boolean; status: string }) {
  const s = (status || '').toUpperCase();
  const isSkipped = s === 'SKIPPED';
  const color = s === 'DROPPED' || s === 'TIMEOUT' ? '#f43f5e' : isSkipped ? '#cbd5e1' : '#10b981';
  return (
    <svg width="18" height="14" viewBox="0 0 18 14" className="shrink-0 overflow-visible">
      <line x1="1" y1="7" x2="15" y2="7" stroke={color} strokeOpacity="0.45" strokeWidth="1.5" strokeDasharray="2.5 2.5" />
      <path d="M 11.5 3.5 L 15.5 7 L 11.5 10.5" fill="none" stroke={color} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
      {active && !isSkipped && <circle cx="8" cy="7" r="2.2" fill={color} className="tf-bead-right" />}
    </svg>
  );
}

/* ==================== Compact Structural Subcomponents ==================== */

function OSHeaderBar({
  osImage,
  kernelVersion,
  kubeProxyMode,
  encapMode,
  extraControls,
}: {
  osImage: string;
  kernelVersion: string;
  kubeProxyMode: string;
  encapMode: string;
  extraControls?: React.ReactNode;
}) {
  return (
    <div className="px-3 py-1 bg-slate-100/90 dark:bg-slate-800/90 border-b border-slate-200 dark:border-slate-700 flex flex-wrap items-center justify-between gap-2 text-[10px]">
      <div className="flex items-center gap-1.5 font-semibold text-slate-700 dark:text-slate-200 min-w-0">
        <Cpu className="w-3 h-3 text-indigo-500 shrink-0" />
        <span className="truncate">宿主机 OS · {osImage}</span>
        <span className="font-mono font-normal text-slate-500 dark:text-slate-400 hidden sm:inline truncate">
          ({kernelVersion})
        </span>
      </div>
      <div className="flex items-center gap-1.5 font-mono text-[9px] text-slate-500 dark:text-slate-400">
        {extraControls}
        <span className="px-1.5 py-0.5 rounded bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700">
          {encapMode} · {kubeProxyMode}
        </span>
      </div>
    </div>
  );
}

function ContainerBox({
  role,
  ip,
  port,
  children,
}: {
  role: string;
  ip?: string;
  port?: number;
  children: React.ReactNode;
}) {
  return (
    <div className="h-full rounded-lg border border-sky-200 dark:border-sky-800/80 bg-white dark:bg-slate-900 px-2 py-1 space-y-1">
      <div className="flex items-center justify-between gap-2 text-[10px]">
        <span className="font-bold text-sky-700 dark:text-sky-400 flex items-center gap-1 truncate">
          <Box className="w-3 h-3 text-sky-500 shrink-0" />
          {role}
        </span>
        {ip && (
          <span className="shrink-0 px-1.5 py-0 rounded bg-sky-50 dark:bg-sky-950/60 font-mono text-[9px] text-sky-700 dark:text-sky-300">
            {ip}
            {port ? `:${port}` : ''}
          </span>
        )}
      </div>
      {children}
    </div>
  );
}

function ControlPlanePill({ name, detail }: { name: string; detail: string }) {
  return (
    <div className="inline-flex items-center gap-1 rounded bg-white/90 dark:bg-slate-900/90 border border-indigo-200/80 dark:border-indigo-800/60 px-1.5 py-0.5 text-[9px] font-mono">
      <span className="w-1.5 h-1.5 rounded-full bg-indigo-500 shrink-0" />
      <span className="font-semibold text-slate-700 dark:text-slate-200 whitespace-nowrap">{name}</span>
      <span className="text-slate-400">·</span>
      <span className="text-slate-500 dark:text-slate-400 whitespace-nowrap">{detail}</span>
    </div>
  );
}

function VirtualSwitchStrip({
  detail,
  snatActive = false,
  upward = false,
}: {
  detail: string;
  snatActive?: boolean;
  upward?: boolean;
}) {
  return (
    <div className="rounded-lg border border-dashed border-teal-300/90 dark:border-teal-800/70 bg-teal-50/55 dark:bg-teal-950/25 px-2 py-1 flex items-center justify-between gap-2 text-[10px] font-mono">
      <div className="flex items-center gap-1.5 text-teal-800 dark:text-teal-300 min-w-0">
        <Waypoints className="w-3 h-3 shrink-0" />
        <span className="font-semibold shrink-0">虚拟交换/隧道:</span>
        <span className="text-slate-600 dark:text-slate-300 truncate" title={detail}>
          {detail}
        </span>
      </div>
      <div className="flex items-center gap-1 shrink-0">
        {snatActive && (
          <span className="px-1 py-0 rounded bg-violet-100 dark:bg-violet-950/60 text-violet-700 dark:text-violet-300 text-[9px] font-semibold">
            SNAT
          </span>
        )}
        <span className="text-teal-600 dark:text-teal-400 text-[9px]">{upward ? '↑ 直通' : '↓ 直通'}</span>
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
    <div className="px-3 py-1 bg-slate-900 dark:bg-black text-slate-100 flex flex-wrap items-center justify-between gap-2 text-[10px]">
      <div className="flex items-center gap-1.5 min-w-0">
        <Server className="w-3 h-3 text-blue-400 shrink-0" />
        <span className="px-1.5 py-0 rounded bg-slate-800 text-blue-300 font-mono text-[9px] uppercase shrink-0">
          {roleLabel}
        </span>
        <span className="font-mono font-bold text-white truncate">{nodeName}</span>
        {meta?.internalIP && <span className="font-mono text-slate-400 shrink-0">({meta.internalIP})</span>}
      </div>
      <div className="flex items-center gap-1.5 font-mono text-[9px] text-slate-400">
        {meta?.containerRuntime && <span>{meta.containerRuntime.split('://')[0]}</span>}
        {meta?.podCIDR && <span className="hidden sm:inline">{meta.podCIDR}</span>}
        {meta?.telemetryEngine && (
          <span className="px-1.5 py-0 rounded bg-emerald-950/80 border border-emerald-800 text-emerald-300">
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
  animate = true,
}: {
  direction: 'down' | 'up';
  label?: string;
  status: string;
  animate?: boolean;
}) {
  const isDown = direction === 'down';
  const Icon = isDown ? ArrowDown : ArrowUp;
  const s = (status || '').toUpperCase();
  const active = animate && s !== 'SKIPPED';

  return (
    <div className={`inline-flex items-center gap-1 text-[10px] font-mono ${connectorColor(status)}`}>
      <span className="relative inline-flex items-center justify-center w-3 h-3">
        <Icon className={`w-3 h-3 shrink-0 ${active ? (isDown ? 'tf-bead-down' : 'tf-bead-up') : ''}`} />
      </span>
      {label && <span className="text-slate-500 dark:text-slate-400">{label}</span>}
    </div>
  );
}

function connectorColor(status: string): string {
  const s = (status || '').toUpperCase();
  if (s === 'DROPPED' || s === 'TIMEOUT') return 'text-rose-500';
  if (s === 'SKIPPED') return 'text-slate-300 dark:text-slate-600';
  if (s === 'NAT' || s === 'RESET' || s === 'ENCAPSULATED' || s === 'DECAPSULATED') return 'text-amber-500';
  return 'text-emerald-500';
}

function CompactHopRow({
  hop,
  selected,
  onClick,
  tag,
  narrow = false,
}: {
  hop: TraceHop;
  selected: boolean;
  onClick: () => void;
  tag: string;
  narrow?: boolean;
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
      className={`w-full text-left rounded-lg border px-2 py-1 transition hover:shadow-sm ${cls} ${
        selected ? 'ring-2 ring-blue-500 border-blue-400' : ''
      }`}
    >
      <div className="flex items-center justify-between gap-1.5">
        <div className="flex items-center gap-1 min-w-0">
          <Icon className="w-3 h-3 text-slate-500 dark:text-slate-400 shrink-0" />
          <span className="text-[9px] font-mono text-slate-400 shrink-0">#{hop.index}</span>
          <span className="text-[11px] font-semibold text-slate-800 dark:text-slate-100 truncate" title={hop.title}>
            {hop.title}
          </span>
        </div>
        <div className="flex items-center gap-1 shrink-0">
          {hop.deltaMs ? (
            <span className="text-[9px] font-mono text-slate-400">+{hop.deltaMs}ms</span>
          ) : null}
          <StatusPill status={hop.status} compact />
        </div>
      </div>

      <div className="mt-0.5 flex items-center justify-between gap-1.5 text-[10px] font-mono">
        <span className="text-slate-500 dark:text-slate-400 truncate" title={hop.subtitle || hop.interface}>
          {hop.subtitle || hop.interface || '—'}
        </span>
        <div className="flex items-center gap-1 shrink-0">
          <span
            className={`w-1.5 h-1.5 rounded-full ${
              hop.observed ? 'bg-emerald-500' : isSkipped ? 'bg-slate-300' : 'bg-slate-400'
            }`}
            title={hop.observed ? 'Wire Observed' : isSkipped ? 'Not Reached' : 'Evaluated'}
          />
          {!narrow && (
            <span className="text-[9px] px-1 rounded bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400">
              {tag}
            </span>
          )}
        </div>
      </div>

      {hop.outerHeader && (
        <div
          className="mt-0.5 px-1.5 py-0 rounded bg-violet-50 dark:bg-violet-950/60 text-violet-700 dark:text-violet-300 font-mono text-[9px] truncate"
          title={hop.outerHeader}
        >
          {hop.outerHeader}
        </div>
      )}
    </button>
  );
}
