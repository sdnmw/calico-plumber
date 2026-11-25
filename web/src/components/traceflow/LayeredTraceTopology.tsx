import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
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
  /** Increment to trigger a calm, step-by-step packet playback along the traced path. Default 0 = static. */
  playTrigger?: number;
  /** Increment to immediately stop an in-progress playback and return to static view. */
  stopTrigger?: number;
  onPlayingChange?: (playing: boolean) => void;
}

interface MeasuredSegment {
  fromIndex: number;
  toIndex: number;
  d: string;
  color: string;
  isFabricBridge: boolean;
}

interface OrbState {
  x: number;
  y: number;
  color: string;
  label: string;
  segmentIdx: number;
  progress: number;
}

const STEP_TRAVEL_MS = 560; // Smooth packet glide between consecutive layers
const STEP_DWELL_MS = 390; // Calm pause on arriving hop so user can read it
const FIRST_HOP_DWELL_MS = 680;
const FINAL_HOP_HOLD_MS = 1350;

function easeInOutCubic(t: number): number {
  return t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2;
}

/**
 * Compact vertical-stack TraceFlow topology with on-demand, hop-by-hop packet flow playback:
 *   - Default: calm & static (zero visual distraction when inspecting or browsing history)
 *   - On Trace / Replay: a packet travels slowly step-by-step directly between the real layers
 *     inside the diagram (Container eth0 ↓ Host OS Kernel ↓ Physical NIC → Fabric → Dest NIC ↑ Kernel ↑ Container/Host).
 */
export function LayeredTraceTopology({
  result,
  nodes,
  selectedHop,
  onSelectHop,
  playTrigger = 0,
  stopTrigger = 0,
  onPlayingChange,
}: LayeredTraceTopologyProps) {
  const hops = result.forwardHops || [];
  const srcHops = hops.filter((h) => h.lane === 'source-node');
  const fabricHop = hops.find((h) => h.lane === 'fabric');
  const dstHops = hops.filter((h) => h.lane === 'dest-node');
  const externalHop = hops.find((h) => h.lane === 'external');

  const isExternal = Boolean(externalHop) || result.destination.kind === 'External';
  const isCrossNode = result.crossNode && dstHops.length > 0;

  // Ordered sequence of hops actually traversed by the packet (stops at first DROPPED/TIMEOUT, never enters SKIPPED)
  const playableHops = useMemo(() => {
    const seq: TraceHop[] = [];
    for (const h of hops) {
      if (h.status === 'SKIPPED') break;
      seq.push(h);
      if (h.status === 'DROPPED' || h.status === 'TIMEOUT') break;
    }
    return seq;
  }, [hops]);

  const wrapperRef = useRef<HTMLDivElement | null>(null);
  const hopRefs = useRef<Record<number, HTMLButtonElement | null>>({});
  const pathRefs = useRef<(SVGPathElement | null)[]>([]);

  const [segments, setSegments] = useState<MeasuredSegment[]>([]);
  const [svgSize, setSvgSize] = useState({ w: 0, h: 0 });
  const [isPlaying, setIsPlaying] = useState(false);
  const [activeStep, setActiveStep] = useState(-1); // -1 = idle/static
  const [orb, setOrb] = useState<OrbState | null>(null);

  const registerHopRef = useCallback(
    (hopIndex: number) => (el: HTMLButtonElement | null) => {
      hopRefs.current[hopIndex] = el;
    },
    [],
  );

  // Measure real DOM positions of consecutive hop cards to compute exact inter-layer SVG trajectories
  const measureSegments = useCallback(() => {
    const root = wrapperRef.current;
    if (!root) return;
    const rootRect = root.getBoundingClientRect();
    setSvgSize({ w: rootRect.width, h: rootRect.height });

    const nextSegs: MeasuredSegment[] = [];
    for (let i = 0; i < playableHops.length - 1; i++) {
      const aHop = playableHops[i];
      const bHop = playableHops[i + 1];
      const aEl = hopRefs.current[aHop.index];
      const bEl = hopRefs.current[bHop.index];
      if (!aEl || !bEl) continue;

      const ar = aEl.getBoundingClientRect();
      const br = bEl.getBoundingClientRect();
      const a = {
        left: ar.left - rootRect.left,
        right: ar.right - rootRect.left,
        top: ar.top - rootRect.top,
        bottom: ar.bottom - rootRect.top,
        cx: ar.left - rootRect.left + ar.width / 2,
        cy: ar.top - rootRect.top + ar.height / 2,
      };
      const b = {
        left: br.left - rootRect.left,
        right: br.right - rootRect.left,
        top: br.top - rootRect.top,
        bottom: br.bottom - rootRect.top,
        cx: br.left - rootRect.left + br.width / 2,
        cy: br.top - rootRect.top + br.height / 2,
      };

      const d = buildInterHopPath(a, b, aHop, bHop, isExternal);
      const color = segmentColor(bHop);
      const isFabricBridge = aHop.lane === 'fabric' || bHop.lane === 'fabric';
      nextSegs.push({ fromIndex: aHop.index, toIndex: bHop.index, d, color, isFabricBridge });
    }
    setSegments(nextSegs);
  }, [playableHops, isExternal]);

  // Re-measure on result change or container resize
  useEffect(() => {
    const raf = requestAnimationFrame(() => measureSegments());
    const ro = new ResizeObserver(() => measureSegments());
    if (wrapperRef.current) ro.observe(wrapperRef.current);
    return () => {
      cancelAnimationFrame(raf);
      ro.disconnect();
    };
  }, [measureSegments, result.id]);

  // Reset to static whenever user switches to a different trace without triggering playback
  const lastResultIdRef = useRef(result.id);
  useEffect(() => {
    if (lastResultIdRef.current !== result.id) {
      lastResultIdRef.current = result.id;
      setIsPlaying(false);
      setActiveStep(-1);
      setOrb(null);
      onPlayingChange?.(false);
    }
  }, [result.id, onPlayingChange]);

  // Stop playback when stopTrigger increments
  const lastStopRef = useRef(stopTrigger);
  useEffect(() => {
    if (stopTrigger !== lastStopRef.current) {
      lastStopRef.current = stopTrigger;
      setIsPlaying(false);
      setActiveStep(-1);
      setOrb(null);
      onPlayingChange?.(false);
    }
  }, [stopTrigger, onPlayingChange]);

  // Start calm hop-by-hop playback ONLY when playTrigger increments (> 0)
  const lastPlayRef = useRef(0);
  useEffect(() => {
    if (playTrigger === 0) {
      lastPlayRef.current = 0;
      setIsPlaying(false);
      setActiveStep(-1);
      setOrb(null);
      onPlayingChange?.(false);
      return;
    }
    if (playTrigger === lastPlayRef.current) return;
    lastPlayRef.current = playTrigger;
    if (playableHops.length === 0) return;

    // Respect prefers-reduced-motion
    if (typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) {
      return;
    }

    measureSegments();
    setIsPlaying(true);
    setActiveStep(0);
    setOrb(null);
    onPlayingChange?.(true);
  }, [playTrigger, playableHops.length, measureSegments, onPlayingChange]);

  // Core 60fps step-by-step packet flight animation loop
  useEffect(() => {
    if (!isPlaying || playableHops.length === 0) return;

    let cancelled = false;
    let rafId = 0;
    let timerId = 0;

    const protoLabel = (hop: TraceHop) => {
      if (hop.outerHeader?.startsWith('SNAT')) return 'SNAT';
      if (hop.outerHeader?.startsWith('VXLAN')) return 'VXLAN';
      if (hop.outerHeader?.startsWith('IPIP')) return 'IPIP';
      if (hop.status === 'NAT') return 'DNAT';
      if (hop.status === 'DROPPED' || hop.status === 'TIMEOUT') return 'DROP';
      return result.protocol || 'PKT';
    };

    const runStep = (stepIdx: number) => {
      if (cancelled) return;
      setActiveStep(stepIdx);
      setOrb(null);

      // Final hop: hold clearly, then settle cleanly back to static view
      if (stepIdx >= playableHops.length - 1) {
        timerId = window.setTimeout(() => {
          if (cancelled) return;
          setIsPlaying(false);
          setActiveStep(-1);
          setOrb(null);
          onPlayingChange?.(false);
        }, FINAL_HOP_HOLD_MS);
        return;
      }

      const dwell = stepIdx === 0 ? FIRST_HOP_DWELL_MS : STEP_DWELL_MS;
      timerId = window.setTimeout(() => {
        if (cancelled) return;

        // Re-measure right before launching flight to guarantee exact alignment
        const pathEl = pathRefs.current[stepIdx];
        const nextHop = playableHops[stepIdx + 1];
        if (!pathEl) {
          runStep(stepIdx + 1);
          return;
        }
        const totalLen = pathEl.getTotalLength();
        if (totalLen <= 0) {
          runStep(stepIdx + 1);
          return;
        }

        const startTime = performance.now();
        const segColor = segmentColor(nextHop);
        const label = protoLabel(nextHop);

        const tick = (now: number) => {
          if (cancelled) return;
          const rawT = Math.min(1, (now - startTime) / STEP_TRAVEL_MS);
          const eased = easeInOutCubic(rawT);
          const pt = pathEl.getPointAtLength(eased * totalLen);
          setOrb({
            x: pt.x,
            y: pt.y,
            color: segColor,
            label,
            segmentIdx: stepIdx,
            progress: eased,
          });

          if (rawT < 1) {
            rafId = requestAnimationFrame(tick);
          } else {
            runStep(stepIdx + 1);
          }
        };
        rafId = requestAnimationFrame(tick);
      }, dwell);
    };

    // Slight initial delay so DOM measurements settle cleanly
    timerId = window.setTimeout(() => {
      if (!cancelled) {
        measureSegments();
        runStep(0);
      }
    }, 80);

    return () => {
      cancelled = true;
      cancelAnimationFrame(rafId);
      clearTimeout(timerId);
    };
  }, [isPlaying, playTrigger, playableHops, result.protocol, measureSegments, onPlayingChange]);

  const activeHop = isPlaying && activeStep >= 0 ? playableHops[activeStep] : null;
  const visitedSet = useMemo(() => {
    const s = new Set<number>();
    if (!isPlaying || activeStep < 0) return s;
    for (let i = 0; i <= activeStep && i < playableHops.length; i++) {
      s.add(playableHops[i].index);
    }
    return s;
  }, [isPlaying, activeStep, playableHops]);

  const hopPlayState = (hop?: TraceHop): 'idle' | 'active' | 'visited' | 'pending' => {
    if (!isPlaying || !hop) return 'idle';
    if (activeHop?.index === hop.index) return 'active';
    if (visitedSet.has(hop.index)) return 'visited';
    return 'pending';
  };

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

  const renderOverlaySVG = () => {
    if (segments.length === 0 || svgSize.w === 0 || svgSize.h === 0) return null;
    return (
      <svg
        width={svgSize.w}
        height={svgSize.h}
        viewBox={`0 0 ${svgSize.w} ${svgSize.h}`}
        className="pointer-events-none absolute inset-0 z-20 overflow-visible"
      >
        <defs>
          <filter id="tf-pkt-glow" x="-60%" y="-60%" width="220%" height="220%">
            <feGaussianBlur stdDeviation="3" result="blur" />
            <feMerge>
              <feMergeNode in="blur" />
              <feMergeNode in="SourceGraphic" />
            </feMerge>
          </filter>
        </defs>

        {/* Hidden geometry paths (always mounted so getPointAtLength works instantaneously) + subtle static fabric bridge + bright trail during playback */}
        {segments.map((seg, idx) => {
          const isCompletedSeg = isPlaying && activeStep > idx;
          const isCurrentSeg = isPlaying && orb?.segmentIdx === idx;
          const staticBridge = !isPlaying && seg.isFabricBridge;
          return (
            <g key={`${seg.fromIndex}-${seg.toIndex}`}>
              <path
                ref={(el) => {
                  pathRefs.current[idx] = el;
                }}
                d={seg.d}
                fill="none"
                stroke={staticBridge ? '#8b5cf6' : seg.color}
                strokeWidth={isCurrentSeg ? 3 : isCompletedSeg ? 2.2 : staticBridge ? 1.8 : 0}
                strokeOpacity={isCurrentSeg ? 0.9 : isCompletedSeg ? 0.6 : staticBridge ? 0.45 : 0}
                strokeLinecap="round"
                strokeDasharray={isCurrentSeg ? '6 4' : staticBridge ? '5 4' : undefined}
              />
            </g>
          );
        })}

        {/* Traveling packet capsule with halo + protocol tag */}
        {isPlaying && orb && (
          <g transform={`translate(${orb.x}, ${orb.y})`} filter="url(#tf-pkt-glow)">
            <circle r="11" fill={orb.color} fillOpacity="0.22" />
            <circle r="6" fill={orb.color} stroke="#ffffff" strokeWidth="2" />
            <g transform="translate(11, -11)">
              <rect
                x="0"
                y="0"
                width={Math.max(32, orb.label.length * 7 + 10)}
                height="16"
                rx="4"
                fill={orb.color}
              />
              <text
                x={Math.max(32, orb.label.length * 7 + 10) / 2}
                y="11"
                textAnchor="middle"
                fill="#ffffff"
                fontSize="9"
                fontWeight="700"
                fontFamily="ui-monospace, SFMono-Regular, Menlo, monospace"
              >
                {orb.label}
              </text>
            </g>
          </g>
        )}
      </svg>
    );
  };

  const renderPlaybackBanner = () => {
    if (!isPlaying || !activeHop) return null;
    const pct = Math.round(((activeStep + 1) / playableHops.length) * 100);
    const isDrop = activeHop.status === 'DROPPED' || activeHop.status === 'TIMEOUT';
    return (
      <div
        className={`mb-2 px-3 py-1.5 rounded-lg border flex flex-wrap items-center justify-between gap-2 text-[11px] ${
          isDrop
            ? 'bg-rose-50 dark:bg-rose-950/40 border-rose-200 dark:border-rose-900 text-rose-800 dark:text-rose-200'
            : 'bg-sky-50 dark:bg-sky-950/40 border-sky-200 dark:border-sky-900 text-sky-900 dark:text-sky-200'
        }`}
      >
        <div className="flex items-center gap-2 min-w-0">
          <span
            className={`w-2 h-2 rounded-full shrink-0 animate-pulse ${isDrop ? 'bg-rose-500' : 'bg-sky-500'}`}
          />
          <span className="font-mono font-bold shrink-0">
            步骤 {activeStep + 1}/{playableHops.length}
          </span>
          <span className="font-semibold truncate">
            #{activeHop.index} {activeHop.title}
          </span>
          {activeHop.subtitle && (
            <span className="font-mono text-[10px] opacity-75 truncate hidden sm:inline">
              ({activeHop.subtitle})
            </span>
          )}
        </div>
        <div className="flex items-center gap-2.5 shrink-0">
          <div className="w-24 h-1.5 rounded-full bg-white/80 dark:bg-slate-800 overflow-hidden">
            <div
              className={`h-full transition-all duration-300 ${isDrop ? 'bg-rose-500' : 'bg-sky-500'}`}
              style={{ width: `${pct}%` }}
            />
          </div>
          <button
            onClick={() => {
              setIsPlaying(false);
              setActiveStep(-1);
              setOrb(null);
              onPlayingChange?.(false);
            }}
            className="text-[10px] font-medium underline opacity-80 hover:opacity-100"
          >
            跳过
          </button>
        </div>
      </div>
    );
  };

  /* ============================================================================
   * LAYOUT 1: SAME-NODE COMPACT SINGLE NODE STACK
   * ==========================================================================*/
  if (!isCrossNode && !isExternal) {
    return (
      <div>
        {renderPlaybackBanner()}
        <div ref={wrapperRef} className="relative">
          {renderOverlaySVG()}

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

            {/* LAYER 1: CONTAINER (POD NETNS) + OS USER SPACE */}
            <div className="px-3 py-2 bg-gradient-to-b from-sky-50/45 to-white dark:from-sky-950/20 dark:to-slate-900 border-b border-dashed border-slate-200 dark:border-slate-800">
              <div className="grid grid-cols-1 lg:grid-cols-11 gap-2 items-stretch">
                <div className="lg:col-span-5">
                  <ContainerBox role="容器层 · Source Pod" ip={result.source.ip} port={result.srcPort}>
                    {srcPodHop && (
                      <CompactHopRow
                        ref={registerHopRef(srcPodHop.index)}
                        hop={srcPodHop}
                        selected={selectedHop?.index === srcPodHop.index}
                        playState={hopPlayState(srcPodHop)}
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
                        ref={registerHopRef(sameNodeDstTarget.index)}
                        hop={sameNodeDstTarget}
                        selected={selectedHop?.index === sameNodeDstTarget.index}
                        playState={hopPlayState(sameNodeDstTarget)}
                        onClick={() => onSelectHop(sameNodeDstTarget)}
                        tag="容器 eth0"
                      />
                    </ContainerBox>
                  ) : (
                    <div className="h-full rounded-lg border border-indigo-200 dark:border-indigo-800 bg-indigo-50/45 dark:bg-indigo-950/25 px-2.5 py-1.5 space-y-1">
                      <div className="flex items-center justify-between gap-2 text-[10px]">
                        <span className="font-bold text-indigo-700 dark:text-indigo-300 flex items-center gap-1">
                          <Terminal className="w-3 h-3" />
                          OS 用户态 · hostNetwork (无容器 Netns)
                        </span>
                        <span className="font-mono text-emerald-600 dark:text-emerald-400">↑ 经 lo 直达</span>
                      </div>
                      {sameNodeDstTarget && (
                        <CompactHopRow
                          ref={registerHopRef(sameNodeDstTarget.index)}
                          hop={sameNodeDstTarget}
                          selected={selectedHop?.index === sameNodeDstTarget.index}
                          playState={hopPlayState(sameNodeDstTarget)}
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
            <div className="px-3 py-0.5 bg-slate-100/80 dark:bg-slate-950/80 border-b border-slate-200 dark:border-slate-800 grid grid-cols-1 lg:grid-cols-11 gap-2 items-center text-[10px] font-mono">
              <div className="lg:col-span-5 flex justify-center">
                <StaticConnector
                  direction="down"
                  label={`veth pair (eth0 → ${srcVethHop?.interface || 'cali*'})`}
                  status={srcVethHop?.status || 'OBSERVED'}
                />
              </div>
              <div className="hidden lg:block lg:col-span-1 text-center text-[9px] text-slate-400">User / Kernel</div>
              <div className="lg:col-span-5 flex justify-center">
                {!isSameNodeHostLocal && sameNodeDstVeth ? (
                  <StaticConnector
                    direction="up"
                    label={`veth pair (${sameNodeDstVeth.interface || 'cali*'} → eth0)`}
                    status={sameNodeDstTarget?.status || sameNodeDstVeth.status}
                  />
                ) : (
                  <StaticConnector
                    direction="up"
                    label="内核 lo 回环 → 宿主机用户态 Socket"
                    status={sameNodeDstTarget?.status || 'DELIVERED'}
                  />
                )}
              </div>
            </div>

            {/* LAYER 2: OS KERNEL SPACE */}
            <div className="px-3 py-2 bg-emerald-50/25 dark:bg-emerald-950/15 space-y-1.5">
              <div className="flex items-center justify-between text-[10px]">
                <span className="font-bold text-emerald-900 dark:text-emerald-300 flex items-center gap-1">
                  <Layers className="w-3 h-3 text-emerald-600" />
                  OS 内核态 · Linux Kernel Space（虚拟网卡 · Netfilter 策略/DNAT · L3 路由折返）
                </span>
                <span className="font-mono text-[9px] text-emerald-700/80 dark:text-emerald-400/80">同节点内核闭环</span>
              </div>

              <div className="grid grid-cols-1 lg:grid-cols-11 gap-2 items-center">
                <div className="lg:col-span-5">
                  {srcVethHop && (
                    <CompactHopRow
                      ref={registerHopRef(srcVethHop.index)}
                      hop={srcVethHop}
                      selected={selectedHop?.index === srcVethHop.index}
                      playState={hopPlayState(srcVethHop)}
                      onClick={() => onSelectHop(srcVethHop)}
                      tag="宿主机 veth"
                    />
                  )}
                </div>
                <div className="hidden lg:flex lg:col-span-1 justify-center text-[10px] font-mono text-slate-400">↓ / ↑</div>
                <div className="lg:col-span-5">
                  {!isSameNodeHostLocal && sameNodeDstVeth ? (
                    <CompactHopRow
                      ref={registerHopRef(sameNodeDstVeth.index)}
                      hop={sameNodeDstVeth}
                      selected={selectedHop?.index === sameNodeDstVeth.index}
                      playState={hopPlayState(sameNodeDstVeth)}
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

              <div className="rounded-lg border border-emerald-200/80 dark:border-emerald-900/60 bg-white/85 dark:bg-slate-900/75 p-2 space-y-1.5">
                <div className="grid grid-cols-1 lg:grid-cols-11 gap-2 items-center">
                  <div className="lg:col-span-5">
                    {srcEgressPolicyHop && (
                      <CompactHopRow
                        ref={registerHopRef(srcEgressPolicyHop.index)}
                        hop={srcEgressPolicyHop}
                        selected={selectedHop?.index === srcEgressPolicyHop.index}
                        playState={hopPlayState(srcEgressPolicyHop)}
                        onClick={() => onSelectHop(srcEgressPolicyHop)}
                        tag="Egress 策略"
                      />
                    )}
                  </div>
                  <div className="hidden lg:flex lg:col-span-1 justify-center">
                    <ArrowRight className="w-3.5 h-3.5 text-slate-300 dark:text-slate-600" />
                  </div>
                  <div className="lg:col-span-5">
                    {!isSameNodeHostLocal && sameNodeIngressPolicy ? (
                      <CompactHopRow
                        ref={registerHopRef(sameNodeIngressPolicy.index)}
                        hop={sameNodeIngressPolicy}
                        selected={selectedHop?.index === sameNodeIngressPolicy.index}
                        playState={hopPlayState(sameNodeIngressPolicy)}
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

                <div className="flex flex-wrap items-center gap-2 pt-1.5 border-t border-dashed border-slate-200 dark:border-slate-800">
                  {kubeProxyHop && (
                    <>
                      <div className="flex-1 min-w-[200px]">
                        <CompactHopRow
                          ref={registerHopRef(kubeProxyHop.index)}
                          hop={kubeProxyHop}
                          selected={selectedHop?.index === kubeProxyHop.index}
                          playState={hopPlayState(kubeProxyHop)}
                          onClick={() => onSelectHop(kubeProxyHop)}
                          tag="Service DNAT"
                        />
                      </div>
                      <ArrowRight className="w-3.5 h-3.5 text-amber-500 shrink-0" />
                    </>
                  )}
                  {routeHop && (
                    <div className="flex-1 min-w-[200px]">
                      <CompactHopRow
                        ref={registerHopRef(routeHop.index)}
                        hop={routeHop}
                        selected={selectedHop?.index === routeHop.index}
                        playState={hopPlayState(routeHop)}
                        onClick={() => onSelectHop(routeHop)}
                        tag="内核 L3 路由"
                      />
                    </div>
                  )}
                  <ArrowRight className="w-3.5 h-3.5 text-teal-500 shrink-0" />
                  <div className="px-2 py-1.5 rounded-lg bg-teal-50/70 dark:bg-teal-950/30 border border-dashed border-teal-300 dark:border-teal-800 text-[10px] font-mono text-teal-800 dark:text-teal-300 whitespace-nowrap">
                    {isSameNodeHostLocal ? 'Local Table → lo 折返 ↑' : 'Calico L3 虚拟路由直转 ↑'}
                  </div>
                </div>
              </div>
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
        </div>
      </div>
    );
  }

  /* ============================================================================
   * LAYOUT 2 & 3: CROSS-NODE (2 TOWERS + CENTERED SYMMETRICAL U-BRIDGE) OR EXTERNAL
   * ==========================================================================*/
  return (
    <div>
      {renderPlaybackBanner()}
      <div ref={wrapperRef} className="relative space-y-2">
        {renderOverlaySVG()}

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

            {/* LAYER 1: CONTAINER + OS USER SPACE */}
            <div className="px-2.5 py-1.5 bg-gradient-to-b from-sky-50/45 to-white dark:from-sky-950/20 dark:to-slate-900 border-b border-dashed border-slate-200 dark:border-slate-800">
              <div className="grid grid-cols-1 md:grid-cols-12 gap-2 items-stretch">
                <div className="md:col-span-8">
                  <ContainerBox role="容器层 · Source Pod" ip={result.source.ip} port={result.srcPort}>
                    {srcPodHop && (
                      <CompactHopRow
                        ref={registerHopRef(srcPodHop.index)}
                        hop={srcPodHop}
                        selected={selectedHop?.index === srcPodHop.index}
                        playState={hopPlayState(srcPodHop)}
                        onClick={() => onSelectHop(srcPodHop)}
                        tag="容器 eth0"
                      />
                    )}
                  </ContainerBox>
                </div>
                <div className="md:col-span-4 rounded-lg border border-indigo-200/80 dark:border-indigo-900/60 bg-indigo-50/40 dark:bg-indigo-950/20 px-2 py-1.5 flex flex-col justify-between gap-1">
                  <div className="text-[10px] font-bold text-indigo-800 dark:text-indigo-300 flex items-center gap-1">
                    <Terminal className="w-3 h-3 shrink-0" />
                    OS 用户态 (控制面)
                  </div>
                  <div className="flex flex-col gap-0.5">
                    <ControlPlanePill name="calico-node" detail="Felix/BIRD" />
                    <ControlPlanePill name="kube-proxy" detail={result.kubeProxyMode} />
                  </div>
                </div>
              </div>
            </div>

            {/* NETNS / USER-KERNEL BOUNDARY */}
            <div className="py-0.5 px-2.5 bg-slate-100/80 dark:bg-slate-950/80 border-b border-slate-200 dark:border-slate-800 flex items-center justify-between text-[10px] font-mono">
              <span className="text-[9px] text-slate-400">Netns · User/Kernel</span>
              <StaticConnector
                direction="down"
                label={`veth pair (eth0 → ${srcVethHop?.interface || 'cali*'})`}
                status={srcVethHop?.status || 'OBSERVED'}
              />
              <span className="w-10 hidden sm:inline" />
            </div>

            {/* LAYER 2: OS KERNEL SPACE (DOWNWARD ↓) */}
            <div className="px-2.5 py-1.5 bg-emerald-50/25 dark:bg-emerald-950/15 space-y-1 flex-1 flex flex-col justify-between">
              <div className="flex items-center justify-between text-[10px]">
                <span className="font-bold text-emerald-900 dark:text-emerald-300 flex items-center gap-1">
                  <Layers className="w-3 h-3 text-emerald-600" />
                  OS 内核态 · Kernel Space (下行 ↓)
                </span>
                <span className="font-mono text-[9px] text-emerald-700/75 dark:text-emerald-400/75">
                  veth → Netfilter/FIB → vSwitch/Tunnel
                </span>
              </div>

              <div className="space-y-1">
                {srcVethHop && (
                  <CompactHopRow
                    ref={registerHopRef(srcVethHop.index)}
                    hop={srcVethHop}
                    selected={selectedHop?.index === srcVethHop.index}
                    playState={hopPlayState(srcVethHop)}
                    onClick={() => onSelectHop(srcVethHop)}
                    tag="宿主机 veth"
                  />
                )}
                <StaticMicroArrow direction="down" status={srcEgressPolicyHop?.status || 'PASSED'} />

                <div className={`grid grid-cols-1 ${kubeProxyHop ? 'sm:grid-cols-3' : 'sm:grid-cols-2'} gap-1.5`}>
                  {srcEgressPolicyHop && (
                    <CompactHopRow
                      ref={registerHopRef(srcEgressPolicyHop.index)}
                      hop={srcEgressPolicyHop}
                      selected={selectedHop?.index === srcEgressPolicyHop.index}
                      playState={hopPlayState(srcEgressPolicyHop)}
                      onClick={() => onSelectHop(srcEgressPolicyHop)}
                      tag="Egress"
                      narrow
                    />
                  )}
                  {kubeProxyHop && (
                    <CompactHopRow
                      ref={registerHopRef(kubeProxyHop.index)}
                      hop={kubeProxyHop}
                      selected={selectedHop?.index === kubeProxyHop.index}
                      playState={hopPlayState(kubeProxyHop)}
                      onClick={() => onSelectHop(kubeProxyHop)}
                      tag="DNAT"
                      narrow
                    />
                  )}
                  {routeHop && (
                    <CompactHopRow
                      ref={registerHopRef(routeHop.index)}
                      hop={routeHop}
                      selected={selectedHop?.index === routeHop.index}
                      playState={hopPlayState(routeHop)}
                      onClick={() => onSelectHop(routeHop)}
                      tag="FIB 路由"
                      narrow
                    />
                  )}
                </div>
                <StaticMicroArrow direction="down" status={srcTunnelHop?.status || srcNicHop?.status || 'PASSED'} />

                {srcTunnelHop ? (
                  <CompactHopRow
                    ref={registerHopRef(srcTunnelHop.index)}
                    hop={srcTunnelHop}
                    selected={selectedHop?.index === srcTunnelHop.index}
                    playState={hopPlayState(srcTunnelHop)}
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
            <div className="px-2.5 py-1.5 bg-amber-50/30 dark:bg-slate-900/90 border-t border-slate-200 dark:border-slate-800 space-y-1">
              <div className="flex items-center justify-between text-[10px]">
                <span className="font-bold text-amber-800 dark:text-amber-400 flex items-center gap-1">
                  <HardDrive className="w-3 h-3" />
                  物理网卡层 · Physical NIC
                </span>
                {srcNodeMeta?.nicMTU && <span className="font-mono text-[9px] text-slate-500">MTU {srcNodeMeta.nicMTU}</span>}
              </div>
              {srcNicHop && (
                <CompactHopRow
                  ref={registerHopRef(srcNicHop.index)}
                  hop={srcNicHop}
                  selected={selectedHop?.index === srcNicHop.index}
                  playState={hopPlayState(srcNicHop)}
                  onClick={() => onSelectHop(srcNicHop)}
                  tag="物理网卡出口"
                />
              )}
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

              {/* LAYER 1: DEST CONTAINER OR HOST USER SPACE */}
              <div className="px-2.5 py-1.5 bg-gradient-to-b from-sky-50/45 to-white dark:from-sky-950/20 dark:to-slate-900 border-b border-dashed border-slate-200 dark:border-slate-800">
                {!isCrossNodeHostNet ? (
                  <div className="grid grid-cols-1 md:grid-cols-12 gap-2 items-stretch">
                    <div className="md:col-span-8">
                      {dstTargetHop && (
                        <ContainerBox
                          role="容器层 · Destination Pod"
                          ip={result.resolvedBackend?.ip || result.destination.ip}
                          port={result.resolvedBackend?.port || result.dstPort}
                        >
                          <CompactHopRow
                            ref={registerHopRef(dstTargetHop.index)}
                            hop={dstTargetHop}
                            selected={selectedHop?.index === dstTargetHop.index}
                            playState={hopPlayState(dstTargetHop)}
                            onClick={() => onSelectHop(dstTargetHop)}
                            tag="容器 eth0"
                          />
                        </ContainerBox>
                      )}
                    </div>
                    <div className="md:col-span-4 rounded-lg border border-indigo-200/80 dark:border-indigo-900/60 bg-indigo-50/40 dark:bg-indigo-950/20 px-2 py-1.5 flex flex-col justify-between gap-1">
                      <div className="text-[10px] font-bold text-indigo-800 dark:text-indigo-300 flex items-center gap-1">
                        <Terminal className="w-3 h-3 shrink-0" />
                        OS 用户态 (控制面)
                      </div>
                      <ControlPlanePill name="calico-node" detail="Felix/BIRD" />
                      <span className="text-[9px] font-mono text-indigo-600/75 dark:text-indigo-400/75">↓ Ingress Policy</span>
                    </div>
                  </div>
                ) : (
                  <div className="grid grid-cols-1 md:grid-cols-12 gap-2 items-stretch">
                    <div className="md:col-span-4 rounded-lg border border-dashed border-slate-300 dark:border-slate-700 bg-slate-50/70 dark:bg-slate-900/40 px-2 py-1 flex flex-col justify-center text-center">
                      <span className="text-[10px] font-semibold text-slate-500">容器层 (跳过)</span>
                      <span className="text-[9px] text-slate-400">hostNetwork / Node</span>
                    </div>
                    <div className="md:col-span-8 rounded-lg border border-indigo-200 dark:border-indigo-800 bg-indigo-50/45 dark:bg-indigo-950/25 px-2.5 py-1.5 space-y-1">
                      <div className="flex items-center justify-between text-[10px]">
                        <span className="font-bold text-indigo-700 dark:text-indigo-300 flex items-center gap-1">
                          <Terminal className="w-3 h-3" />
                          OS 用户态 · hostNetwork
                        </span>
                        <span className="font-mono text-emerald-600 dark:text-emerald-400">↑ 内核直交</span>
                      </div>
                      {dstTargetHop && (
                        <CompactHopRow
                          ref={registerHopRef(dstTargetHop.index)}
                          hop={dstTargetHop}
                          selected={selectedHop?.index === dstTargetHop.index}
                          playState={hopPlayState(dstTargetHop)}
                          onClick={() => onSelectHop(dstTargetHop)}
                          tag="宿主机用户态"
                        />
                      )}
                    </div>
                  </div>
                )}
              </div>

              {/* NETNS / USER-KERNEL BOUNDARY (UPWARD ↑) */}
              <div className="py-0.5 px-2.5 bg-slate-100/80 dark:bg-slate-950/80 border-b border-slate-200 dark:border-slate-800 flex items-center justify-between text-[10px] font-mono">
                <span className="text-[9px] text-slate-400">Netns · User/Kernel</span>
                {!isCrossNodeHostNet ? (
                  <StaticConnector
                    direction="up"
                    label={`veth pair (${dstVethHop?.interface || 'cali*'} → eth0)`}
                    status={dstTargetHop?.status || dstVethHop?.status || 'DELIVERED'}
                  />
                ) : (
                  <StaticConnector
                    direction="up"
                    label="内核 INPUT 链 → 宿主机用户态 Socket"
                    status={dstTargetHop?.status || 'DELIVERED'}
                  />
                )}
                <span className="w-10 hidden sm:inline" />
              </div>

              {/* LAYER 2: OS KERNEL SPACE (UPWARD ↑) */}
              <div className="px-2.5 py-1.5 bg-emerald-50/25 dark:bg-emerald-950/15 space-y-1 flex-1 flex flex-col justify-between">
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
                  <div className="space-y-1">
                    {dstVethHop && (
                      <CompactHopRow
                        ref={registerHopRef(dstVethHop.index)}
                        hop={dstVethHop}
                        selected={selectedHop?.index === dstVethHop.index}
                        playState={hopPlayState(dstVethHop)}
                        onClick={() => onSelectHop(dstVethHop)}
                        tag="目标 veth"
                      />
                    )}
                    <StaticMicroArrow direction="up" status={dstVethHop?.status || 'PASSED'} />
                    {dstIngressPolicy && (
                      <CompactHopRow
                        ref={registerHopRef(dstIngressPolicy.index)}
                        hop={dstIngressPolicy}
                        selected={selectedHop?.index === dstIngressPolicy.index}
                        playState={hopPlayState(dstIngressPolicy)}
                        onClick={() => onSelectHop(dstIngressPolicy)}
                        tag="Ingress 策略"
                      />
                    )}
                    <StaticMicroArrow direction="up" status={dstIngressPolicy?.status || 'PASSED'} />
                    {dstTunnelHop ? (
                      <CompactHopRow
                        ref={registerHopRef(dstTunnelHop.index)}
                        hop={dstTunnelHop}
                        selected={selectedHop?.index === dstTunnelHop.index}
                        playState={hopPlayState(dstTunnelHop)}
                        onClick={() => onSelectHop(dstTunnelHop)}
                        tag="隧道解封装"
                      />
                    ) : (
                      <VirtualSwitchStrip detail={tunnelIfaceHint(dstNodeMeta, dstTunnelHop)} upward />
                    )}
                  </div>
                ) : (
                  <div className="space-y-1.5 my-auto">
                    <StaticMicroArrow direction="up" status={dstTargetHop?.status || 'DELIVERED'} />
                    <div className="rounded-lg border border-emerald-200/80 dark:border-emerald-800/70 bg-white/90 dark:bg-slate-900/80 px-2.5 py-1.5 flex items-center justify-between gap-2 text-[11px]">
                      <div className="min-w-0">
                        <div className="font-semibold text-slate-800 dark:text-slate-100">内核本地协议栈 (Local Delivery · INPUT)</div>
                        <div className="text-[10px] font-mono text-slate-500 truncate">本机宿主机地址直交用户态，跳过 Pod veth</div>
                      </div>
                      <StatusPill status={dstTargetHop?.status || 'PASSED'} compact />
                    </div>
                    <StaticMicroArrow direction="up" status={dstNicHop?.status || 'PASSED'} />
                    {dstTunnelHop ? (
                      <CompactHopRow
                        ref={registerHopRef(dstTunnelHop.index)}
                        hop={dstTunnelHop}
                        selected={selectedHop?.index === dstTunnelHop.index}
                        playState={hopPlayState(dstTunnelHop)}
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
              <div className="px-2.5 py-1.5 bg-amber-50/30 dark:bg-slate-900/90 border-t border-slate-200 dark:border-slate-800 space-y-1">
                <div className="flex items-center justify-between text-[10px]">
                  <span className="font-bold text-amber-800 dark:text-amber-400 flex items-center gap-1">
                    <HardDrive className="w-3 h-3" />
                    物理网卡层 · Physical NIC
                  </span>
                  {dstNodeMeta?.nicMTU && <span className="font-mono text-[9px] text-slate-500">MTU {dstNodeMeta.nicMTU}</span>}
                </div>
                {dstNicHop && (
                  <CompactHopRow
                    ref={registerHopRef(dstNicHop.index)}
                    hop={dstNicHop}
                    selected={selectedHop?.index === dstNicHop.index}
                    playState={hopPlayState(dstNicHop)}
                    onClick={() => onSelectHop(dstNicHop)}
                    tag="物理网卡入口"
                  />
                )}
              </div>

              <NodeBaseplate roleLabel="DEST NODE" nodeName={dstNodeName || 'Dest Node'} meta={dstNodeMeta} />
            </div>
          ) : (
            /* ==================== COMPACT EXTERNAL L-BRANCH ==================== */
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

                {fabricHop && (
                  <CompactHopRow
                    ref={registerHopRef(fabricHop.index)}
                    hop={fabricHop}
                    selected={selectedHop?.index === fabricHop.index}
                    playState={hopPlayState(fabricHop)}
                    onClick={() => onSelectHop(fabricHop)}
                    tag="外部网关"
                  />
                )}
                <StaticMicroArrow direction="down" status={externalHop?.status || 'DELIVERED'} />
                {externalHop && (
                  <CompactHopRow
                    ref={registerHopRef(externalHop.index)}
                    hop={externalHop}
                    selected={selectedHop?.index === externalHop.index}
                    playState={hopPlayState(externalHop)}
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

        {/* ==================== CENTERED SYMMETRICAL BOTTOM U-BRIDGE FOR CROSS-NODE ==================== */}
        {isCrossNode && fabricHop && (
          <div className="rounded-xl border border-dashed border-violet-300/90 dark:border-violet-800 bg-violet-50/40 dark:bg-violet-950/20 px-3 py-2 grid grid-cols-1 md:grid-cols-12 items-center gap-2">
            <div className="md:col-span-4 flex items-center gap-2 text-[11px] min-w-0">
              <Globe className="w-3.5 h-3.5 text-violet-600 dark:text-violet-400 shrink-0" />
              <div className="min-w-0">
                <div className="font-semibold text-slate-800 dark:text-slate-100 truncate">底层物理网络 / 隧道织物</div>
                <div className="font-mono text-[9px] text-slate-500 dark:text-slate-400 truncate">
                  {srcNodeMeta?.internalIP || result.source.nodeName} ({srcNicHop?.interface || 'ens4'})
                  {' ──▶ '}
                  {dstNodeMeta?.internalIP || dstNodeName} ({dstNicHop?.interface || 'ens4'})
                </div>
              </div>
            </div>

            <div className="md:col-span-4">
              <CompactHopRow
                ref={registerHopRef(fabricHop.index)}
                hop={fabricHop}
                selected={selectedHop?.index === fabricHop.index}
                playState={hopPlayState(fabricHop)}
                onClick={() => onSelectHop(fabricHop)}
                tag="物理/隧道织物"
              />
            </div>

            <div className="hidden md:flex md:col-span-4 items-center justify-end gap-1.5 text-[10px] font-mono text-violet-600 dark:text-violet-400">
              <span>{srcNicHop?.interface || 'ens4'}</span>
              <ArrowRight className="w-3 h-3" />
              <span>Fabric ({result.encapMode})</span>
              <ArrowRight className="w-3 h-3" />
              <span>{dstNicHop?.interface || 'ens4'} ↑</span>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

/* ==================== SVG Trajectory Builder ==================== */

interface BoxRect {
  left: number;
  right: number;
  top: number;
  bottom: number;
  cx: number;
  cy: number;
}

function buildInterHopPath(
  a: BoxRect,
  b: BoxRect,
  aHop: TraceHop,
  bHop: TraceHop,
  isExternal: boolean,
): string {
  // 1. Cross-Node: Source Physical NIC (left) -> Centered Bottom Underlay Fabric (L-curve down & right)
  if (aHop.lane === 'source-node' && bHop.lane === 'fabric' && !isExternal) {
    const x1 = a.cx;
    const y1 = a.bottom;
    const x2 = b.left;
    const y2 = b.cy;
    const r = Math.min(16, Math.abs(y2 - y1) / 2, Math.abs(x2 - x1) / 2);
    if (x2 > x1 + 10 && y2 > y1 + 10) {
      return `M ${x1} ${y1} L ${x1} ${y2 - r} Q ${x1} ${y2} ${x1 + r} ${y2} L ${x2} ${y2}`;
    }
  }

  // 2. Cross-Node: Centered Bottom Underlay Fabric -> Dest Physical NIC (right, L-curve right & up)
  if (aHop.lane === 'fabric' && bHop.lane === 'dest-node') {
    const x1 = a.right;
    const y1 = a.cy;
    const x2 = b.cx;
    const y2 = b.bottom;
    const r = Math.min(16, Math.abs(y1 - y2) / 2, Math.abs(x2 - x1) / 2);
    if (x2 > x1 + 10 && y1 > y2 + 10) {
      return `M ${x1} ${y1} L ${x2 - r} ${y1} Q ${x2} ${y1} ${x2} ${y1 - r} L ${x2} ${y2}`;
    }
  }

  // 3. External: Source NIC (left) -> External Gateway (right)
  if (aHop.lane === 'source-node' && bHop.lane === 'fabric' && isExternal) {
    const x1 = a.right;
    const y1 = a.cy;
    const x2 = b.left;
    const y2 = b.cy;
    const midX = (x1 + x2) / 2;
    return `M ${x1} ${y1} C ${midX} ${y1}, ${midX} ${y2}, ${x2} ${y2}`;
  }

  // 4. Same row horizontal RIGHT (e.g., Egress Policy -> Kernel Route, or DNAT -> Route)
  if (b.left >= a.right - 6 && Math.abs(a.cy - b.cy) < 42) {
    return `M ${a.right} ${a.cy} L ${b.left} ${b.cy}`;
  }

  // 5. Same-Node Kernel U-Turn going UP-RIGHT (e.g., Kernel Route -> Ingress Policy or Host Local Stack)
  if (b.bottom <= a.top + 8 && b.cx > a.cx + 40) {
    const x1 = a.right;
    const y1 = a.cy;
    const x2 = b.cx;
    const y2 = b.bottom;
    const r = Math.min(16, Math.abs(y1 - y2) / 2, Math.abs(x2 - x1) / 2);
    if (x2 > x1 + 8) {
      return `M ${x1} ${y1} L ${x2 - r} ${y1} Q ${x2} ${y1} ${x2} ${y1 - r} L ${x2} ${y2}`;
    }
    const midY = (a.top + b.bottom) / 2;
    return `M ${a.cx} ${a.top} C ${a.cx} ${midY}, ${b.cx} ${midY}, ${b.cx} ${b.bottom}`;
  }

  // 6. Vertical DOWN (Source Node layers: eth0 -> veth -> policy -> route -> NIC)
  if (b.top >= a.bottom - 6) {
    const midY = (a.bottom + b.top) / 2;
    if (Math.abs(a.cx - b.cx) < 18) {
      return `M ${a.cx} ${a.bottom} L ${b.cx} ${b.top}`;
    }
    return `M ${a.cx} ${a.bottom} C ${a.cx} ${midY}, ${b.cx} ${midY}, ${b.cx} ${b.top}`;
  }

  // 7. Vertical UP (Dest Node layers: NIC -> policy -> veth -> eth0 / hostNetwork)
  if (a.top >= b.bottom - 6) {
    const midY = (a.top + b.bottom) / 2;
    if (Math.abs(a.cx - b.cx) < 18) {
      return `M ${a.cx} ${a.top} L ${b.cx} ${b.bottom}`;
    }
    return `M ${a.cx} ${a.top} C ${a.cx} ${midY}, ${b.cx} ${midY}, ${b.cx} ${b.bottom}`;
  }

  // Fallback smooth curve
  const midX = (a.cx + b.cx) / 2;
  return `M ${a.cx} ${a.cy} C ${midX} ${a.cy}, ${midX} ${b.cy}, ${b.cx} ${b.cy}`;
}

function segmentColor(targetHop: TraceHop): string {
  const s = (targetHop.status || '').toUpperCase();
  if (s === 'DROPPED' || s === 'TIMEOUT') return '#f43f5e';
  if (s === 'NAT' || s === 'ENCAPSULATED' || s === 'DECAPSULATED' || s === 'RESET') return '#f59e0b';
  if (targetHop.lane === 'fabric') return '#7c3aed';
  if (targetHop.lane === 'dest-node' || targetHop.status === 'DELIVERED') return '#10b981';
  return '#0ea5e9';
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

function StaticConnector({
  direction,
  label,
  status,
}: {
  direction: 'down' | 'up';
  label?: string;
  status: string;
}) {
  const Icon = direction === 'down' ? ArrowDown : ArrowUp;
  return (
    <div className={`inline-flex items-center gap-1 text-[10px] font-mono ${connectorColor(status)}`}>
      <Icon className="w-3 h-3 shrink-0" />
      {label && <span className="text-slate-500 dark:text-slate-400">{label}</span>}
    </div>
  );
}

function StaticMicroArrow({ direction, status }: { direction: 'down' | 'up'; status: string }) {
  const Icon = direction === 'down' ? ArrowDown : ArrowUp;
  return (
    <div className={`flex justify-center leading-none ${connectorColor(status)}`}>
      <Icon className="w-3 h-3" />
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

const CompactHopRow = React.forwardRef<
  HTMLButtonElement,
  {
    hop: TraceHop;
    selected: boolean;
    playState: 'idle' | 'active' | 'visited' | 'pending';
    onClick: () => void;
    tag: string;
    narrow?: boolean;
  }
>(function CompactHopRow({ hop, selected, playState, onClick, tag, narrow = false }, ref) {
  const isBad = hop.status === 'DROPPED' || hop.status === 'TIMEOUT';
  const isSkipped = hop.status === 'SKIPPED';
  const isWarn =
    hop.status === 'RESET' ||
    hop.status === 'NAT' ||
    hop.status === 'ENCAPSULATED' ||
    hop.status === 'DECAPSULATED';

  const baseCls = isBad
    ? 'border-rose-400 bg-rose-50/90 dark:bg-rose-950/40'
    : isSkipped
      ? 'border-slate-200 dark:border-slate-800 border-dashed bg-slate-50/60 dark:bg-slate-900/50 opacity-70'
      : isWarn
        ? 'border-amber-300 dark:border-amber-800 bg-white dark:bg-slate-900'
        : 'border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900';

  const playCls =
    playState === 'active'
      ? isBad
        ? 'border-rose-500 tf-hop-drop-active relative z-10'
        : 'border-sky-500 bg-sky-50/40 dark:bg-sky-950/30 tf-hop-active relative z-10'
      : playState === 'visited'
        ? 'border-emerald-300/90 dark:border-emerald-800'
        : playState === 'pending'
          ? 'opacity-45'
          : '';

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
      ref={ref}
      onClick={onClick}
      className={`w-full text-left rounded-lg border px-2 py-1 transition-all duration-300 hover:shadow-sm ${baseCls} ${playCls} ${
        selected && playState === 'idle' ? 'ring-2 ring-blue-500 border-blue-400' : ''
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
          {hop.deltaMs ? <span className="text-[9px] font-mono text-slate-400">+{hop.deltaMs}ms</span> : null}
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
});
