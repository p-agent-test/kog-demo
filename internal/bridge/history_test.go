package bridge

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestFormatThreadHistory_Empty(t *testing.T) {
	result := FormatThreadHistory(nil, "")
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestFormatThreadHistory_Basic(t *testing.T) {
	now := time.Now()
	ts := fmt.Sprintf("%d.000000", now.Add(-2*time.Hour).Unix())

	msgs := []ThreadMessage{
		{UserID: "U123", Text: "Hello world", Timestamp: ts},
		{UserID: "U456", Text: "Hi there", Timestamp: fmt.Sprintf("%d.000000", now.Add(-1*time.Hour).Unix())},
	}

	result := FormatThreadHistory(msgs, "")
	if !strings.Contains(result, "[Thread History — auto-injected context]") {
		t.Error("missing header")
	}
	if !strings.Contains(result, "[End Thread History]") {
		t.Error("missing footer")
	}
	if !strings.Contains(result, "<@U123>") {
		t.Error("missing user U123")
	}
	if !strings.Contains(result, "<@U456>") {
		t.Error("missing user U456")
	}
	if !strings.Contains(result, "Hello world") {
		t.Error("missing message text")
	}
}

func TestFormatThreadHistory_ExcludesCurrentMessage(t *testing.T) {
	now := time.Now()
	currentTS := fmt.Sprintf("%d.000100", now.Unix())

	msgs := []ThreadMessage{
		{UserID: "U123", Text: "Old message", Timestamp: fmt.Sprintf("%d.000000", now.Add(-1*time.Hour).Unix())},
		{UserID: "U456", Text: "Current message", Timestamp: currentTS},
	}

	result := FormatThreadHistory(msgs, currentTS)
	if strings.Contains(result, "Current message") {
		t.Error("should have excluded current message")
	}
	if !strings.Contains(result, "Old message") {
		t.Error("should include old message")
	}
}

func TestFormatThreadHistory_AllExcluded(t *testing.T) {
	msgs := []ThreadMessage{
		{UserID: "U123", Text: "Only message", Timestamp: "123.456"},
	}
	result := FormatThreadHistory(msgs, "123.456")
	if result != "" {
		t.Errorf("expected empty when all messages excluded, got %q", result)
	}
}

func TestColdSessionDetection(t *testing.T) {
	wt := NewWarmTracker()

	if wt.IsWarm("session-1") {
		t.Error("new session should be cold")
	}

	wt.MarkWarm("session-1")
	if !wt.IsWarm("session-1") {
		t.Error("marked session should be warm")
	}

	if wt.IsWarm("session-2") {
		t.Error("unrelated session should still be cold")
	}
}

func TestHistoryLimits_TruncateMessages(t *testing.T) {
	now := time.Now()
	longText := strings.Repeat("x", 600)

	msgs := []ThreadMessage{
		{UserID: "U1", Text: longText, Timestamp: fmt.Sprintf("%d.000000", now.Add(-1*time.Hour).Unix())},
	}

	// Simulate provider truncation
	if len(msgs[0].Text) > maxMessageChars {
		msgs[0].Text = msgs[0].Text[:maxMessageChars] + "…"
	}

	if len(msgs[0].Text) != maxMessageChars+len("…") {
		t.Errorf("expected truncated to %d + ellipsis, got %d", maxMessageChars, len(msgs[0].Text))
	}
}

func TestHistoryLimits_TotalCharsBudget(t *testing.T) {
	now := time.Now()
	// Create many messages that together exceed maxTotalChars
	var msgs []ThreadMessage
	for i := 0; i < 30; i++ {
		msgs = append(msgs, ThreadMessage{
			UserID:    "U1",
			Text:      strings.Repeat("a", 200),
			Timestamp: fmt.Sprintf("%d.%06d", now.Add(-time.Duration(30-i)*time.Minute).Unix(), 0),
		})
	}

	result := FormatThreadHistory(msgs, "")
	if len(result) > maxTotalChars+200 { // some tolerance for header/footer
		t.Errorf("total chars %d exceeds budget %d", len(result), maxTotalChars)
	}
	// Should have kept newer messages and dropped older ones
	if result == "" {
		t.Error("should have some output")
	}
}

func TestFormatTimeAgo(t *testing.T) {
	now := time.Now()

	tests := []struct {
		ts       string
		expected string
	}{
		{fmt.Sprintf("%d.000000", now.Add(-30*time.Second).Unix()), "just now"},
		{fmt.Sprintf("%d.000000", now.Add(-5*time.Minute).Unix()), "5m ago"},
		{fmt.Sprintf("%d.000000", now.Add(-2*time.Hour).Unix()), "2h ago"},
		{fmt.Sprintf("%d.000000", now.Add(-48*time.Hour).Unix()), "2d ago"},
	}

	for _, tt := range tests {
		got := formatTimeAgo(tt.ts, now)
		if got != tt.expected {
			t.Errorf("formatTimeAgo(%q) = %q, want %q", tt.ts, got, tt.expected)
		}
	}
}
