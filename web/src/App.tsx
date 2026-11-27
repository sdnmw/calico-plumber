import React, { useEffect, useState } from 'react';
import {
  LayoutDashboard,
  Share2,
  Grid,
  Route,
  Wrench,
  HeartPulse,
  ShieldCheck,
  Download,
  Search,
  Sun,
  Moon,
  BookOpen,
  ExternalLink,
} from 'lucide-react';
import { api } from './api/client';
import type { ClusterNetworkOverview, SearchResultItem, TraceFlowRequest } from './api/types';
import { OverviewPage } from './pages/OverviewPage';
import { TopologyPage } from './pages/TopologyPage';
import { IpamSearchPage } from './pages/IpamSearchPage';
import { TraceFlowPage } from './pages/TraceFlowPage';
import { TroubleshootPage } from './pages/TroubleshootPage';
import { HealthPage } from './pages/HealthPage';
import { AuditPage } from './pages/AuditPage';
import { OmniSearchModal } from './components/layout/OmniSearchModal';
import { PodInspectorDrawer } from './components/ipam/PodInspectorDrawer';

type NavTab = 'overview' | 'topology' | 'ipam' | 'traceflow' | 'troubleshoot' | 'audit' | 'health';

export interface TopologyContextSnapshot {
  windowSec: number;
  namespace: string;
  granularity: 'namespace' | 'workload' | 'pod' | 'node';
  viewMode: 'anomaly' | 'panorama';
  selectedEdgeId?: string | null;
}

export default function App() {
  const [tab, setTab] = useState<NavTab>('overview');
  const [dark, setDark] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const [overview, setOverview] = useState<ClusterNetworkOverview | null>(null);

  // Cross-page navigation context
  const [inspectPod, setInspectPod] = useState<{ ns: string; name: string } | null>(null);
  const [tracePrefill, setTracePrefill] = useState<Partial<TraceFlowRequest> | null>(null);
  const [topologyReturnCtx, setTopologyReturnCtx] = useState<TopologyContextSnapshot | null>(null);
  const [troubleshootPrefill, setTroubleshootPrefill] = useState<{
    ns: string;
    pod: string;
    tool: 'tcpdump' | 'tcp' | 'http';
  } | null>(null);
  const [healthJumpIntent, setHealthJumpIntent] = useState<{
    filter: 'all' | 'abnormal' | 'broken' | 'degraded' | 'no-endpoints' | 'healthy';
    ts: number;
  } | null>(null);

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark);
  }, [dark]);

  useEffect(() => {
    const loadSummary = () => api.getOverview().then(setOverview).catch(() => {});
    loadSummary();
    const timer = setInterval(loadSummary, 15000);
    return () => clearInterval(timer);
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setSearchOpen((v) => !v);
      } else if (e.key === 'Escape') {
        setSearchOpen(false);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const launchTrace = (req: Partial<TraceFlowRequest>, fromTopo?: TopologyContextSnapshot) => {
    setTracePrefill({ ...req });
    setTopologyReturnCtx(fromTopo || null);
    setTab('traceflow');
  };

  const launchTroubleshoot = (ns: string, pod: string, tool: 'tcpdump' | 'tcp' | 'http') => {
    setTroubleshootPrefill({ ns, pod, tool });
    setTab('troubleshoot');
  };

  const launchTraceFromSearch = (item: SearchResultItem) => {
    if (item.kind === 'Pod' && item.namespace) {
      launchTrace({ srcNamespace: item.namespace, srcPod: item.name });
    } else if (item.kind === 'Service' && item.namespace) {
      launchTrace({ targetType: 'service', dstNamespace: item.namespace, dstName: item.name });
    } else if (item.pods && item.pods.length > 0) {
      launchTrace({ srcNamespace: item.pods[0].namespace, srcPod: item.pods[0].name });
    }
  };

  const navItems: { id: NavTab; label: string; icon: React.ComponentType<{ className?: string }> }[] = [
    { id: 'overview', label: '集群总览', icon: LayoutDashboard },
    { id: 'topology', label: '流量拓扑', icon: Share2 },
    { id: 'ipam', label: 'IPAM 与搜索', icon: Grid },
    { id: 'traceflow', label: 'TraceFlow', icon: Route },
    { id: 'troubleshoot', label: 'Pod 排障', icon: Wrench },
    { id: 'audit', label: '路由/ARP 状态', icon: ShieldCheck },
    { id: 'health', label: '全局健康', icon: HeartPulse },
  ];

  return (
    <div className="min-h-screen flex bg-[#FBF9F6] dark:bg-[#14110F] text-[#201B18] dark:text-[#F5F0EA]">
      {/* Left Navigation Sidebar */}
      <aside className="w-[216px] shrink-0 bg-white dark:bg-[#1C1815] border-r border-[#E9E1D6] dark:border-[#2E2721] flex flex-col justify-between sticky top-0 h-screen">
        <div>
          {/* Brand Mark */}
          <div className="h-14 px-4 flex items-center gap-2.5 border-b border-[#F2ECE4] dark:border-[#2A231D] bg-gradient-to-br from-[#FFFBF5] via-white to-[#FFF4E4]/40 dark:from-[#241D17] dark:via-[#1C1815] dark:to-[#1C1612]">
            <img
              src="/logo.png"
              alt="calico-plumber Logo"
              className="w-9 h-9 object-contain shrink-0 drop-shadow-sm"
            />
            <div>
              <div className="text-sm font-extrabold tracking-tight text-[#201B18] dark:text-white leading-tight">
                calico-plumber
              </div>
              <div className="text-[10px] text-[#6C4824]/80 dark:text-[#C9A882] font-mono">Calico NetDiag & Flow</div>
            </div>
          </div>

          {/* Nav Links */}
          <nav className="p-2.5 space-y-1">
            {navItems.map((item) => {
              const Icon = item.icon;
              const active = tab === item.id;
              return (
                <button
                  key={item.id}
                  onClick={() => setTab(item.id)}
                  className={`w-full flex items-center gap-2.5 px-3 py-2 rounded-lg text-xs transition border ${
                    active
                      ? 'bg-[#FFF4E4] dark:bg-[#F08C18]/15 text-[#9E4A05] dark:text-[#FBB040] border-[#FAD4A1] dark:border-[#F08C18]/40 font-bold'
                      : 'border-transparent text-[#574D44] dark:text-[#B5A89B] hover:bg-[#F7F3EE] dark:hover:bg-[#26201B] hover:text-[#201B18] dark:hover:text-[#F5F0EA] font-medium'
                  }`}
                >
                  <Icon className={`w-4 h-4 ${active ? 'text-[#D9720B] dark:text-[#F08C18]' : ''}`} />
                  <span>{item.label}</span>
                </button>
              );
            })}
          </nav>
        </div>

        {/* Footer Cluster Fabric Badge */}
        <div className="p-3 border-t border-[#F2ECE4] dark:border-[#2A231D] bg-[#FDFBF8] dark:bg-[#191512] space-y-1.5 text-[11px]">
          <div className="flex items-center justify-between text-[#8C7F73]">
            <span>CNI Mode</span>
            <span className="font-mono font-bold text-[#9E4A05] dark:text-[#FBB040]">
              {overview?.primaryEncapMode || 'Detecting...'}
            </span>
          </div>
          <div className="flex items-center justify-between text-[#8C7F73]">
            <span>kube-proxy</span>
            <span className="font-mono font-semibold text-[#201B18] dark:text-[#E5DDD5]">{overview?.kubeProxyMode || '—'}</span>
          </div>
          <div className="flex items-center justify-between text-[#8C7F73]">
            <span>Telemetry</span>
            <span className="font-mono font-semibold text-emerald-700 dark:text-emerald-400">
              {overview ? `${overview.globalTraffic.ebpfNodes} eBPF / ${overview.globalTraffic.conntrackNodes} Legacy` : '—'}
            </span>
          </div>
        </div>
      </aside>

      {/* Main Content Area */}
      <div className="flex-1 flex flex-col min-w-0">
        {/* Sticky Topbar with Global Omni-Search */}
        <header className="h-14 px-5 bg-white/95 dark:bg-[#1C1815]/95 backdrop-blur border-b border-[#E9E1D6] dark:border-[#2E2721] flex items-center justify-between gap-4 sticky top-0 z-30">
          <button
            onClick={() => setSearchOpen(true)}
            className="flex items-center gap-2.5 px-3.5 py-1.5 rounded-lg bg-[#F7F3EE] dark:bg-[#25201C] hover:bg-[#EFE8DF] dark:hover:bg-[#2E2721] border border-[#E9E1D6] dark:border-[#362E27] text-xs text-[#6E6257] dark:text-[#A39587] w-full max-w-md transition"
          >
            <Search className="w-3.5 h-3.5 text-[#8C7F73]" />
            <span className="flex-1 text-left truncate">快速搜索 Pod IP / Deployment / Service / Node...</span>
            <kbd className="px-1.5 py-0.5 text-[10px] font-mono rounded bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#362E27] text-[#574D44] dark:text-[#C5B8AA]">
              ⌘K
            </kbd>
          </button>

          <div className="flex items-center gap-2.5">
            <span className="hidden sm:inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-[11px] font-semibold bg-emerald-50 dark:bg-emerald-950/40 text-emerald-700 dark:text-emerald-300 border border-emerald-200/80 dark:border-emerald-900">
              <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse" />
              {overview ? `${overview.nodes.length} Nodes · ${overview.podCount} Pods` : 'Connected'}
            </span>
            <a
              href={api.getBundleExportUrl()}
              download
              title="一键打包导出集群离线诊断报告 (HTML + 内核/路由/IPAM/TraceFlow JSON)"
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-semibold bg-[#F08C18] hover:bg-[#D9720B] border border-[#D9720B] text-white shadow-sm transition"
            >
              <Download className="w-3.5 h-3.5" />
              <span className="hidden md:inline">导出诊断包</span>
            </a>
            <a
              href="/docs/"
              target="_blank"
              rel="noopener noreferrer"
              title="打开原理、性能影响与使用教程文档"
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-white dark:bg-[#25201C] hover:bg-[#FFF4E4] dark:hover:bg-[#F08C18]/15 hover:text-[#9E4A05] dark:hover:text-[#FBB040] border border-[#E9E1D6] dark:border-[#362E27] text-[#574D44] dark:text-[#D4C7B9] transition"
            >
              <BookOpen className="w-3.5 h-3.5" />
              <span>文档</span>
              <ExternalLink className="w-3 h-3 opacity-60" />
            </a>
            <button
              onClick={() => setDark((v) => !v)}
              title="切换明暗主题"
              className="p-2 rounded-lg border border-[#E9E1D6] dark:border-[#362E27] bg-white dark:bg-[#25201C] hover:bg-[#FFF4E4] dark:hover:bg-[#2E2721] text-[#6C4824] dark:text-[#FBB040] transition"
            >
              {dark ? <Sun className="w-4 h-4 text-[#F08C18]" /> : <Moon className="w-4 h-4" />}
            </button>
          </div>
        </header>

        {/* Active Page View */}
        <main className="flex-1 min-w-0">
          {tab === 'overview' && (
            <OverviewPage
              onNavigate={(t) => setTab(t as NavTab)}
              onOpenHealth={(filter) => {
                setHealthJumpIntent(filter ? { filter, ts: Date.now() } : null);
                setTab('health');
              }}
            />
          )}
          {tab === 'topology' && (
            <TopologyPage
              initialContext={topologyReturnCtx}
              onLaunchTrace={launchTrace}
              onLaunchTCPDump={(ns, pod) => launchTroubleshoot(ns, pod, 'tcpdump')}
            />
          )}
          {tab === 'ipam' && (
            <IpamSearchPage
              onInspectPod={(ns, name) => setInspectPod({ ns, name })}
              onLaunchTrace={launchTrace}
              onLaunchTroubleshoot={launchTroubleshoot}
            />
          )}
          {tab === 'traceflow' && (
            <TraceFlowPage
              initialReq={tracePrefill}
              returnTopologyCtx={topologyReturnCtx}
              onReturnTopology={() => setTab('topology')}
            />
          )}
          {tab === 'troubleshoot' && (
            <TroubleshootPage
              initialTarget={troubleshootPrefill}
              onLaunchTrace={launchTrace}
              onNavigate={(t) => setTab(t as NavTab)}
            />
          )}
          {tab === 'audit' && <AuditPage />}
          {tab === 'health' && (
            <HealthPage
              onLaunchTrace={launchTrace}
              initialFilterIntent={healthJumpIntent}
            />
          )}
        </main>
      </div>

      {/* Global Modals & Drawers */}
      <OmniSearchModal
        open={searchOpen}
        onClose={() => setSearchOpen(false)}
        onInspectPod={(ns, name) => setInspectPod({ ns, name })}
        onLaunchTrace={launchTraceFromSearch}
        onLaunchTCPDump={(ns, name) => launchTroubleshoot(ns, name, 'tcpdump')}
      />

      {inspectPod && (
        <PodInspectorDrawer
          namespace={inspectPod.ns}
          podName={inspectPod.name}
          onClose={() => setInspectPod(null)}
          onLaunchTrace={launchTrace}
          onLaunchTroubleshoot={launchTroubleshoot}
        />
      )}
    </div>
  );
}
