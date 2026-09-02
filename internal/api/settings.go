package api

import (
	"encoding/json"
	"os"
	"path/filepath"

	"claude-remote-server/internal/models"
)

// App-settings persistence (2026-09-02): ~/.claude/claude-remote-settings.json
// holds the server's own remote-interaction settings (approval wait, log
// auto-clear). Same home-dir resolution pattern as the auth token and relay
// URL files.

// appSettingsPath overrides the default file location for tests.
var appSettingsPath string

func appSettingsFile() (string, error) {
	if appSettingsPath != "" {
		return appSettingsPath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "claude-remote-settings.json"), nil
}

// LoadPersistedAppSettings returns saved settings or the defaults (60s / off).
func LoadPersistedAppSettings() models.AppSettings {
	out := models.AppSettings{ApprovalWaitS: 60, LogAutoClearMin: 0}
	p, err := appSettingsFile()
	if err != nil {
		return out
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(data, &out)
	if out.ApprovalWaitS <= 0 {
		out.ApprovalWaitS = 60
	}
	return out
}

// persistAppSettings writes the settings file (a two-field document; a
// failed write keeps the in-memory setting effective for this run).
func (s *Server) persistAppSettings(v models.AppSettings) {
	p, err := appSettingsFile()
	if err != nil {
		return
	}
	if dir := filepath.Dir(p); dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = os.WriteFile(p, data, 0600)
}
