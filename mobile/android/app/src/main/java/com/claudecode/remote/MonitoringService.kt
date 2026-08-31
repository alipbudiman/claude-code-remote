package com.claudecode.remote

import android.app.Service
import android.app.ServiceInfo
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.SystemClock
import android.util.Log
import androidx.annotation.RequiresApi
import androidx.core.content.ContextCompat
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

/**
 * Native monitoring foreground service (M4a).
 *
 * Owns the WebSocket connection to the desktop server and the progress
 * notification, so tracking (and honest notification state) survives the app
 * being closed or swiped away. Previously ALL phone-side tracking lived in
 * the WebView's JavaScript: closing the app killed tracking and froze a
 * pinned, lying "Working" notification.
 *
 * The WebView keeps its own separate JS connection while the app is
 * foregrounded for live UI (the server handles multiple subscribers); this
 * service is the durable, notification-owning connection. To avoid doubled
 * heads-up alerts, only this service fires popup notifications for live
 * server "notification" frames.
 *
 * M4b adds two reliability pieces: (1) the server broadcasts a lightweight
 * "stats" frame every 20s, which keeps [lastMessageAt] fresh during quiet
 * stretches so the staleness watchdog stops force-reconnecting healthy
 * links; (2) a persisted notification-id watermark replays alerts that were
 * raised server-side while this service was disconnected (initial_state
 * snapshot diff — see [replayMissedNotifications]).
 *
 * Foreground service type is dataSync:
 *  - Android 14+: requires the FOREGROUND_SERVICE_DATA_SYNC permission.
 *  - Android 15: cumulative dataSync time is capped at ~6h per 24h; the
 *    system calls onTimeout(int) (see below) when the cap is reached.
 */
class MonitoringService : Service() {

    companion object {
        private const val TAG = "MonitoringService"
        private const val APP_TITLE = "Claude Code"

        /** SharedPreferences written by the JS bridge (MainActivity.saveServerConfig). */
        const val PREFS_NAME = "claude_remote_config"
        const val PREF_SERVER_URL = "server_url"
        const val PREF_TOKEN = "token"

        /**
         * M4b missed-alert watermark: id of the newest notification this
         * service has ever alerted. Notification ids are "notif-<UnixNano>";
         * only the numeric suffix is stored/compared. Kept in the same prefs
         * so alerts raised while disconnected are replayed from the next
         * initial_state snapshot instead of being silently lost (RC-A5).
         */
        const val PREF_LAST_NOTIF_ID = "last_seen_notification_id"

        /** At most this many missed notifications are re-alerted on reconnect. */
        private const val MAX_REPLAY_ALERTS = 10

        /** Sentinel: no watermark loaded/stored yet (first run after install). */
        private const val WATERMARK_UNLOADED = Long.MIN_VALUE

        private const val NORMAL_CLOSE = 1000

        /** Exponential reconnect backoff: 1s doubling to 60s, +/-25% jitter. */
        private const val RECONNECT_BASE_MS = 1_000L
        private const val RECONNECT_MAX_MS = 60_000L

        /**
         * Staleness watchdog: check every 30s; force a reconnect if connected
         * but no data frame has arrived for 45s. Since M4b the server
         * broadcasts a stats heartbeat every 20s, so a healthy-but-idle link
         * keeps the clock fresh and this only fires on genuinely dead links
         * (WebSocket pong/control frames still never reach OkHttp's listener,
         * which is why the app-level heartbeat exists).
         */
        private const val WATCHDOG_CHECK_MS = 30_000L
        private const val STALE_AFTER_MS = 45_000L

        /** OkHttp transport-level keepalive: client pings, fails the socket if no pong. */
        private const val OKHTTP_PING_INTERVAL_S = 20L

        fun start(context: Context) {
            ContextCompat.startForegroundService(context, Intent(context, MonitoringService::class.java))
        }

        fun stop(context: Context) {
            context.stopService(Intent(context, MonitoringService::class.java))
        }
    }

    private lateinit var notificationHelper: NotificationHelper
    private lateinit var client: OkHttpClient

    /** True between onCreate and onDestroy; gates every callback and timer. */
    private val running = AtomicBoolean(false)

    /** ElapsedRealtime of the last received data frame (any thread). */
    private val lastMessageAt = AtomicLong(0L)

    private val mainHandler = Handler(Looper.getMainLooper())

    // --- State below is only touched on the main thread. ---
    private var webSocket: WebSocket? = null
    private var connected = false
    private var configReady = false
    private var activeUrl: String? = null
    private var activeToken: String? = null
    private var reconnectPending = false
    private var reconnectDelayMs = RECONNECT_BASE_MS
    private var reconnectRunnable: Runnable? = null
    private var watchdogRunning = false
    private var finalNoticePosted = false

    /**
     * M4b replay watermark (main thread only, like the fields above): nano
     * suffix of the newest alerted notification id, or [WATERMARK_UNLOADED]
     * until prefs have been read. An unparsable/absent stored value keeps the
     * first-run behavior (adopt newest silently).
     */
    private var lastSeenNotifNanos: Long = WATERMARK_UNLOADED

    // ------------------------------------------------------------------------- lifecycle

    override fun onCreate() {
        super.onCreate()
        notificationHelper = NotificationHelper(this)
        client = OkHttpClient.Builder()
            .pingInterval(OKHTTP_PING_INTERVAL_S, TimeUnit.SECONDS)
            .connectTimeout(10, TimeUnit.SECONDS)
            // Long-lived WebSocket: reads must never time out on their own;
            // liveness is driven by the ping interval (and the watchdog below).
            .readTimeout(0, TimeUnit.MILLISECONDS)
            .build()
        running.set(true)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (!startInForeground()) {
            return START_NOT_STICKY
        }
        startWatchdog()

        val prefs = getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        val url = prefs.getString(PREF_SERVER_URL, null)?.trim().orEmpty()
        val token = prefs.getString(PREF_TOKEN, null)?.trim().orEmpty()

        // M4b: load the missed-alert watermark once, before any frame can
        // arrive (frames only flow after the connection starts below).
        if (lastSeenNotifNanos == WATERMARK_UNLOADED) {
            parseNotifNanos(prefs.getString(PREF_LAST_NOTIF_ID, null))?.let { lastSeenNotifNanos = it }
        }

        if (url.isEmpty() || token.isEmpty()) {
            // Fresh install before the user ever saved connection settings.
            notificationHelper.postStatusNotification(
                APP_TITLE, "Waiting for configuration — open the app", ongoing = true
            )
            stopReconnectLoop()
            return START_STICKY
        }

        if (url == activeUrl && token == activeToken && (webSocket != null || reconnectPending)) {
            // Same config already connected or connecting — nothing to do.
            return START_STICKY
        }

        activeUrl = url
        activeToken = token
        configReady = true
        restartConnection()
        return START_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onTaskRemoved(rootIntent: Intent?) {
        // Deliberately does NOTHING except call super. The whole point of
        // this service is that tracking survives the user swiping the app
        // away from Recents. (The default already keeps running; this
        // override documents that it is intentional, not an oversight.)
        super.onTaskRemoved(rootIntent)
    }

    override fun onDestroy() {
        running.set(false)
        mainHandler.removeCallbacks(watchdogRunnable)
        cancelPendingReconnect()
        webSocket?.close(NORMAL_CLOSE, null)
        webSocket = null
        client.dispatcher.executorService.shutdown()
        client.connectionPool.evictAll()

        // Drop foreground FIRST, then post the stopped notice: stopForeground
        // (STOP_FOREGROUND_REMOVE) cancels the notification with this id, so a
        // notice posted before it would be cancelled too.
        stopForeground(STOP_FOREGROUND_REMOVE)
        if (!finalNoticePosted) {
            notificationHelper.postStatusNotification(
                APP_TITLE, "Tracking stopped — open app to resume", ongoing = false
            )
        }
        super.onDestroy()
    }

    /**
     * Android 15 (API 35) enforces a ~6h cumulative daily cap on dataSync
     * foreground services and calls Service.onTimeout(int) when it is
     * reached. compileSdk is 34, so this method cannot use the `override`
     * keyword (the SDK 34 Service class has no onTimeout); declaring the
     * same JVM signature — onTimeout(I)V — makes the framework dispatch
     * here on API 35+ devices, while older versions simply never call it.
     * Gracefully stopping matters: otherwise Android 15 crashes the app with
     * ForegroundServiceDidNotStopInTimeException a few seconds after the cap.
     */
    @RequiresApi(35)
    fun onTimeout(fgsType: Int) {
        Log.w(TAG, "dataSync foreground time cap reached (Android 15); pausing tracking")
        running.set(false)
        mainHandler.removeCallbacks(watchdogRunnable)
        cancelPendingReconnect()
        webSocket?.close(NORMAL_CLOSE, null)
        webSocket = null
        stopForeground(STOP_FOREGROUND_REMOVE)
        notificationHelper.postStatusNotification(
            APP_TITLE,
            "Tracking paused after 6 hours (Android 15 limit) — tap to resume",
            ongoing = false
        )
        finalNoticePosted = true // keep onDestroy from overwriting this notice
        stopSelf()
    }

    // ------------------------------------------------------------------------- foreground + connection management

    private fun startInForeground(): Boolean {
        val notification = notificationHelper.buildStatusNotification(
            APP_TITLE, "Waiting for session data", ongoing = true
        )
        return try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                startForeground(
                    NotificationHelper.NOTIFICATION_PROGRESS_ID,
                    notification,
                    ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC
                )
            } else {
                startForeground(NotificationHelper.NOTIFICATION_PROGRESS_ID, notification)
            }
            true
        } catch (t: Throwable) {
            // E.g. Android 15 boot path: ForegroundServiceStartNotAllowedException.
            Log.e(TAG, "startForeground failed; stopping service", t)
            stopSelf()
            false
        }
    }

    private fun startWatchdog() {
        if (watchdogRunning) return
        watchdogRunning = true
        mainHandler.postDelayed(watchdogRunnable, WATCHDOG_CHECK_MS)
    }

    private val watchdogRunnable: Runnable = object : Runnable {
        override fun run() {
            if (!running.get()) {
                watchdogRunning = false
                return
            }
            val now = SystemClock.elapsedRealtime()
            val last = lastMessageAt.get()
            if (connected && now - last > STALE_AFTER_MS) {
                connected = false
                Log.w(TAG, "No data for ${now - last}ms while connected — forcing reconnect")
                notificationHelper.postStatusNotification(
                    APP_TITLE,
                    "Last update ${clockOfElapsed(last)} — reconnecting…",
                    ongoing = false
                )
                // close() triggers onClosed/onFailure, which drives
                // scheduleReconnect(); if the close handshake wedges, OkHttp's
                // own ping watchdog fails the socket instead.
                webSocket?.close(NORMAL_CLOSE, "stale link")
            }
            mainHandler.postDelayed(this, WATCHDOG_CHECK_MS)
        }
    }

    /** Tears down the socket and dials fresh (config new/changed, or first start). */
    private fun restartConnection() {
        webSocket?.close(NORMAL_CLOSE, "config changed")
        webSocket = null
        connected = false
        cancelPendingReconnect()
        reconnectDelayMs = RECONNECT_BASE_MS
        connect()
    }

    private fun stopReconnectLoop() {
        configReady = false
        cancelPendingReconnect()
        webSocket?.close(NORMAL_CLOSE, null)
        webSocket = null
        connected = false
    }

    private fun connect() {
        if (!running.get() || !configReady) return
        val url = activeUrl ?: return
        val token = activeToken ?: return
        val wsUrl = deriveWsUrl(url)
        if (wsUrl == null) {
            Log.e(TAG, "Unparseable server_url: $url")
            notificationHelper.postStatusNotification(
                APP_TITLE, "Invalid server address — open the app to fix", ongoing = true
            )
            stopReconnectLoop()
            return
        }
        try {
            val request = Request.Builder()
                .url(wsUrl)
                // OkHttp CAN set handshake headers: send the token both ways.
                // The subprotocol header is what the server echoes back and
                // what its auth gate accepts.
                .header("Authorization", "Bearer $token")
                .header("Sec-WebSocket-Protocol", "claude-remote.$token")
                .build()
            webSocket = client.newWebSocket(request, MonitorSocketListener())
            Log.i(TAG, "Connecting to $wsUrl")
        } catch (t: Throwable) {
            Log.e(TAG, "WebSocket request build failed", t)
            scheduleReconnect()
        }
    }

    private fun scheduleReconnect() {
        if (!running.get() || !configReady || reconnectPending) return
        reconnectPending = true
        // +/-25% jitter so a fleet of clients cannot sync their retries.
        val jitter = 1.0 + (Math.random() * 0.5 - 0.25) // 0.75..1.25
        val delay = (reconnectDelayMs * jitter)
            .toLong()
            .coerceIn(RECONNECT_BASE_MS, RECONNECT_MAX_MS)
        reconnectDelayMs = (reconnectDelayMs * 2).coerceAtMost(RECONNECT_MAX_MS)
        val host = activeUrl?.let { hostOf(it) } ?: "server"
        notificationHelper.postStatusNotification(
            APP_TITLE, "Reconnecting to $host… (last update ${lastUpdateClock()})", ongoing = false
        )
        val runnable = Runnable {
            reconnectPending = false
            reconnectRunnable = null
            connect()
        }
        reconnectRunnable = runnable
        mainHandler.postDelayed(runnable, delay)
    }

    private fun cancelPendingReconnect() {
        reconnectRunnable?.let { mainHandler.removeCallbacks(it) }
        reconnectRunnable = null
        reconnectPending = false
    }

    /** Only true while [ws] is the socket this service currently tracks. */
    private fun isCurrent(ws: WebSocket): Boolean = running.get() && ws === webSocket

    private inner class MonitorSocketListener : WebSocketListener() {
        override fun onOpen(ws: WebSocket, response: Response) {
            mainHandler.post { if (isCurrent(ws)) handleConnected() }
        }

        override fun onMessage(ws: WebSocket, text: String) {
            // OkHttp reader thread. Control frames (pings/pongs) never appear
            // here, so this really is the "last data" clock — including the
            // server's 20s stats heartbeat, which keeps a healthy-but-idle
            // link from looking stale. Actual parsing/dispatch happens on the
            // main thread like the other callbacks, gated to the current
            // socket so a superseded connection cannot mutate state.
            lastMessageAt.set(SystemClock.elapsedRealtime())
            mainHandler.post { if (isCurrent(ws)) handleFrame(text) }
        }

        override fun onClosed(ws: WebSocket, code: Int, reason: String) {
            mainHandler.post { if (isCurrent(ws)) handleDisconnected("closed: $reason") }
        }

        override fun onFailure(ws: WebSocket, t: Throwable, response: Response?) {
            Log.w(TAG, "WebSocket failure: ${t.message}")
            mainHandler.post { if (isCurrent(ws)) handleDisconnected("failure: ${t.message}") }
        }
    }

    // ------------------------------------------------------------------------- frame handling (org.json only)

    private fun handleConnected() {
        connected = true
        reconnectDelayMs = RECONNECT_BASE_MS
        lastMessageAt.set(SystemClock.elapsedRealtime())
        Log.i(TAG, "Connected to ${activeUrl}")
    }

    private fun handleDisconnected(reason: String) {
        webSocket = null
        connected = false
        Log.i(TAG, "Disconnected ($reason); scheduling reconnect")
        scheduleReconnect()
    }

    private fun handleFrame(text: String) {
        val root = try {
            JSONObject(text)
        } catch (e: JSONException) {
            Log.w(TAG, "Non-JSON frame ignored: ${e.message}")
            return
        }
        when (root.optString("type")) {
            "initial_state" -> handleInitialState(root.optJSONObject("data"))
            "session_update" -> applySession(root.optJSONObject("data"))
            "notification" -> handleLiveNotification(root.optJSONObject("data"))
            "stats" -> {
                // Liveness only: onMessage already refreshed lastMessageAt.
                // No notification update — never churn the shade every 20s.
            }
            else -> {
                // "subagent_update", future types: the notification only
                // needs session state, which session_update carries.
            }
        }
    }

    private fun handleInitialState(data: JSONObject?) {
        applySession(data?.optJSONObject("active_session"))
        // M4b: the snapshot carries the full (newest-first) notification
        // list — diff it against the watermark to replay missed alerts.
        replayMissedNotifications(data?.optJSONArray("notifications"))
    }

    /** Live frame: fire the heads-up (M4a behavior) and advance the watermark. */
    private fun handleLiveNotification(data: JSONObject?) {
        if (data == null) return
        notificationHelper.showPopupAlertNotification(
            data.optString("title"),
            data.optString("body"),
            data.optString("type", "info")
        )
        parseNotifNanos(data.optString("id"))?.let { advanceWatermark(it) }
    }

    // ------------------------------------------------------------------------- missed-alert replay (M4b, root cause RC-A5)

    /**
     * Re-alerts notifications raised server-side while this service was
     * disconnected, diffing the initial_state snapshot (newest-first) against
     * the persisted watermark.
     *
     * First run (no stored watermark): adopt the newest id silently so a
     * fresh install never buzzes through stored history. Otherwise every
     * entry strictly newer than the watermark is replayed oldest→newest,
     * bounded to the [MAX_REPLAY_ALERTS] most recent — older missed ones are
     * stale history and only advance the watermark, silently.
     */
    private fun replayMissedNotifications(notifs: JSONArray?) {
        if (notifs == null || notifs.length() == 0) return

        if (lastSeenNotifNanos == WATERMARK_UNLOADED) {
            // First run: set the watermark to the newest seen WITHOUT alerting.
            parseNotifNanos(notifs.optJSONObject(0)?.optString("id"))?.let { advanceWatermark(it) }
            return
        }

        // Collect strictly-newer entries; index 0 is the newest. Unparsable
        // ids compare as older and are skipped entirely.
        val missed = ArrayList<JSONObject>(8)
        var newestNanos = lastSeenNotifNanos
        for (i in 0 until notifs.length()) {
            val notif = notifs.optJSONObject(i) ?: continue
            val nanos = parseNotifNanos(notif.optString("id")) ?: continue
            if (nanos <= lastSeenNotifNanos) continue
            if (nanos > newestNanos) newestNanos = nanos
            missed.add(notif)
        }
        if (missed.isEmpty()) return

        val alertCount = missed.size.coerceAtMost(MAX_REPLAY_ALERTS)
        for (i in alertCount - 1 downTo 0) { // oldest → newest of the most recent
            notificationHelper.showPopupAlertNotification(
                missed[i].optString("title"),
                missed[i].optString("body"),
                missed[i].optString("type", "info")
            )
        }
        advanceWatermark(newestNanos)
    }

    /** Moves the watermark forward (never backward) and persists it. */
    private fun advanceWatermark(nanos: Long) {
        if (nanos <= lastSeenNotifNanos) return
        lastSeenNotifNanos = nanos
        getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
            .edit()
            .putString(PREF_LAST_NOTIF_ID, "notif-$nanos")
            .apply()
    }

    /**
     * "notif-<UnixNano>" -> the nano suffix, or null when absent/unparsable
     * (unparsable ids compare as older than any real watermark).
     */
    private fun parseNotifNanos(id: String?): Long? {
        if (id == null) return null
        val idx = id.lastIndexOf('-')
        if (idx < 0) return null
        return id.substring(idx + 1).toLongOrNull()
    }

    /** Mirrors App.tsx status semantics: working/waiting + subagent count. */
    private fun applySession(session: JSONObject?) {
        if (session == null) {
            notificationHelper.postStatusNotification(
                APP_TITLE, "Waiting for session data", ongoing = true
            )
            return
        }
        val status = session.optString("status", "idle")
        val isWorking = status == "active" || status == "subagent_running" || status == "waiting_permission"
        val isWaitingInput = status == "waiting_permission" || !session.isNull("pending_question")
        val subagentCount = session.optJSONObject("active_subagents")?.length() ?: 0
        notificationHelper.updateOngoingProgressNotification(
            session.optString("project_name", APP_TITLE),
            session.optString("current_tool_status", ""),
            isWorking,
            isWaitingInput,
            subagentCount
        )
    }

    // ------------------------------------------------------------------------- helpers

    /** http(s)://host[:port] -> ws(s)://host[:port]/ws (idempotent on /ws). */
    private fun deriveWsUrl(serverUrl: String): String? {
        val s = serverUrl.trim().trimEnd('/')
        val base = when {
            s.startsWith("http://", ignoreCase = true) -> "ws://" + s.substring(7)
            s.startsWith("https://", ignoreCase = true) -> "wss://" + s.substring(8)
            s.startsWith("ws://", ignoreCase = true) || s.startsWith("wss://", ignoreCase = true) -> s
            else -> return null
        }
        return if (base.endsWith("/ws")) base else base + "/ws"
    }

    private fun hostOf(serverUrl: String): String {
        return serverUrl.trim()
            .removePrefix("http://")
            .removePrefix("https://")
            .substringBefore('/')
    }

    /** Wall-clock "HH:mm" for an elapsedRealtime stamp. */
    private fun clockOfElapsed(elapsed: Long): String {
        val ageMs = (SystemClock.elapsedRealtime() - elapsed).coerceAtLeast(0)
        return SimpleDateFormat("HH:mm", Locale.getDefault())
            .format(Date(System.currentTimeMillis() - ageMs))
    }

    private fun lastUpdateClock(): String {
        val last = lastMessageAt.get()
        return if (last == 0L) "—" else clockOfElapsed(last)
    }
}
