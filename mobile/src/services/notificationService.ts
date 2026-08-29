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

  public notify(title: string, body: string, type: 'working' | 'idle' | 'permission' | 'subagent' | 'info' | 'task_done' = 'info') {
    // 1. Play haptic feedback vibration on Android
    if ('vibrate' in navigator) {
      if (type === 'permission') {
        // Urgent alert pattern for user input
        navigator.vibrate([150, 80, 150, 80, 300]);
      } else if (type === 'task_done') {
        // Double success buzz
        navigator.vibrate([100, 50, 150]);
      } else if (type === 'working') {
        navigator.vibrate([80, 40, 80]);
      } else {
        navigator.vibrate(60);
      }
    }

    // 2. Play audio chime
    this.playChime(type);

    // 3. System / Local Notification
    if (this.hasPermission && 'Notification' in window) {
      try {
        const notif = new Notification(title, {
          body,
          icon: type === 'working' || type === 'permission' ? '/assets/claudecode-color.svg' : '/assets/claudecode.svg',
          badge: '/assets/claudecode.svg',
          tag: 'claude-status-' + Date.now(),
        });

        setTimeout(() => notif.close(), 7000);
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

      const now = this.audioContext.currentTime;

      if (type === 'permission') {
        // Urgent 2-tone prompt chime (A4 -> E5)
        const osc = this.audioContext.createOscillator();
        const gain = this.audioContext.createGain();
        osc.connect(gain);
        gain.connect(this.audioContext.destination);

        osc.frequency.setValueAtTime(440, now);
        osc.frequency.setValueAtTime(659.25, now + 0.12);
        gain.gain.setValueAtTime(0.12, now);
        gain.gain.exponentialRampToValueAtTime(0.001, now + 0.4);
        osc.start(now);
        osc.stop(now + 0.4);
      } else if (type === 'task_done') {
        // Celebration finished chime (C5 -> E5 -> G5)
        const osc = this.audioContext.createOscillator();
        const gain = this.audioContext.createGain();
        osc.connect(gain);
        gain.connect(this.audioContext.destination);

        osc.frequency.setValueAtTime(523.25, now);
        osc.frequency.setValueAtTime(659.25, now + 0.1);
        osc.frequency.setValueAtTime(783.99, now + 0.2);
        gain.gain.setValueAtTime(0.1, now);
        gain.gain.exponentialRampToValueAtTime(0.001, now + 0.45);
        osc.start(now);
        osc.stop(now + 0.45);
      } else if (type === 'working' || type === 'subagent') {
        // Working started chime
        const osc = this.audioContext.createOscillator();
        const gain = this.audioContext.createGain();
        osc.connect(gain);
        gain.connect(this.audioContext.destination);

        osc.frequency.setValueAtTime(587.33, now);
        osc.frequency.exponentialRampToValueAtTime(880, now + 0.15);
        gain.gain.setValueAtTime(0.07, now);
        gain.gain.exponentialRampToValueAtTime(0.001, now + 0.25);
        osc.start(now);
        osc.stop(now + 0.25);
      } else {
        // Soft idle note
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
