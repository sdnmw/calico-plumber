import React from 'react';
import { Cpu, Layers, ShieldAlert, CheckCircle2, AlertTriangle, XCircle, MinusCircle } from 'lucide-react';

export function EncapBadge({ mode }: { mode: string }) {
  const m = mode || 'Unknown';
  const style = m.includes('VXLAN')
    ? 'bg-violet-50 text-violet-700 border-violet-200 dark:bg-violet-950/50 dark:text-violet-300 dark:border-violet-800'
    : m.includes('IPIP')
      ? 'bg-[#FFF4E4] text-[#9E4A05] border-[#FAD4A1] dark:bg-[#F08C18]/15 dark:text-[#FBB040] dark:border-[#F08C18]/40'
      : 'bg-[#EDF5FC] text-[#155A96] border-[#C4DCF0] dark:bg-[#155A96]/25 dark:text-[#7EB8E8] dark:border-[#155A96]/50';
  return (
    <span className={`inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-semibold border ${style}`}>
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

export function StatusPill({ status, label, compact = false }: { status: string; label?: string; compact?: boolean }) {
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
  } else if (s.includes('skip')) {
    cls = 'bg-slate-100 text-slate-500 border-slate-200 dark:bg-slate-800/80 dark:text-slate-400 dark:border-slate-700';
    Icon = MinusCircle;
  }

  return (
    <span
      className={`inline-flex items-center gap-1 rounded-full font-medium border whitespace-nowrap ${
        compact ? 'px-1.5 py-0 text-[10px] leading-4' : 'px-2 py-0.5 text-xs'
      } ${cls}`}
    >
      <Icon className={`${compact ? 'w-2.5 h-2.5' : 'w-3 h-3'} shrink-0`} />
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
      className={`bg-white dark:bg-[#1C1815] border border-[#E9E1D6] dark:border-[#2E2721] rounded-xl shadow-[0_1px_3px_rgba(44,30,18,0.035)] ${className}`}
    >
      {(title || action) && (
        <div className="px-4 py-3 border-b border-[#F2ECE4] dark:border-[#2A231D] bg-gradient-to-b from-white to-[#FDFBF8] dark:from-[#201B17] dark:to-[#1C1815] rounded-t-xl flex items-center justify-between gap-3">
          <div>
            {title && <h3 className="text-sm font-bold text-[#201B18] dark:text-[#F5F0EA]">{title}</h3>}
            {subtitle && <p className="text-xs text-[#6E6257] dark:text-[#A39587] mt-0.5">{subtitle}</p>}
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
