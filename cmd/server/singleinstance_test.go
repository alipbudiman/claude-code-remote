package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// startHealthProbe spins up an httptest server answering /api/health with body
// (or status when body == "").
func startHealthProbe(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// (a) the probe answers OUR version -> the port owner is another instance of
// this server -> duplicate launch, exit 0.
func TestIsOurInstanceAnswersOurVersion(t *testing.T) {
	ts := startHealthProbe(t, http.StatusOK,
		`{"status":"ok","version":"1.0.0","uptime_s":42,"last_event_at":null}`)

	if !isOurInstance(ts.URL, testProbeToken) {
		t.Fatal("isOurInstance = false for a matching version, want true")
	}
}

// (b) the probe answers a different version (some other HTTP server happens
// to own the port) -> NOT ours -> fatal.
func TestIsOurInstanceAnswersOtherVersion(t *testing.T) {
	ts := startHealthProbe(t, http.StatusOK,
		`{"status":"ok","version":"9.9.9","uptime_s":1,"last_event_at":null}`)

	if isOurInstance(ts.URL, testProbeToken) {
		t.Fatal("isOurInstance = true for a foreign version, want false")
	}
}

// (b') the probe answers garbage (not our JSON) -> NOT ours -> fatal.
func TestIsOurInstanceAnswersGarbage(t *testing.T) {
	ts := startHealthProbe(t, http.StatusOK, `<html>It works!</html>`)

	if isOurInstance(ts.URL, testProbeToken) {
		t.Fatal("isOurInstance = true for garbage body, want false")
	}
}

// (b”) the probe rejects the token (401) -> we cannot identify it -> fatal.
func TestIsOurInstanceAnswersUnauthorized(t *testing.T) {
	ts := startHealthProbe(t, http.StatusUnauthorized, `{"error":"unauthorized"}`)

	if isOurInstance(ts.URL, testProbeToken) {
		t.Fatal("isOurInstance = true for a 401 probe, want false")
	}
}

// (c) nothing is listening (connection refused) -> fatal.
func TestIsOurInstanceConnectionRefused(t *testing.T) {
	// A closed server's port is not listening anymore.
	ts := startHealthProbe(t, http.StatusOK, `{"version":"1.0.0"}`)
	ts.Close()

	if isOurInstance(ts.URL, testProbeToken) {
		t.Fatal("isOurInstance = true on connection refused, want false")
	}
}
