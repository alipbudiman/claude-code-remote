import React from 'react';
import { Monitor, ExternalLink } from 'lucide-react';
import { notificationService } from '../services/notificationService';

interface ChromeRemoteButtonProps {
  isWaitingInput?: boolean;
}

export const ChromeRemoteButton: React.FC<ChromeRemoteButtonProps> = ({ isWaitingInput = false }) => {
  const handleClick = () => {
    notificationService.launchChromeRemoteDesktop();
  };

  return (
    <div
      onClick={handleClick}
      className={`group relative overflow-hidden rounded-2xl p-4 transition-all duration-300 cursor-pointer border shadow-lg ${
        isWaitingInput
          ? 'bg-gradient-to-r from-amber-950/60 via-blue-950/40 to-[#12141c] border-amber-500/50 shadow-[0_0_25px_rgba(245,158,11,0.2)] animate-pulse'
          : 'bg-gradient-to-r from-blue-950/40 via-[#181a24] to-[#12141c] border-blue-500/30 hover:border-blue-400/60 shadow-[0_4px_20px_rgba(0,0,0,0.3)] hover:shadow-[0_0_25px_rgba(59,130,246,0.25)]'
      }`}
    >
      {/* Subtle Glow Overlay */}
      <div className="absolute top-0 right-0 -mt-4 -mr-4 w-24 h-24 bg-blue-500/10 rounded-full blur-xl pointer-events-none" />

      <div className="flex items-center justify-between gap-3 relative z-10">
        <div className="flex items-center space-x-3">
          {/* Chrome Remote Icon Box */}
          <div className="w-11 h-11 rounded-xl bg-gradient-to-br from-blue-500 to-indigo-600 p-0.5 shadow-md flex items-center justify-center shrink-0 group-hover:scale-105 transition-transform">
            <div className="w-full h-full bg-[#0d0e12]/80 rounded-[10px] flex items-center justify-center text-blue-400 group-hover:text-blue-300">
              <Monitor size={22} className="group-hover:rotate-6 transition-transform" />
            </div>
          </div>

          <div>
            <div className="flex items-center space-x-2">
              <h4 className="text-sm font-bold text-white group-hover:text-blue-200 transition-colors flex items-center gap-1.5">
                Chrome Remote Desktop
                {isWaitingInput && (
                  <span className="text-[9px] font-extrabold px-1.5 py-0.2 rounded-full bg-amber-500/20 text-amber-300 border border-amber-500/40 uppercase">
                    INPUT READY
                  </span>
                )}
              </h4>
            </div>
            <p className="text-xs text-slate-400 mt-0.5">
              {isWaitingInput
                ? 'Tap to open PC terminal and respond to Claude'
                : 'Control PC terminal & live workspace'}
            </p>
          </div>
        </div>

        {/* Right Arrow / Action Button */}
        <div className="flex items-center space-x-1.5 px-3 py-2 rounded-xl bg-blue-500/15 border border-blue-500/30 text-blue-300 group-hover:bg-blue-500 group-hover:text-white transition-all text-xs font-semibold shrink-0">
          <span>Open</span>
          <ExternalLink size={14} />
        </div>
      </div>
    </div>
  );
};
