import React, { useEffect, useRef, useState } from 'react';
import {
  Terminal,
  Activity,
  Globe,
  Play,
  Square,
  Download,
  RefreshCw,
  ArrowRight,
  ArrowLeft,
  ShieldCheck,
  CheckCircle2,
  XCircle,
} from 'lucide-react';
import { api } from '../api/client';
import type {
  HTTPProbeResponse,
  PacketFrame,
  PodBrief,
  PodNetworkDetail,
  TCPDumpControlMessage,
  TCPHandshakeResponse,
} from '../api/types';
import { SectionCard, StatusPill } from '../components/common/Badges';

interface TroubleshootPageProps {
  initialTarget?: { ns: string; pod: string; tool: 'tcpdump' | 'tcp' | 'http' } | null;
}

export function TroubleshootPage({ initialTarget }: TroubleshootPageProps) {
  const [pods, setPods] = useState<PodBrief[]>([]);
  const [ns, setNs] = useState('');
  const [podName, setPodName] = useState('');
  const [podDetail, setPodDetail] = useState<PodNetworkDetail | null>(null);
  const [activeTool, setActiveTool] = useState<'tcpdump' | 'tcp' | 'http'>('tcpdump');

  // TCPdump state
  const [targetIface, setTargetIface] = useState('pod:eth0');
  const [bpfFilter, setBpfFilter] = useState('');
  const [maxPackets, setMaxPackets] = useState(300);
  const [durationSec, setDurationSec] = useState(25);
  const [dumpRunning, setDumpRunning] = useState(false);
  const [dumpStatus, setDumpStatus] = useState('');
  const [captureId, setCaptureId] = useState('');
  const [pcapReady, setPcapReady] = useState(false);
  const [frames, setFrames] = useState<PacketFrame[]>([]);
  const [selectedFrame, setSelectedFrame] = useState<PacketFrame | null>(null);
  const wsRef = useRef<WebSocket | null>(null);

  // TCP Handshake state
  const [tcpHost, setTcpHost] = useState('kubernetes.default.svc.cluster.local');
  const [tcpPort, setTcpPort] = useState(443);
  const [tcpCount, setTcpCount] = useState(3);
  const [tcpLoading, setTcpLoading] = useState(false);
  const [tcpResult, setTcpResult] = useState<TCPHandshakeResponse | null>(null);
  const [tcpError, setTcpError] = useState('');

  // HTTP Probe state
  const [httpMethod, setHttpMethod] = useState('GET');
  const [httpURL, setHttpURL] = useState('https://kubernetes.default.svc.cluster.local:443/healthz');
  const [httpHostHdr, setHttpHostHdr] = useState('');
  const [httpInsecure, setHttpInsecure] = useState(true);
  const [httpLoading, setHttpLoading] = useState(false);
  const [httpResult, setHttpResult] = useState<HTTPProbeResponse | null>(null);
  const [httpError, setHttpError] = useState('');

  useEffect(() => {
    api.listPods().then((list) => {
      const running = list.filter((p) => p.phase === 'Running' && p.ip);
      setPods(running);
      if (!initialTarget && !podName && running.length > 0) {
        const def = running.find((p) => p.namespace === 'petclinic-training' || p.namespace === 'demo') || running[0];
        setNs(def.namespace);
        setPodName(def.name);
      }
    });
    return () => {
      wsRef.current?.close();
    };
  }, []);

  useEffect(() => {
    if (!initialTarget) return;
    setNs(initialTarget.ns);
    setPodName(initialTarget.pod);
    setActiveTool(initialTarget.tool);
  }, [initialTarget]);

  useEffect(() => {
    if (!ns || !podName) return;
    api.getPodDetail(ns, podName).then(setPodDetail).catch(() => setPodDetail(null));
  }, [ns, podName]);

  const namespaces = Array.from(new Set(pods.map((p) => p.namespace))).sort();
  const podsInNs = pods.filter((p) => !ns || p.namespace === ns);
  const selectedPodObj = pods.find((p) => p.namespace === ns && p.name === podName);

  // TCPdump start/stop
  const startDump = () => {
    if (!ns || !podName) return;
    wsRef.current?.close();
    setFrames([]);
    setSelectedFrame(null);
    setCaptureId('');
    setPcapReady(false);
    setDumpRunning(true);
    setDumpStatus('正在建立 WebSocket 抓包流...');

    const ws = new WebSocket(api.getTCPDumpWSUrl());
    wsRef.current = ws;

    ws.onopen = () => {
      ws.send(
        JSON.stringify({
          namespace: ns,
          podName,
          targetIface,
          bpfFilter,
          maxPackets,
          durationSec,
          snapLen: 256,
        }),
      );
    };
    ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data) as TCPDumpControlMessage;
        if (msg.type === 'started') {
          setCaptureId(msg.captureId || '');
          setDumpStatus(msg.message || `Capturing on ${msg.interface}`);
        } else if (msg.type === 'packet' && msg.frame) {
          setFrames((prev) => [...prev.slice(-1499), msg.frame!]);
        } else if (msg.type === 'done') {
          setDumpRunning(false);
          setPcapReady(Boolean(msg.pcapReady));
          if (msg.captureId) setCaptureId(msg.captureId);
          setDumpStatus(msg.message || '抓包已完成');
        } else if (msg.type === 'error') {
          setDumpRunning(false);
          setDumpStatus(`错误: ${msg.message}`);
        }
      } catch {
        // ignore
      }
    };
    ws.onerror = () => {
      setDumpRunning(false);
      setDumpStatus('WebSocket 连接中断');
    };
    ws.onclose = () => {
      setDumpRunning(false);
    };
  };

  const stopDump = () => {
    if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
      wsRef.current.send('stop');
    }
    setDumpRunning(false);
  };

  // TCP Handshake run
  const runTCP = async () => {
    if (!ns || !podName || !tcpHost) return;
    setTcpLoading(true);
    setTcpError('');
    try {
      const res = await api.runTCPHandshake({
        srcNamespace: ns,
        srcPod: podName,
        targetHost: tcpHost,
        targetPort: tcpPort,
        count: tcpCount,
        timeoutMs: 3000,
      });
      setTcpResult(res);
    } catch (e: unknown) {
      setTcpError(e instanceof Error ? e.message : String(e));
    } finally {
      setTcpLoading(false);
    }
  };

  // HTTP Probe run
  const runHTTP = async () => {
    if (!ns || !podName || !httpURL) return;
    setHttpLoading(true);
    setHttpError('');
    try {
      const res = await api.runHTTPProbe({
        srcNamespace: ns,
        srcPod: podName,
        url: httpURL,
        method: httpMethod,
        hostHeader: httpHostHdr || undefined,
        insecureSkipTLS: httpInsecure,
        followRedirects: true,
        timeoutMs: 5000,
      });
      setHttpResult(res);
    } catch (e: unknown) {
      setHttpError(e instanceof Error ? e.message : String(e));
    } finally {
      setHttpLoading(false);
    }
  };

  return (
    <div className="p-6 space-y-5 max-w-[1600px] mx-auto">
      <div>
        <h1 className="text-xl font-bold tracking-tight text-[#201B18] dark:text-[#F5F0EA]">
          Pod 深度排障工作台
        </h1>
        <p className="text-xs text-[#8C7F73] dark:text-[#A39587] mt-1">
          直接切入目标 Pod Netns（无需业务容器预装 tcpdump/curl/shell），执行实时抓包、TCP 三次握手报文分析与 HTTP/HTTPS 全链路耗时探测
        </p>
      </div>

      {/* Target Pod Selector & Live Context Strip */}
      <SectionCard className="p-4 space-y-3">
        <div className="flex flex-wrap items-center gap-3">
          <div className="flex items-center gap-2">
            <span className="text-xs font-semibold text-[#8C7F73]">目标 Pod:</span>
            <select
              value={ns}
              onChange={(e) => {
                const nextNs = e.target.value;
                setNs(nextNs);
                const first = pods.find((p) => p.namespace === nextNs);
                if (first) setPodName(first.name);
              }}
              className="px-2.5 py-1.5 text-xs rounded-lg bg-[#F7F3EE] dark:bg-[#25201C] border border-[#E9E1D6] dark:border-[#2E2721] outline-none"
            >
              {namespaces.map((n) => (
                <option key={n} value={n}>{n}</option>
              ))}
            </select>
            <select
              value={podName}
              onChange={(e) => setPodName(e.target.value)}
              className="px-2.5 py-1.5 text-xs rounded-lg bg-[#F7F3EE] dark:bg-[#25201C] border border-[#E9E1D6] dark:border-[#2E2721] font-mono min-w-[260px] outline-none"
            >
              {podsInNs.map((p) => (
                <option key={p.name} value={p.name}>
                  {p.name} ({p.ip})
                </option>
              ))}
            </select>
          </div>

          {podDetail && (
            <div className="flex flex-wrap items-center gap-2 text-[11px] font-mono text-[#574D44] dark:text-[#D5C9BC]">
              <span className="px-2 py-1 rounded bg-[#F7F3EE] dark:bg-[#25201C]">IP: <b>{podDetail.pod.ip}</b></span>
              <span className="px-2 py-1 rounded bg-[#F7F3EE] dark:bg-[#25201C]">Node: <b>{podDetail.pod.nodeName}</b></span>
              {podDetail.hostVeth && <span className="px-2 py-1 rounded bg-[#EDF5FC] dark:bg-[#155A96]/25 text-[#155A96] dark:text-[#7EB8E8] border border-[#C4DCF0] dark:border-[#4B84B8]/40">veth: <b>{podDetail.hostVeth}</b></span>}
              {podDetail.podMTU ? <span className="px-2 py-1 rounded bg-[#F7F3EE] dark:bg-[#25201C]">MTU: {podDetail.podMTU}</span> : null}
              <span className="px-2 py-1 rounded bg-[#F7F3EE] dark:bg-[#25201C]">Policies: {podDetail.networkPolicies?.length || 0}</span>
            </div>
          )}
        </div>

        {/* Tool Tabs */}
        <div className="flex items-center gap-2 pt-2 border-t border-[#F2ECE4] dark:border-[#2E2721]">
          {[
            { id: 'tcpdump', label: 'TCPdump 实时抓包', icon: Terminal },
            { id: 'tcp', label: 'TCP 三次握手检测', icon: Activity },
            { id: 'http', label: 'HTTP / HTTPS 瀑布流探测', icon: Globe },
          ].map((t) => {
            const Icon = t.icon;
            return (
              <button
                key={t.id}
                onClick={() => setActiveTool(t.id as 'tcpdump' | 'tcp' | 'http')}
                className={`inline-flex items-center gap-1.5 px-3.5 py-2 rounded-lg text-xs font-semibold transition ${
                  activeTool === t.id
                    ? 'bg-[#F08C18] text-white shadow-sm'
                    : 'bg-[#F7F3EE] dark:bg-[#25201C] text-[#574D44] dark:text-[#D5C9BC] hover:bg-[#FFF4E4] hover:text-[#9E4A05] dark:hover:bg-[#332B24]'
                }`}
              >
                <Icon className="w-3.5 h-3.5" />
                {t.label}
              </button>
            );
          })}
        </div>
      </SectionCard>

      {/* TAB 1: LIVE TCPDUMP */}
      {activeTool === 'tcpdump' && (
        <SectionCard
          title="TCPdump 实时抓包终端"
          subtitle={dumpStatus || '支持标准 BPF 过滤语法，抓包结束后可一键下载原始 .pcap 文件使用 Wireshark 深度分析'}
          action={
            pcapReady && captureId && selectedPodObj ? (
              <a
                href={api.getPcapDownloadUrl(selectedPodObj.nodeName, captureId)}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-emerald-600 hover:bg-emerald-700 text-white text-xs font-semibold"
              >
                <Download className="w-3.5 h-3.5" />
                下载 .pcap
              </a>
            ) : null
          }
        >
          <div className="p-4 border-b border-slate-100 dark:border-slate-800 space-y-3">
            <div className="grid grid-cols-1 md:grid-cols-12 gap-3 items-end">
              <div className="md:col-span-3 space-y-1">
                <label className="text-[11px] font-semibold text-slate-500">抓包网卡位置</label>
                <select
                  value={targetIface}
                  onChange={(e) => setTargetIface(e.target.value)}
                  className="w-full px-2.5 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
                >
                  <option value="pod:eth0">Pod 内部 eth0 (Netns)</option>
                  <option value="host:veth">宿主机 Calico veth ({podDetail?.hostVeth || 'cali*'})</option>
                  <option value="host:primary">宿主机主物理网卡</option>
                  <option value="host:tunl0">IPIP 隧道口 (tunl0)</option>
                  <option value="host:vxlan.calico">VXLAN 隧道口 (vxlan.calico)</option>
                </select>
              </div>

              <div className="md:col-span-5 space-y-1">
                <label className="text-[11px] font-semibold text-slate-500">BPF 过滤表达式 (可选)</label>
                <input
                  value={bpfFilter}
                  onChange={(e) => setBpfFilter(e.target.value)}
                  placeholder="例如 tcp port 8080 或 udp port 53 或 icmp"
                  className="w-full px-3 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
                />
              </div>

              <div className="md:col-span-2 grid grid-cols-2 gap-1.5">
                <div className="space-y-1">
                  <label className="text-[11px] font-semibold text-slate-500">包数上限</label>
                  <input
                    type="number"
                    value={maxPackets}
                    onChange={(e) => setMaxPackets(Number(e.target.value))}
                    className="w-full px-2 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
                  />
                </div>
                <div className="space-y-1">
                  <label className="text-[11px] font-semibold text-slate-500">时长(s)</label>
                  <input
                    type="number"
                    value={durationSec}
                    onChange={(e) => setDurationSec(Number(e.target.value))}
                    className="w-full px-2 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
                  />
                </div>
              </div>

              <div className="md:col-span-2">
                {!dumpRunning ? (
                  <button
                    onClick={startDump}
                    className="w-full inline-flex items-center justify-center gap-1.5 px-3 py-2 rounded-lg bg-[#F08C18] hover:bg-[#D9720B] text-white text-xs font-semibold"
                  >
                    <Play className="w-3.5 h-3.5" /> 开始抓包
                  </button>
                ) : (
                  <button
                    onClick={stopDump}
                    className="w-full inline-flex items-center justify-center gap-1.5 px-3 py-2 rounded-lg bg-rose-600 hover:bg-rose-700 text-white text-xs font-semibold"
                  >
                    <Square className="w-3.5 h-3.5" /> 停止 ({frames.length})
                  </button>
                )}
              </div>
            </div>

            {/* Quick BPF presets */}
            <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
              <span className="text-slate-400">快捷过滤：</span>
              {[
                { label: '全部流量', val: '' },
                { label: 'TCP SYN/RST', val: 'tcp[tcpflags] & (tcp-syn|tcp-rst) != 0' },
                { label: 'DNS (53)', val: 'port 53' },
                { label: 'HTTP/80/8080', val: 'tcp port 80 or tcp port 8080' },
                { label: 'ICMP', val: 'icmp' },
              ].map((p) => (
                <button
                  key={p.label}
                  onClick={() => setBpfFilter(p.val)}
                  className="px-2 py-0.5 rounded bg-slate-100 dark:bg-slate-800 hover:bg-blue-50 dark:hover:bg-blue-950/60 text-slate-600 dark:text-slate-300 font-mono"
                >
                  {p.label}
                </button>
              ))}
            </div>
          </div>

          {/* Packet Frames Table */}
          <div className="bg-slate-950 text-slate-200 font-mono text-[11px] h-[360px] overflow-auto">
            <table className="w-full">
              <thead className="bg-slate-900 text-slate-400 sticky top-0">
                <tr>
                  <th className="text-left px-3 py-1.5">No.</th>
                  <th className="text-left px-3 py-1.5">Time(ms)</th>
                  <th className="text-left px-3 py-1.5">Source</th>
                  <th className="text-left px-3 py-1.5">Destination</th>
                  <th className="text-left px-3 py-1.5">Proto</th>
                  <th className="text-right px-3 py-1.5">Len</th>
                  <th className="text-left px-3 py-1.5">Info</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-800/80">
                {frames.length === 0 && (
                  <tr>
                    <td colSpan={7} className="px-4 py-12 text-center text-slate-500">
                      {dumpRunning ? '等待流量报文到达...' : '点击“开始抓包”实时捕获数据包'}
                    </td>
                  </tr>
                )}
                {frames.map((fr) => (
                  <tr
                    key={fr.number}
                    onClick={() => setSelectedFrame(fr)}
                    className={`cursor-pointer hover:bg-slate-800/80 ${
                      selectedFrame?.number === fr.number ? 'bg-blue-950/70' : ''
                    }`}
                  >
                    <td className="px-3 py-1 text-slate-500">{fr.number}</td>
                    <td className="px-3 py-1 text-slate-400">{fr.relTimeMs.toFixed(2)}</td>
                    <td className="px-3 py-1 text-sky-300">
                      {fr.srcIP}{fr.srcPort ? `:${fr.srcPort}` : ''}
                    </td>
                    <td className="px-3 py-1 text-emerald-300">
                      {fr.dstIP}{fr.dstPort ? `:${fr.dstPort}` : ''}
                    </td>
                    <td className="px-3 py-1">
                      <span
                        className={`px-1.5 py-0.2 rounded text-[10px] ${
                          fr.tcpFlags?.includes('RST')
                            ? 'bg-rose-900/80 text-rose-200'
                            : fr.protocol === 'DNS'
                              ? 'bg-violet-900/80 text-violet-200'
                              : fr.protocol === 'ICMP'
                                ? 'bg-amber-900/80 text-amber-200'
                                : 'bg-slate-800 text-slate-200'
                        }`}
                      >
                        {fr.protocol}
                      </span>
                    </td>
                    <td className="px-3 py-1 text-right text-slate-400">{fr.length}</td>
                    <td className="px-3 py-1 text-slate-300 truncate max-w-[460px]">{fr.info}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {selectedFrame?.hexDump && (
            <div className="p-3 bg-slate-900 border-t border-slate-800 font-mono text-[11px] text-emerald-300/90 overflow-x-auto">
              <div className="text-slate-400 text-[10px] mb-1">
                Frame #{selectedFrame.number} Hex / ASCII Payload ({selectedFrame.length} bytes on {selectedFrame.interface})
              </div>
              <pre className="leading-relaxed">{selectedFrame.hexDump}</pre>
            </div>
          )}
        </SectionCard>
      )}

      {/* TAB 2: TCP 3-WAY HANDSHAKE */}
      {activeTool === 'tcp' && (
        <div className="space-y-4">
          <SectionCard className="p-4">
            <div className="grid grid-cols-1 md:grid-cols-12 gap-3 items-end">
              <div className="md:col-span-6 space-y-1">
                <label className="text-[11px] font-semibold text-slate-500">目标主机 / Service 域名 / Pod IP</label>
                <input
                  value={tcpHost}
                  onChange={(e) => setTcpHost(e.target.value)}
                  placeholder="如 api-gateway.petclinic-training.svc 或 10.250.252.92"
                  className="w-full px-3 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
                />
              </div>
              <div className="md:col-span-2 space-y-1">
                <label className="text-[11px] font-semibold text-slate-500">目标端口</label>
                <input
                  type="number"
                  value={tcpPort}
                  onChange={(e) => setTcpPort(Number(e.target.value))}
                  className="w-full px-3 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
                />
              </div>
              <div className="md:col-span-2 space-y-1">
                <label className="text-[11px] font-semibold text-slate-500">探测次数</label>
                <input
                  type="number"
                  min={1}
                  max={10}
                  value={tcpCount}
                  onChange={(e) => setTcpCount(Number(e.target.value))}
                  className="w-full px-3 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
                />
              </div>
              <div className="md:col-span-2">
                <button
                  onClick={runTCP}
                  disabled={tcpLoading}
                  className="w-full inline-flex items-center justify-center gap-1.5 px-4 py-2 rounded-lg bg-[#F08C18] hover:bg-[#D9720B] disabled:opacity-50 text-white text-xs font-semibold"
                >
                  <RefreshCw className={`w-3.5 h-3.5 ${tcpLoading ? 'animate-spin' : ''}`} />
                  {tcpLoading ? '检测中...' : '执行握手检测'}
                </button>
              </div>
            </div>
            {tcpError && <p className="mt-2 text-xs text-rose-600">{tcpError}</p>}
          </SectionCard>

          {tcpResult && (
            <SectionCard title="TCP 三次握手时序诊断报告" subtitle={tcpResult.diagnosis}>
              <div className="p-4 space-y-4">
                <div className="grid grid-cols-2 md:grid-cols-5 gap-3 text-xs font-mono">
                  <StatPill label="成功率" value={`${tcpResult.successCount}/${tcpResult.totalCount}`} ok={tcpResult.successCount === tcpResult.totalCount} />
                  <StatPill label="解析目标 IP" value={`${tcpResult.resolvedIP || '—'}:${tcpResult.targetPort}`} />
                  <StatPill label="最小 SYN-ACK RTT" value={`${tcpResult.minRttMs} ms`} />
                  <StatPill label="平均 SYN-ACK RTT" value={`${tcpResult.avgRttMs} ms`} />
                  <StatPill label="最大 SYN-ACK RTT" value={`${tcpResult.maxRttMs} ms`} />
                </div>

                {/* Sequence Ladder per Round */}
                <div className="space-y-3">
                  {tcpResult.attempts.map((att) => (
                    <div key={att.round} className="rounded-xl border border-slate-200 dark:border-slate-800 p-3.5 bg-slate-50/50 dark:bg-slate-950/40">
                      <div className="flex items-center justify-between mb-3">
                        <div className="flex items-center gap-2 text-xs">
                          <span className="font-mono font-bold">Round #{att.round}</span>
                          <StatusPill status={att.result} />
                        </div>
                        <span className="font-mono text-xs text-slate-500">
                          SYN→SYN-ACK: <b className="text-slate-800 dark:text-slate-100">{att.synAckRttMs} ms</b> · 总计 {att.totalMs} ms
                        </span>
                      </div>

                      <div className="space-y-1.5 font-mono text-[11px]">
                        {att.steps.map((st, idx) => (
                          <div key={idx} className="flex items-center gap-3 px-3 py-1.5 rounded-lg bg-white dark:bg-slate-900 border border-slate-200/70 dark:border-slate-800">
                            <span className="w-16 text-slate-400">+{st.timestampMs.toFixed(2)}ms</span>
                            <span
                              className={`w-20 text-center px-1.5 py-0.5 rounded font-semibold text-[10px] ${
                                st.step === 'RST' || st.step === 'TIMEOUT'
                                  ? 'bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300'
                                  : st.step === 'SYN-ACK'
                                    ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300'
                                    : 'bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300'
                              }`}
                            >
                              {st.step}
                            </span>
                            {st.direction === 'outbound' ? (
                              <ArrowRight className="w-4 h-4 text-blue-500 shrink-0" />
                            ) : st.direction === 'inbound' ? (
                              <ArrowLeft className="w-4 h-4 text-emerald-500 shrink-0" />
                            ) : (
                              <span className="w-4 text-center text-slate-400">•</span>
                            )}
                            <span className="flex-1 text-slate-700 dark:text-slate-200 truncate">{st.detail}</span>
                            <span className="text-slate-400">Δ {st.deltaMs.toFixed(2)}ms</span>
                          </div>
                        ))}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </SectionCard>
          )}
        </div>
      )}

      {/* TAB 3: HTTP WATERFALL PROBE */}
      {activeTool === 'http' && (
        <div className="space-y-4">
          <SectionCard className="p-4">
            <div className="grid grid-cols-1 md:grid-cols-12 gap-3 items-end">
              <div className="md:col-span-2 space-y-1">
                <label className="text-[11px] font-semibold text-slate-500">Method</label>
                <select
                  value={httpMethod}
                  onChange={(e) => setHttpMethod(e.target.value)}
                  className="w-full px-2.5 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
                >
                  <option value="GET">GET</option>
                  <option value="HEAD">HEAD</option>
                  <option value="POST">POST</option>
                </select>
              </div>
              <div className="md:col-span-6 space-y-1">
                <label className="text-[11px] font-semibold text-slate-500">目标 URL</label>
                <input
                  value={httpURL}
                  onChange={(e) => setHttpURL(e.target.value)}
                  placeholder="http://api-gateway.petclinic-training.svc:8080/"
                  className="w-full px-3 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
                />
              </div>
              <div className="md:col-span-2 space-y-1">
                <label className="text-[11px] font-semibold text-slate-500">Host 头 (可选)</label>
                <input
                  value={httpHostHdr}
                  onChange={(e) => setHttpHostHdr(e.target.value)}
                  placeholder="demo.local"
                  className="w-full px-3 py-2 text-xs rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 font-mono outline-none"
                />
              </div>
              <div className="md:col-span-2">
                <button
                  onClick={runHTTP}
                  disabled={httpLoading}
                  className="w-full inline-flex items-center justify-center gap-1.5 px-4 py-2 rounded-lg bg-[#F08C18] hover:bg-[#D9720B] disabled:opacity-50 text-white text-xs font-semibold"
                >
                  <RefreshCw className={`w-3.5 h-3.5 ${httpLoading ? 'animate-spin' : ''}`} />
                  {httpLoading ? '请求中...' : '发送探测'}
                </button>
              </div>
            </div>
            <label className="mt-2.5 inline-flex items-center gap-2 text-xs text-slate-500 cursor-pointer">
              <input type="checkbox" checked={httpInsecure} onChange={(e) => setHttpInsecure(e.target.checked)} className="rounded" />
              忽略自签名 HTTPS 证书校验 (InsecureSkipVerify)
            </label>
            {httpError && <p className="mt-2 text-xs text-rose-600">{httpError}</p>}
          </SectionCard>

          {httpResult && (
            <SectionCard title="HTTP / HTTPS 全链路耗时瀑布流" subtitle={httpResult.diagnosis}>
              <div className="p-4 space-y-4">
                <div className="flex flex-wrap items-center gap-3">
                  {httpResult.success ? (
                    <span className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-semibold bg-emerald-50 text-emerald-700 border border-emerald-200 dark:bg-emerald-950/60 dark:text-emerald-300 dark:border-emerald-800">
                      <CheckCircle2 className="w-4 h-4" /> {httpResult.statusText || `HTTP ${httpResult.statusCode}`}
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-semibold bg-rose-50 text-rose-700 border border-rose-200 dark:bg-rose-950/60 dark:text-rose-300 dark:border-rose-800">
                      <XCircle className="w-4 h-4" /> {httpResult.statusCode ? `HTTP ${httpResult.statusCode}` : '探测失败'}
                    </span>
                  )}
                  <span className="font-mono text-xs text-slate-500">Remote: {httpResult.remoteAddr || httpResult.resolvedIP || '—'}</span>
                  <span className="font-mono text-xs text-slate-500">Total: <b>{httpResult.totalMs} ms</b></span>
                  <span className="font-mono text-xs text-slate-500">Body: {httpResult.bodyBytes} B</span>
                  {httpResult.tlsVersion && (
                    <span className="inline-flex items-center gap-1 font-mono text-xs px-2 py-0.5 rounded bg-violet-50 dark:bg-violet-950/50 text-violet-700 dark:text-violet-300">
                      <ShieldCheck className="w-3.5 h-3.5" /> {httpResult.tlsVersion} · CN={httpResult.tlsCertSubject} (exp {httpResult.tlsCertExpiry})
                    </span>
                  )}
                </div>

                {/* Waterfall Visual Bar */}
                <HTTPWaterfallBar r={httpResult} />

                {/* Headers & Body Preview */}
                <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
                  <div className="rounded-lg border border-slate-200 dark:border-slate-800 p-3 bg-slate-50 dark:bg-slate-950/60">
                    <div className="text-[11px] font-semibold text-slate-500 mb-2">响应头 (Response Headers)</div>
                    <div className="font-mono text-[11px] space-y-1 max-h-[220px] overflow-auto">
                      {Object.entries(httpResult.responseHeaders || {}).map(([k, v]) => (
                        <div key={k}>
                          <span className="text-blue-600 dark:text-blue-400">{k}:</span>{' '}
                          <span className="text-slate-700 dark:text-slate-300">{v.join(', ')}</span>
                        </div>
                      ))}
                    </div>
                  </div>
                  <div className="rounded-lg border border-slate-200 dark:border-slate-800 p-3 bg-slate-950 text-slate-200">
                    <div className="text-[11px] font-semibold text-slate-400 mb-2">响应体预览 (Body Preview)</div>
                    <pre className="font-mono text-[11px] whitespace-pre-wrap break-all max-h-[220px] overflow-auto">
                      {httpResult.bodyPreview || httpResult.error || '(empty)'}
                    </pre>
                  </div>
                </div>
              </div>
            </SectionCard>
          )}
        </div>
      )}
    </div>
  );
}

function HTTPWaterfallBar({ r }: { r: HTTPProbeResponse }) {
  const total = Math.max(r.totalMs, 0.1);
  const segs = [
    { label: 'DNS 解析', ms: r.dnsLookupMs, color: 'bg-sky-500' },
    { label: 'TCP 建连', ms: r.tcpConnectMs, color: 'bg-indigo-500' },
    { label: 'TLS 握手', ms: r.tlsHandshakeMs, color: 'bg-violet-500' },
    { label: 'TTFB 等待', ms: r.serverWaitTtfbMs, color: 'bg-amber-500' },
    { label: '内容传输', ms: r.contentTransferMs, color: 'bg-emerald-500' },
  ];
  return (
    <div className="space-y-2.5 p-3.5 rounded-xl bg-slate-50 dark:bg-slate-950/50 border border-slate-200/80 dark:border-slate-800">
      <div className="h-5 w-full rounded-md overflow-hidden flex bg-slate-200 dark:bg-slate-800">
        {segs.map(
          (s) =>
            s.ms > 0 && (
              <div
                key={s.label}
                className={`${s.color} h-full transition-all`}
                style={{ width: `${Math.max(2, (s.ms / total) * 100)}%` }}
                title={`${s.label}: ${s.ms} ms`}
              />
            ),
        )}
      </div>
      <div className="grid grid-cols-5 gap-2 text-[11px]">
        {segs.map((s) => (
          <div key={s.label} className="flex items-center gap-1.5">
            <span className={`w-2.5 h-2.5 rounded-sm ${s.color} shrink-0`} />
            <span className="text-slate-500 truncate">{s.label}:</span>
            <span className="font-mono font-semibold text-slate-800 dark:text-slate-100">{s.ms}ms</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function StatPill({ label, value, ok }: { label: string; value: string; ok?: boolean }) {
  return (
    <div className="p-2.5 rounded-lg bg-slate-50 dark:bg-slate-950/60 border border-slate-200 dark:border-slate-800">
      <div className="text-[10px] text-slate-400 font-sans">{label}</div>
      <div className={`text-sm font-bold mt-0.5 ${ok === true ? 'text-emerald-600' : ok === false ? 'text-rose-600' : 'text-slate-800 dark:text-slate-100'}`}>
        {value}
      </div>
    </div>
  );
}
