package self_test

import (
	"testing"

	"github.com/p-blackswan/platform-agent/internal/self"
)

func TestSelfModel_Register(t *testing.T) {
	m := self.New(nil)
	m.Register(self.Capability{
		ID:          "exec_shell",
		Description: "Run shell commands",
		Available:   true,
	})

	if !m.CanDo("exec_shell") {
		t.Error("expected exec_shell to be available")
	}
	if m.CanDo("nonexistent") {
		t.Error("expected nonexistent to be unavailable")
	}
}

func TestSelfModel_SetAvailable(t *testing.T) {
	m := self.New(nil)
	m.Register(self.Capability{ID: "github_api", Available: true})

	if err := m.SetAvailable("github_api", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.CanDo("github_api") {
		t.Error("expected github_api to be unavailable after SetAvailable(false)")
	}

	if err := m.SetAvailable("nonexistent", false); err == nil {
		t.Error("expected error for unknown capability")
	}
}

func TestSelfModel_RecordUse(t *testing.T) {
	m := self.New(nil)
	m.Register(self.Capability{ID: "tool_a", Available: true})

	m.RecordUse("tool_a")
	m.RecordUse("tool_a")

	c := m.Get("tool_a")
	if c == nil {
		t.Fatal("capability not found")
	}
	if c.UseCount != 2 {
		t.Errorf("expected UseCount=2, got %d", c.UseCount)
	}
	if c.LastUsed.IsZero() {
		t.Error("expected LastUsed to be set")
	}
}

func TestSelfModel_Load(t *testing.T) {
	m := self.New(nil)
	m.IncrementActive()
	m.IncrementActive()

	load := m.Load()
	if load.ActiveTasks != 2 {
		t.Errorf("expected 2 active tasks, got %d", load.ActiveTasks)
	}
	if load.GoroutineCount <= 0 {
		t.Error("expected positive goroutine count")
	}

	m.DecrementActive()
	load2 := m.Load()
	if load2.ActiveTasks != 1 {
		t.Errorf("expected 1 active task after decrement, got %d", load2.ActiveTasks)
	}
}

func TestSelfModel_CanHandleMore(t *testing.T) {
	m := self.New(nil)
	m.IncrementActive()
	m.IncrementActive()

	if !m.CanHandleMore(3) {
		t.Error("expected CanHandleMore(3) true with 2 active")
	}
	if m.CanHandleMore(2) {
		t.Error("expected CanHandleMore(2) false with 2 active")
	}
	if !m.CanHandleMore(0) {
		t.Error("expected CanHandleMore(0) always true")
	}
}

func TestSelfModel_AvailableCapabilities(t *testing.T) {
	m := self.New(nil)
	m.RegisterAll([]self.Capability{
		{ID: "a", Available: true},
		{ID: "b", Available: false},
		{ID: "c", Available: true},
	})

	avail := m.AvailableCapabilities()
	if len(avail) != 2 {
		t.Errorf("expected 2 available capabilities, got %d", len(avail))
	}
}

func TestSelfModel_Summary(t *testing.T) {
	m := self.New(nil)
	s := m.Summary()
	if s == "" {
		t.Error("expected non-empty summary")
	}
}
