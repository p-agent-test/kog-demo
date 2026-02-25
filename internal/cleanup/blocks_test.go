package cleanup

import (
	"testing"
	"time"

	"github.com/slack-go/slack"
)

func TestWarningBlocks(t *testing.T) {
	blocks := WarningBlocks("session-key-1", "C123", "T456", time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC), 3)

	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}

	// First block should be section
	section, ok := blocks[0].(*slack.SectionBlock)
	if !ok {
		t.Fatal("first block should be SectionBlock")
	}
	if section.Text == nil {
		t.Fatal("section text should not be nil")
	}

	// Second block should be actions
	actions, ok := blocks[1].(*slack.ActionBlock)
	if !ok {
		t.Fatal("second block should be ActionBlock")
	}
	if len(actions.Elements.ElementSet) != 2 {
		t.Fatalf("expected 2 buttons, got %d", len(actions.Elements.ElementSet))
	}

	// Check button action IDs
	btn1 := actions.Elements.ElementSet[0].(*slack.ButtonBlockElement)
	if btn1.ActionID != "session_keep_session-key-1" {
		t.Errorf("got action_id=%s", btn1.ActionID)
	}

	btn2 := actions.Elements.ElementSet[1].(*slack.ButtonBlockElement)
	if btn2.ActionID != "session_close_session-key-1" {
		t.Errorf("got action_id=%s", btn2.ActionID)
	}
}

func TestKeptBlocks(t *testing.T) {
	blocks := KeptBlocks()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
}

func TestClosedBlocks(t *testing.T) {
	blocks := ClosedBlocks()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
}

func TestExpiredBlocks(t *testing.T) {
	blocks := ExpiredBlocks()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
}
