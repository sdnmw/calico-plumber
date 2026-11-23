import React from 'react';
import { Cpu, Layers, ShieldAlert, CheckCircle2, AlertTriangle, XCircle } from 'lucide-react';

export function EncapBadge({ mode }: { mode: string }) {
  const m = mode || 'Unknown';
  const style = m.includes('VXLAN')
    ? 'bg-violet-50 text-violet-700 border-violet-200 dark:bg-violet-950/50 dark:text-violet-300 dark:border-violet-800'
    : m.includes('IPIP')
      ? 'bg-indigo-50 text-indigo-700 border-indigo-200 dark:bg-indigo-950/50 dark:text-indigo-300 dark:border-indigo-800'
      : 'bg-blue-50 text-blue-700 border-blue-200 dark:bg-blue-950/50 dark:text-blue-300 dark:border-blue-800';
  return (
    <span className={`inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium border ${style}`}>
      <Layers className="w-3 h-3" />
      {m}
    </span>
  );
}

export function EngineBadge({ engine, reason }: { engine: string; reason?: string }) {
  const isEBPF = engine === 'ebpf';
  return (
    <span
      title={reason || engine}
      className={`inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium border ${
        isEBPF
          ? 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-950/50 dark:text-emerald-300 dark:border-emerald-800'
          : 'bg-sky-50 text-sky-700 border-sky-200 dark:bg-sky-950/50 dark:text-sky-300 dark:border-sky-800'
      }`}
    >
      <Cpu className="w-3 h-3" />
      {isEBPF ? 'eBPF' : 'Legacy'}
    </span>
  );
}

export function StatusPill({ status, label }: { status: string; label?: string }) {
  const s = (status || '').toLowerCase();
  let cls = 'bg-slate-100 text-slate-700 border-slate-200 dark:bg-slate-800 dark:text-slate-300 dark:border-slate-700';
  let Icon = CheckCircle2;

  if (['healthy', 'pass', 'reachable', 'established', 'running', 'ready', 'delivered', 'observed', 'passed', 'up'].some((k) => s.includes(k))) {
    cls = 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-950/50 dark:text-emerald-300 dark:border-emerald-800';
    Icon = CheckCircle2;
  } else if (['warn', 'degraded', 'reset', 'borrowed', 'nat', 'encap', 'decap', 'no-endpoints'].some((k) => s.includes(k))) {
    cls = 'bg-amber-50 text-amber-700 border-amber-200 dark:bg-amber-950/50 dark:text-amber-300 dark:border-amber-800';
    Icon = AlertTriangle;
  } else if (['fail', 'critical', 'broken', 'dropped', 'timeout', 'error', 'orphan', 'notready'].some((k) => s.includes(k))) {
    cls = 'bg-rose-50 text-rose-700 border-rose-200 dark:bg-rose-950/50 dark:text-rose-300 dark:border-rose-800';
    Icon = XCircle;
  }

  return (
    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium border ${cls}`}>
      <Icon className="w-3 h-3 shrink-0" />
      <span>{label || status}</span>
    </span>
  );
}

export function SectionCard({
  title,
  subtitle,
  action,
  children,
  className = '',
}: {
  title?: string;
  subtitle?: string;
  action?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={`bg-white dark:bg-slate-900/90 border border-slate-200/90 dark:border-slate-800 rounded-xl shadow-[0_1px_2px_rgba(15,23,42,0.04)] ${className}`}
    >
      {(title || action) && (
        <div className="px-4 py-3 border-b border-slate-100 dark:border-slate-800/80 flex items-center justify-between gap-3">
          <div>
            {title && <h3 className="text-sm font-semibold text-slate-800 dark:text-slate-100">{title}</h3>}
            {subtitle && <p className="text-xs text-slate-500 dark:text-slate-400 mt-0.5">{subtitle}</p>}
          </div>
          {action}
        </div>
      )}
      {children}
    </div>
  );
}

export function EmptyState({ title, description }: { title: string; description?: string }) {
  return (
    <div className="flex flex-col items-center justify-center py-12 px-4 text-center">
      <ShieldAlert className="w-8 h-8 text-slate-300 dark:text-slate-600 mb-2" />
      <p className="text-sm font-medium text-slate-600 dark:text-slate-300">{title}</p>
      {description && <p className="text-xs text-slate-400 dark:text-slate-500 mt-1 max-w-md">{description}</p>}
    </div>
  );
}
