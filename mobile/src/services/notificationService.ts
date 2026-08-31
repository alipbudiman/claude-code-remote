// Extend window object to recognize native AndroidBridge
declare global {
  interface Window {
    AndroidBridge?: {
      showNotification: (title: string, message: string, type: string) => void;
      updateOngoingNotification: (
        projectName: string,
        toolStatus: string,
        isWorking: boolean,
        isWaitingInput: boolean,
        subagentCount: number
      ) => void;
      // Persists server URL + token for the native MonitoringService
      // (foreground service) so it owns its own connection config.
      saveServerConfig: (url: string, token: string) => void;
      openChromeRemoteDesktop: () => void;
      isNativeAndroid: () => boolean;
      isBatteryUnrestricted: () => boolean;
      openBatterySettings: () => void;
    };
    __onBatteryStatusUpdate?: (isUnrestricted: boolean) => void;
  }
}

// Notification Service for Android APK & Mobile Web
//
// M4a note: heads-up alerts for live server `notification` frames are now
// owned by the native MonitoringService foreground service (it holds its own
// WebSocket), so App.tsx no longer routes server frames through notify().
// The chime/haptic paths below are currently exercised only by the local
// "Notifications Enabled" test alert and the web (non-APK) fallback. Full
// removal/retirement of the JS chime path is M4b scope — do not delete yet.
class NotificationService {
  private hasPermission: boolean = false;
  private audioContext: AudioContext | null = null;

  constructor() {
    this.checkPermission();
  }

  public async requestPermission(): Promise<boolean> {
    if (window.AndroidBridge) {
      this.hasPermission = true;
      return true;
    }

    if (!('Notification' in window)) {
      console.warn('Notifications not supported in this browser/webview');
      return false;
    }

    try {
      const perm = await Notification.requestPermission();
      this.hasPermission = perm === 'granted';
      return this.hasPermission;
    } catch (e) {
      console.error('Failed to request notification permission', e);
      return false;
    }
  }

  public checkPermission(): boolean {
    if (window.AndroidBridge) {
      this.hasPermission = true;
      return true;
    }
    if ('Notification' in window) {
      this.hasPermission = Notification.permission === 'granted';
    }
    return this.hasPermission;
  }

  public notify(
    title: string,
    body: string,
    type: 'working' | 'idle' | 'permission' | 'subagent' | 'info' | 'task_done' = 'info'
  ) {
    // 1. Send to Native Android Notification Manager if inside APK
    if (window.AndroidBridge && typeof window.AndroidBridge.showNotification === 'function') {
      try {
        window.AndroidBridge.showNotification(title, body, type);
      } catch (e) {
        console.error('AndroidBridge notification error', e);
      }
    }

    // 2. Play haptic feedback vibration on Android
    if ('vibrate' in navigator) {
      if (type === 'permission') {
        // Urgent alert pattern for user input / asking
        navigator.vibrate([200, 100, 200, 100, 400]);
      } else if (type === 'task_done') {
        // Double success buzz
        navigator.vibrate([100, 50, 150]);
      } else if (type === 'working') {
        navigator.vibrate([80, 40, 80]);
      } else {
        navigator.vibrate(60);
      }
    }

    // 3. Play audio chime
    this.playChime(type);

    // 4. Web Local Notification fallback
    if (!window.AndroidBridge && this.hasPermission && 'Notification' in window) {
      try {
        const notif = new Notification(title, {
          body,
          icon:
            type === 'working' || type === 'permission'
              ? './assets/claudecode-color.svg'
              : './assets/claudecode.svg',
          badge: './assets/claudecode.svg',
          tag: 'claude-status-' + Date.now(),
        });

        setTimeout(() => notif.close(), 7000);
      } catch (e) {
        console.warn('Native web notification failed', e);
      }
    }
  }

  /**
   * Updates the persistent ongoing notification in the Android notification shade (Task/Progress Bar)
   */
  public updateOngoingTaskBar(
    projectName: string,
    toolStatus: string,
    isWorking: boolean,
    isWaitingInput: boolean,
    subagentCount: number
  ) {
    if (window.AndroidBridge && typeof window.AndroidBridge.updateOngoingNotification === 'function') {
      try {
        window.AndroidBridge.updateOngoingNotification(
          projectName,
          toolStatus,
          isWorking,
          isWaitingInput,
          subagentCount
        );
      } catch (e) {
        console.error('Failed to update ongoing Android notification', e);
      }
    }
  }

  /**
   * Launches Chrome Remote Desktop app or web URL
   */
  public launchChromeRemoteDesktop() {
    if (window.AndroidBridge && typeof window.AndroidBridge.openChromeRemoteDesktop === 'function') {
      try {
        window.AndroidBridge.openChromeRemoteDesktop();
        return;
      } catch (e) {
        console.error('AndroidBridge CRD launch failed', e);
      }
    }
    // Fallback to browser URL
    window.open('https://remotedesktop.google.com/access', '_blank');
  }

  private playChime(type: string) {
    try {
      const AudioCtx = window.AudioContext || (window as any).webkitAudioContext;
      if (!AudioCtx) return;

      if (!this.audioContext) {
        this.audioContext = new AudioCtx();
      }

      if (this.audioContext.state === 'suspended') {
        this.audioContext.resume();
      }

      const now = this.audioContext.currentTime;

      if (type === 'permission') {
        // Urgent 2-tone prompt chime (A4 -> E5)
        const osc = this.audioContext.createOscillator();
        const gain = this.audioContext.createGain();
        osc.connect(gain);
        gain.connect(this.audioContext.destination);

        osc.frequency.setValueAtTime(440, now);
        osc.frequency.setValueAtTime(659.25, now + 0.12);
        gain.gain.setValueAtTime(0.18, now);
        gain.gain.exponentialRampToValueAtTime(0.001, now + 0.45);
        osc.start(now);
        osc.stop(now + 0.45);
      } else if (type === 'task_done') {
        // Celebration finished chime (C5 -> E5 -> G5)
        const osc = this.audioContext.createOscillator();
        const gain = this.audioContext.createGain();
        osc.connect(gain);
        gain.connect(this.audioContext.destination);

        osc.frequency.setValueAtTime(523.25, now);
        osc.frequency.setValueAtTime(659.25, now + 0.1);
        osc.frequency.setValueAtTime(783.99, now + 0.2);
        gain.gain.setValueAtTime(0.12, now);
        gain.gain.exponentialRampToValueAtTime(0.001, now + 0.5);
        osc.start(now);
        osc.stop(now + 0.5);
      } else if (type === 'working' || type === 'subagent') {
        const osc = this.audioContext.createOscillator();
        const gain = this.audioContext.createGain();
        osc.connect(gain);
        gain.connect(this.audioContext.destination);

        osc.frequency.setValueAtTime(587.33, now);
        osc.frequency.exponentialRampToValueAtTime(880, now + 0.15);
        gain.gain.setValueAtTime(0.08, now);
        gain.gain.exponentialRampToValueAtTime(0.001, now + 0.25);
        osc.start(now);
        osc.stop(now + 0.25);
      } else {
        const osc = this.audioContext.createOscillator();
        const gain = this.audioContext.createGain();
        osc.connect(gain);
        gain.connect(this.audioContext.destination);

        osc.frequency.setValueAtTime(659.25, now);
        osc.frequency.exponentialRampToValueAtTime(440, now + 0.18);
        gain.gain.setValueAtTime(0.06, now);
        gain.gain.exponentialRampToValueAtTime(0.001, now + 0.25);
        osc.start(now);
        osc.stop(now + 0.25);
      }
    } catch {
      // Audio policy safe
    }
  }
}

export const notificationService = new NotificationService();
