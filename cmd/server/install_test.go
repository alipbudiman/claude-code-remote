package main

import (
	"reflect"
	"testing"
)

// M3 3.1: installTaskArgs must build the exact schtasks argv that registers
// the logon Scheduled Task (the /TR value embeds its own quoting).
func TestInstallTaskArgs(t *testing.T) {
	exe := `C:\Users\alif\bin\claude-remote-server.exe`
	logFile := `%USERPROFILE%\.claude\claude-remote-server.log`

	got := installTaskArgs(exe, 9280, logFile)
	want := []string{
		"/Create",
		"/TN", "ClaudeRemoteServer",
		"/TR", `"C:\Users\alif\bin\claude-remote-server.exe" -port 9280 -log-file "%USERPROFILE%\.claude\claude-remote-server.log"`,
		"/SC", "ONLOGON",
		"/RL", "LIMITED",
		"/F",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("installTaskArgs =\n%q\nwant\n%q", got, want)
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
