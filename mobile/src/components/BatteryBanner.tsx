import React, { useState, useEffect } from 'react';
import { BatteryWarning, Settings, X, ChevronDown } from 'lucide-react';

/**
 * BatteryOptimizationBanner
 *
 * Displays a dismissible in-app warning when the Android device's
 * battery optimization mode is NOT set to "Unrestricted" for this app.
 * Provides a direct "Open Settings" quick action that launches the
 * system battery optimization whitelist dialog via the native bridge.
 */
export const BatteryBanner: React.FC = () => {
  const [isUnrestricted, setIsUnrestricted] = useState<boolean>(true);
  const [isDismissed, setIsDismissed] = useState<boolean>(false);
  const [isExpanded, setIsExpanded] = useState<boolean>(false);

  useEffect(() => {
    // Check initial status from native bridge
    if (window.AndroidBridge?.isBatteryUnrestricted) {
      try {
        setIsUnrestricted(window.AndroidBridge.isBatteryUnrestricted());
      } catch {
        // Fallback: assume unrestricted if bridge fails
      }
    }

    // Listen for push updates from MainActivity.onResume()
    window.__onBatteryStatusUpdate = (status: boolean) => {
      setIsUnrestricted(status);
      if (status) setIsDismissed(false); // Auto-dismiss when fixed
    };

    return () => {
      window.__onBatteryStatusUpdate = undefined;
    };
  }, []);

  // Don't render if already unrestricted, dismissed, or not on native Android
  if (isUnrestricted || isDismissed || !window.AndroidBridge) {
    return null;
  }

  const handleOpenSettings = () => {
    if (window.AndroidBridge?.openBatterySettings) {
      try {
        window.AndroidBridge.openBatterySettings();
      } catch (e) {
        console.error('Failed to open battery settings', e);
      }
    }
  };

  return (
    <div className="rounded-2xl bg-amber-500/10 border border-amber-500/30 overflow-hidden transition-all duration-300">
      {/* Compact Banner */}
      <div className="px-4 py-3 flex items-center justify-between gap-2">
        <div
          className="flex items-center gap-2.5 flex-1 min-w-0 cursor-pointer"
          onClick={() => setIsExpanded(!isExpanded)}
        >
          <div className="p-1.5 rounded-lg bg-amber-500/20 shrink-0">
            <BatteryWarning size={16} className="text-amber-400" />
          </div>
          <div className="min-w-0">
            <p className="text-xs font-bold text-amber-300 tracking-wide">
              Background Access Restricted
            </p>
            <p className="text-[11px] text-amber-400/70 truncate">
              Tap for details • Battery set to Optimized
            </p>
          </div>
          <ChevronDown
            size={14}
            className={`text-amber-400/60 shrink-0 transition-transform duration-300 ${
              isExpanded ? 'rotate-180' : ''
            }`}
          />
        </div>

        <div className="flex items-center gap-1.5 shrink-0">
          <button
            onClick={handleOpenSettings}
            className="px-2.5 py-1 rounded-lg bg-amber-500/20 border border-amber-500/40 text-amber-300 text-[10px] font-bold uppercase tracking-wider hover:bg-amber-500/30 transition-colors flex items-center gap-1"
          >
            <Settings size={11} />
            Fix
          </button>
          <button
            onClick={() => setIsDismissed(true)}
            className="p-1 rounded-lg text-amber-500/50 hover:text-amber-300 hover:bg-amber-500/10 transition-colors"
          >
            <X size={14} />
          </button>
        </div>
      </div>

      {/* Expanded Details */}
      <div
        className={`overflow-hidden transition-all duration-300 ease-in-out ${
          isExpanded ? 'max-h-60 opacity-100' : 'max-h-0 opacity-0'
        }`}
      >
        <div className="px-4 pb-4 pt-1 border-t border-amber-500/15">
          <p className="text-xs text-amber-200/80 leading-relaxed mb-3">
            Claude Remote requires <span className="font-bold text-amber-200">Unrestricted</span> battery
            mode to maintain persistent WebSocket connections with your workstation.
            Without this, Android's Doze optimization will:
          </p>
          <ul className="space-y-1.5 text-[11px] text-amber-300/70 mb-3 pl-1">
            <li className="flex items-start gap-1.5">
              <span className="text-amber-400 mt-0.5">•</span>
              Terminate background network connections
            </li>
            <li className="flex items-start gap-1.5">
              <span className="text-amber-400 mt-0.5">•</span>
              Delay or suppress push notifications for questions & task completions
            </li>
            <li className="flex items-start gap-1.5">
              <span className="text-amber-400 mt-0.5">•</span>
              Prevent real-time session monitoring while the app is minimized
            </li>
          </ul>
          <button
            onClick={handleOpenSettings}
            className="w-full py-2 rounded-xl bg-amber-500/20 border border-amber-500/40 text-amber-200 text-xs font-bold hover:bg-amber-500/30 transition-colors flex items-center justify-center gap-2"
          >
            <Settings size={13} />
            Open Battery Settings → Select "Unrestricted"
          </button>
        </div>
      </div>
    </div>
  );
};
