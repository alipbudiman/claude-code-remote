import React from 'react';
import { Activity, BellRing, Sparkles } from 'lucide-react';
import { AppNotification } from '../types';

interface ActivityLogsProps {
  logs: string[];
  notifications: AppNotification[];
}

export const ActivityLogs: React.FC<ActivityLogsProps> = ({ logs, notifications }) => {
  return (
    <div className="rounded-3xl bg-[#16181f]/90 border border-white/10 p-5 shadow-xl">
      <div className="flex items-center space-x-2 mb-4">
        <div className="p-1.5 rounded-lg bg-[#D97757]/20 text-[#D97757]">
          <Activity size={18} />
        </div>
        <div>
          <h3 className="text-sm font-bold uppercase tracking-wider text-slate-300">
            Real-Time Activity Stream
          </h3>
          <p className="text-xs text-slate-500">Live tool executions & notifications</p>
        </div>
      </div>

      {/* Notifications highlights */}
      {notifications.length > 0 && (
        <div className="mb-3 space-y-1.5">
          {notifications.slice(0, 3).map((notif) => (
            <div
              key={notif.id}
              className="flex items-start space-x-2.5 p-2.5 rounded-xl bg-[#D97757]/10 border border-[#D97757]/20 text-xs"
            >
              <BellRing size={14} className="text-[#D97757] shrink-0 mt-0.5" />
              <div className="flex-1 min-w-0">
                <div className="font-semibold text-white truncate">{notif.title}</div>
                <div className="text-slate-300 text-[11px] break-words">{notif.body}</div>
              </div>
              <span className="text-[10px] text-slate-500 font-mono shrink-0">
                {new Date(notif.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
              </span>
            </div>
          ))}
        </div>
      )}

      {/* Activity Log Feed */}
      <div className="space-y-1.5 max-h-52 overflow-y-auto pr-1">
        {logs.length > 0 ? (
          logs.map((log, idx) => (
            <div
              key={idx}
              className="flex items-center space-x-2 px-3 py-2 rounded-xl bg-black/30 border border-white/5 font-mono text-[11px] text-slate-300"
            >
              <Sparkles size={11} className="text-[#D97757] shrink-0" />
              <span className="truncate">{log}</span>
            </div>
          ))
        ) : (
          <div className="text-center py-4 text-xs text-slate-600 font-mono">
            Waiting for session logs...
          </div>
        )}
      </div>
    </div>
  );
};
