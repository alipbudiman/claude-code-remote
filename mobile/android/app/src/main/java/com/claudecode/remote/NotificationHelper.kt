package com.claudecode.remote

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.media.AudioAttributes
import android.media.RingtoneManager
import android.net.Uri
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat

class NotificationHelper(private val context: Context) {

    companion object {
        const val CHANNEL_PROGRESS_ID = "claude_tasks_ongoing"
        const val CHANNEL_ALERTS_ID = "claude_alerts_heads_up"
        const val NOTIFICATION_PROGRESS_ID = 1001
    }

    private val notificationManager =
        context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager

    init {
        createNotificationChannels()
    }

    private fun createNotificationChannels() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            // 1. Channel for Ongoing Persistent Task Bar in Notification Shade
            val progressChannel = NotificationChannel(
                CHANNEL_PROGRESS_ID,
                "Claude Task Progress (Ongoing)",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "Persistent task progress bar in notification shade"
                setShowBadge(false)
                lockscreenVisibility = NotificationCompat.VISIBILITY_PUBLIC
            }

            // 2. Channel for Urgent Asking, Permissions & Task Completion Alerts
            val defaultSoundUri = RingtoneManager.getDefaultUri(RingtoneManager.TYPE_NOTIFICATION)
            val audioAttributes = AudioAttributes.Builder()
                .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
                .setUsage(AudioAttributes.USAGE_NOTIFICATION_COMMUNICATION_INSTANT)
                .build()

            val alertsChannel = NotificationChannel(
                CHANNEL_ALERTS_ID,
                "Claude Alerts & Questions",
                NotificationManager.IMPORTANCE_HIGH
            ).apply {
                description = "Heads-up popups and alerts when Claude asks questions or finishes tasks"
                enableLights(true)
                enableVibration(true)
                vibrationPattern = longArrayOf(0, 200, 100, 200, 100, 400)
                setSound(defaultSoundUri, audioAttributes)
                lockscreenVisibility = NotificationCompat.VISIBILITY_PUBLIC
            }

            notificationManager.createNotificationChannel(progressChannel)
            notificationManager.createNotificationChannel(alertsChannel)
        }
    }

    /**
     * Updates the persistent ongoing notification in the Android notification shade (like a media/progress bar)
     */
    fun updateOngoingProgressNotification(
        projectName: String,
        toolStatus: String,
        isWorking: Boolean,
        isWaitingInput: Boolean,
        subagentCount: Int
    ) {
        try {
            // Main app intent
            val appIntent = Intent(context, MainActivity::class.java).apply {
                flags = Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP
            }
            val appPendingIntent = PendingIntent.getActivity(
                context,
                0,
                appIntent,
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
            )

            // Chrome Remote Desktop action intent
            val crdPendingIntent = getChromeRemotePendingIntent()

            val title = when {
                isWaitingInput -> "⚠️ Claude Needs Input • $projectName"
                isWorking -> "⚡ Claude Working • $projectName"
                else -> "⏸️ Claude Idling • $projectName"
            }

            val statusText = if (toolStatus.isNotEmpty()) toolStatus else (if (isWorking) "Executing tools..." else "Ready for next prompt")
            val subText = if (subagentCount > 0) "$subagentCount Sub-agent${if (subagentCount > 1) "s" else ""} active" else "Claude Code"

            val builder = NotificationCompat.Builder(context, CHANNEL_PROGRESS_ID)
                .setSmallIcon(R.drawable.ic_launcher_foreground)
                .setContentTitle(title)
                .setContentText(statusText)
                .setSubText(subText)
                .setContentIntent(appPendingIntent)
                .setOngoing(isWorking || isWaitingInput)
                .setOnlyAlertOnce(true)
                .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
                .setPriority(NotificationCompat.PRIORITY_LOW)
                .setCategory(NotificationCompat.CATEGORY_PROGRESS)
                .addAction(
                    android.R.drawable.ic_menu_view,
                    "🖥️ Chrome Remote",
                    crdPendingIntent
                )
                .addAction(
                    android.R.drawable.ic_menu_agenda,
                    "📱 Open Monitor",
                    appPendingIntent
                )

            notificationManager.notify(NOTIFICATION_PROGRESS_ID, builder.build())
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }

    /**
     * Builds a plain status notification for the MonitoringService foreground
     * service (waiting / reconnecting / stopped states). Uses the same
     * low-importance channel and NOTIFICATION_PROGRESS_ID as the progress
     * notification, so posting it updates the foreground service's
     * notification in place.
     */
    fun buildStatusNotification(title: String, text: String, ongoing: Boolean): Notification {
        val appIntent = Intent(context, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP
        }
        val appPendingIntent = PendingIntent.getActivity(
            context,
            0,
            appIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        return NotificationCompat.Builder(context, CHANNEL_PROGRESS_ID)
            .setSmallIcon(R.drawable.ic_launcher_foreground)
            .setContentTitle(title)
            .setContentText(text)
            .setContentIntent(appPendingIntent)
            .setOngoing(ongoing)
            .setOnlyAlertOnce(true)
            .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .build()
    }

    /**
     * Posts (or updates) the monitoring service status notification identified
     * by NOTIFICATION_PROGRESS_ID.
     */
    fun postStatusNotification(title: String, text: String, ongoing: Boolean) {
        try {
            notificationManager.notify(
                NOTIFICATION_PROGRESS_ID,
                buildStatusNotification(title, text, ongoing)
            )
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }

    /**
     * Shows high-priority heads-up notification for questions, permissions, and task completions
     */
    fun showPopupAlertNotification(title: String, message: String, type: String) {
        try {
            val appIntent = Intent(context, MainActivity::class.java).apply {
                flags = Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP
            }
            val appPendingIntent = PendingIntent.getActivity(
                context,
                (System.currentTimeMillis() % 10000).toInt(),
                appIntent,
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
            )

            val crdPendingIntent = getChromeRemotePendingIntent()

            val notificationId = (System.currentTimeMillis() % 10000).toInt() + 2000

            val builder = NotificationCompat.Builder(context, CHANNEL_ALERTS_ID)
                .setSmallIcon(R.drawable.ic_launcher_foreground)
                .setContentTitle(title)
                .setContentText(message)
                .setStyle(NotificationCompat.BigTextStyle().bigText(message))
                .setContentIntent(appPendingIntent)
                .setAutoCancel(true)
                .setPriority(NotificationCompat.PRIORITY_MAX)
                .setCategory(NotificationCompat.CATEGORY_CALL)
                .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
                .setVibrate(longArrayOf(0, 250, 100, 250, 100, 400))
                .addAction(
                    android.R.drawable.ic_menu_send,
                    "🖥️ Open Chrome Remote",
                    crdPendingIntent
                )

            notificationManager.notify(notificationId, builder.build())
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }

    /**
     * Creates a PendingIntent to launch Chrome Remote Desktop app or fallback to browser
     */
    private fun getChromeRemotePendingIntent(): PendingIntent {
        val launchIntent = context.packageManager.getLaunchIntentForPackage("com.google.chromeremotedesktop")
        val targetIntent = if (launchIntent != null) {
            launchIntent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        } else {
            Intent(Intent.ACTION_VIEW, Uri.parse("https://remotedesktop.google.com/access")).apply {
                addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            }
        }

        return PendingIntent.getActivity(
            context,
            102,
            targetIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
    }

    /**
     * Directly launches Chrome Remote Desktop app from within the app
     */
    fun launchChromeRemoteDesktop() {
        try {
            val launchIntent = context.packageManager.getLaunchIntentForPackage("com.google.chromeremotedesktop")
            if (launchIntent != null) {
                launchIntent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                context.startActivity(launchIntent)
            } else {
                val browserIntent = Intent(Intent.ACTION_VIEW, Uri.parse("https://remotedesktop.google.com/access")).apply {
                    addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                }
                context.startActivity(browserIntent)
            }
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }
}
