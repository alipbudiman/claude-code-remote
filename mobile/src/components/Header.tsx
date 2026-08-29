import React from 'react';
import { Wifi, WifiOff, Settings, Bell, BellOff } from 'lucide-react';
import ColorIcon from '../assets/claudecode-color.svg';
import MonoIcon from '../assets/claudecode.svg';

interface HeaderProps {
  isConnected: boolean;
  isWorking: boolean;
  hasNotificationPerm: boolean;
  onOpenSettings: () => void;
  onRequestNotifications: () => void;
}

export const Header: React.FC<HeaderProps> = ({
  isConnected,
  isWorking,
  hasNotificationPerm,
  onOpenSettings,
  onRequestNotifications,
}) => {
  return (
    <header className="sticky top-0 z-40 bg-[#0d0e12]/90 backdrop-blur-md border-b border-white/10 px-4 py-3 flex items-center justify-between">
      <div className="flex items-center space-x-3">
        <div className={`w-8 h-8 rounded-lg flex items-center justify-center transition-all duration-300 ${
          isWorking ? 'bg-[#D97757]/20 shadow-[0_0_15px_rgba(217,119,87,0.4)]' : 'bg-white/5'
        }`}>
          <img
            src={isWorking ? ColorIcon : MonoIcon}
            alt="Claude Code"
            className="w-6 h-6 object-contain"
          />
        </div>
        <div>
          <h1 className="text-base font-bold tracking-tight text-white flex items-center gap-1.5">
            Claude Remote
            <span className="text-[10px] px-1.5 py-0.5 rounded bg-white/10 text-slate-300 font-mono">
              v1.0
            </span>
          </h1>
          <p className="text-[11px] text-slate-400 font-medium">Session Monitor</p>
        </div>
      </div>

      <div className="flex items-center space-x-2">
        {/* Notification Status */}
        <button
          onClick={onRequestNotifications}
          className={`p-2 rounded-xl border transition-all ${
            hasNotificationPerm
              ? 'bg-emerald-500/10 border-emerald-500/30 text-emerald-400'
              : 'bg-amber-500/10 border-amber-500/30 text-amber-400 animate-pulse'
          }`}
          title={hasNotificationPerm ? 'Notifications Active' : 'Enable Notifications'}
        >
          {hasNotificationPerm ? <Bell size={16} /> : <BellOff size={16} />}
        </button>

        {/* Connection Status & Settings */}
        <button
          onClick={onOpenSettings}
          className={`flex items-center space-x-1.5 px-2.5 py-1.5 rounded-xl border text-xs font-semibold transition-all ${
            isConnected
              ? 'bg-emerald-500/10 border-emerald-500/30 text-emerald-400'
              : 'bg-rose-500/10 border-rose-500/30 text-rose-400'
          }`}
        >
          {isConnected ? <Wifi size={14} /> : <WifiOff size={14} className="animate-pulse" />}
          <span className="hidden sm:inline">{isConnected ? 'LAN Connected' : 'Offline'}</span>
          <Settings size={14} className="ml-0.5 opacity-70" />
        </button>
      </div>
    </header>
  );
};
