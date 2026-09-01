package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// taskName is the Scheduled Task name used for logon autostart.
const taskName = "ClaudeRemoteServer"

// defaultInstallLogFile is the -log-file path baked into the registered task.
// The literal %USERPROFILE% form (expanded by Task Scheduler at run time)
// keeps the task valid for any user profile the exe is installed under.
const defaultInstallLogFile = `%USERPROFILE%\.claude\claude-remote-server.log`

// installTaskArgs builds the exact schtasks argv registering the logon
// Scheduled Task. The /TR value embeds its own quoting (exe path and log path
// may contain spaces), which Go's exec passes through as one escaped argument.
// A non-empty relayURL is appended as --relay so the autostarted server keeps
// its off-LAN relay connection across reboots (M7) — without it every reboot
// silently downgraded the server to LAN-only.
func installTaskArgs(exePath string, port int, logFile, relayURL string) []string {
	tr := fmt.Sprintf(`"%s" -port %d -log-file "%s"`, exePath, port, logFile)
	if relayURL != "" {
		// Unquoted like -port: a URL can never contain a literal space.
		tr += fmt.Sprintf(" --relay %s", relayURL)
	}
	return []string{
		"/Create",
		"/TN", taskName,
		"/TR", tr,
		"/SC", "ONLOGON",
		"/RL", "LIMITED",
		"/F",
	}
}

// uninstallTaskArgs builds the schtasks argv removing the Scheduled Task (/F
// suppresses the confirmation prompt).
func uninstallTaskArgs() []string {
	return []string{"/Delete", "/TN", taskName, "/F"}
}

// installScheduledTask registers the logon Scheduled Task pointing at this
// exe. "Survives reboot" here means "starts at the user's logon": Claude Code
// (the event source) runs in the user session, so a SYSTEM service would buy
// nothing over a logon-triggered task. A non-empty relayURL (from -relay or
// RELAY_URL) is baked into the task command so the relay survives reboots too.
func installScheduledTask(port int, relayURL string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not resolve the exe path: %w", err)
	}
	if exe, err = filepath.Abs(exe); err != nil {
		return fmt.Errorf("could not make the exe path absolute: %w", err)
	}

	args := installTaskArgs(exe, port, defaultInstallLogFile, relayURL)
	out, err := exec.Command("schtasks", args...).CombinedOutput()
	if err != nil {
		blob := strings.ToLower(string(out) + " " + err.Error())
		// /SC ONLOGON registration is elevation-gated on client Windows:
		// a non-elevated console gets "Access is denied" even though the
		// argv is valid. Point the owner at the fix instead of a bare error.
		if strings.Contains(blob, "access is denied") || strings.Contains(blob, "0x80070005") {
			return fmt.Errorf("registering the logon task requires an elevated console — re-run -install from an Administrator terminal (schtasks said: %s)", strings.TrimSpace(string(out)))
		}
		return fmt.Errorf("schtasks /Create failed: %v: %s", err, strings.TrimSpace(string(out)))
	}

	fmt.Printf("✅ Scheduled task \"%s\" registered — the server will start automatically at your logon\n", taskName)
	fmt.Printf("   Exe: %s\n   Command: schtasks %s\n", exe, strings.Join(args, " "))
	if relayURL != "" {
		fmt.Printf("   Relay preserved across reboots: --relay %s\n", relayURL)
	} else {
		fmt.Printf("   Note: no --relay given — the autostarted server will be LAN-only (pass --relay wss://... or set RELAY_URL to keep the relay connection)\n")
	}
	fmt.Printf("   Remove it any time with: %s -uninstall\n", exe)
	return nil
}

// uninstallScheduledTask removes the logon Scheduled Task. A missing task is
// tolerated (nothing to remove), anything else is fatal.
func uninstallScheduledTask() {
	out, err := exec.Command("schtasks", uninstallTaskArgs()...).CombinedOutput()
	if err != nil {
		// Not registered yet: schtasks exits 1 with "cannot find" (or the
		// localized / HRESULT form 0x80070002). That is success for -uninstall.
		blob := strings.ToLower(string(out) + " " + err.Error())
		if strings.Contains(blob, "cannot find") || strings.Contains(blob, "0x80070002") {
			fmt.Printf("✅ Scheduled task \"%s\" was not registered (nothing to remove)\n", taskName)
			return
		}
		log.Fatalf("Fatal: -uninstall failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	fmt.Printf("✅ Scheduled task \"%s\" removed — the server will no longer start at logon\n", taskName)
}
