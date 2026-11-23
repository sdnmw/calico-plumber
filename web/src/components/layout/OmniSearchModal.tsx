import React, { useEffect, useRef, useState } from 'react';
import { Search, Box, Layers, Server, Cpu, Route, Terminal, X } from 'lucide-react';
import { api } from '../../api/client';
import type { SearchResultItem } from '../../api/types';

interface OmniSearchModalProps {
  open: boolean;
  onClose: () => void;
  onInspectPod: (ns: string, name: string) => void;
  onLaunchTrace: (item: SearchResultItem) => void;
  onLaunchTCPDump: (ns: string, name: string) => void;
}

export function OmniSearchModal({
  open,
  onClose,
  onInspectPod,
  onLaunchTrace,
  onLaunchTCPDump,
}: OmniSearchModalProps) {
  const [q, setQ] = useState('');
  const [results, setResults] = useState<SearchResultItem[]>([]);
  const [loading, setLoading] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (open) {
      setTimeout(() => inputRef.current?.focus(), 40);
    }
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const t = setTimeout(() => {
      setLoading(true);
      api
        .search(q, '', '', 25)
        .then((r) => setResults(r.results || []))
        .catch(() => setResults([]))
        .finally(() => setLoading(false));
    }, 120);
    return () => clearTimeout(t);
  }, [q, open]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 bg-slate-900/40 backdrop-blur-[2px] flex items-start justify-center pt-[10vh] px-4" onClick={onClose}>
      <div
        className="w-full max-w-2xl bg-white dark:bg-slate-900 rounded-2xl shadow-2xl border border-slate-200 dark:border-slate-800 overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="px-4 py-3.5 border-b border-slate-200 dark:border-slate-800 flex items-center gap-3">
          <Search className="w-5 h-5 text-blue-500 shrink-0" />
          <input
            ref={inputRef}
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="输入 Pod IP、ClusterIP、Node IP、Pod / Deployment / Service 名称或标签 (如 app=nginx)..."
            className="flex-1 bg-transparent text-sm text-slate-900 dark:text-slate-100 placeholder-slate-400 outline-none"
          />
          {q && (
            <button onClick={() => setQ('')} className="text-slate-400 hover:text-slate-600">
              <X className="w-4 h-4" />
            </button>
          )}
          <kbd className="px-1.5 py-0.5 text-[10px] font-mono rounded border border-slate-200 dark:border-slate-700 text-slate-400">
            ESC
          </kbd>
        </div>

        <div className="max-h-[60vh] overflow-y-auto divide-y divide-slate-100 dark:divide-slate-800/80">
          {loading && results.length === 0 && (
            <div className="py-10 text-center text-xs text-slate-400">搜索中...</div>
          )}
          {!loading && results.length === 0 && (
            <div className="py-10 text-center text-xs text-slate-400">未找到匹配的 IP、Pod、工作负载、Service 或节点</div>
          )}
          {results.map((item, idx) => (
            <div
              key={`${item.kind}-${item.namespace}-${item.name}-${idx}`}
              className="px-4 py-2.5 hover:bg-slate-50 dark:hover:bg-slate-800/60 flex items-center justify-between gap-3 transition"
            >
              <button
                className="flex items-center gap-3 min-w-0 flex-1 text-left"
                onClick={() => {
                  if (item.kind === 'Pod' && item.namespace) {
                    onClose();
                    onInspectPod(item.namespace, item.name);
                  } else if (item.pods && item.pods.length > 0) {
                    onClose();
                    onInspectPod(item.pods[0].namespace, item.pods[0].name);
                  }
                }}
              >
                <KindIcon kind={item.kind} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="text-xs font-semibold text-slate-800 dark:text-slate-100 truncate font-mono">
                      {item.namespace ? `${item.namespace}/${item.name}` : item.name}
                    </span>
                    <span className="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400">
                      {item.kind}
                    </span>
                  </div>
                  <div className="text-[11px] text-slate-500 dark:text-slate-400 font-mono flex items-center gap-3 mt-0.5">
                    {item.ip && <span>IP: {item.ip}</span>}
                    {item.nodeName && <span>Node: {item.nodeName}</span>}
                    {item.ipamBlock && <span>Block: {item.ipamBlock}</span>}
                    {item.podCount !== undefined && <span>Pods: {item.readyPods}/{item.podCount}</span>}
                  </div>
                </div>
              </button>

              <div className="flex items-center gap-1.5 shrink-0">
                {(item.kind === 'Pod' || (item.pods && item.pods.length > 0)) && (
                  <>
                    <button
                      onClick={() => {
                        onClose();
                        onLaunchTrace(item);
                      }}
                      className="inline-flex items-center gap-1 px-2 py-1 rounded-md text-[11px] font-medium bg-blue-50 text-blue-700 hover:bg-blue-100 dark:bg-blue-950/60 dark:text-blue-300"
                    >
                      <Route className="w-3 h-3" /> TraceFlow
                    </button>
                    <button
                      onClick={() => {
                        const targetPod = item.kind === 'Pod' ? { ns: item.namespace!, name: item.name } : item.pods?.[0] ? { ns: item.pods[0].namespace, name: item.pods[0].name } : null;
                        if (targetPod) {
                          onClose();
                          onLaunchTCPDump(targetPod.ns, targetPod.name);
                        }
                      }}
                      className="inline-flex items-center gap-1 px-2 py-1 rounded-md text-[11px] font-medium bg-slate-100 text-slate-700 hover:bg-slate-200 dark:bg-slate-800 dark:text-slate-300"
                    >
                      <Terminal className="w-3 h-3" /> 抓包
                    </button>
                  </>
                )}
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function KindIcon({ kind }: { kind: string }) {
  const cls = 'w-8 h-8 rounded-lg flex items-center justify-center shrink-0';
  switch (kind) {
    case 'Pod':
      return <div className={`${cls} bg-emerald-50 text-emerald-600 dark:bg-emerald-950/60 dark:text-emerald-400`}><Box className="w-4 h-4" /></div>;
    case 'Service':
      return <div className={`${cls} bg-teal-50 text-teal-600 dark:bg-teal-950/60 dark:text-teal-400`}><Server className="w-4 h-4" /></div>;
    case 'Node':
      return <div className={`${cls} bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300`}><Cpu className="w-4 h-4" /></div>;
    default:
      return <div className={`${cls} bg-blue-50 text-blue-600 dark:bg-blue-950/60 dark:text-blue-400`}><Layers className="w-4 h-4" /></div>;
  }
}
