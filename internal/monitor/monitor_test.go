package monitor_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/p-blackswan/platform-agent/internal/monitor"
)

// mockMonitor is a simple test monitor.
type mockMonitor struct {
	id   string
	obs  []monitor.Observation
	err  error
}

func (m *mockMonitor) ID() string { return m.id }
func (m *mockMonitor) Check(_ context.Context) ([]monitor.Observation, error) {
	return m.obs, m.err
}

func newObs(monitorID, sitID string, sev int, msg string) monitor.Observation {
	return monitor.Observation{
		MonitorID:   monitorID,
		SituationID: sitID,
		Severity:    sev,
		Message:     msg,
		ObservedAt:  time.Now(),
	}
}

func TestRegistry_Register(t *testing.T) {
	r := monitor.NewRegistry(nil)
	m := &mockMonitor{id: "test"}

	if err := r.Register(m); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Duplicate should fail.
	if err := r.Register(m); err == nil {
		t.Error("expected error for duplicate registration")
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := monitor.NewRegistry(nil)
	m := &mockMonitor{id: "removable"}
	r.Register(m) //nolint:errcheck

	r.Unregister("removable")
	if r.Count() != 0 {
		t.Errorf("expected 0 monitors after unregister, got %d", r.Count())
	}
}

func TestRegistry_CheckAll_Empty(t *testing.T) {
	r := monitor.NewRegistry(nil)
	obs, err := r.CheckAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(obs) != 0 {
		t.Errorf("expected 0 observations, got %d", len(obs))
	}
}

func TestRegistry_CheckAll_Observations(t *testing.T) {
	r := monitor.NewRegistry(nil)
	r.Register(&mockMonitor{ //nolint:errcheck
		id:  "m1",
		obs: []monitor.Observation{newObs("m1", "test_sit", 3, "test message")},
	})
	r.Register(&mockMonitor{ //nolint:errcheck
		id:  "m2",
		obs: []monitor.Observation{newObs("m2", "other_sit", 5, "other message")},
	})

	obs, err := r.CheckAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(obs) != 2 {
		t.Errorf("expected 2 observations, got %d", len(obs))
	}
}

func TestRegistry_CheckAll_WithErrors(t *testing.T) {
	r := monitor.NewRegistry(nil)
	r.Register(&mockMonitor{id: "good", obs: []monitor.Observation{newObs("good", "s1", 1, "ok")}}) //nolint:errcheck
	r.Register(&mockMonitor{id: "bad", err: errors.New("check failed")})                            //nolint:errcheck

	obs, err := r.CheckAll(context.Background())
	// Should return partial observations AND an error.
	if err == nil {
		t.Error("expected error from failing monitor")
	}
	if len(obs) != 1 {
		t.Errorf("expected 1 observation from good monitor, got %d", len(obs))
	}
}

func TestRegistry_Get(t *testing.T) {
	r := monitor.NewRegistry(nil)
	m := &mockMonitor{id: "findme"}
	r.Register(m) //nolint:errcheck

	got, ok := r.Get("findme")
	if !ok {
		t.Fatal("expected to find monitor")
	}
	if got.ID() != "findme" {
		t.Errorf("unexpected monitor ID: %s", got.ID())
	}

	_, ok = r.Get("notfound")
	if ok {
		t.Error("expected not found")
	}
}

func TestSystemMonitor_Check(t *testing.T) {
	// SystemMonitor should run without error on CI (may return no observations).
	m := monitor.NewSystemMonitor("sys-test", nil)
	m.MaxAllocMB = 99999   // high threshold so we don't trigger warnings in tests
	m.MaxGoroutines = 99999
	m.MaxDiskUsePct = 99.9

	obs, err := m.Check(context.Background())
	if err != nil {
		t.Fatalf("system monitor check failed: %v", err)
	}
	t.Logf("system observations: %d", len(obs))
}

func TestSystemMonitor_HighMemory(t *testing.T) {
	m := monitor.NewSystemMonitor("sys-mem", nil)
	m.MaxAllocMB = 0.001 // set impossibly low threshold to trigger observation

	obs, err := m.Check(context.Background())
	if err != nil {
		t.Fatalf("system monitor check failed: %v", err)
	}

	found := false
	for _, o := range obs {
		if o.SituationID == "system_high_memory" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected system_high_memory observation with low threshold")
	}
}
