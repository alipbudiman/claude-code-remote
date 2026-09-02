import { useEffect, useState } from 'react';
import { ShieldCheck, Trash2, X } from 'lucide-react';
import type { AppSettings, PermissionsConfig } from '../types';

interface Props {
  open: boolean;
  onClose: () => void;
  settings: AppSettings;
  permissions?: PermissionsConfig;
  onAppSettings: (s: AppSettings) => void;
  onPermissions: (p: PermissionsConfig) => void;
  onClearLogs: () => void;
}

const MODES = ['default', 'acceptEdits', 'plan', 'bypassPermissions'] as const;
type RuleKind = 'deny' | 'ask' | 'allow';

/**
 * Remote Settings (2026-09-02): the phone-side editor for Claude Code's
 * settings.json permission rules (mode + allow/ask/deny lists — syntax is
 * Claude Code's own, evaluation order deny → ask → allow), plus this
 * server's approval wait and the activity-log auto-clear window.
 */
export default function SettingsModal({
  open, onClose, settings, permissions, onAppSettings, onPermissions, onClearLogs,
}: Props) {
  const [mode, setMode] = useState<string>('default');
  const [lists, setLists] = useState<Record<RuleKind, string[]>>({ allow: [], ask: [], deny: [] });
  const [newRule, setNewRule] = useState<Record<RuleKind, string>>({ allow: '', ask: '', deny: '' });

  useEffect(() => {
    if (permissions) {
      setMode((permissions.defaultMode as string) ?? 'default');
      setLists({
        allow: (permissions.allow as string[]) ?? [],
        ask: (permissions.ask as string[]) ?? [],
        deny: (permissions.deny as string[]) ?? [],
      });
    }
  }, [permissions]);

  if (!open) return null;

  // pushRules sends the FULL permissions object built from local state. Until
  // permissions_get's reply arrives that state is empty — sending now would
  // wipe every existing rule in settings.json. Block all writes until loaded.
  const pushRules = (next: typeof lists, nextMode = mode) => {
    if (!permissions) return;
    onPermissions({
      ...permissions,
      defaultMode: nextMode,
      allow: next.allow,
      ask: next.ask,
      deny: next.deny,
    });
  };

  const sectionCls = 'rounded-2xl bg-black/30 border border-white/5 p-4 space-y-3';
  const labelCls = 'text-[10px] font-extrabold uppercase tracking-wider text-slate-400';

  return (
    <div className="fixed inset-0 z-50 flex items-end sm:items-center justify-center p-0 sm:p-4 bg-black/80 backdrop-blur-md">
      <div className="w-full max-w-lg max-h-[85vh] overflow-y-auto rounded-t-3xl sm:rounded-3xl bg-[#16181f] border border-white/10 p-6 shadow-2xl animate-in fade-in zoom-in-95 duration-200">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-sm font-bold uppercase tracking-wider text-white flex items-center gap-2">
            <ShieldCheck size={15} className="text-[#D97757]" /> Remote Settings
          </h2>
          <button
            type="button"
            onClick={onClose}
            className="p-1.5 rounded-lg bg-black/40 border border-white/10 text-slate-400 hover:text-white"
          >
            <X size={14} />
          </button>
        </div>

        <div className={sectionCls}>
          <p className={labelCls}>Permission Mode (settings.json)</p>
          {!permissions && (
            <p className="text-[11px] text-slate-500 animate-pulse">Loading current rules from PC…</p>
          )}
          <div className={`grid grid-cols-2 gap-2 ${!permissions ? 'opacity-40 pointer-events-none' : ''}`}>
            {MODES.map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => {
                  setMode(m);
                  pushRules(lists, m);
                }}
                className={`min-h-[40px] px-3 py-2 rounded-xl border text-[11px] font-semibold
                  ${mode === m
                    ? 'bg-[#D97757]/20 border-[#D97757]/60 text-[#e88666]'
                    : 'bg-black/30 border-white/10 text-slate-300'}`}
              >
                {m}
              </button>
            ))}
          </div>
          <p className="text-[10px] text-slate-600">
            bypassPermissions skips approvals entirely (use only in trusted environments).
          </p>
        </div>

        {(['deny', 'ask', 'allow'] as const).map((kind) => (
          <div key={kind} className={sectionCls}>
            <p className={labelCls}>{kind} rules</p>
            {lists[kind].map((r, i) => (
              <div key={`${kind}-${i}`} className="flex items-center gap-2">
                <code className="flex-1 px-2.5 py-2 rounded-lg bg-black/40 border border-white/5 font-mono text-[11px] text-slate-300 truncate">
                  {r}
                </code>
                <button
                  type="button"
                  onClick={() => {
                    const next = { ...lists, [kind]: lists[kind].filter((_, j) => j !== i) };
                    setLists(next);
                    pushRules(next);
                  }}
                  className="p-2 rounded-lg bg-red-500/10 border border-red-500/30 text-red-400"
                >
                  <Trash2 size={12} />
                </button>
              </div>
            ))}
            <div className="flex gap-2">
              <input
                value={newRule[kind]}
                onChange={(e) => setNewRule({ ...newRule, [kind]: e.target.value })}
                disabled={!permissions}
                placeholder={`e.g. ${kind === 'allow' ? 'Bash(npm run *)' : 'Bash(git push *)'}`}
                className="flex-1 min-h-[40px] px-3 py-2 rounded-xl bg-black/40 border border-white/10 font-mono text-[11px] text-white placeholder-slate-600 focus:outline-none focus:border-[#D97757]"
              />
              <button
                type="button"
                onClick={() => {
                  const v = newRule[kind].trim();
                  if (!v) return;
                  const next = { ...lists, [kind]: [...lists[kind], v] };
                  setLists(next);
                  pushRules(next);
                  setNewRule({ ...newRule, [kind]: '' });
                }}
                disabled={!permissions}
                className="px-3 py-2 rounded-xl bg-[#D97757]/20 border border-[#D97757]/40 text-[#e88666] text-[11px] font-bold disabled:opacity-40"
              >
                Add
              </button>
            </div>
          </div>
        ))}

        <div className={sectionCls}>
          <p className={labelCls}>Remote approval wait</p>
          <div className="flex items-center gap-3">
            <input
              type="range"
              min={15}
              max={105}
              step={5}
              value={settings.approval_wait_s}
              onChange={(e) => onAppSettings({ ...settings, approval_wait_s: Number(e.target.value) })}
              className="flex-1 accent-[#D97757]"
            />
            <span className="text-xs font-mono text-slate-300 w-12 text-right">
              {settings.approval_wait_s}s
            </span>
          </div>
          <p className="text-[10px] text-slate-600">
            How long a permission waits for your phone before the PC terminal prompt appears.
          </p>
        </div>

        <div className={sectionCls}>
          <p className={labelCls}>Activity log auto-clear</p>
          <div className="grid grid-cols-4 gap-2">
            {[0, 5, 15, 30].map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => onAppSettings({ ...settings, log_autoclear_min: m })}
                className={`min-h-[40px] px-2 py-2 rounded-xl border text-[11px] font-semibold
                  ${settings.log_autoclear_min === m
                    ? 'bg-[#D97757]/20 border-[#D97757]/60 text-[#e88666]'
                    : 'bg-black/30 border-white/10 text-slate-300'}`}
              >
                {m === 0 ? 'Off' : `${m}m`}
              </button>
            ))}
          </div>
          <button
            type="button"
            onClick={onClearLogs}
            className="w-full min-h-[40px] mt-1 py-2.5 rounded-xl bg-black/40 border border-white/10 text-xs font-bold text-slate-300 hover:text-white"
          >
            Clear logs now
          </button>
        </div>
      </div>
    </div>
  );
}
