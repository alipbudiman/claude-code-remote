package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	hookScriptName = "claude-remote-hook.js"
)

var hookEvents = []string{
	"SessionStart",
	"SessionEnd",
	"Stop",
	"PermissionRequest",
	"Notification",
	"PreToolUse",
	"PostToolUse",
	"PostToolUseFailure",
	"SubagentStart",
	"SubagentStop",
	"TeammateIdle",
	"TaskCompleted",
}

// HookEntry represents a Claude Code hook entry in settings.json
type HookEntry struct {
	Matcher string      `json:"matcher"`
	Hooks   []HookAction `json:"hooks"`
}

// HookAction represents the action performed by a hook
type HookAction struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// GetClaudeDir returns the ~/.claude directory path
func GetClaudeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// EnsureHookBridgeScript creates the lightweight hook script in ~/.claude/
func EnsureHookBridgeScript(port int) (string, error) {
	claudeDir := GetClaudeDir()
	if claudeDir == "" {
		return "", fmt.Errorf("could not resolve home directory")
	}

	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(claudeDir, hookScriptName)

	// JavaScript script that reads JSON from stdin and sends it to our Go server
	scriptContent := fmt.Sprintf(`// Claude Remote Session Monitor Hook Bridge
const http = require('http');
const fs = require('fs');
const os = require('os');
const path = require('path');

// Read the shared auth token synchronously at script start so it is always
// available before the POST fires. Fail-safe: empty string when unavailable.
let authToken = '';
try {
  authToken = fs.readFileSync(path.join(os.homedir(), '.claude', 'claude-remote-token'), 'utf8').trim();
} catch (e) {
  authToken = '';
}

let inputData = '';
process.stdin.setEncoding('utf-8');

process.stdin.on('data', (chunk) => {
  inputData += chunk;
});

process.stdin.on('end', () => {
  if (!inputData.trim()) {
    process.exit(0);
  }

  let payload;
  try {
    payload = JSON.parse(inputData);
  } catch (e) {
    process.exit(0);
  }

  const postData = JSON.stringify(payload);
  const headers = {
    'Content-Type': 'application/json',
    'Content-Length': Buffer.byteLength(postData)
  };
  if (authToken) {
    headers['Authorization'] = 'Bearer ' + authToken;
  }
  const req = http.request({
    hostname: '127.0.0.1',
    port: %d,
    path: '/api/hook',
    method: 'POST',
    headers: headers,
    timeout: 1500
  }, (res) => {
    res.resume();
    res.on('end', () => process.exit(0));
  });

  req.on('error', () => {
    // Fail silently so Claude Code execution is never interrupted
    process.exit(0);
  });

  req.on('timeout', () => {
    req.destroy();
    process.exit(0);
  });

  req.write(postData);
  req.end();
});
`, port)

	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
		return "", err
	}

	return scriptPath, nil
}

// InstallClaudeHooks adds or updates hooks in ~/.claude/settings.json
func InstallClaudeHooks(port int) error {
	scriptPath, err := EnsureHookBridgeScript(port)
	if err != nil {
		return fmt.Errorf("failed to create hook script: %w", err)
	}

	settingsPath := filepath.Join(GetClaudeDir(), "settings.json")
	var settings map[string]interface{}

	if data, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			settings = make(map[string]interface{})
		}
	} else {
		settings = make(map[string]interface{})
	}

	hooksMap, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		hooksMap = make(map[string]interface{})
		settings["hooks"] = hooksMap
	}

	hookCommand := fmt.Sprintf("node \"%s\"", scriptPath)

	for _, event := range hookEvents {
		entry := map[string]interface{}{
			"matcher": "",
			"hooks": []map[string]interface{}{
				{
					"type":    "command",
					"command": hookCommand,
					"timeout": 5,
				},
			},
		}

		existing, hasEvent := hooksMap[event]
		if !hasEvent {
			hooksMap[event] = []interface{}{entry}
		} else if existingList, ok := existing.([]interface{}); ok {
			// Check if our hook is already registered
			alreadyRegistered := false
			for _, item := range existingList {
				if itemMap, ok := item.(map[string]interface{}); ok {
					if innerHooks, ok := itemMap["hooks"].([]interface{}); ok {
						for _, ih := range innerHooks {
							if ihMap, ok := ih.(map[string]interface{}); ok {
								if cmd, ok := ihMap["command"].(string); ok && cmd == hookCommand {
									alreadyRegistered = true
									break
								}
							}
						}
					}
				}
			}
			if !alreadyRegistered {
				hooksMap[event] = append(existingList, entry)
			}
		}
	}

	updatedData, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode settings: %w", err)
	}

	return os.WriteFile(settingsPath, updatedData, 0644)
}
