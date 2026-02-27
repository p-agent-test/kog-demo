// Package goal defines the Goal struct, a decomposer that breaks goals into tasks,
// and an executor that runs them with dependency awareness.
package goal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/p-blackswan/platform-agent/internal/llm"
)

// Status tracks a goal or task's lifecycle.
type Status string

const (
	StatusPending  Status = "pending"
	StatusRunning  Status = "running"
	StatusDone     Status = "done"
	StatusFailed   Status = "failed"
	StatusBlocked  Status = "blocked"  // blocked on a dependency
	StatusCancelled Status = "cancelled"
)

// Task is a single atomic unit of work within a goal.
type Task struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	DependsOn   []string `json:"depends_on,omitempty"` // IDs of tasks this depends on
	Status      Status   `json:"status"`
	Result      string   `json:"result,omitempty"`
	Error       string   `json:"error,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	FinishedAt  time.Time `json:"finished_at,omitempty"`
}

// Goal is a high-level objective decomposed into an ordered task list.
type Goal struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	Tasks       []Task    `json:"tasks"`
	Status      Status    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	FinishedAt  time.Time `json:"finished_at,omitempty"`
}

// TaskExecutorFunc is a function that executes a task and returns its result.
type TaskExecutorFunc func(ctx context.Context, t *Task) error

// Decomposer uses an LLM to break a goal description into a task list with dependencies.
type Decomposer struct {
	provider llm.LLMProvider
	logger   *slog.Logger
}

var decompositionPrompt = `You are a goal decomposition engine. Given a goal description, break it into an ordered list of atomic tasks with dependencies.

Respond ONLY with valid JSON (no markdown, no explanation):
{
  "tasks": [
    {
      "id": "t1",
      "description": "<clear, self-contained task>",
      "depends_on": [],
      "status": "pending"
    },
    {
      "id": "t2",
      "description": "<next task that may depend on t1>",
      "depends_on": ["t1"],
      "status": "pending"
    }
  ]
}

Rules:
- IDs must be unique strings: t1, t2, t3...
- Each task must be independently executable once its dependencies are done.
- depends_on contains IDs of tasks that must complete before this one starts.
- Maximum 15 tasks.
- No circular dependencies.`

// NewDecomposer creates a Decomposer backed by an LLM provider.
func NewDecomposer(provider llm.LLMProvider, logger *slog.Logger) *Decomposer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Decomposer{provider: provider, logger: logger}
}

// Decompose calls the LLM to break goalDescription into tasks and builds a Goal.
func (d *Decomposer) Decompose(ctx context.Context, goalDescription string) (*Goal, error) {
	resp, err := d.provider.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: decompositionPrompt,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Goal: " + goalDescription},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("decompose: llm call: %w", err)
	}

	var parsed struct {
		Tasks []Task `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &parsed); err != nil {
		d.logger.Warn("decompose: parse failed, fallback single task",
			"text", resp.Text[:min(200, len(resp.Text))], "err", err)
		parsed.Tasks = []Task{
			{ID: "t1", Description: goalDescription, Status: StatusPending},
		}
	}

	goal := &Goal{
		ID:          newGoalID(),
		Description: goalDescription,
		Tasks:       parsed.Tasks,
		Status:      StatusPending,
		CreatedAt:   time.Now().UTC(),
	}
	return goal, nil
}

// Executor runs a Goal's tasks respecting dependency order.
// Tasks whose dependencies are all done run concurrently (up to maxParallel).
type Executor struct {
	logger      *slog.Logger
	maxParallel int
}

// NewExecutor creates an Executor.
// maxParallel=0 or 1 means sequential; >1 enables parallel execution.
func NewExecutor(maxParallel int, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	if maxParallel <= 0 {
		maxParallel = 1
	}
	return &Executor{logger: logger, maxParallel: maxParallel}
}

// Execute runs all tasks in the goal using the provided executor function.
// Tasks are run in dependency order; independent tasks run in parallel if maxParallel > 1.
func (e *Executor) Execute(ctx context.Context, g *Goal, exec TaskExecutorFunc) error {
	g.Status = StatusRunning

	// Build a map for quick task lookup.
	taskMap := make(map[string]*Task, len(g.Tasks))
	for i := range g.Tasks {
		taskMap[g.Tasks[i].ID] = &g.Tasks[i]
	}

	// Validate no unknown dependencies.
	for _, t := range g.Tasks {
		for _, dep := range t.DependsOn {
			if _, ok := taskMap[dep]; !ok {
				return fmt.Errorf("executor: task %q depends on unknown task %q", t.ID, dep)
			}
		}
	}

	// Execute tasks using topological ordering with concurrency.
	if err := e.topoExec(ctx, g.Tasks, taskMap, exec); err != nil {
		g.Status = StatusFailed
		return err
	}

	// Determine final goal status.
	for _, t := range g.Tasks {
		if t.Status == StatusFailed {
			g.Status = StatusFailed
			g.FinishedAt = time.Now().UTC()
			return fmt.Errorf("executor: goal %q failed: task %q failed: %s", g.ID, t.ID, t.Error)
		}
	}

	g.Status = StatusDone
	g.FinishedAt = time.Now().UTC()
	e.logger.Info("goal done", "goal_id", g.ID, "tasks", len(g.Tasks))
	return nil
}

// topoExec runs tasks in topological order, parallelizing independent tasks.
func (e *Executor) topoExec(ctx context.Context, tasks []Task, taskMap map[string]*Task, exec TaskExecutorFunc) error {
	done := make(map[string]bool)
	var mu sync.Mutex

	remaining := make([]*Task, len(tasks))
	for i := range tasks {
		remaining[i] = taskMap[tasks[i].ID]
	}

	sem := make(chan struct{}, e.maxParallel)

	for {
		// Find tasks that are ready (all deps done).
		var ready []*Task
		mu.Lock()
		for _, t := range remaining {
			if t.Status != StatusPending {
				continue
			}
			allDone := true
			for _, dep := range t.DependsOn {
				if !done[dep] {
					// Check if dep failed.
					if dep, ok := taskMap[dep]; ok && dep.Status == StatusFailed {
						t.Status = StatusBlocked
						t.Error = fmt.Sprintf("dependency %q failed", dep.ID)
					}
					allDone = false
					break
				}
			}
			if allDone && t.Status == StatusPending {
				ready = append(ready, t)
			}
		}
		mu.Unlock()

		if len(ready) == 0 {
			// Check if all tasks are terminal.
			mu.Lock()
			allDone := true
			for _, t := range remaining {
				if t.Status == StatusPending || t.Status == StatusRunning {
					allDone = false
					break
				}
			}
			mu.Unlock()
			if allDone {
				break
			}
			// Deadlock: tasks remain but none are ready (circular deps or all blocked).
			return fmt.Errorf("executor: deadlock — no tasks are ready but %d remain", len(ready))
		}

		var wg sync.WaitGroup
		for _, t := range ready {
			t := t
			select {
			case <-ctx.Done():
				return ctx.Err()
			case sem <- struct{}{}:
			}

			mu.Lock()
			t.Status = StatusRunning
			t.StartedAt = time.Now().UTC()
			mu.Unlock()

			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()

				e.logger.Info("executing task", "task_id", t.ID, "desc", t.Description)

				if err := exec(ctx, t); err != nil {
					mu.Lock()
					t.Status = StatusFailed
					t.Error = err.Error()
					t.FinishedAt = time.Now().UTC()
					mu.Unlock()
					e.logger.Error("task failed", "task_id", t.ID, "err", err)
				} else {
					mu.Lock()
					t.Status = StatusDone
					t.FinishedAt = time.Now().UTC()
					done[t.ID] = true
					mu.Unlock()
					e.logger.Info("task done", "task_id", t.ID)
				}
			}()
		}
		wg.Wait()
	}
	return nil
}

// Summary returns a one-line status of the goal.
func (g *Goal) Summary() string {
	done, failed, pending := 0, 0, 0
	for _, t := range g.Tasks {
		switch t.Status {
		case StatusDone:
			done++
		case StatusFailed:
			failed++
		case StatusPending, StatusBlocked:
			pending++
		}
	}
	return fmt.Sprintf("goal=%q status=%s tasks=%d done=%d failed=%d pending=%d",
		g.Description[:min(50, len(g.Description))], g.Status, len(g.Tasks), done, failed, pending)
}

func newGoalID() string {
	return fmt.Sprintf("goal_%d", time.Now().UnixNano())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
