import { useEffect, useState } from 'react';
import { Brain, Check, ClipboardCheck, HelpCircle, Send, ShieldAlert } from 'lucide-react';
import type { DecisionRespondInput, PendingDecision, QuestionSpec } from '../types';

interface Props {
  decision?: PendingDecision;
  onRespond: (input: DecisionRespondInput) => void;
}

/** Live "expires in" label driven by the server's expires_at. */
function Countdown({ expiresAt }: { expiresAt: string }) {
  const [, forceTick] = useState(0);
  useEffect(() => {
    const t = window.setInterval(() => forceTick((n) => n + 1), 1000);
    return () => window.clearInterval(t);
  }, [expiresAt]);
  // Computed WITHOUT useMemo: the 1s re-render must recompute, and the memo's
  // dep on a stable setter never invalidated it (frozen countdown bug).
  const left = Math.max(0, Math.round((new Date(expiresAt).getTime() - Date.now()) / 1000));
  return (
    <span className="ml-auto text-[10px] font-mono text-amber-400/80">
      {left > 0 ? `${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')}` : 'expiring…'}
    </span>
  );
}

function QuestionBlock({ q, onPick }: { q: QuestionSpec; onPick: (label: string) => void }) {
  const [picked, setPicked] = useState<string[]>([]);
  const toggle = (label: string) => {
    const next = q.multi_select
      ? (picked.includes(label) ? picked.filter((p) => p !== label) : [...picked, label])
      : [label];
    setPicked(next);
    // Comma separator matches Claude Code's own multi-select join.
    onPick(next.join(', '));
  };
  return (
    <div className="space-y-1.5">
      <p className="text-xs font-semibold text-amber-100">{q.question}</p>
      {q.options.map((o) => (
        <button
          key={o.label}
          type="button"
          onClick={() => toggle(o.label)}
          className={`w-full min-h-[44px] text-left px-3 py-2.5 rounded-xl border text-xs transition
            ${picked.includes(o.label)
              ? 'bg-amber-500/20 border-amber-400/60 text-amber-100'
              : 'bg-black/30 border-white/10 text-slate-200 hover:border-amber-500/40'}`}
        >
          <span className="font-semibold">{o.label}</span>
          {o.description && <span className="block text-[11px] text-slate-400 mt-0.5">{o.description}</span>}
        </button>
      ))}
    </div>
  );
}

/**
 * Interactive remote decision banner (2026-09-02): the phone-side equivalent
 * of Claude Code's permission dialog / AskUserQuestion picker / plan review.
 * Permission → Allow / Always-Allow / Deny (+ optional note). Question → tap
 * options (multiSelect aware) + free-text Other. Plan → plan text in a
 * fixed-height scroll box + Approve/Reject. Expiry falls back to the PC
 * terminal prompt (server broadcasts decision_resolved action:"expire").
 */
export default function DecisionBanner({ decision, onRespond }: Props) {
  const [notes, setNotes] = useState('');
  const [answers, setAnswers] = useState<Record<string, string>>({});
  // Reset per-decision state when a NEW decision replaces this one — notes
  // and picked options must never leak across decisions.
  useEffect(() => {
    setNotes('');
    setAnswers({});
  }, [decision?.id]);
  if (!decision) return null;

  const isQ = decision.kind === 'question';
  const isPlan = decision.kind === 'plan';
  const Icon = isQ ? HelpCircle : isPlan ? ClipboardCheck : ShieldAlert;
  const label = isQ ? 'CLAUDE HAS A QUESTION' : isPlan ? 'PLAN REVIEW' : 'PERMISSION REQUIRED';
  const questions = decision.questions ?? [];
  // Every question must be answered before sending — a partial answers map
  // would ship Claude an incomplete updatedInput.answers object.
  const hasAnswer = questions.length > 0 && Object.keys(answers).length >= questions.length;
  // Long question text (a command, a file, an error) renders in the same
  // fixed-height scroll box the spec mandates for long process text.
  const longQuestion = !isPlan && !!decision.question && decision.question.length > 240;

  return (
    <section className="rounded-3xl bg-gradient-to-r from-amber-950/70 via-amber-900/50 to-[#181a24] border-2 border-amber-500/60 p-5 shadow-[0_0_30px_rgba(245,158,11,0.25)] animate-in fade-in zoom-in-95 duration-300">
      <div className="flex items-center gap-2 mb-3">
        <Icon size={16} className="text-amber-300 animate-bounce" />
        <span className="text-[10px] font-extrabold uppercase tracking-wider text-amber-300">{label}</span>
        <Countdown expiresAt={decision.expires_at} />
      </div>
      <h3 className="text-sm font-bold text-white mb-1">{decision.title}</h3>

      {isPlan && decision.question && (
        <div className="h-48 overflow-y-auto rounded-xl bg-black/40 border border-white/10 p-3 mb-3 font-mono text-[11px] text-slate-300 whitespace-pre-wrap">
          {decision.question}
        </div>
      )}
      {!isPlan && decision.question && longQuestion && (
        <div className="h-32 overflow-y-auto rounded-xl bg-black/40 border border-white/10 p-3 mb-3 font-mono text-[11px] text-amber-100/90 whitespace-pre-wrap">
          {decision.question}
        </div>
      )}
      {!isPlan && decision.question && !longQuestion && (
        <p className="text-xs text-amber-100/90 mb-3 break-words">{decision.question}</p>
      )}
      {decision.tool_name && (
        <p className="text-[10px] font-mono text-amber-400/70 mb-3">tool: {decision.tool_name}</p>
      )}

      {isQ &&
        (decision.questions ?? []).map((q) => (
          <QuestionBlock
            key={q.question}
            q={q}
            onPick={(v) => setAnswers((prev) => ({ ...prev, [q.question]: v }))}
          />
        ))}

      <input
        value={notes}
        onChange={(e) => setNotes(e.target.value)}
        placeholder={
          isQ
            ? 'Other / notes (optional, sent with your answer)'
            : 'Note for Claude (optional)'
        }
        className="w-full mt-3 px-3 py-2.5 rounded-xl bg-black/40 border border-white/10 text-xs text-white placeholder-slate-600 focus:outline-none focus:border-amber-400/60"
      />

      <div className="flex flex-wrap gap-2 mt-3">
        {isQ ? (
          <button
            type="button"
            disabled={!hasAnswer}
            onClick={() =>
              onRespond({
                decision_id: decision.id,
                action: 'answer',
                answer: answers,
                notes: notes || undefined,
              })
            }
            className="flex-1 min-w-[120px] min-h-[44px] py-2.5 rounded-xl bg-amber-500 hover:bg-amber-400 disabled:opacity-40 disabled:hover:bg-amber-500 text-black text-xs font-bold flex items-center justify-center gap-1.5"
          >
            <Send size={13} /> Send Answer
          </button>
        ) : (
          <>
            <button
              type="button"
              onClick={() =>
                onRespond({ decision_id: decision.id, action: 'allow', notes: notes || undefined })
              }
              className="flex-1 min-w-[100px] min-h-[44px] py-2.5 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-bold flex items-center justify-center gap-1.5"
            >
              <Check size={13} /> {isPlan ? 'Approve Plan' : 'Allow'}
            </button>
            <button
              type="button"
              onClick={() =>
                onRespond({ decision_id: decision.id, action: 'deny', notes: notes || undefined })
              }
              className="flex-1 min-w-[100px] min-h-[44px] py-2.5 rounded-xl bg-red-600/90 hover:bg-red-500 text-white text-xs font-bold"
            >
              {isPlan ? 'Reject' : 'Deny'}
            </button>
          </>
        )}
      </div>

      {!isQ && !isPlan &&
        (decision.permission_suggestions ?? []).map((s, i) => (
          <button
            key={i}
            type="button"
            onClick={() =>
              onRespond({ decision_id: decision.id, action: 'always_allow', suggestion_index: i })
            }
            className="w-full min-h-[40px] mt-2 py-2 rounded-xl bg-black/30 border border-amber-500/30 text-[11px] text-amber-200 hover:bg-amber-500/10"
          >
            <Brain size={11} className="inline mr-1" />
            Always allow{typeof s.label === 'string' && s.label ? `: ${s.label}` : ''}
          </button>
        ))}
    </section>
  );
}
