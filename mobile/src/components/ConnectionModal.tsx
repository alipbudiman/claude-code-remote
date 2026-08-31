import React, { useState } from 'react';
import { X, Network, Check, RefreshCw, Smartphone, KeyRound } from 'lucide-react';
import { wsService } from '../services/websocketService';

interface ConnectionModalProps {
  isOpen: boolean;
  onClose: () => void;
  currentUrl: string;
  hostIps: string[];
  port: number;
  onSaveUrl: (url: string) => void;
}

export const ConnectionModal: React.FC<ConnectionModalProps> = ({
  isOpen,
  onClose,
  currentUrl,
  hostIps,
  port,
  onSaveUrl,
}) => {
  const [inputUrl, setInputUrl] = useState(currentUrl);
  const [inputToken, setInputToken] = useState(wsService.getToken());

  if (!isOpen) return null;

  const handleSave = (e: React.FormEvent) => {
    e.preventDefault();
    onSaveUrl(inputUrl);
    wsService.setToken(inputToken);
    onClose();
  };

  const handleSelectIP = (ip: string) => {
    const url = `http://${ip}:${port || 9280}`;
    setInputUrl(url);
    onSaveUrl(url);
    onClose();
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/80 backdrop-blur-md">
      <div className="w-full max-w-md rounded-3xl bg-[#16181f] border border-white/10 p-6 shadow-2xl animate-in fade-in zoom-in-95 duration-200">
        <div className="flex items-center justify-between mb-5">
          <div className="flex items-center space-x-2.5">
            <div className="p-2 rounded-xl bg-[#D97757]/20 text-[#D97757]">
              <Network size={20} />
            </div>
            <div>
              <h3 className="text-base font-bold text-white">LAN Connection Settings</h3>
              <p className="text-xs text-slate-400">Connect to Claude Remote Desktop</p>
            </div>
          </div>
          <button
            onClick={onClose}
            className="p-1.5 rounded-full bg-white/5 text-slate-400 hover:text-white"
          >
            <X size={18} />
          </button>
        </div>

        <form onSubmit={handleSave} className="space-y-4">
          <div>
            <label className="block text-xs font-semibold uppercase tracking-wider text-slate-400 mb-1.5">
              Desktop Server IP & Port
            </label>
            <input
              type="text"
              value={inputUrl}
              onChange={(e) => setInputUrl(e.target.value)}
              placeholder="http://192.168.1.15:9280"
              className="w-full px-4 py-3 rounded-xl bg-black/40 border border-white/10 text-white placeholder-slate-600 font-mono text-sm focus:outline-none focus:border-[#D97757]"
            />
            <p className="text-[11px] text-slate-500 mt-1">
              Example: <code>http://192.168.100.48:9280</code> (binds on 0.0.0.0, no internet required)
            </p>
          </div>

          <div>
            <label className="block text-xs font-semibold uppercase tracking-wider text-slate-400 mb-1.5">
              Token
            </label>
            <div className="relative">
              <KeyRound size={14} className="absolute left-3.5 top-1/2 -translate-y-1/2 text-slate-600" />
              <input
                type="password"
                value={inputToken}
                onChange={(e) => setInputToken(e.target.value)}
                placeholder="64-char token from desktop QR / URL"
                autoComplete="off"
                spellCheck={false}
                className="w-full pl-9 pr-4 py-3 rounded-xl bg-black/40 border border-white/10 text-white placeholder-slate-600 font-mono text-sm focus:outline-none focus:border-[#D97757]"
              />
            </div>
            <p className="text-[11px] text-slate-500 mt-1">
              Authentication token — scan the desktop QR code or copy <code>?token=...</code> from the server URL
            </p>
          </div>

          {hostIps.length > 0 && (
            <div>
              <label className="block text-xs font-semibold uppercase tracking-wider text-slate-400 mb-1.5">
                Detected PC Addresses
              </label>
              <div className="space-y-1.5">
                {hostIps.map((ip) => (
                  <button
                    key={ip}
                    type="button"
                    onClick={() => handleSelectIP(ip)}
                    className="w-full flex items-center justify-between px-3.5 py-2.5 rounded-xl bg-black/30 border border-white/5 text-xs text-left font-mono text-slate-300 hover:border-[#D97757]/40 transition-colors"
                  >
                    <span>http://{ip}:{port || 9280}</span>
                    <Check size={14} className="text-[#D97757]" />
                  </button>
                ))}
              </div>
            </div>
          )}

          <div className="pt-2 flex gap-2">
            <button
              type="submit"
              className="flex-1 py-3 rounded-xl bg-[#D97757] hover:bg-[#e88666] text-white font-bold text-sm shadow-lg shadow-[#D97757]/30 transition-all flex items-center justify-center gap-2"
            >
              <RefreshCw size={16} />
              Connect Server
            </button>
          </div>
        </form>

        <div className="mt-5 p-3 rounded-xl bg-white/[0.02] border border-white/5 flex items-start space-x-2 text-xs text-slate-400">
          <Smartphone size={16} className="text-[#D97757] shrink-0 mt-0.5" />
          <p>
            Make sure your phone and PC are connected to the same Wi-Fi network or Hotspot. No mobile data or internet is consumed.
          </p>
        </div>
      </div>
    </div>
  );
};
