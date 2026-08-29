package com.claudecode.remote

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.util.Log
import android.webkit.*
import androidx.appcompat.app.AppCompatActivity
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import androidx.webkit.WebViewAssetLoader
import java.io.InputStream

class MainActivity : AppCompatActivity() {

    private lateinit var webView: WebView
    private lateinit var notificationHelper: NotificationHelper

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        notificationHelper = NotificationHelper(this)

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

        webView = WebView(this)
        setContentView(webView)

        setupWebView()
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
