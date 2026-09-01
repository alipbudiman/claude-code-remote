package main

import (
	"reflect"
	"testing"
)

// M3 3.1: installTaskArgs must build the exact schtasks argv that registers
// the logon Scheduled Task (the /TR value embeds its own quoting).
// M7: a non-empty relay URL must be preserved in the task command, so an
// autostarted server keeps its off-LAN relay connection across reboots
// instead of silently downgrading to LAN-only.
func TestInstallTaskArgs(t *testing.T) {
	exe := `C:\Users\alif\bin\claude-remote-server.exe`
	logFile := `%USERPROFILE%\.claude\claude-remote-server.log`

	// Without a relay URL the argv is exactly the pre-M7 shape.
	got := installTaskArgs(exe, 9280, logFile, "")
	want := []string{
		"/Create",
		"/TN", "ClaudeRemoteServer",
		"/TR", `"C:\Users\alif\bin\claude-remote-server.exe" -port 9280 -log-file "%USERPROFILE%\.claude\claude-remote-server.log"`,
		"/SC", "ONLOGON",
		"/RL", "LIMITED",
		"/F",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("installTaskArgs (no relay) =\n%q\nwant\n%q", got, want)
	}

	// With a relay URL, --relay is appended after -log-file (unquoted like
	// -port: a URL can never contain a literal space).
	got = installTaskArgs(exe, 9280, logFile, "wss://relay.example.com")
	want = []string{
		"/Create",
		"/TN", "ClaudeRemoteServer",
		"/TR", `"C:\Users\alif\bin\claude-remote-server.exe" -port 9280 -log-file "%USERPROFILE%\.claude\claude-remote-server.log" --relay wss://relay.example.com`,
		"/SC", "ONLOGON",
		"/RL", "LIMITED",
		"/F",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("installTaskArgs (with relay) =\n%q\nwant\n%q", got, want)
	}
}

// M3 3.1: uninstallTaskArgs must force-delete the ClaudeRemoteServer task.
func TestUninstallTaskArgs(t *testing.T) {
	got := uninstallTaskArgs()
	want := []string{"/Delete", "/TN", "ClaudeRemoteServer", "/F"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uninstallTaskArgs = %q, want %q", got, want)
	}
}
