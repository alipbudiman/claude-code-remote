package main

import (
	"encoding/json"
	"net/http"
	"time"

	"claude-remote-server/internal/api"
)

// testProbeToken is a bearer token constant for probe tests (a valid 64-char
// hex secret, mirroring the real token format).
const testProbeToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// isOurInstance reports whether the /api/health endpoint at baseURL answers
// with THIS server's version — i.e. the port is owned by another Claude
// Remote Server instance. The probe is authenticated because /api/health is
// token-gated like every other /api/* route.
//
// Decision table for a failed port bind:
//
//	probe answers our version -> duplicate launch, exit 0
//	probe answers anything else / refuses -> not ours, fatal
func isOurInstance(baseURL, token string) bool {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/health", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false // connection refused / timeout: nothing recognizable there
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}
	var h struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return false // not our JSON
	}
	return h.Version == api.ServerVersion
}
