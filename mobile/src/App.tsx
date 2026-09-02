import React, { useState, useEffect } from 'react';
import { Header } from './components/Header';
import { StatusHero } from './components/StatusHero';
import { ChromeRemoteButton } from './components/ChromeRemoteButton';
import { SubagentInspector } from './components/SubagentInspector';
import { ActivityLogs } from './components/ActivityLogs';
import { ConnectionModal } from './components/ConnectionModal';
import { QuestionPromptBanner } from './components/QuestionPromptBanner';
import { LiveStreamBar } from './components/LiveStreamBar';
import { BatteryBanner } from './components/BatteryBanner';
import ProcessFeed from './components/ProcessFeed';
import DecisionBanner from './components/DecisionBanner';
import PromptComposer from './components/PromptComposer';
import SettingsModal from './components/SettingsModal';
import { wsService } from './services/websocketService';
import { notificationService } from './services/notificationService';
import {
  AppSettings, PermissionsConfig, Session, AppNotification,
  PendingDecision, ProcessEvent, ServerStateSnapshot, WebSocketMessage,
} from './types';

export const App: React.FC = () => {
  const [isConnected, setIsConnected] = useState<boolean>(false);
  const [hasNotificationPerm, setHasNotificationPerm] = useState<boolean>(false);
  // M4b: with no saved URL there is nothing to connect to — open the
  // ConnectionModal automatically on first launch instead of hammering a
  // hardcoded fallback address.
  const [isSettingsOpen, setIsSettingsOpen] = useState<boolean>(() => wsService.getServerUrl() === '');
  // Tracked in state so the offline banner reflects saves AND failover
  // promotions without re-reading the service on every render.
  const [serverUrl, setServerUrl] = useState<string>(wsService.getServerUrl());
  const [activeSession, setActiveSession] = useState<Session | undefined>();
  const [hostIps, setHostIps] = useState<string[]>([]);
  const [serverPort, setServerPort] = useState<number>(9280);
  const [notifications, setNotifications] = useState<AppNotification[]>([]);
  const [logs, setLogs] = useState<string[]>([]);
  // M7: true when the relay says we are ALONE in the room (peers 0) — the
  // desktop server is not dialed in, so nothing will ever arrive. Cleared by
  // any real data frame (initial_state / session_update / notification).
  const [relayWaiting, setRelayWaiting] = useState<boolean>(false);
  // Live process feed (2026-09-02): streaming agent execution steps.
  const [processEvents, setProcessEvents] = useState<ProcessEvent[]>([]);
  // Remote decision (2026-09-02): the interactive banner's pending approval /
  // question / plan. Cleared on decision_resolved (answer or expiry).
  const [activeDecision, setActiveDecision] = useState<PendingDecision | undefined>();
  // Remote Settings modal + its server-side state.
  const [isRemoteSettingsOpen, setIsRemoteSettingsOpen] = useState<boolean>(false);
  const [appSettings, setAppSettings] = useState<AppSettings>({ approval_wait_s: 60, log_autoclear_min: 0 });
  const [permissions, setPermissions] = useState<PermissionsConfig | undefined>();

  useEffect(() => {
    // Check initial notification status
    setHasNotificationPerm(notificationService.checkPermission());

    // Connection listener
    const unsubConn = wsService.onConnectionChange((connected) => {
      setIsConnected(connected);
      if (connected) {
        // A failover candidate may have been promoted to the saved URL.
        setServerUrl(wsService.getServerUrl());
        // Fetch initial snapshot on connect
        wsService.fetchStatus().then((snap) => {
          if (snap) handleSnapshot(snap);
        });
      }
    });

    // Message listener
    const unsubMsg = wsService.onMessage((msg: WebSocketMessage) => {
      handleWebSocketMessage(msg);
    });

    // Start WebSocket
    wsService.connect();

    return () => {
      unsubConn();
      unsubMsg();
    };
  }, []);

  const handleSnapshot = (snap: ServerStateSnapshot) => {
    setHostIps(snap.host_ips || []);
    setServerPort(snap.port || 9280);
    setActiveSession(snap.active_session);
    setNotifications(snap.notifications || []);
    // M4b: let the socket service fail over to these hosts if the saved URL
    // stops answering (the PC's LAN IP can change via DHCP).
    wsService.setFailoverCandidates(snap.host_ips || []);
    if (snap.active_session?.recent_logs) {
      setLogs(snap.active_session.recent_logs);
    }
    setProcessEvents(snap.recent_process_events || []);
    // Show the newest parked decision (usually none or one).
    const pending = snap.pending_decisions || [];
    setActiveDecision(pending.length > 0 ? pending[pending.length - 1] : undefined);
    if (snap.app_settings) setAppSettings(snap.app_settings);
  };

  const handleWebSocketMessage = (msg: WebSocketMessage) => {
    switch (msg.type) {
      case 'initial_state':
        setRelayWaiting(false);
        handleSnapshot(msg.data as ServerStateSnapshot);
        break;

      case 'session_update': {
        setRelayWaiting(false);
        const sess = msg.data as Session;
        setActiveSession(sess);
        if (sess.recent_logs) {
          setLogs(sess.recent_logs);
        }
        break;
      }

      case 'notification': {
        setRelayWaiting(false);
        const notif = msg.data as AppNotification;
        setNotifications((prev) => [notif, ...prev.slice(0, 49)]);
        // M4a: heads-up alerts inside the APK are owned by the native
        // MonitoringService (its own WebSocket), so they keep working when
        // the app is closed — this JS path must stay silent there or every
        // alert fires twice. Anywhere else (browser/PWA) there is no native
        // service, so the web fallback still has to fire here (M4b).
        if (!window.AndroidBridge) {
          notificationService.notify(notif.title, notif.body, notif.type);
        }
        break;
      }

      case 'room_status':
        // M7: relay says how many OTHER members share the room. peers 0 means
        // the desktop server has not dialed in — surface that instead of a
        // silent blank screen. Any peers > 0 means the snapshot is imminent,
        // so make sure no stale waiting banner lingers.
        setRelayWaiting(msg.data?.peers === 0);
        break;

      case 'process_event': {
        const evt = msg.data as ProcessEvent;
        if (!evt || evt.id === 0) break;
        setProcessEvents((prev) => {
          // Dedup by id (a replayed snapshot may overlap) and cap at 200.
          const next = prev.filter((p) => p.id !== evt.id);
          next.push(evt);
          next.sort((a, b) => a.id - b.id);
          return next.slice(-200);
        });
        break;
      }

      case 'process_sync': {
        const evts = (msg.data?.events || []) as ProcessEvent[];
        setProcessEvents(evts.slice(-200));
        break;
      }

      case 'logs_cleared':
        setProcessEvents([]);
        setLogs([]);
        setNotifications([]);
        break;

      case 'decision_pending':
        setRelayWaiting(false);
        setActiveDecision(msg.data as PendingDecision);
        break;

      case 'decision_resolved':
        setActiveDecision((cur) =>
          cur && cur.id === (msg.data as { decision_id: string }).decision_id ? undefined : cur
        );
        break;

      case 'app_settings':
        setAppSettings(msg.data as AppSettings);
        break;

      case 'permissions':
        setPermissions((msg.data as { permissions: PermissionsConfig }).permissions);
        break;
    }
  };

  // Determine working state for Hero Icon & Live Stream
  const isWorking =
    isConnected &&
    activeSession !== undefined &&
    (activeSession.status === 'active' ||
      activeSession.status === 'subagent_running' ||
      activeSession.status === 'waiting_permission');

  const isWaitingInput =
    activeSession?.status === 'waiting_permission' || activeSession?.pending_question !== undefined;

  // M9: explicit completion display — a session that completed at some point
  // (verified turn-end stamped last_completed_at) and hasn't started a new
  // turn (a new turn clears the marker server-side and flips status active).
  const taskCompleted =
    activeSession?.status === 'idle' && !!activeSession?.last_completed_at;

  // Sync with persistent ongoing Android notification bar
  useEffect(() => {
    const subCount = Object.keys(activeSession?.active_subagents || {}).length;
    notificationService.updateOngoingTaskBar(
      activeSession?.project_name || 'Claude Code',
      activeSession?.current_tool_status || (isWorking ? 'Executing tools...' : 'Ready for next prompt'),
      isWorking,
      isWaitingInput,
      subCount
    );
  }, [activeSession, isWorking, isWaitingInput]);

  const handleRequestNotifications = async () => {
    const granted = await notificationService.requestPermission();
    setHasNotificationPerm(granted);
    if (granted) {
      notificationService.notify(
        'Notifications Enabled',
        'You will now receive instant alerts for task completion and user questions.',
        'info'
      );
    }
  };

  const handleSaveUrl = (url: string) => {
    wsService.setServerUrl(url);
    // setServerUrl normalizes (scheme defaulting, inline-token extraction) —
    // read back what was actually saved so the banner shows the truth.
    setServerUrl(wsService.getServerUrl());
  };

  return (
    <div className="min-h-screen bg-[#0d0e12] text-slate-100 flex flex-col font-sans selection:bg-[#D97757]/30 pb-28">
      {/* Top Header */}
      <Header
        isConnected={isConnected}
        isWorking={isWorking}
        hasNotificationPerm={hasNotificationPerm}
        onOpenSettings={() => setIsSettingsOpen(true)}
        onOpenRemoteSettings={() => {
          setIsRemoteSettingsOpen(true);
          wsService.sendCommand('permissions_get');
        }}
        onRequestNotifications={handleRequestNotifications}
      />

      {/* Main Content Area */}
      <main className="flex-1 max-w-lg w-full mx-auto px-4 pt-4 space-y-4">
        {/* Offline Warning Banner */}
        {!isConnected && (
          <div
            onClick={() => setIsSettingsOpen(true)}
            className="p-3 rounded-2xl bg-amber-500/10 border border-amber-500/30 text-amber-300 text-xs font-semibold flex items-center justify-between cursor-pointer animate-pulse"
          >
            {serverUrl ? (
              <span>Connecting to PC ({serverUrl})...</span>
            ) : (
              <span>Set up your server — no desktop address saved yet</span>
            )}
            <span className="underline">{serverUrl ? 'Change IP' : 'Set up'}</span>
          </div>
        )}

        {/* M7: Relay Presence Banner — connected to the relay but ALONE in the
            room; clears as soon as the desktop server pushes real data. */}
        {isConnected && relayWaiting && (
          <div className="p-3 rounded-2xl bg-amber-500/10 border border-amber-500/30 text-amber-300 text-xs font-semibold flex items-center">
            <span>Connected to relay — waiting for your PC. Run the server with {'--relay <relay-url>'}.</span>
          </div>
        )}

        {/* 0a. Battery Optimization Warning Banner */}
        <BatteryBanner />

        {/* 0b. Interactive Decision Banner (remote approvals / questions /
            plan review). Falls back to the legacy display-only banner when
            no interactive decision is parked (approvals off, server older,
            or the question arrived while we were offline). */}
        {activeDecision ? (
          <DecisionBanner
            decision={activeDecision}
            onRespond={(input) => wsService.sendDecision(input)}
          />
        ) : (
          activeSession?.pending_question && (
            <QuestionPromptBanner
              pendingQuestion={activeSession.pending_question}
              projectName={activeSession.project_name}
            />
          )
        )}

        {/* 1. Status Hero Monitor with Color/Mono SVG Icons */}
        <StatusHero session={activeSession} isWorking={isWorking} taskCompleted={taskCompleted} />

        {/* 2. Chrome Remote Desktop Quick Launcher (Above Sub-Agents) */}
        <ChromeRemoteButton isWaitingInput={isWaitingInput} />

        {/* 2b. Remote Prompt Composer (mid-task prompt injection) */}
        <PromptComposer
          session={activeSession}
          isWorking={isWorking}
          onSend={(t) => wsService.sendPrompt(activeSession?.id ?? '', t)}
        />

        {/* 3. Sub-Agent Live Inspector */}
        <SubagentInspector
          activeSubagents={activeSession?.active_subagents || {}}
          subagentHistory={activeSession?.subagent_history || []}
        />

        {/* 4. Live Process Feed (streaming agent execution) */}
        <ProcessFeed events={processEvents} />

        {/* 5. Real-Time Activity Log */}
        <ActivityLogs
          logs={logs}
          notifications={notifications}
          autoClearMin={appSettings.log_autoclear_min}
          onClear={() => wsService.sendCommand('clear_logs')}
        />
      </main>

      {/* 5. Live Stream Bar / Music-style Agent Monitor */}
      <LiveStreamBar
        session={activeSession}
        isWorking={isWorking}
        taskCompleted={taskCompleted}
      />

      {/* Settings Modal */}
      <ConnectionModal
        isOpen={isSettingsOpen}
        onClose={() => setIsSettingsOpen(false)}
        currentUrl={wsService.getServerUrl()}
        hostIps={hostIps}
        port={serverPort}
        onSaveUrl={handleSaveUrl}
      />

      {/* Remote Settings Modal (permissions, approvals, log auto-clear) */}
      <SettingsModal
        open={isRemoteSettingsOpen}
        onClose={() => setIsRemoteSettingsOpen(false)}
        settings={appSettings}
        permissions={permissions}
        onAppSettings={(s) => {
          setAppSettings(s);
          wsService.sendCommand('app_settings', {
            approval_wait_s: s.approval_wait_s,
            log_autoclear_min: s.log_autoclear_min,
          });
        }}
        onPermissions={(p) => {
          setPermissions(p);
          wsService.sendCommand('permissions_set', { permissions: p });
        }}
        onClearLogs={() => wsService.sendCommand('clear_logs')}
      />
    </div>
  );
};
export default App;
