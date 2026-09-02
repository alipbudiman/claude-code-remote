package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claude-remote-server/internal/models"
)

// withTempSettings points the permissions and app-settings file resolvers at
// a temp directory, optionally seeding settings.json.
func withTempSettings(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	permissionsFilePath = filepath.Join(dir, "settings.json")
	if content != "" {
		if err := os.WriteFile(permissionsFilePath, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	appSettingsPath = filepath.Join(dir, "appsettings.json")
	return permissionsFilePath
}

func TestPermissionsGetSetPreservesOtherKeys(t *testing.T) {
	withTempSettings(t, `{"model":"opus","permissions":{"defaultMode":"default","allow":["Bash(go test *)"]}}`)
	s, _ := newDecideServer(t)

	perms, err := s.permissionsGet()
	if err != nil {
		t.Fatal(err)
	}
	if perms["defaultMode"] != "default" {
		t.Fatalf("got %+v", perms)
	}

	err = s.permissionsSet(map[string]interface{}{
		"defaultMode": "acceptEdits",
		"allow":       []interface{}{"Bash(go test *)", "Bash(git commit *)"},
		"deny":        []interface{}{"Bash(git push *)"},
	})
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(permissionsFilePath)
	if !strings.Contains(string(data), `"model": "opus"`) {
		t.Fatalf("unrelated keys must survive: %s", data)
	}
	got, _ := s.permissionsGet()
	if got["defaultMode"] != "acceptEdits" {
		t.Fatalf("mode not written: %+v", got)
	}
}

func TestPermissionsGetAbsentFile(t *testing.T) {
	withTempSettings(t, "")
	s, _ := newDecideServer(t)
	perms, err := s.permissionsGet()
	if err != nil || len(perms) != 0 {
		t.Fatalf("absent settings must yield empty map, got %+v err=%v", perms, err)
	}
}

func TestPermissionsSetValidation(t *testing.T) {
	withTempSettings(t, `{}`)
	s, _ := newDecideServer(t)
	if err := s.permissionsSet(map[string]interface{}{"defaultMode": "yolo"}); err == nil {
		t.Fatal("invalid mode must be rejected")
	}
	if err := s.permissionsSet(map[string]interface{}{"allow": "not-an-array"}); err == nil {
		t.Fatal("non-array rule list must be rejected")
	}
}

func TestPermissionsHTTPEndpoints(t *testing.T) {
	withTempSettings(t, `{}`)
	s, _ := newDecideServer(t)
	// POST writes.
	req := httptest.NewRequest(http.MethodPost, "/api/permissions?token="+testToken,
		strings.NewReader(`{"permissions":{"defaultMode":"plan","deny":["Bash(rm *)"]}}`))
	rec := httptest.NewRecorder()
	s.handlePermissions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/permissions = %d body=%s", rec.Code, rec.Body.String())
	}
	// GET reads back.
	req = httptest.NewRequest(http.MethodGet, "/api/permissions?token="+testToken, nil)
	rec = httptest.NewRecorder()
	s.handlePermissions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/permissions = %d", rec.Code)
	}
	var out struct {
		Permissions map[string]interface{} `json:"permissions"`
	}
	if err := decodeJSON(rec.Body.String(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Permissions["defaultMode"] != "plan" {
		t.Fatalf("round trip failed: %+v", out.Permissions)
	}
	// Invalid body → 400.
	req = httptest.NewRequest(http.MethodPost, "/api/permissions?token="+testToken,
		strings.NewReader(`{"permissions":{"defaultMode":"nope"}}`))
	rec = httptest.NewRecorder()
	s.handlePermissions(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid mode = %d, want 400", rec.Code)
	}
}

func TestAppSettingsPersistence(t *testing.T) {
	withTempSettings(t, `{}`)
	s, _ := newDecideServer(t)
	in := models.AppSettings{ApprovalWaitS: 45, LogAutoClearMin: 5}
	s.persistAppSettings(in)
	if got := LoadPersistedAppSettings(); got != in {
		t.Fatalf("round trip: %+v", got)
	}
	// Defaults when absent.
	os.Remove(appSettingsPath)
	if got := LoadPersistedAppSettings(); got.ApprovalWaitS != 60 || got.LogAutoClearMin != 0 {
		t.Fatalf("defaults: %+v", got)
	}
}

// decodeJSON is a tiny helper for response bodies.
func decodeJSON(body string, v interface{}) error {
	return json.Unmarshal([]byte(body), v)
}
