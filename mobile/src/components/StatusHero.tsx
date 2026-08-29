import React from 'react';
import { Session } from '../types';
import { FolderGit2, Clock, Sparkles, Terminal, ShieldAlert } from 'lucide-react';
import ColorIcon from '../assets/claudecode-color.svg';
import MonoIcon from '../assets/claudecode.svg';

interface StatusHeroProps {
  session?: Session;
  isWorking: boolean;
}

export const StatusHero: React.FC<StatusHeroProps> = ({ session, isWorking }) => {
  const getStatusBadge = () => {
    if (!session) {
      return {
        text: 'IDLING',
        color: 'bg-slate-800 text-slate-400 border-slate-700',
        icon: null,
      };
    }

    switch (session.status) {
      case 'active':
        return {
          text: 'WORKING ON TASK',
          color: 'bg-[#D97757]/20 text-[#ffaa88] border-[#D97757]/40 shadow-[0_0_15px_rgba(217,119,87,0.25)]',
          icon: <Sparkles size={13} className="animate-pulse text-[#D97757]" />,
        };
      case 'subagent_running':
        return {
          text: 'SUB-AGENTS ACTIVE',
          color: 'bg-indigo-500/20 text-indigo-300 border-indigo-500/40 shadow-[0_0_15px_rgba(99,102,241,0.25)]',
          icon: <Terminal size={13} className="text-indigo-400" />,
        };
      case 'waiting_permission':
        return {
          text: 'WAITING FOR PERMISSION',
          color: 'bg-amber-500/20 text-amber-300 border-amber-500/40 animate-pulse',
          icon: <ShieldAlert size={13} className="text-amber-400" />,
        };
      case 'completed':
        return {
          text: 'COMPLETED',
          color: 'bg-emerald-500/20 text-emerald-300 border-emerald-500/40',
          icon: null,
        };
      default:
        return {
          text: 'IDLING',
          color: 'bg-slate-800/80 text-slate-400 border-slate-700/60',
          icon: null,
        };
    }
  };

  const badge = getStatusBadge();
  const toolDesc = session?.current_tool_status || (isWorking ? 'Executing task...' : 'Ready for next prompt');
  const projectName = session?.project_name || 'No active session';

  return (
    <div className="relative overflow-hidden rounded-3xl bg-gradient-to-b from-[#181a24] to-[#12131a] border border-white/10 p-6 flex flex-col items-center text-center shadow-2xl">
      {/* Background Decorative Ambient Glow */}
      <div
        className={`absolute -top-24 w-72 h-72 rounded-full blur-3xl pointer-events-none transition-all duration-700 ${
          isWorking ? 'bg-[#D97757]/20 scale-125' : 'bg-slate-600/10 scale-90'
        }`}
      />

      {/* Main Status SVG Icon */}
      <div className="relative mb-5">
        {/* Pulsing Ripple Effect when Working */}
        {isWorking && (
          <>
            <div className="absolute inset-0 rounded-full bg-[#D97757]/30 animate-ping" />
            <div className="absolute -inset-3 rounded-full border border-[#D97757]/40 animate-pulse" />
          </>
        )}

        <div
          className={`relative w-28 h-28 rounded-full flex items-center justify-center border transition-all duration-500 ${
            isWorking
              ? 'bg-[#D97757]/15 border-[#D97757]/60 shadow-[0_0_40px_rgba(217,119,87,0.4)]'
              : 'bg-white/5 border-white/10'
          }`}
        >
          <img
            src={isWorking ? ColorIcon : MonoIcon}
            alt={isWorking ? 'Working Status' : 'Idle Status'}
            className="w-16 h-16 object-contain transition-transform duration-300 transform"
          />
        </div>
      </div>

      {/* Status Badge */}
      <div className={`inline-flex items-center space-x-1.5 px-3.5 py-1 rounded-full border text-xs font-bold tracking-wider uppercase mb-3 ${badge.color}`}>
        {badge.icon}
        <span>{badge.text}</span>
      </div>

      {/* Current Tool Activity Description */}
      <h2 className="text-xl font-extrabold text-white tracking-tight leading-snug max-w-sm mb-2 break-words">
        {toolDesc}
      </h2>

      {/* Project and Session Metadata */}
      <div className="flex flex-wrap items-center justify-center gap-3 text-xs text-slate-400 mt-1 font-mono">
        <div className="flex items-center space-x-1 bg-black/30 px-2.5 py-1 rounded-lg border border-white/5">
          <FolderGit2 size={13} className="text-slate-500" />
          <span className="truncate max-w-[200px]">{projectName}</span>
        </div>

        {session && (
          <div className="flex items-center space-x-1 bg-black/30 px-2.5 py-1 rounded-lg border border-white/5">
            <Clock size={13} className="text-slate-500" />
            <span>{new Date(session.last_activity).toLocaleTimeString()}</span>
          </div>
        )}
      </div>
    </div>
  );
};
