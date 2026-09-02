package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"claude-remote-server/internal/models"
)

// Permission-rules editor (2026-09-02): reads and writes the `permissions`
// section of ~/.claude/settings.json, preserving every other key. Rule
// syntax is Claude Code's own ("Bash(npm run *)", "Read(./.env)", …);
// evaluation order inside Claude Code is deny → ask → allow.

// permissionsFilePath overrides ~/.claude/settings.json for tests.
var permissionsFilePath string

var validModes = map[string]bool{
	"default": true, "manual": true, "acceptEdits": true, "plan": true,
	"auto": true, "dontAsk": true, "bypassPermissions": true,
}

func settingsPath() (string, error) {
	if permissionsFilePath != "" {
		return permissionsFilePath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func readSettings() (map[string]interface{}, error) {
	p, err := settingsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return map[string]interface{}{}, nil
	}
	if err != nil {
		return nil, err
	}
	cfg := map[string]interface{}{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("settings.json not valid JSON: %w", err)
	}
	return cfg, nil
}

func writeSettings(cfg map[string]interface{}) error {
	p, err := settingsPath()
	if err != nil {
		return err
	}
	if dir := filepath.Dir(p); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0644)
}

// permissionsGet returns the settings.json permissions section ({} if absent).
func (s *Server) permissionsGet() (map[string]interface{}, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg, err := readSettings()
	if err != nil {
		return nil, err
	}
	if perms, ok := cfg["permissions"].(map[string]interface{}); ok {
		return perms, nil
	}
	return map[string]interface{}{}, nil
}

// permissionsSet validates and MERGES the incoming permissions into the
// current section (keys the caller omits — e.g. additionalDirectories —
// survive), preserving every other settings key, then broadcasts the new
// value. The whole read-modify-write holds settingsMu: concurrent
// command-frame dispatches would otherwise interleave writes and corrupt
// the file.
func (s *Server) permissionsSet(perms map[string]interface{}) error {
	if mode, ok := perms["defaultMode"].(string); ok && !validModes[mode] {
		return fmt.Errorf("invalid defaultMode %q", mode)
	}
	for _, key := range []string{"allow", "ask", "deny", "additionalDirectories"} {
		v, ok := perms[key]
		if !ok {
			continue // omitted keys merge: the existing value survives
		}
		list, ok := v.([]interface{})
		if !ok {
			return fmt.Errorf("%s must be an array of strings", key)
		}
		for _, el := range list {
			if _, ok := el.(string); !ok {
				return fmt.Errorf("%s must contain only strings", key)
			}
		}
	}

	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg, err := readSettings()
	if err != nil {
		return err
	}
	merged, _ := cfg["permissions"].(map[string]interface{})
	if merged == nil {
		merged = map[string]interface{}{}
	}
	for k, v := range perms {
		merged[k] = v
	}
	cfg["permissions"] = merged
	if err := writeSettings(cfg); err != nil {
		return err
	}
	out := merged
	s.store.Publish(models.WebSocketMessage{
		Type: "permissions", Data: map[string]interface{}{"permissions": out}, Timestamp: time.Now(),
	})
	return nil
}

// writeErr emits a properly escaped JSON error body (raw string
// concatenation breaks when the error text contains quotes).
func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// handlePermissions serves GET/POST /api/permissions.
func (s *Server) handlePermissions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		perms, err := s.permissionsGet()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"permissions": perms})
	case http.MethodPost:
		var body struct {
			Permissions map[string]interface{} `json:"permissions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Permissions == nil {
			writeErr(w, http.StatusBadRequest, `body must be {"permissions":{...}}`)
			return
		}
		if err := s.permissionsSet(body.Permissions); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeOK(w)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
