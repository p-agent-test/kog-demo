package goal_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/p-blackswan/platform-agent/internal/goal"
	"github.com/p-blackswan/platform-agent/internal/llm"
)

// mockProvider is a minimal LLMProvider for testing.
type mockProvider struct {
	response string
}

func (m *mockProvider) Complete(_ context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return &llm.CompletionResponse{
		Text:       m.response,
		StopReason: llm.StopReasonEndTurn,
	}, nil
}
func (m *mockProvider) Stream(_ context.Context, _ llm.CompletionRequest, out chan<- llm.Token) error {
	out <- llm.Token{Text: m.response, Done: true}
	return nil
}
func (m *mockProvider) ModelID() string  { return "mock" }
func (m *mockProvider) MaxTokens() int   { return 1024 }

func TestDecomposer_ValidJSON(t *testing.T) {
	provider := &mockProvider{
		response: `{"tasks":[
			{"id":"t1","description":"Step one","depends_on":[],"status":"pending"},
			{"id":"t2","description":"Step two","depends_on":["t1"],"status":"pending"}
		]}`,
	}
	d := goal.NewDecomposer(provider, nil)
	g, err := d.Decompose(context.Background(), "Do something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(g.Tasks))
	}
	if g.Tasks[1].DependsOn[0] != "t1" {
		t.Errorf("expected t2 to depend on t1")
	}
}

func TestDecomposer_FallbackOnBadJSON(t *testing.T) {
	provider := &mockProvider{response: "not json at all"}
	d := goal.NewDecomposer(provider, nil)
	g, err := d.Decompose(context.Background(), "Do something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Tasks) != 1 {
		t.Errorf("expected 1 fallback task, got %d", len(g.Tasks))
	}
}

func TestExecutor_Sequential(t *testing.T) {
	exec := goal.NewExecutor(1, nil)

	g := &goal.Goal{
		ID:          "g1",
		Description: "test goal",
		Tasks: []goal.Task{
			{ID: "t1", Description: "first", Status: goal.StatusPending},
			{ID: "t2", Description: "second", DependsOn: []string{"t1"}, Status: goal.StatusPending},
		},
		Status:    goal.StatusPending,
		CreatedAt: time.Now(),
	}

	var order []string
	err := exec.Execute(context.Background(), g, func(_ context.Context, t *goal.Task) error {
		order = append(order, t.ID)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g.Status != goal.StatusDone {
		t.Errorf("expected goal status done, got %s", g.Status)
	}
	if len(order) != 2 {
		t.Errorf("expected 2 tasks executed, got %d", len(order))
	}
	// t1 must come before t2
	if order[0] != "t1" {
		t.Errorf("expected t1 first, got %s", order[0])
	}
}

func TestExecutor_Parallel(t *testing.T) {
	exec := goal.NewExecutor(4, nil)

	var counter int64
	g := &goal.Goal{
		ID:          "g2",
		Description: "parallel test",
		Tasks: []goal.Task{
			{ID: "t1", Description: "a", Status: goal.StatusPending},
			{ID: "t2", Description: "b", Status: goal.StatusPending},
			{ID: "t3", Description: "c", Status: goal.StatusPending},
		},
		Status:    goal.StatusPending,
		CreatedAt: time.Now(),
	}

	err := exec.Execute(context.Background(), g, func(_ context.Context, _ *goal.Task) error {
		atomic.AddInt64(&counter, 1)
		time.Sleep(10 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if counter != 3 {
		t.Errorf("expected 3 tasks executed, got %d", counter)
	}
}

func TestExecutor_TaskFailure(t *testing.T) {
	exec := goal.NewExecutor(1, nil)

	g := &goal.Goal{
		ID:          "g3",
		Description: "fail test",
		Tasks: []goal.Task{
			{ID: "t1", Description: "fail me", Status: goal.StatusPending},
		},
		Status:    goal.StatusPending,
		CreatedAt: time.Now(),
	}

	err := exec.Execute(context.Background(), g, func(_ context.Context, _ *goal.Task) error {
		return errors.New("simulated failure")
	})
	if err == nil {
		t.Fatal("expected error from failed task")
	}
	if g.Status != goal.StatusFailed {
		t.Errorf("expected goal status failed, got %s", g.Status)
	}
}

func TestExecutor_UnknownDependency(t *testing.T) {
	exec := goal.NewExecutor(1, nil)

	g := &goal.Goal{
		ID:          "g4",
		Description: "bad dep",
		Tasks: []goal.Task{
			{ID: "t1", Description: "depends on ghost", DependsOn: []string{"ghost"}, Status: goal.StatusPending},
		},
		Status:    goal.StatusPending,
		CreatedAt: time.Now(),
	}

	err := exec.Execute(context.Background(), g, func(_ context.Context, _ *goal.Task) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for unknown dependency")
	}
}

func TestGoal_Summary(t *testing.T) {
	g := &goal.Goal{
		ID:          "g5",
		Description: "summary test",
		Tasks: []goal.Task{
			{ID: "t1", Status: goal.StatusDone},
			{ID: "t2", Status: goal.StatusFailed},
			{ID: "t3", Status: goal.StatusPending},
		},
		Status: goal.StatusRunning,
	}
	s := g.Summary()
	if s == "" {
		t.Error("expected non-empty summary")
	}
	t.Logf("summary: %s", s)
	_ = fmt.Sprintf("%s", s) // use fmt
}
