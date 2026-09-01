# Claude Code Remote Session Monitor (Go Server EXE & Android APK)

Aplikasi remote monitoring sesi dan sub-agent untuk [Claude Code](https://github.com/anthropics/claude-code) berbasis **Go Server Executable (Desktop)** dan **Android Application (APK)**. Sistem ini bekerja 100% pada jaringan lokal (**Local LAN / Wi-Fi**) melalui port `0.0.0.0:9280`, sehingga **tidak memerlukan kuota internet** untuk komunikasi antara PC dan HP Android. Butuh akses dari luar jaringan lokal (selular/sekolah/kantor)? Lihat bagian [**Akses Online via Railway (Relay)**](#akses-online-via-railway-relay) di bawah.

Dibuat berdasarkan analisis arsitektur event-driven & hook system dari [pixel-agents](https://github.com/pixel-agents-hq/pixel-agents).

---

## Fitur Utama

1. **Desktop Server (Go Executable `claude-remote-server.exe`)**:
   - Ditulis dalam bahasa **Go** (kinerja tinggi, single binary `.exe` tanpa dependensi runtime).
   - Membuka koneksi `0.0.0.0:9280` sehingga bisa diakses dari perangkat manapun di jaringan Wi-Fi/LAN lokal yang sama.
   - Mendeteksi alamat IP lokal (misal: `http://192.168.100.48:9280`) dan mencetak **QR Code ASCII** di konsol terminal untuk koneksi instan dari HP Android.
   - **Auto-Hook Installer**: Mengintegrasikan hook secara otomatis ke `~/.claude/settings.json` (`SessionStart`, `SessionEnd`, `Stop`, `PermissionRequest`, `PreToolUse`, `PostToolUse`, `SubagentStart`, `SubagentStop`, `TeammateIdle`, `TaskCompleted`).
   - **JSONL Redundancy Watcher**: Memantau file transcript di `~/.claude/projects/` dan `subagents/` secara real-time.
   - **Embedded Web Dashboard**: Jika diakses via browser laptop/PC, langsung membuka dashboard status interaktif.

2. **Android Application (APK)**:
   - **Read-Only Dashboard**: Dirancang khusus untuk monitor di HP Android tanpa perlu input manual.
   - **Status Icon Dinamis**:
     - 🎨 **Sedang Bekerja / Active**: Menggunakan icon berwarna [`icon/claudecode-color.svg`](icon/claudecode-color.svg) dengan animasi efek glow/pulse.
     - 💤 **Sedang Diam / Idling**: Menggunakan icon monokrom [`icon/claudecode.svg`](icon/claudecode.svg).
   - **Sub-Agent Activity Inspector**: Memantau seluruh sub-agent yang sedang berjalan, peran sub-agent (misal: `Bug-Fixer`, `Task-Agent`), aktivitas spesifik yang sedang dilakukan (misal: `Reading config.go`, `Running: go test`, `Editing app.vue`), durasi waktu, serta riwayat sub-task yang telah selesai.
   - **Notifikasi Android Lokal**: Memunculkan notifikasi pop-up, getar (*vibration*), dan nada notifikasi lembut saat Claude Code mulai bekerja, selesai giliran (*turn finished*), meminta izin (*permission request*), atau saat sub-agent selesai.

3. **Otomatisasi CI/CD GitHub Actions**:
   - `.github/workflows/build-apk.yml`: Otomatis meng-compile APK dari repositori GitHub dan menerbitkan rilis APK siap download di HP Android.
   - `.github/workflows/build-server.yml`: Otomatis meng-compile binary Go untuk Windows (`.exe`), Linux, dan macOS.

---

## Akses Online via Railway (Relay)

Mode default sistem ini adalah LAN lokal. Untuk memantau dari jaringan mana pun (selular, Wi-Fi sekolah/kantor, dsb.), deploy **relay** ke [Railway](https://railway.com): server desktop melakukan *dial-out* ke relay tersebut (tanpa port-forward / IP publik), lalu HP cukup terhubung ke URL relay yang sama dengan token yang sama.

[![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/new/template/YOUR-TEMPLATE-ID?ref=github-alipbudiman)

> **Catatan:** tombol di atas membutuhkan template Railway yang di-*publish* sekali oleh pemilik repo. Cara publish: buka project di Railway → menu **Settings → Templates → Create Template** (atau langsung ke https://railway.com/templates/create), publish dari service `claude-remote-relay`, lalu ganti `YOUR-TEMPLATE-ID` pada link tombol dengan kode template yang didapat. Sampai template ter-*publish*, gunakan langkah manual berikut (sudah berfungsi hari ini).

### 1. Deploy Relay ke Railway (Manual)

1. Login ke https://railway.com → **New Project → Deploy from GitHub repo** → pilih repo `claude-code-remote` (fork dulu jika bukan milik Anda).
2. Railway memakai `Dockerfile` di root (membangun `cmd/relay` — server relay, bukan desktop monitor).
3. Service → **Settings → Networking → Generate Domain** → catat URL (mis. `https://xxx.up.railway.app`).
4. Cek: `https://<domain>/health` harus mengembalikan `{"service":"claude-remote-relay","status":"ok"}`.

### 2. Setup di PC (Desktop Server)

Jalankan server desktop dengan `claude-remote-server.exe -port 9280 --relay wss://<domain-relay-anda>` (atau set env `RELAY_URL=wss://...`). Token diambil otomatis dari `~/.claude/claude-remote-token`. Server melakukan *dial-out* ke relay — **tidak perlu port-forward atau IP publik**.

### 3. Setup di HP (APK)

1. Install APK → buka pengaturan (ikon gear).
2. Isi **Railway URL** `https://<domain-relay-anda>` + **Token** (isi file `claude-remote-token` di PC).
3. Status & notifikasi mengalir dari jaringan mana pun (selular/sekolah/kantor).

Tips: **Scan QR** — tombol **Scan QR** di pengaturan aplikasi → arahkan ke QR di terminal server → URL + token terisi otomatis dan langsung tersambung.

### 4. Catatan Keamanan

Token = kunci room relay. Siapa pun yang memegang token bisa membaca stream status — **jangan dibagikan**. Rotasi token: hapus `~/.claude/claude-remote-token` lalu restart server (token baru otomatis dibuat), kemudian perbarui token di HP.

### 5. Troubleshooting

- **APK terhubung ke relay tapi tidak ada apa-apa** → PC belum masuk room: pastikan server dijalankan dengan `--relay wss://<domain>` dan log menampilkan `🌐 Relay client active`; setelah M7 versi baru, app juga menampilkan banner "waiting for your PC".
- **Token salah** → handshake ditolak 401 secara senyap; salin utuh 64 karakter dari `%USERPROFILE%\.claude\claude-remote-token` (tanpa spasi/baris baru).
- **QR tidak terhubung di HP** → cek IP di QR vs daftar alamat (jaringan virtual WSL/VPN bisa terpilih); pastikan HP dan PC satu Wi-Fi; buka port di firewall Windows:
  `netsh advfirewall firewall add rule name="Claude Remote Server" dir=in action=allow protocol=TCP localport=9280`.

---

## Struktur Proyek

```
claude-status-apk/
├── .github/
│   └── workflows/
│       ├── build-apk.yml          # GitHub Actions workflow untuk build Android APK
│       └── build-server.yml       # GitHub Actions workflow untuk build Go binaries
├── bin/
│   └── claude-remote-server.exe   # Compiled executable Go desktop server
├── cmd/
│   └── server/
│       └── main.go                # Entry point Go application
├── internal/
│   ├── api/                       # HTTP Server, WebSocket Hub, REST endpoints
│   ├── hooks/                     # Claude hook installer & tool status parser
│   ├── models/                    # Data models (Session, Subagent, Notification)
│   ├── network/                   # Local IP detection & QR Code generator
│   ├── state/                     # Thread-safe in-memory state store
│   └── watcher/                   # JSONL transcript file watcher
├── mobile/                        # Android Mobile Application codebase
│   ├── android/                   # Native Android Gradle configuration & WebView runner
│   ├── src/                       # React/TypeScript mobile interface
│   │   ├── assets/                # claudecode-color.svg & claudecode.svg
│   │   ├── components/            # Header, StatusHero, SubagentInspector, ActivityLogs
│   │   └── services/              # Notification & WebSocket services
│   └── package.json
├── web/                           # Embedded Web UI Dashboard
│   ├── index.html
│   └── web.go
├── scripts/
│   ├── build-go.bat               # Script kompilasi Go .exe untuk Windows
│   ├── run-server.bat             # Script menjalankan server di Windows
│   └── test-mock-hook.ps1         # Script pengujian hook event tiruan
├── icon/
│   ├── claudecode-color.svg       # Icon status working
│   └── claudecode.svg             # Icon status idling
├── go.mod
└── README.md
```

---

## Panduan Penggunaan

### 1. Menjalankan Server di Komputer (Windows)

#### Menggunakan Binary yang Sudah Dikompilasi:
Cukup jalankan script batch atau file executable:
```cmd
scripts\run-server.bat
```
Atau langsung jalankan binary:
```cmd
bin\claude-remote-server.exe
```

#### Mengompilasi Ulang Binary Go:
Jika Anda ingin mengompilasi ulang kode sumber Go:
```cmd
scripts\build-go.bat
```
Atau via terminal Go:
```bash
go build -ldflags="-s -w" -o bin/claude-remote-server.exe ./cmd/server
```

Saat server berjalan, terminal akan menampilkan alamat IP lokal dan QR Code:
```
========================================================================
   ____ _                 _         ____          _      
  / ___| | __ _ _   _  __| | ___   / ___|___   __| | ___ 
 | |   | |/ _` | | | |/ _` |/ _ \ | |   / _ \ / _` |/ _ \
 | |___| | (_| | |_| | (_| |  __/ | |__| (_) | (_| |  __/
  \____|_|\__,_|\__,_|\__,_|\___|  \____\___/ \__,_|\___|
  
          REMOTE SESSION & SUB-AGENT MONITOR (GO EXE)
========================================================================
✅ Claude Code Hooks successfully linked to ~/.claude/settings.json
✅ JSONL Transcript file watcher active on ~/.claude/projects/

------------------------------------------------------------------------
🚀 Server running on 0.0.0.0:9280 (Local LAN / Offline Wi-Fi)
   Available Network Addresses:
   • http://192.168.100.48:9280
   • http://localhost:9280 (Local Desktop Browser)
------------------------------------------------------------------------
```

#### Auto-Start saat Login Windows (`-install`)

Server adalah aplikasi console. "Survive reboot" berarti "mulai otomatis saat
login pengguna" — bukan Windows Service — karena Claude Code (sumber event)
berjalan di sesi pengguna, jadi service SYSTEM tidak memberi keuntungan apa pun
dibanding task yang dipicu login.

```cmd
:: Dari terminal Administrator (registrasi task ONLOGON memerlukan elevasi;
:: tanpa elevasi schtasks menjawab "Access is denied")
:: --relay (atau env RELAY_URL) ikut dipertahankan task yang didaftarkan —
:: tanpa itu, setiap reboot otomatis turun ke mode LAN-only.
bin\claude-remote-server.exe -install --relay wss://<domain-relay-anda>

:: Verifikasi task terdaftar
schtasks /Query /TN ClaudeRemoteServer

:: Hapus autostart kapan saja (boleh dari terminal biasa)
bin\claude-remote-server.exe -uninstall
```

- `-install` mendaftarkan Scheduled Task `ClaudeRemoteServer` via `schtasks`
  (`/SC ONLOGON /RL LIMITED /F`) yang menjalankan:
  `"<path-exe>" -port 9280 -log-file "%USERPROFILE%\.claude\claude-remote-server.log"`
  — plus `--relay <url>` bila flag `--relay` atau env `RELAY_URL` aktif saat
  `-install` dijalankan, sehingga koneksi relay tetap hidup setelah reboot.
- `-log-file` menyalin semua log diagnostik ke file tersebut (di samping
  stdout); file otomatis dirotasi ke `.1` bila melebihi 5MB.
- Menutup jendela console (tombol X), logoff, dan shutdown memicu jalur
  graceful shutdown (offset watcher disimpan; durasi dibatasi ~3 detik).
- Peluncuran ganda pada port yang sama tidak fatal: instance kedua
  mendeteksi instance pertama via `GET /api/health` lalu keluar dengan kode 0.
- Batas hidup: tracking hanya ada selama Claude Code berjalan di sesi
  pengguna — PC sleep / belum login = tidak ada yang bisa dilacak.
- Alternatif "service sejati" dengan restart-on-crash: pemilik dapat membungkus
  EXE ini dengan WinSW atau NSSM (didokumentasikan sebagai opsi saja, tidak
  dibangun di repo ini).

---

### 2. Menginstal & Menghubungkan Aplikasi Android (APK)

#### Opsi A: Download APK Otomatis dari GitHub Releases
1. Buka repositori GitHub Anda di HP Android: [https://github.com/alipbudiman/claude-code-remote/releases](https://github.com/alipbudiman/claude-code-remote/releases)
2. Download file `claude-remote.apk`.
3. Install file APK di HP Android Anda.

#### Opsi B: Membuka Langsung via Browser HP (PWA/Webview)
1. Pastikan HP Android Anda terhubung ke jaringan Wi-Fi yang sama dengan PC.
2. Buka browser di HP Android (Chrome/Firefox) dan ketik alamat IP yang muncul di terminal (misal: `http://192.168.100.48:9280`).
3. Tekan **"Add to Home Screen"** untuk memasangnya seperti aplikasi native.

#### Menghubungkan ke Server:
1. Buka aplikasi di HP Android.
2. Jika belum otomatis terhubung, klik tombol **Settings (ikon gear/WiFi)** di pojok kanan atas.
3. Masukkan IP PC Anda (contoh: `http://192.168.100.48:9280`) lalu klik **Connect Server**.
4. Tekan ikon **Lonceng (Bell)** untuk mengaktifkan izin notifikasi getar & pop-up di HP Anda.

Cara tercepat: tombol **Scan QR** di pengaturan aplikasi → arahkan ke QR di terminal server → URL + token terisi otomatis dan langsung tersambung.

---

### 3. Menguji Sistem

Untuk melakukan simulasi event Claude Code (memulai tool, memunculkan sub-agent, dan menyelesaikan giliran), Anda dapat menjalankan script simulasi di PowerShell:
```powershell
powershell -ExecutionPolicy Bypass -File scripts\test-mock-hook.ps1
```

Status di HP Android Anda akan langsung berubah secara real-time disertai notifikasi!
