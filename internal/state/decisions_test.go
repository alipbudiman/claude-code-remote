package state

import (
	"testing"
	"time"

	"claude-remote-server/internal/models"
)

func TestDecisionResolveWakesWaiter(t *testing.T) {
	s := NewStore(0, nil)
	d := &models.PendingDecision{SessionID: "s1", Kind: "permission", Title: "Run: rm -rf"}
	id := s.CreatePendingDecision(d)
	if id == "" || d.ID != id {
		t.Fatalf("expected assigned id, got %q", id)
	}
	if got := s.PendingDecisions(); len(got) != 1 || got[0].ID != id {
		t.Fatalf("expected 1 pending decision %q, got %+v", id, got)
	}

	go func() {
		time.Sleep(30 * time.Millisecond)
		if !s.ResolveDecision(id, models.DecisionResolution{Action: "allow", By: "phone"}) {
			t.Error("ResolveDecision should succeed on pending id")
		}
	}()
	res, ok := s.WaitForDecision(id, 2*time.Second)
	if !ok || res == nil || res.Action != "allow" {
		t.Fatalf("expected allow resolution, got %+v ok=%v", res, ok)
	}
	// Double resolve must fail; a wait after resolution reports unknown.
	if s.ResolveDecision(id, models.DecisionResolution{Action: "deny"}) {
		t.Error("double resolve must fail")
	}
	if _, ok := s.WaitForDecision(id, 10*time.Millisecond); ok {
		t.Error("wait on resolved/removed id must report false")
	}
}

func TestDecisionWaitTimeout(t *testing.T) {
	s := NewStore(0, nil)
	id := s.CreatePendingDecision(&models.PendingDecision{SessionID: "s1", Kind: "question"})
	start := time.Now()
	if _, ok := s.WaitForDecision(id, 50*time.Millisecond); ok {
		t.Fatal("expected timeout false")
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatal("wait honored timeout")
	}
	// Timed-out decision is expired, not pending.
	if got := s.PendingDecisions(); len(got) != 0 {
		t.Fatalf("expected no pending after expiry, got %d", len(got))
	}
}

func TestPromptQueue(t *testing.T) {
	s := NewStore(0, nil)
	if d := s.EnqueuePrompt("s1", "first"); d != 1 {
		t.Fatalf("depth after first = %d, want 1", d)
	}
	if d := s.EnqueuePrompt("s1", "second"); d != 2 {
		t.Fatalf("depth after second = %d, want 2", d)
	}
	if s.PromptQueueDepth("s1") != 2 {
		t.Fatalf("depth = %d, want 2", s.PromptQueueDepth("s1"))
	}
	p, ok := s.DrainNextPrompt("s1")
	if !ok || p != "first" {
		t.Fatalf("expected FIFO 'first', got %q ok=%v", p, ok)
	}
	if s.PromptQueueDepth("s1") != 1 {
		t.Fatal("depth should drop to 1")
	}
	if _, ok := s.DrainNextPrompt("nope"); ok {
		t.Fatal("empty queue must return false")
	}
}

func TestAppSettingsRoundTrip(t *testing.T) {
	s := NewStore(0, nil)
	if s.AppSettings().ApprovalWaitS != 60 { // default
		t.Fatalf("default ApprovalWaitS = %d, want 60", s.AppSettings().ApprovalWaitS)
	}
	s.SetAppSettings(models.AppSettings{ApprovalWaitS: 120, LogAutoClearMin: 15})
	got := s.AppSettings()
	if got.ApprovalWaitS != 120 || got.LogAutoClearMin != 15 {
		t.Fatalf("round trip failed: %+v", got)
	}
}
