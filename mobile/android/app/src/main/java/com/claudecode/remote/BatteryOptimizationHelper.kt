package com.claudecode.remote

import android.app.AlertDialog
import android.content.Context
import android.content.Intent
import android.content.SharedPreferences
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import android.provider.Settings
import android.util.Log
import androidx.core.app.NotificationCompat

/**
 * BatteryOptimizationHelper
 *
 * Manages battery optimization state for Claude Remote.
 * This application relies on persistent background WebSocket connections
 * to stream real-time agent session data. Default battery restrictions
 * ("Optimized" / "Restricted") will terminate these connections,
 * causing missed notifications and stale session state.
 *
 * This helper:
 * 1. Shows a professional onboarding dialog on first launch
 * 2. Checks battery optimization status on each resume
 * 3. Displays a persistent dismissible notification when restricted
 * 4. Provides a direct intent to the system battery settings page
 */
class BatteryOptimizationHelper(private val context: Context) {

    companion object {
        private const val TAG = "BatteryOptHelper"
        private const val PREFS_NAME = "claude_remote_battery_prefs"
        private const val KEY_FIRST_LAUNCH_SHOWN = "first_launch_battery_dialog_shown"
        private const val KEY_BANNER_DISMISSED = "battery_banner_dismissed"
        private const val NOTIFICATION_BATTERY_ID = 3001
    }

    private val prefs: SharedPreferences =
        context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)

    private val powerManager: PowerManager =
        context.getSystemService(Context.POWER_SERVICE) as PowerManager

    /**
     * Returns true if the app is whitelisted from battery optimizations (unrestricted).
     */
    fun isIgnoringBatteryOptimizations(): Boolean {
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            powerManager.isIgnoringBatteryOptimizations(context.packageName)
        } else {
            true // Pre-Marshmallow doesn't have Doze
        }
    }

    /**
     * Show first-launch onboarding dialog explaining why unrestricted battery is needed.
     * Only shown once per install.
     */
    fun showFirstLaunchDialogIfNeeded() {
        if (prefs.getBoolean(KEY_FIRST_LAUNCH_SHOWN, false)) return
        if (isIgnoringBatteryOptimizations()) {
            prefs.edit().putBoolean(KEY_FIRST_LAUNCH_SHOWN, true).apply()
            return
        }

        try {
            AlertDialog.Builder(context, R.style.AppTheme_Dialog)
                .setTitle("⚡ Enable Unrestricted Background Access")
                .setMessage(
                    "Claude Remote requires unrestricted battery access to maintain " +
                    "persistent WebSocket connections with your development workstation.\n\n" +
                    "Without this, Android's battery optimization (Doze mode) will:\n\n" +
                    "• Terminate background network connections\n" +
                    "• Delay or suppress push notifications\n" +
                    "• Prevent real-time session monitoring\n\n" +
                    "This ensures you receive instant alerts when Claude Code needs " +
                    "your input or completes a task — even when the app is in the background.\n\n" +
                    "Tap 'Configure' to open Battery Settings → Select 'Unrestricted'."
                )
                .setPositiveButton("Configure") { _, _ ->
                    openBatteryOptimizationSettings()
                }
                .setNegativeButton("Later") { dialog, _ ->
                    dialog.dismiss()
                }
                .setCancelable(true)
                .show()
        } catch (e: Exception) {
            Log.e(TAG, "Failed to show first-launch battery dialog", e)
        }

        prefs.edit().putBoolean(KEY_FIRST_LAUNCH_SHOWN, true).apply()
    }

    /**
     * If battery optimization is NOT unrestricted, show a persistent notification
     * with a quick action to open settings.
     */
    fun showBatteryWarningNotificationIfNeeded() {
        if (isIgnoringBatteryOptimizations()) {
            // Already unrestricted — dismiss any existing warning
            dismissBatteryWarningNotification()
            return
        }

        try {
            val settingsIntent = Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS).apply {
                data = Uri.parse("package:${context.packageName}")
                flags = Intent.FLAG_ACTIVITY_NEW_TASK
            }
            val settingsPendingIntent = android.app.PendingIntent.getActivity(
                context,
                3002,
                settingsIntent,
                android.app.PendingIntent.FLAG_UPDATE_CURRENT or android.app.PendingIntent.FLAG_IMMUTABLE
            )

            val builder = NotificationCompat.Builder(context, NotificationHelper.CHANNEL_ALERTS_ID)
                .setSmallIcon(R.drawable.ic_launcher_foreground)
                .setContentTitle("⚠️ Background Access Restricted")
                .setContentText("Claude Remote may miss notifications. Tap to set battery to Unrestricted.")
                .setStyle(
                    NotificationCompat.BigTextStyle().bigText(
                        "Your battery setting is currently set to 'Optimized' or 'Restricted'. " +
                        "This will cause Android to terminate background WebSocket connections, " +
                        "resulting in missed real-time notifications and stale session data.\n\n" +
                        "Tap this notification to open App Settings → Battery → Unrestricted."
                    )
                )
                .setContentIntent(settingsPendingIntent)
                .setAutoCancel(true)
                .setPriority(NotificationCompat.PRIORITY_HIGH)
                .setCategory(NotificationCompat.CATEGORY_RECOMMENDATION)
                .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
                .setOngoing(false)
                .addAction(
                    android.R.drawable.ic_menu_manage,
                    "⚙️ Open Battery Settings",
                    settingsPendingIntent
                )

            val notifManager =
                context.getSystemService(Context.NOTIFICATION_SERVICE) as android.app.NotificationManager
            notifManager.notify(NOTIFICATION_BATTERY_ID, builder.build())
        } catch (e: Exception) {
            Log.e(TAG, "Failed to show battery warning notification", e)
        }
    }

    /**
     * Dismiss the battery warning notification (called when user grants unrestricted).
     */
    fun dismissBatteryWarningNotification() {
        try {
            val notifManager =
                context.getSystemService(Context.NOTIFICATION_SERVICE) as android.app.NotificationManager
            notifManager.cancel(NOTIFICATION_BATTERY_ID)
        } catch (e: Exception) {
            Log.e(TAG, "Failed to dismiss battery notification", e)
        }
    }

    /**
     * Opens the system battery optimization settings page for this app.
     * Tries ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS first (direct whitelist dialog),
     * then falls back to the app detail settings page.
     */
    fun openBatteryOptimizationSettings() {
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
                // Direct request to whitelist (shows system dialog)
                val intent = Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS).apply {
                    data = Uri.parse("package:${context.packageName}")
                    flags = Intent.FLAG_ACTIVITY_NEW_TASK
                }
                context.startActivity(intent)
            }
        } catch (e: Exception) {
            // Fallback: Open app detail settings
            try {
                val fallback = Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS).apply {
                    data = Uri.parse("package:${context.packageName}")
                    flags = Intent.FLAG_ACTIVITY_NEW_TASK
                }
                context.startActivity(fallback)
            } catch (e2: Exception) {
                Log.e(TAG, "Failed to open battery settings", e2)
            }
        }
    }

    /**
     * Resets the banner dismissed state (for testing/development).
     */
    fun resetBannerDismissed() {
        prefs.edit().putBoolean(KEY_BANNER_DISMISSED, false).apply()
    }
}
