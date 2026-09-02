package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readHooks parses the settings.json hooks map under dir.
func readHooks(t *testing.T, dir string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	hooks, ok := cfg["hooks"].(map[string]interface{})
	if !ok {
		t.Fatalf("no hooks map: %s", data)
	}
	return hooks
}

func TestInstallerRegistersDecisionHooks(t *testing.T) {
	dir := t.TempDir()
	if err := installClaudeHooksAt(dir, 9280); err != nil {
		t.Fatal(err)
	}
	hooks := readHooks(t, dir)
	script := filepath.Join(dir, ".claude", hookScriptName)

	expect := map[string]struct {
		decide  bool
		timeout float64
	}{
		"PreToolUse":        {true, 120},
		"PermissionRequest": {true, 120},
		"Stop":              {true, 30},
		"PostToolUse":       {false, 5},
		"SessionStart":      {false, 5},
	}
	for ev, want := range expect {
		groups, ok := hooks[ev].([]interface{})
		if !ok {
			t.Errorf("%s: no entry groups", ev)
			continue
		}
		found := false
		for _, g := range groups {
			for _, h := range g.(map[string]interface{})["hooks"].([]interface{}) {
				ha := h.(map[string]interface{})
				cmd := ha["command"].(string)
				hasDecide := strings.Contains(cmd, "--decide")
				if hasDecide == want.decide && ha["timeout"].(float64) == want.timeout &&
					strings.Contains(cmd, script) {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s: missing entry decide=%v timeout=%v", ev, want.decide, want.timeout)
		}
	}
}

func TestInstallerRemovesStaleNonDecideEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	oldCmd := fmt.Sprintf("node \"%s\"", filepath.Join(dir, ".claude", hookScriptName))
	stale := fmt.Sprintf(`{"hooks":{"PreToolUse":[{"matcher":"","hooks":[{"type":"command","command":%q,"timeout":5}]}]}}`, oldCmd)
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(stale), 0600); err != nil {
		t.Fatal(err)
	}
	if err := installClaudeHooksAt(dir, 9280); err != nil {
		t.Fatal(err)
	}

	// The three decision events must not hold the feed-mode command
	// (they get the --decide form); feed events legitimately keep it.
	hooks := readHooks(t, dir)
	for _, ev := range []string{"PreToolUse", "PermissionRequest", "Stop"} {
		for _, g := range hooks[ev].([]interface{}) {
			for _, h := range g.(map[string]interface{})["hooks"].([]interface{}) {
				cmd := h.(map[string]interface{})["command"].(string)
				if cmd == oldCmd {
					t.Fatalf("%s still holds the stale feed-mode command %q", ev, cmd)
				}
			}
		}
	}
	groups := hooks["PreToolUse"].([]interface{})
	if len(groups) != 1 {
		t.Fatalf("PreToolUse must hold one group, got %d", len(groups))
	}
	actions := groups[0].(map[string]interface{})["hooks"].([]interface{})
	if len(actions) != 1 || !strings.Contains(actions[0].(map[string]interface{})["command"].(string), "--decide") {
		t.Fatalf("PreToolUse must hold exactly the decide entry, got %+v", actions)
	}
}

func TestInstallerPreservesForeignHooks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	foreign := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"my-linter.sh","timeout":10}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(foreign), 0600); err != nil {
		t.Fatal(err)
	}
	if err := installClaudeHooksAt(dir, 9280); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if !strings.Contains(string(data), "my-linter.sh") {
		t.Fatalf("foreign hook must survive: %s", data)
	}
}

func TestBridgeScriptContainsDecideMode(t *testing.T) {
	dir := t.TempDir()
	scriptPath, err := ensureHookBridgeScriptAt(dir, 9280)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	for _, needle := range []string{
		"process.argv.includes('--decide')",
		"?decide=1",
		"hookSpecificOutput",
		"process.stdout.write(body)",
	} {
		if !strings.Contains(src, needle) {
			t.Errorf("bridge script missing %q", needle)
		}
	}
}
