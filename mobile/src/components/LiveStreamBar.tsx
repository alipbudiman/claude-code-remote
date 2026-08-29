import React, { useState, useEffect } from 'react';
import { Radio, Pause, ChevronUp, ChevronDown, Activity, Bot, Sparkles } from 'lucide-react';
import { Session } from '../types';
import ColorIcon from '../assets/claudecode-color.svg';
import MonoIcon from '../assets/claudecode.svg';

interface LiveStreamBarProps {
  session?: Session;
  isWorking: boolean;
}

export const LiveStreamBar: React.FC<LiveStreamBarProps> = ({
  session,
  isWorking,
}) => {
  const [isExpanded, setIsExpanded] = useState<boolean>(false);
  const [elapsedSeconds, setElapsedSeconds] = useState<number>(0);

  // Live timer for active sessions
  useEffect(() => {
    let interval: number;
    if (isWorking) {
      interval = window.setInterval(() => {
        setElapsedSeconds((prev) => prev + 1);
      }, 1000);
    } else {
      setElapsedSeconds(0);
    }
    return () => clearInterval(interval);
  }, [isWorking]);

  const formatTimer = (seconds: number) => {
    const mins = Math.floor(seconds / 60);
    const secs = seconds % 60;
    return `${mins.toString().padStart(2, '0')}:${secs.toString().padStart(2, '0')}`;
  };

  const isWaitingInput = session?.status === 'waiting_permission' || session?.pending_question !== undefined;
  const subagentCount = Object.keys(session?.active_subagents || {}).length;
  const trackTitle = session?.current_tool_status || (isWorking ? 'Claude Agent executing...' : 'Session idle');
  const artistName = `${session?.project_name || 'Claude Code'} ${subagentCount > 0 ? `• ${subagentCount} Sub-agent${subagentCount > 1 ? 's' : ''}` : ''}`;

  return (
    <>
      {/* ── 1. Mini Floating Stream Bar (Docked at Bottom) ── */}
      <div className="fixed bottom-3 left-3 right-3 z-40 max-w-lg mx-auto">
        <div
          onClick={() => setIsExpanded(!isExpanded)}
          className={`relative overflow-hidden rounded-2xl p-3 backdrop-blur-xl border transition-all duration-300 shadow-2xl cursor-pointer ${
            isWaitingInput
              ? 'bg-[#1e1710]/95 border-amber-500/60 shadow-[0_0_25px_rgba(245,158,11,0.25)]'
              : isWorking
              ? 'bg-[#181a24]/95 border-[#D97757]/50 shadow-[0_0_25px_rgba(217,119,87,0.25)]'
              : 'bg-[#12131a]/95 border-white/10'
          }`}
        >
          {/* Top Progress Stream Glow Bar */}
          {isWorking && (
            <div className="absolute top-0 left-0 right-0 h-1 bg-gradient-to-r from-transparent via-[#D97757] to-transparent animate-pulse" />
          )}
          {isWaitingInput && (
            <div className="absolute top-0 left-0 right-0 h-1 bg-gradient-to-r from-transparent via-amber-400 to-transparent animate-pulse" />
          )}

          <div className="flex items-center justify-between gap-3">
            {/* Left: Album / Stream Vinyl Art */}
            <div className="relative shrink-0">
              <div
                className={`w-11 h-11 rounded-xl flex items-center justify-center border transition-all duration-300 ${
                  isWorking
                    ? 'bg-[#D97757]/20 border-[#D97757]/60 shadow-[0_0_15px_rgba(217,119,87,0.4)]'
                    : 'bg-white/5 border-white/10'
                }`}
              >
                <img
                  src={isWorking || isWaitingInput ? ColorIcon : MonoIcon}
                  alt="Stream Icon"
                  className={`w-6 h-6 object-contain ${isWorking ? 'animate-[spin_6s_linear_infinite]' : ''}`}
                />
              </div>

              {/* Live Equalizer indicator over icon */}
              {isWorking && (
                <span className="absolute -top-1 -right-1 flex h-3 w-3">
                  <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-[#D97757] opacity-75"></span>
                  <span className="relative inline-flex rounded-full h-3 w-3 bg-[#D97757]"></span>
                </span>
              )}
            </div>

            {/* Center: Track & Artist Info */}
            <div className="flex-1 min-w-0">
              <div className="flex items-center space-x-1.5 mb-0.5">
                <span
                  className={`text-[9px] font-extrabold px-1.5 py-0.2 rounded-full uppercase tracking-wider ${
                    isWaitingInput
                      ? 'bg-amber-500/20 text-amber-300 border border-amber-500/40 animate-pulse'
                      : isWorking
                      ? 'bg-[#D97757]/20 text-[#ffaa88] border border-[#D97757]/40'
                      : 'bg-slate-800 text-slate-400 border border-slate-700'
                  }`}
                >
                  {isWaitingInput ? 'INPUT NEEDED' : isWorking ? 'LIVE STREAM' : 'PAUSED'}
                </span>
                <span className="text-[11px] font-mono text-slate-400 truncate">
                  {artistName}
                </span>
              </div>

              <div className="text-xs font-bold text-white truncate leading-tight">
                {trackTitle}
              </div>
            </div>

            {/* Right: Soundwave Equalizer & Timer / Expand */}
            <div className="flex items-center space-x-2.5 shrink-0">
              {/* Animated Soundwave / Frequency Equalizer */}
              {isWorking ? (
                <div className="flex items-end space-x-0.5 h-5 px-1.5 py-0.5 rounded bg-black/40 border border-white/5">
                  <div className="w-1 bg-[#D97757] rounded-full animate-[bounce_0.8s_infinite_100ms] h-4" />
                  <div className="w-1 bg-[#D97757] rounded-full animate-[bounce_0.6s_infinite_200ms] h-5" />
                  <div className="w-1 bg-[#D97757] rounded-full animate-[bounce_0.9s_infinite_50ms] h-3" />
                  <div className="w-1 bg-[#D97757] rounded-full animate-[bounce_0.7s_infinite_300ms] h-4" />
                </div>
              ) : (
                <div className="flex items-center space-x-1 text-slate-500">
                  <Pause size={14} />
                </div>
              )}

              {/* Timer */}
              <div className="text-[11px] font-mono font-bold text-slate-300">
                {formatTimer(elapsedSeconds)}
              </div>

              {/* Expand Toggle */}
              <button
                type="button"
                className="p-1 rounded-lg bg-white/5 text-slate-400 hover:text-white"
              >
                {isExpanded ? <ChevronDown size={16} /> : <ChevronUp size={16} />}
              </button>
            </div>
          </div>
        </div>
      </div>

      {/* ── 2. Expanded Sidebar / Stream Drawer Modal ── */}
      {isExpanded && (
        <div className="fixed inset-0 z-50 flex items-end sm:items-center justify-center p-0 sm:p-4 bg-black/80 backdrop-blur-md animate-in fade-in duration-200">
          <div className="w-full max-w-lg rounded-t-3xl sm:rounded-3xl bg-[#16181f] border border-white/10 p-6 shadow-2xl max-h-[85vh] overflow-y-auto">
            {/* Drawer Header */}
            <div className="flex items-center justify-between pb-4 border-b border-white/10 mb-5">
              <div className="flex items-center space-x-2.5">
                <div className="p-2 rounded-xl bg-[#D97757]/20 text-[#D97757]">
                  <Radio size={20} className={isWorking ? 'animate-pulse' : ''} />
                </div>
                <div>
                  <h3 className="text-base font-bold text-white flex items-center gap-2">
                    Agent Stream Console
                    <span className="text-[10px] font-mono px-2 py-0.5 rounded-full bg-[#D97757]/20 text-[#D97757] border border-[#D97757]/30">
                      LIVE
                    </span>
                  </h3>
                  <p className="text-xs text-slate-400 font-mono">{session?.project_name || 'Active Session'}</p>
                </div>
              </div>
              <button
                onClick={() => setIsExpanded(false)}
                className="p-2 rounded-full bg-white/5 text-slate-400 hover:text-white"
              >
                <ChevronDown size={20} />
              </button>
            </div>

            {/* Visualizer Hero Card in Drawer */}
            <div className="rounded-2xl bg-black/40 border border-white/10 p-5 flex flex-col items-center text-center mb-5">
              <div className="relative w-20 h-20 rounded-full flex items-center justify-center bg-[#D97757]/15 border border-[#D97757]/40 mb-3 shadow-[0_0_30px_rgba(217,119,87,0.3)]">
                <img
                  src={isWorking ? ColorIcon : MonoIcon}
                  alt="Stream Vinyl"
                  className={`w-12 h-12 object-contain ${isWorking ? 'animate-[spin_8s_linear_infinite]' : ''}`}
                />
              </div>

              <div className="text-sm font-bold text-white mb-1">
                {session?.current_tool_status || 'Session Active'}
              </div>
              <div className="text-xs text-slate-400 font-mono mb-3">
                Current Channel: {session?.current_tool ? `Tool [${session.current_tool}]` : 'Processing'}
              </div>

              {/* Big Waveform Visualizer */}
              <div className="flex items-end justify-center space-x-1 h-8 w-full max-w-xs px-4">
                {[40, 70, 30, 90, 60, 100, 45, 80, 50, 95, 35, 75, 60, 85, 40].map((h, idx) => (
                  <div
                    key={idx}
                    className={`flex-1 rounded-full transition-all duration-300 ${
                      isWorking ? 'bg-[#D97757]' : 'bg-slate-700'
                    }`}
                    style={{
                      height: isWorking ? `${Math.max(15, (h * (idx % 2 === 0 ? 1 : 0.8)))}%` : '15%',
                      opacity: isWorking ? 0.7 + (idx % 3) * 0.1 : 0.3,
                    }}
                  />
                ))}
              </div>
            </div>

            {/* Sub-Agent Channels */}
            {subagentCount > 0 && (
              <div className="mb-5">
                <div className="text-xs font-bold uppercase tracking-wider text-slate-400 mb-2 flex items-center gap-1.5">
                  <Bot size={14} className="text-indigo-400" />
                  Live Sub-Agent Channels ({subagentCount})
                </div>
                <div className="space-y-2">
                  {Object.values(session?.active_subagents || {}).map((sub) => (
                    <div
                      key={sub.id}
                      className="flex items-center justify-between p-3 rounded-xl bg-indigo-950/30 border border-indigo-500/30 text-xs"
                    >
                      <div className="truncate">
                        <div className="font-bold text-indigo-300">{sub.agent_type}</div>
                        <div className="text-slate-300 text-[11px] truncate">{sub.current_tool_status || sub.description}</div>
                      </div>
                      <span className="shrink-0 px-2 py-0.5 rounded text-[10px] font-bold bg-indigo-500/20 text-indigo-300 border border-indigo-500/30 animate-pulse">
                        STREAMING
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Tracklist / Recent Steps */}
            <div>
              <div className="text-xs font-bold uppercase tracking-wider text-slate-400 mb-2 flex items-center gap-1.5">
                <Activity size={14} className="text-[#D97757]" />
                Recent Execution Tracklist
              </div>
              <div className="space-y-1.5 max-h-44 overflow-y-auto pr-1">
                {session?.recent_logs && session.recent_logs.length > 0 ? (
                  session.recent_logs.map((log, idx) => (
                    <div
                      key={idx}
                      className="flex items-center space-x-2 p-2 rounded-xl bg-black/25 border border-white/5 text-[11px] font-mono text-slate-300"
                    >
                      <Sparkles size={11} className="text-[#D97757] shrink-0" />
                      <span className="truncate">{log}</span>
                    </div>
                  ))
                ) : (
                  <div className="text-xs text-slate-600 font-mono text-center py-3">
                    No stream history yet
                  </div>
                )}
              </div>
            </div>

            {/* Close Drawer button */}
            <div className="mt-5 pt-3 border-t border-white/10">
              <button
                onClick={() => setIsExpanded(false)}
                className="w-full py-2.5 rounded-xl bg-white/10 hover:bg-white/15 text-white font-bold text-xs transition-colors"
              >
                Close Stream Console
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
};
