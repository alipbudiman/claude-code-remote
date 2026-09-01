package com.claudecode.remote

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.util.Log
import android.webkit.*
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import androidx.webkit.WebViewAssetLoader
import java.io.InputStream

class MainActivity : AppCompatActivity() {

    private lateinit var webView: WebView
    private lateinit var notificationHelper: NotificationHelper
    private lateinit var batteryHelper: BatteryOptimizationHelper

    companion object {
        // requestCode for the on-demand CAMERA permission flow (M11). 101 is
        // taken by the POST_NOTIFICATIONS request in onCreate.
        private const val CAMERA_PERMISSION_REQUEST_CODE = 102
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        notificationHelper = NotificationHelper(this)
        batteryHelper = BatteryOptimizationHelper(this)

        // Start the native monitoring foreground service: it owns the
        // WebSocket connection and notifications so tracking survives the
        // app being closed (M4a root-cause fix). No-op if already running;
        // it idles on a "waiting for configuration" notification until the
        // WebView bridge saves the server config (saveServerConfig below).
        MonitoringService.start(this)

        // Request POST_NOTIFICATIONS permission on Android 13+ (API 33+)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            if (ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS)
                != PackageManager.PERMISSION_GRANTED) {
                ActivityCompat.requestPermissions(
                    this,
                    arrayOf(Manifest.permission.POST_NOTIFICATIONS),
                    101
                )
            }
        }

        // M11: CAMERA is no longer requested blindly here. The QR scanner now
        // asks on demand through the AndroidBridge (requestCameraPermission),
        // so the user only sees the prompt with the scanner in front of them —
        // see WebAppInterface below.

        webView = WebView(this)
        setContentView(webView)

        setupWebView()

        // Show first-launch battery optimization dialog (only once per install)
        batteryHelper.showFirstLaunchDialogIfNeeded()
    }

    override fun onResume() {
        super.onResume()

        // Every time the app is resumed, check battery optimization status
        // and show a warning notification if still restricted
        batteryHelper.showBatteryWarningNotificationIfNeeded()

        // Push battery status to the WebView so the frontend banner can update
        pushBatteryStatusToWebView()
    }

    /**
     * Injects the current battery optimization status into the WebView
     * so the React frontend can render its in-app banner accordingly.
     */
    private fun pushBatteryStatusToWebView() {
        val isUnrestricted = batteryHelper.isIgnoringBatteryOptimizations()
        webView.post {
            webView.evaluateJavascript(
                "if(window.__onBatteryStatusUpdate){window.__onBatteryStatusUpdate($isUnrestricted);}",
                null
            )
        }
    }

    /**
     * Delivers the CAMERA permission result to the WebView (M11). Same
     * evaluateJavascript push pattern as pushBatteryStatusToWebView: the
     * React scanner registers window.__onCameraPermission before requesting,
     * so a page reload between request and result degrades gracefully.
     */
    private fun notifyCameraPermission(granted: Boolean) {
        webView.post {
            webView.evaluateJavascript(
                "window.__onCameraPermission && window.__onCameraPermission($granted)",
                null
            )
        }
    }

    override fun onRequestPermissionsResult(
        requestCode: Int,
        permissions: Array<out String>,
        grantResults: IntArray
    ) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        // M11: forward the on-demand CAMERA result to the WebView scanner.
        if (requestCode == CAMERA_PERMISSION_REQUEST_CODE) {
            val granted = grantResults.isNotEmpty() &&
                grantResults[0] == PackageManager.PERMISSION_GRANTED
            notifyCameraPermission(granted)
        }
    }

    private fun setupWebView() {
        val settings = webView.settings
        settings.javaScriptEnabled = true
        settings.domStorageEnabled = true
        settings.databaseEnabled = true
        settings.allowFileAccess = true
        settings.allowContentAccess = true
        settings.mixedContentMode = WebSettings.MIXED_CONTENT_ALWAYS_ALLOW
        settings.setSupportZoom(false)
        settings.loadWithOverviewMode = true
        settings.useWideViewPort = true

        // Register Native JavaScript Bridge
        webView.addJavascriptInterface(WebAppInterface(this), "AndroidBridge")

        val assetLoader = WebViewAssetLoader.Builder()
            .setDomain("appassets.androidplatform.net")
            .addPathHandler("/assets/", WebViewAssetLoader.AssetsPathHandler(this))
            .addPathHandler("/res/", WebViewAssetLoader.ResourcesPathHandler(this))
            .build()

        webView.webViewClient = object : WebViewClient() {
            override fun shouldInterceptRequest(
                view: WebView?,
                request: WebResourceRequest?
            ): WebResourceResponse? {
                val uri = request?.url ?: return null
                
                val standardResponse = assetLoader.shouldInterceptRequest(uri)
                if (standardResponse != null) {
                    return standardResponse
                }

                // Custom fallback for assets
                val path = uri.path?.trimStart('/') ?: return null
                try {
                    val cleanPath = when {
                        path.startsWith("assets/") -> path.substring(7)
                        else -> path
                    }
                    val stream: InputStream = assets.open(cleanPath)
                    val mimeType = getMimeType(cleanPath)
                    val headers = mapOf(
                        "Access-Control-Allow-Origin" to "*",
                        "Access-Control-Allow-Methods" to "GET, OPTIONS",
                        "Access-Control-Allow-Headers" to "*"
                    )
                    return WebResourceResponse(mimeType, "UTF-8", 200, "OK", headers, stream)
                } catch (e: Exception) {
                    Log.d("ClaudeRemote", "Asset not found: $path (${e.message})")
                }

                return super.shouldInterceptRequest(view, request)
            }

            override fun onReceivedError(
                view: WebView?,
                request: WebResourceRequest?,
                error: WebResourceError?
            ) {
                super.onReceivedError(view, request, error)
                Log.e("ClaudeRemote", "WebView error: ${error?.description} for ${request?.url}")
            }

            override fun onPageFinished(view: WebView?, url: String?) {
                super.onPageFinished(view, url)
                // Push battery status once the page has fully loaded
                pushBatteryStatusToWebView()
            }
        }

        webView.webChromeClient = object : WebChromeClient() {
            override fun onConsoleMessage(consoleMessage: ConsoleMessage?): Boolean {
                Log.d("ClaudeRemoteJS", "${consoleMessage?.message()} [line ${consoleMessage?.lineNumber()}]")
                return true
            }

            override fun onPermissionRequest(request: PermissionRequest?) {
                request?.grant(request.resources)
            }
        }

        // Load via secure custom virtual domain
        webView.loadUrl("https://appassets.androidplatform.net/assets/index.html")

        // Initialize persistent ongoing task notification
        notificationHelper.updateOngoingProgressNotification(
            "Claude Code",
            "Waiting for active session",
            false,
            false,
            0
        )
    }

    /**
     * Native JavaScript Interface Bridge
     */
    inner class WebAppInterface(private val context: Context) {
        @JavascriptInterface
        fun showNotification(title: String, message: String, type: String) {
            notificationHelper.showPopupAlertNotification(title, message, type)
        }

        @JavascriptInterface
        fun updateOngoingNotification(
            projectName: String,
            toolStatus: String,
            isWorking: Boolean,
            isWaitingInput: Boolean,
            subagentCount: Int
        ) {
            notificationHelper.updateOngoingProgressNotification(
                projectName,
                toolStatus,
                isWorking,
                isWaitingInput,
                subagentCount
            )
        }

        @JavascriptInterface
        fun openChromeRemoteDesktop() {
            notificationHelper.launchChromeRemoteDesktop()
        }

        @JavascriptInterface
        fun isNativeAndroid(): Boolean = true

        /**
         * Persists the connection settings for the native MonitoringService
         * (its config source), then pokes it so it reconnects immediately.
         * Called by the WebView whenever the user saves connection settings.
         */
        @JavascriptInterface
        fun saveServerConfig(url: String, token: String) {
            getSharedPreferences(MonitoringService.PREFS_NAME, Context.MODE_PRIVATE)
                .edit()
                .putString(MonitoringService.PREF_SERVER_URL, url.trim())
                .putString(MonitoringService.PREF_TOKEN, token.trim())
                .apply()
            MonitoringService.start(this@MainActivity)
        }

        @JavascriptInterface
        fun isBatteryUnrestricted(): Boolean = batteryHelper.isIgnoringBatteryOptimizations()

        @JavascriptInterface
        fun openBatterySettings() {
            batteryHelper.openBatteryOptimizationSettings()
        }

        /**
         * M11: whether the app-level CAMERA permission (a prerequisite for
         * the WebView to open the camera at all) is currently granted.
         */
        @JavascriptInterface
        fun hasCameraPermission(): Boolean =
            ContextCompat.checkSelfPermission(
                context, Manifest.permission.CAMERA
            ) == PackageManager.PERMISSION_GRANTED

        /**
         * M11: on-demand CAMERA permission flow for the QR scanner. Runs on
         * the main thread: if the grant somehow already landed, the WebView is
         * told immediately; otherwise a brief rationale dialog explains why
         * the camera is needed, and Allow triggers the real system prompt
         * (requestCode 102, answered in onRequestPermissionsResult). "Not
         * now" dismisses and leaves the WebView's manual fallback.
         */
        @JavascriptInterface
        fun requestCameraPermission() {
            webView.post {
                if (hasCameraPermission()) {
                    notifyCameraPermission(true)
                    return@post
                }
                AlertDialog.Builder(this@MainActivity)
                    .setTitle("Camera permission")
                    .setMessage("Camera access is needed to scan the server QR code.")
                    .setPositiveButton("Allow") { _, _ ->
                        ActivityCompat.requestPermissions(
                            this@MainActivity,
                            arrayOf(Manifest.permission.CAMERA),
                            CAMERA_PERMISSION_REQUEST_CODE
                        )
                    }
                    .setNegativeButton("Not now") { dialog, _ -> dialog.dismiss() }
                    .show()
            }
        }

        /**
         * M11: escape hatch for the permanently-denied ("don't ask again")
         * state, where no in-app prompt can appear again — opens this app's
         * page in system settings so the user can flip the toggle there.
         */
        @JavascriptInterface
        fun openAppSettings() {
            webView.post {
                try {
                    val intent = Intent(
                        android.provider.Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                        Uri.fromParts("package", packageName, null)
                    )
                    startActivity(intent)
                } catch (e: Exception) {
                    Log.e("ClaudeRemote", "openAppSettings failed: ${e.message}")
                }
            }
        }
    }

    private fun getMimeType(path: String): String {
        return when {
            path.endsWith(".html") -> "text/html"
            path.endsWith(".js") || path.endsWith(".mjs") -> "application/javascript"
            path.endsWith(".css") -> "text/css"
            path.endsWith(".svg") -> "image/svg+xml"
            path.endsWith(".png") -> "image/png"
            path.endsWith(".json") -> "application/json"
            path.endsWith(".woff2") -> "font/woff2"
            path.endsWith(".woff") -> "font/woff"
            path.endsWith(".ttf") -> "font/ttf"
            else -> "application/octet-stream"
        }
    }

    override fun onBackPressed() {
        if (webView.canGoBack()) {
            webView.goBack()
        } else {
            super.onBackPressed()
        }
    }
}
