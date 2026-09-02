package state

import (
	"strings"
	"testing"

	"claude-remote-server/internal/models"
)

func TestProcessEventRingCapsAndSeq(t *testing.T) {
	s := NewStore(0, nil)
	for i := 0; i < 250; i++ {
		s.AppendProcessEvent("s1", &models.ProcessEvent{Kind: models.EventText, Title: "e"})
	}
	evts := s.ProcessEvents("s1", 0, 500)
	if len(evts) != 200 {
		t.Fatalf("ring cap: got %d, want 200", len(evts))
	}
	if evts[0].ID == 0 || evts[len(evts)-1].ID <= evts[0].ID {
		t.Fatal("IDs must be ascending")
	}
	after := s.ProcessEvents("s1", evts[len(evts)-2].ID, 10)
	if len(after) != 1 {
		t.Fatalf("after-filter: got %d, want 1", len(after))
	}
}

func TestAppendProcessEventCapsDetail(t *testing.T) {
	s := NewStore(0, nil)
	s.AppendProcessEvent("s1", &models.ProcessEvent{Kind: models.EventToolResult, Title: "r", Detail: strings.Repeat("x", 20000)})
	evts := s.ProcessEvents("s1", 0, 10)
	if len(evts) != 1 || len(evts[0].Detail) > MaxEventDetail {
		t.Fatalf("detail cap failed: %d", len(evts[0].Detail))
	}
}

func TestAppendProcessEventCreatesSession(t *testing.T) {
	s := NewStore(0, nil)
	s.AppendProcessEvent("fresh", &models.ProcessEvent{Kind: models.EventText, Title: "x"})
	if got := s.ProcessEvents("fresh", 0, 10); len(got) != 1 {
		t.Fatalf("expected 1 event on fresh session, got %d", len(got))
	}
}
