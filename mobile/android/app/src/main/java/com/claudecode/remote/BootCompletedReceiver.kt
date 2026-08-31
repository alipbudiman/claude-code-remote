package com.claudecode.remote

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log

/**
 * Best-effort tracking resume after device reboot (M4a).
 *
 * BOOT_COMPLETED is a protected system broadcast: it is delivered to this
 * receiver even though exported="false" (exported only gates sends from
 * OTHER apps). Starting a dataSync foreground service from the boot path is
 * allowed up to Android 14; Android 15 restricts it, and
 * ContextCompat.startForegroundService / Service.startForeground may then
 * throw ForegroundServiceStartNotAllowedException. That is why everything is
 * wrapped in a catch-all: failing to resume tracking after a reboot must
 * never crash the app. The user simply reopens the app once (MainActivity
 * starts the service on every onCreate) and tracking resumes.
 */
class BootCompletedReceiver : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_BOOT_COMPLETED) return

        try {
            MonitoringService.start(context)
            Log.i("BootCompletedReceiver", "MonitoringService start requested after boot")
        } catch (t: Throwable) {
            // Android 15: starting a dataSync FGS from BOOT_COMPLETED can be
            // forbidden (ForegroundServiceStartNotAllowedException). Log and
            // give up gracefully — reopening the app resumes tracking.
            Log.w("BootCompletedReceiver", "Could not start MonitoringService after boot: ${t.message}")
        }
    }
}
