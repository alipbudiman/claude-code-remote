import { useState } from 'react';
import { CornerDownLeft, Inbox, Loader2, WifiOff } from 'lucide-react';
import type { Session } from '../types';

interface Props {
  session?: Session;
  isWorking: boolean;
  /** Returns false when the send could not go out (socket down) — the
 *  composer then keeps the text instead of silently dropping it. */
  onSend: (text: string) => boolean;
}

/**
 * Remote prompt composer (2026-09-02): mid-task prompt injection. Prompts
 * sent while a turn is running are QUEUED server-side and delivered when the
 * turn ends (the Stop hook continues the conversation with the queued text) —
 * the same queue-until-turn-end semantics as Claude Remote Control.
 */
export default function PromptComposer({ session, isWorking, onSend }: Props) {
  const [text, setText] = useState('');
  const [sendFailed, setSendFailed] = useState(false);
  const queueDepth = session?.prompt_queue_depth ?? 0;
  const disabled = !session;

  const send = () => {
    const t = text.trim();
    if (!t || disabled) return;
    if (!onSend(t)) {
      // Socket not open (reconnect window) — keep the text so nothing is
      // lost; the user retries when the link is back.
      setSendFailed(true);
      return;
    }
    setSendFailed(false);
    setText('');
  };

  return (
    <section className="rounded-3xl bg-[#16181f]/90 border border-white/10 p-4 shadow-xl">
      <div className="flex items-center gap-2 mb-2">
        <div className="p-1.5 rounded-lg bg-[#D97757]/20 text-[#D97757]">
          <CornerDownLeft size={13} />
        </div>
        <h2 className="text-xs font-bold uppercase tracking-wider text-white">Send Prompt</h2>
        {isWorking && (
          <span className="flex items-center gap-1 text-[10px] font-semibold text-amber-300">
            <Loader2 size={10} className="animate-spin" /> queued until turn ends
          </span>
        )}
        {queueDepth > 0 && (
          <span className="flex items-center gap-1 text-[10px] font-semibold text-sky-300">
            <Inbox size={10} /> {queueDepth} queued
          </span>
        )}
      </div>
      <div className="flex gap-2">
        <input
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && send()}
          disabled={disabled}
          placeholder={
            disabled
              ? 'Waiting for a session…'
              : isWorking
                ? 'Queue a prompt for this task…'
                : 'Send a prompt to Claude…'
          }
          className="flex-1 min-h-[44px] px-4 py-3 rounded-xl bg-black/40 border border-white/10 text-sm text-white placeholder-slate-600 focus:outline-none focus:border-[#D97757] disabled:opacity-50"
        />
        <button
          type="button"
          onClick={send}
          disabled={disabled || !text.trim()}
          className="px-5 min-h-[44px] py-3 rounded-xl bg-[#D97757] hover:bg-[#e88666] disabled:opacity-40 text-white font-bold text-sm shadow-lg shadow-[#D97757]/30"
        >
          Send
        </button>
      </div>
      {sendFailed && (
        <p className="flex items-center gap-1.5 text-[10px] text-amber-400 mt-2">
          <WifiOff size={11} /> Not sent — connection is down. Your text is kept; try again when reconnected.
        </p>
      )}
      <p className="text-[10px] text-slate-600 mt-2">
        {isWorking
          ? 'Claude is working — your prompt is queued and delivered when the current turn finishes (same behavior as Claude Remote Control).'
          : 'Delivered via the Stop hook when you press Send.'}
      </p>
    </section>
  );
}
