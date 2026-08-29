import React from 'react';
import { Subagent } from '../types';
import { Bot, CheckCircle2, Cpu, ArrowRight } from 'lucide-react';

interface SubagentInspectorProps {
  activeSubagents: Record<string, Subagent>;
  subagentHistory: Subagent[];
}

export const SubagentInspector: React.FC<SubagentInspectorProps> = ({
  activeSubagents,
  subagentHistory,
}) => {
  const activeList = Object.values(activeSubagents || {});
  const historyList = subagentHistory || [];

  return (
    <div className="rounded-3xl bg-[#16181f]/90 border border-white/10 p-5 shadow-xl">
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center space-x-2">
          <div className="p-1.5 rounded-lg bg-indigo-500/20 text-indigo-400">
            <Cpu size={18} />
          </div>
          <div>
            <h3 className="text-sm font-bold uppercase tracking-wider text-slate-300">
              Sub-Agent Activity Inspector
            </h3>
            <p className="text-xs text-slate-500">Autonomous Worker Threads</p>
          </div>
        </div>
        <span className={`px-2.5 py-0.5 rounded-full text-xs font-bold font-mono ${
          activeList.length > 0
            ? 'bg-indigo-500/20 text-indigo-300 border border-indigo-500/40 animate-pulse'
            : 'bg-white/5 text-slate-500 border border-white/5'
        }`}>
          {activeList.length} Active
        </span>
      </div>

      {/* Active Subagents List */}
      {activeList.length > 0 ? (
        <div className="space-y-3 mb-4">
          {activeList.map((sub) => (
            <div
              key={sub.id}
              className="relative overflow-hidden rounded-2xl bg-gradient-to-r from-indigo-950/40 to-slate-900/40 border border-indigo-500/30 p-4"
            >
              <div className="absolute top-0 left-0 bottom-0 w-1 bg-indigo-500" />
              <div className="flex items-start justify-between gap-2 mb-2">
                <div className="flex items-center space-x-2">
                  <Bot size={16} className="text-indigo-400 shrink-0" />
                  <span className="font-bold text-xs uppercase tracking-wider text-indigo-300">
                    {sub.agent_type || 'Sub-Agent'}
                  </span>
                </div>
                <span className="inline-flex items-center px-2 py-0.5 rounded text-[10px] font-bold bg-emerald-500/20 text-emerald-300 border border-emerald-500/30">
                  RUNNING
                </span>
              </div>

              <div className="text-sm font-semibold text-white mb-1.5 flex items-center gap-1.5">
                <ArrowRight size={14} className="text-indigo-400 shrink-0" />
                <span className="break-words">
                  {sub.current_tool_status || sub.description || 'Executing task'}
                </span>
              </div>

              {sub.description && sub.description !== sub.current_tool_status && (
                <p className="text-xs text-slate-400 mb-2 pl-5 italic line-clamp-2">
                  "{sub.description}"
                </p>
              )}

              <div className="text-[11px] text-slate-500 font-mono pl-5">
                Started: {new Date(sub.started_at).toLocaleTimeString()}
              </div>
            </div>
          ))}
        </div>
      ) : (
        <div className="text-center py-6 border border-dashed border-white/5 rounded-2xl bg-white/[0.01] mb-4">
          <Bot size={28} className="mx-auto text-slate-600 mb-2 opacity-60" />
          <p className="text-xs text-slate-400 font-medium">No sub-agents currently running</p>
          <p className="text-[11px] text-slate-600 mt-0.5">
            Sub-agents spawned by Claude Code will appear here live
          </p>
        </div>
      )}

      {/* Completed Subagents History */}
      {historyList.length > 0 && (
        <div className="mt-4 pt-3 border-t border-white/5">
          <div className="text-[11px] font-bold uppercase tracking-wider text-slate-500 mb-2">
            Completed Sub-Tasks ({historyList.length})
          </div>
          <div className="space-y-1.5 max-h-36 overflow-y-auto pr-1 text-xs">
            {historyList.slice(0, 5).map((sub) => (
              <div
                key={sub.id}
                className="flex items-center justify-between p-2 rounded-xl bg-black/20 border border-white/5 text-slate-400"
              >
                <div className="flex items-center space-x-2 truncate">
                  <CheckCircle2 size={13} className="text-emerald-400 shrink-0" />
                  <span className="truncate font-medium text-slate-300">
                    {sub.description || sub.agent_type}
                  </span>
                </div>
                <span className="text-[10px] font-mono text-slate-500 shrink-0 ml-2">
                  {sub.completed_at ? new Date(sub.completed_at).toLocaleTimeString() : 'Done'}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
};
