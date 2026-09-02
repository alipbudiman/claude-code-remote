package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	hookScriptName = "claude-remote-hook.js"
	spoolFileName  = "claude-remote-hook.spool.jsonl"
)

// hookRegistration describes how one event's bridge entry is registered.
// Decision events run the bridge with --decide: the script long-polls
// /api/hook?decide=1 and forwards the server's hook-response JSON on stdout
// (permission allow/deny, AskUserQuestion answers, ExitPlanMode approval,
// Stop prompt delivery). Their settings.json timeout must exceed the
// server-side wait (15–110s) and the script's own 110s HTTP timeout.
type hookRegistration struct {
	event   string
	decide  bool
	timeout int
}

var hookRegistrations = []hookRegistration{
	{"SessionStart", false, 5},
	{"SessionEnd", false, 5},
	// Turn lifecycle: UserPromptSubmit opens a turn, STOP closes it — and,
	// in decide mode, delivers the next queued remote prompt.
	{"UserPromptSubmit", false, 5},
	{"Stop", true, 30},
	{"StopFailure", false, 5},
	// PermissionRequest parks a remote approval; PreToolUse (decide) answers
	// AskUserQuestion / ExitPlanMode remotely. 120s > 110s script cap.
	{"PermissionRequest", true, 120},
	{"PermissionDenied", false, 5},
	{"Notification", false, 5},
	{"PreToolUse", true, 120},
	{"PostToolUse", false, 5},
	{"PostToolUseFailure", false, 5},
	{"SubagentStart", false, 5},
	{"SubagentStop", false, 5},
	{"TeammateIdle", false, 5},
	{"TaskCompleted", false, 5},
	{"PreCompact", false, 5},
}

// HookEntry represents a Claude Code hook entry in settings.json
type HookEntry struct {
	Matcher string       `json:"matcher"`
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

// EnsureHookBridgeScript creates the hook bridge script in the real ~/.claude.
func EnsureHookBridgeScript(port int) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not resolve home directory")
	}
	return ensureHookBridgeScriptAt(home, port)
}

// ensureHookBridgeScriptAt writes the bridge script under <homeDir>/.claude/
// (tests pass a temp home).
func ensureHookBridgeScriptAt(homeDir string, port int) (string, error) {
	claudeDir := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(claudeDir, hookScriptName)

	// JavaScript script that reads JSON from stdin and sends it to our Go
	// server. Feed mode (default): fire-and-forget with spool — on delivery
	// failure the payload is spooled to claude-remote-hook.spool.jsonl so
	// the server can drain it on restart; no event is ever permanently lost
	// while the server is down. Decide mode (--decide): long-poll the
	// server's decision endpoint and forward the hook-response JSON
	// ({"hookSpecificOutput":…} or {"decision":"block",…}) on stdout so
	// Claude Code applies the remote user's choice. A lost/failed decide
	// request prints nothing and exits 0 — Claude Code falls back to its
	// normal terminal flow.
	scriptContent := fmt.Sprintf(`// Claude Remote Session Monitor Hook Bridge
const http = require('http');
const fs = require('fs');
const os = require('os');
const path = require('path');

// Decision mode (--decide): long-poll the server for a remote
// permission/question/stop decision and forward the hook-response JSON on
// stdout. Feed mode (default): fire-and-forget with spool, as before.
const DECIDE = process.argv.includes('--decide');

// Spool bounds: trim the spool once it passes the cheap size check (256KB),
// keeping only the most recent lines (max 1000).
const SPOOL_MAX_BYTES = 256 * 1024;
const SPOOL_MAX_LINES = 1000;

// Append the serialized payload as ONE line to the spool file so the server
// can replay it when it comes back up. Synchronous, no fsync, to stay well
// inside the script budget; every failure is swallowed so Claude Code
// execution is never interrupted.
function spool(postData) {
  try {
    const p = path.join(os.homedir(), '.claude', '%s');
    try {
      const st = fs.statSync(p);
      if (st.size > SPOOL_MAX_BYTES) {
        const lines = fs.readFileSync(p, 'utf8').split('\n').filter(function (l) {
          return l.length > 0;
        });
        fs.writeFileSync(p, lines.slice(-SPOOL_MAX_LINES).join('\n') + '\n', 'utf8');
      }
    } catch (e) {
      // No spool file yet — nothing to trim.
    }
    fs.appendFileSync(p, postData + '\n', 'utf8');
  } catch (e) {
    // Spooling failed; still exit cleanly.
  }
}

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
    path: '/api/hook' + (DECIDE ? '?decide=1' : ''),
    method: 'POST',
    headers: headers,
    timeout: DECIDE ? 110000 : 1500
  }, (res) => {
    let body = '';
    res.on('data', (c) => { body += c; });
    res.on('end', () => {
      // Only forward real decision bodies: a hookSpecificOutput or a
      // top-level decision. Plain {"status":"ok"} (no decision, timeout
      // fallback) stays silent so the normal flow proceeds.
      if (DECIDE) {
        try {
          const parsed = JSON.parse(body);
          if (parsed && (parsed.hookSpecificOutput || parsed.decision)) {
            process.stdout.write(body);
          }
        } catch (e) {
          // Not a decision body — stay silent.
        }
      }
      process.exit(0);
    });
  });

  req.on('error', () => {
    // Server unreachable: spool for later replay (feed value), then fail
    // silently so Claude Code execution is never interrupted.
    spool(postData);
    process.exit(0);
  });

  req.on('timeout', () => {
    req.destroy();
    spool(postData);
    process.exit(0);
  });

  req.write(postData);
  req.end();
});
`, spoolFileName, port)

	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
		return "", err
	}

	return scriptPath, nil
}

// InstallClaudeHooks adds or updates hooks in the real ~/.claude/settings.json.
func InstallClaudeHooks(port int) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not resolve home directory: %w", err)
	}
	return installClaudeHooksAt(home, port)
}

// installClaudeHooksAt registers the bridge for every event under
// <homeDir>/.claude/settings.json (tests pass a temp home). For each event it
// removes any existing entry that runs THIS bridge script (old feed-mode or
// decide-mode command) before appending the current one, so upgrades never
// double-deliver; hooks from other tools are preserved untouched.
func installClaudeHooksAt(homeDir string, port int) error {
	scriptPath, err := ensureHookBridgeScriptAt(homeDir, port)
	if err != nil {
		return fmt.Errorf("failed to create hook script: %w", err)
	}

	feedCmd := fmt.Sprintf("node \"%s\"", scriptPath)
	decideCmd := feedCmd + " --decide"

	settingsPath := filepath.Join(homeDir, ".claude", "settings.json")
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

	for _, reg := range hookRegistrations {
		wantCmd := feedCmd
		if reg.decide {
			wantCmd = decideCmd
		}

		existing, hasEvent := hooksMap[reg.event]
		if !hasEvent {
			hooksMap[reg.event] = []interface{}{newEntry(wantCmd, reg.timeout)}
			continue
		}
		existingList, ok := existing.([]interface{})
		if !ok {
			hooksMap[reg.event] = []interface{}{newEntry(wantCmd, reg.timeout)}
			continue
		}

		// Drop stale copies of OUR bridge (feed or decide form) for this
		// event, keep foreign hooks, then append the current command fresh
		// (remove-then-append is idempotent and upgrades cleanly).
		kept := make([]interface{}, 0, len(existingList))
		for _, item := range existingList {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				kept = append(kept, item)
				continue
			}
			innerHooks, ok := itemMap["hooks"].([]interface{})
			if !ok {
				kept = append(kept, item)
				continue
			}
			keptInner := make([]interface{}, 0, len(innerHooks))
			for _, ih := range innerHooks {
				ihMap, ok := ih.(map[string]interface{})
				if !ok {
					keptInner = append(keptInner, ih)
					continue
				}
				cmd, _ := ihMap["command"].(string)
				if cmd == feedCmd || cmd == decideCmd {
					continue // our bridge — replaced below
				}
				keptInner = append(keptInner, ih)
			}
			if len(keptInner) > 0 {
				itemMap["hooks"] = keptInner
				kept = append(kept, itemMap)
			}
		}
		kept = append(kept, newEntry(wantCmd, reg.timeout))
		hooksMap[reg.event] = kept
	}

	updatedData, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode settings: %w", err)
	}

	return os.WriteFile(settingsPath, updatedData, 0644)
}

// newEntry builds one matcher group running cmd with the given timeout.
func newEntry(cmd string, timeout int) map[string]interface{} {
	return map[string]interface{}{
		"matcher": "",
		"hooks": []map[string]interface{}{
			{
				"type":    "command",
				"command": cmd,
				"timeout": timeout,
			},
		},
	}
}
