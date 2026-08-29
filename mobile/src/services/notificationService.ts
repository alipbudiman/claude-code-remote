// Notification Service for Android & Mobile Browser
class NotificationService {
  private hasPermission: boolean = false;
  private audioContext: AudioContext | null = null;

  constructor() {
    this.checkPermission();
  }

  public async requestPermission(): Promise<boolean> {
    if (!('Notification' in window)) {
      console.warn('Notifications not supported in this browser/webview');
      return false;
    }

    try {
      const perm = await Notification.requestPermission();
      this.hasPermission = (perm === 'granted');
      return this.hasPermission;
    } catch (e) {
      console.error('Failed to request notification permission', e);
      return false;
    }
  }

  public checkPermission(): boolean {
    if ('Notification' in window) {
      this.hasPermission = (Notification.permission === 'granted');
    }
    return this.hasPermission;
  }

  public notify(title: string, body: string, type: 'working' | 'idle' | 'permission' | 'subagent' | 'info' = 'info') {
    // 1. Play subtle haptic feedback vibration if on Android
    if ('vibrate' in navigator) {
      if (type === 'permission') {
        navigator.vibrate([100, 50, 100, 50, 200]);
      } else if (type === 'working') {
        navigator.vibrate([80, 40, 80]);
      } else {
        navigator.vibrate(60);
      }
    }

    // 2. Play soft chime
    this.playChime(type);

    // 3. System / Local Notification
    if (this.hasPermission && 'Notification' in window) {
      try {
        const notif = new Notification(title, {
          body,
          icon: type === 'working' ? '/assets/claudecode-color.svg' : '/assets/claudecode.svg',
          badge: '/assets/claudecode.svg',
          tag: 'claude-status-' + Date.now(),
        });

        setTimeout(() => notif.close(), 6000);
      } catch (e) {
        console.warn('Native notification failed, falling back to in-app toast', e);
      }
    }
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

      const osc = this.audioContext.createOscillator();
      const gain = this.audioContext.createGain();

      osc.connect(gain);
      gain.connect(this.audioContext.destination);

      const now = this.audioContext.currentTime;
      if (type === 'working' || type === 'subagent') {
        osc.frequency.setValueAtTime(587.33, now); // D5
        osc.frequency.exponentialRampToValueAtTime(880, now + 0.15); // A5
        gain.gain.setValueAtTime(0.08, now);
        gain.gain.exponentialRampToValueAtTime(0.001, now + 0.25);
        osc.start(now);
        osc.stop(now + 0.25);
      } else if (type === 'permission') {
        osc.frequency.setValueAtTime(440, now); // A4
        osc.frequency.setValueAtTime(659.25, now + 0.1); // E5
        gain.gain.setValueAtTime(0.1, now);
        gain.gain.exponentialRampToValueAtTime(0.001, now + 0.35);
        osc.start(now);
        osc.stop(now + 0.35);
      } else {
        // Idle / completion chime
        osc.frequency.setValueAtTime(783.99, now); // G5
        osc.frequency.exponentialRampToValueAtTime(523.25, now + 0.2); // C5
        gain.gain.setValueAtTime(0.08, now);
        gain.gain.exponentialRampToValueAtTime(0.001, now + 0.3);
        osc.start(now);
        osc.stop(now + 0.3);
      }
    } catch {
      // Audio autoplay policy might restrict until user gesture
    }
  }
}

export const notificationService = new NotificationService();
