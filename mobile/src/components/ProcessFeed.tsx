import { useEffect, useRef, useState } from 'react';
import {
  Brain, CheckCircle2, ChevronDown, ChevronRight, CircleAlert,
  FileText, Flag, Hammer, User, Wrench, XCircle,
} from 'lucide-react';
import type { ProcessEvent, ProcessEventKind } from '../types';

interface Props {
  events: ProcessEvent[];
}

const kindIcon: Record<ProcessEventKind, typeof Brain> = {
  user_prompt: User,
  thinking: Brain,
  text: FileText,
  tool_use: Wrench,
  tool_result: CheckCircle2,
  tool_error: XCircle,
  turn_end: Flag,
};

const kindColor: Record<ProcessEventKind, string> = {
  user_prompt: 'text-[#D97757]',
  thinking: 'text-violet-300',
  text: 'text-slate-200',
  tool_use: 'text-sky-300',
  tool_result: 'text-emerald-300',
  tool_error: 'text-red-400',
  turn_end: 'text-amber-300',
};

/**
 * Fixed-height internal-scroll box — THE constraint from the spec: the
 * container must never grow with content length (matching how the VS Code
 * extension renders long output). Height is fixed (h-40/h-48), never max-h.
 */
function ScrollBox({ text, tall }: { text: string; tall?: boolean }) {
  return (
    <div
      className={`${tall ? 'h-48' : 'h-40'} overflow-y-auto rounded-xl bg-black/40 border border-white/5 p-3 font-mono text-[11px] leading-relaxed text-slate-300 whitespace-pre-wrap break-words`}
    >
      {text}
    </div>
  );
}

function EventRow({ evt }: { evt: ProcessEvent }) {
  const [open, setOpen] = useState(false);
  const Icon = kindIcon[evt.kind] ?? Hammer;
  const hasDetail = !!evt.detail && evt.detail.trim().length > 0;
  // Short single-line details render inline; anything longer (or multi-line)
  // collapses behind the expander with a fixed-height scroll box.
  const shortDetail = hasDetail && evt.detail!.length <= 120 && !evt.detail!.includes('\n');
  const expandable = hasDetail && !shortDetail;
  return (
    <div className="rounded-xl bg-black/30 border border-white/5">
      <button
        type="button"
        onClick={() => expandable && setOpen(!open)}
        className="w-full flex items-start gap-2 px-3 py-2 text-left"
      >
        {expandable
          ? (open
            ? <ChevronDown size={13} className="mt-0.5 text-slate-500 shrink-0" />
            : <ChevronRight size={13} className="mt-0.5 text-slate-500 shrink-0" />)
          : <span className="w-[13px] shrink-0" />}
        <Icon size={13} className={`mt-0.5 shrink-0 ${kindColor[evt.kind]}`} />
        <div className="min-w-0 flex-1">
          <p className="text-xs font-medium text-slate-200 truncate">
            {evt.kind === 'tool_use' && evt.tool_name ? `${evt.tool_name}: ${evt.title}` : evt.title}
          </p>
          {shortDetail && <p className="text-[11px] text-slate-400 truncate">{evt.detail}</p>}
        </div>
        <span className="text-[10px] font-mono text-slate-600 shrink-0 mt-0.5">
          {new Date(evt.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}
        </span>
      </button>
      {open && hasDetail && <div className="px-3 pb-3"><ScrollBox text={evt.detail!} tall={evt.kind === 'thinking'} /></div>}
      {open && !hasDetail && <p className="px-3 pb-3 text-[11px] text-slate-500">No output</p>}
    </div>
  );
}

/**
 * Live process view: streaming timeline of the agent's execution — prompts,
 * thinking, text, tool calls, tool results/errors, turn ends. Mirrors the VS
 * Code extension's Focus view pattern: tool detail and thinking hide behind
 * expandable rows; long text scrolls inside fixed-height boxes.
 */
export default function ProcessFeed({ events }: Props) {
  const endRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    endRef.current?.scrollIntoView({ block: 'nearest' });
  }, [events.length]);

  return (
    <section className="rounded-3xl bg-[#16181f]/90 border border-white/10 p-5 shadow-xl">
      <div className="flex items-center gap-2 mb-3">
        <div className="p-1.5 rounded-lg bg-[#D97757]/20 text-[#D97757]"><Hammer size={14} /></div>
        <div>
          <h2 className="text-sm font-bold uppercase tracking-wider text-white">Live Process</h2>
          <p className="text-[11px] text-slate-500">Streaming agent steps — tap rows to expand</p>
        </div>
      </div>
      {events.length === 0
        ? <p className="text-xs text-slate-500 py-6 text-center">Waiting for agent activity…</p>
        : (
          <div className="space-y-1.5 max-h-96 overflow-y-auto pr-1">
            {events.map((e) => <EventRow key={`${e.session_id}-${e.id}`} evt={e} />)}
            <div ref={endRef} />
          </div>
        )}
      <div className="flex items-center gap-1.5 mt-3 text-[10px] text-slate-600">
        <CircleAlert size={11} /> Long outputs scroll inside fixed boxes — the view stays stable
      </div>
    </section>
  );
}
