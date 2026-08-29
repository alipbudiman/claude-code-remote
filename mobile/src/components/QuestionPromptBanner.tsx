import React from 'react';
import { HelpCircle, ShieldAlert, Terminal, AlertTriangle } from 'lucide-react';
import { PendingQuestion } from '../types';

interface QuestionPromptBannerProps {
  pendingQuestion?: PendingQuestion;
  projectName?: string;
}

export const QuestionPromptBanner: React.FC<QuestionPromptBannerProps> = ({
  pendingQuestion,
  projectName,
}) => {
  if (!pendingQuestion) return null;

  const isPermission = pendingQuestion.type === 'permission';

  return (
    <div className="relative overflow-hidden rounded-3xl bg-gradient-to-r from-amber-950/70 via-amber-900/50 to-[#181a24] border-2 border-amber-500/60 p-5 shadow-[0_0_30px_rgba(245,158,11,0.25)] animate-in fade-in zoom-in-95 duration-300">
      {/* Top Animated Pulse Strip */}
      <div className="absolute top-0 left-0 right-0 h-1.5 bg-gradient-to-r from-amber-400 via-orange-500 to-amber-400 animate-pulse" />

      <div className="flex items-start justify-between gap-3 mb-3">
        <div className="flex items-center space-x-2.5">
          <div className="p-2 rounded-xl bg-amber-500/20 text-amber-400 ring-2 ring-amber-500/40">
            {isPermission ? <ShieldAlert size={20} className="animate-bounce" /> : <HelpCircle size={20} className="animate-bounce" />}
          </div>
          <div>
            <div className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[10px] font-extrabold uppercase tracking-wider bg-amber-500/20 text-amber-300 border border-amber-500/40">
              <AlertTriangle size={11} />
              {isPermission ? 'PERMISSION REQUIRED' : 'CLAUDE HAS A QUESTION'}
            </div>
            <h3 className="text-sm font-bold text-white mt-1">
              {pendingQuestion.title || 'User Input Needed'}
            </h3>
          </div>
        </div>

        <span className="text-[11px] font-mono text-amber-400/80 bg-black/40 px-2 py-1 rounded-lg border border-amber-500/20">
          {new Date(pendingQuestion.asked_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
        </span>
      </div>

      {/* Main Question Body */}
      <div className="p-3.5 rounded-2xl bg-black/40 border border-amber-500/20 mb-3">
        <p className="text-sm font-semibold text-amber-100 leading-relaxed break-words">
          {pendingQuestion.question}
        </p>

        {pendingQuestion.reason && (
          <p className="text-xs text-amber-300/70 mt-1.5 font-mono">
            Reason: {pendingQuestion.reason}
          </p>
        )}
      </div>

      {/* Options if provided (for AskUserQuestion) */}
      {pendingQuestion.options && pendingQuestion.options.length > 0 && (
        <div className="space-y-1.5 mb-3">
          <span className="text-[11px] font-bold text-amber-300/80 uppercase tracking-wider">
            Available Choices:
          </span>
          <div className="grid grid-cols-1 gap-1.5">
            {pendingQuestion.options.map((opt, i) => (
              <div
                key={i}
                className="flex items-center space-x-2 px-3 py-2 rounded-xl bg-amber-500/10 border border-amber-500/20 text-xs font-mono text-amber-200"
              >
                <span className="w-4 h-4 rounded-full bg-amber-500/30 text-amber-300 flex items-center justify-center text-[10px] font-bold">
                  {i + 1}
                </span>
                <span className="truncate">{opt}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Terminal Action Guidance */}
      <div className="flex items-center justify-between pt-2 border-t border-amber-500/20 text-xs text-amber-300/80">
        <div className="flex items-center space-x-1.5">
          <Terminal size={14} className="text-amber-400" />
          <span className="font-medium">Reply on your PC Terminal</span>
        </div>
        <span className="text-[11px] font-mono opacity-80 truncate max-w-[150px]">
          {projectName || 'Claude Code CLI'}
        </span>
      </div>
    </div>
  );
};
