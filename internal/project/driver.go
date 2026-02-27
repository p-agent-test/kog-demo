package project

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Driver manages auto-drive tickers for projects.
type Driver struct {
	store   *Store
	manager *Manager
	bridge  SessionAwareForwarder
	poster  SlackResponder
	logger  zerolog.Logger

	mu      sync.Mutex
	tickers map[string]*driveTicker // projectID → ticker
	stopCh  chan struct{}
}

type driveTicker struct {
	projectID   string
	driveTimer  *time.Ticker
	reportTimer *time.Ticker
	cancel      context.CancelFunc
	busy        int32 // atomic-like via mutex; 1 if currently processing
}

// NewDriver creates a new auto-drive engine.
func NewDriver(store *Store, manager *Manager, bridge SessionAwareForwarder, poster SlackResponder, logger zerolog.Logger) *Driver {
	return &Driver{
		store:   store,
		manager: manager,
		bridge:  bridge,
		poster:  poster,
		logger:  logger.With().Str("component", "project.driver").Logger(),
		tickers: make(map[string]*driveTicker),
		stopCh:  make(chan struct{}),
	}
}

// StartDriving starts drive + report tickers for a project.
func (d *Driver) StartDriving(p *Project) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Stop existing ticker if any
	if existing, ok := d.tickers[p.ID]; ok {
		existing.cancel()
		existing.driveTimer.Stop()
		if existing.reportTimer != nil {
			existing.reportTimer.Stop()
		}
		delete(d.tickers, p.ID)
	}

	if p.DriveIntervalMs <= 0 {
		p.DriveIntervalMs = 600000 // default 10min
	}

	ctx, cancel := context.WithCancel(context.Background())
	dt := &driveTicker{
		projectID:  p.ID,
		driveTimer: time.NewTicker(time.Duration(p.DriveIntervalMs) * time.Millisecond),
		cancel:     cancel,
	}

	if p.ReportIntervalMs > 0 {
		dt.reportTimer = time.NewTicker(time.Duration(p.ReportIntervalMs) * time.Millisecond)
	}

	d.tickers[p.ID] = dt

	d.logger.Info().
		Str("project", p.Slug).
		Int64("drive_ms", p.DriveIntervalMs).
		Int64("report_ms", p.ReportIntervalMs).
		Msg("auto-drive started")

	go d.runDriveLoop(ctx, p.ID, dt)
}

func (d *Driver) runDriveLoop(ctx context.Context, projectID string, dt *driveTicker) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-dt.driveTimer.C:
			d.driveTick(ctx, projectID)
		case <-func() <-chan time.Time {
			if dt.reportTimer != nil {
				return dt.reportTimer.C
			}
			return make(chan time.Time) // never fires
		}():
			d.reportTick(ctx, projectID)
		}
	}
}

func (d *Driver) driveTick(ctx context.Context, projectID string) {
	d.mu.Lock()
	dt, ok := d.tickers[projectID]
	if !ok {
		d.mu.Unlock()
		return
	}
	if dt.busy != 0 {
		d.mu.Unlock()
		d.logger.Debug().Str("project_id", projectID).Msg("drive tick skipped — busy")
		return
	}
	dt.busy = 1
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		if dt, ok := d.tickers[projectID]; ok {
			dt.busy = 0
		}
		d.mu.Unlock()
	}()

	p, err := d.store.GetProjectByID(projectID)
	if err != nil || p == nil || !p.AutoDrive || p.Status != "active" {
		if p != nil && (!p.AutoDrive || p.Status != "active") {
			d.StopDriving(projectID)
		}
		return
	}

	// Extract repo name from URL for local path hint
	repoLocalPath := ""
	if p.RepoURL != "" {
		parts := strings.Split(strings.TrimSuffix(p.RepoURL, ".git"), "/")
		if len(parts) > 0 {
			repoLocalPath = parts[len(parts)-1]
		}
	}

	localPathHint := ""
	if repoLocalPath != "" {
		localPathHint = fmt.Sprintf(`
Repo: %s
Local path: ~/workspace/%s (ALREADY CLONED — read/write files directly, do NOT use git.get-file API)`, p.RepoURL, repoLocalPath)
	} else if p.RepoURL != "" {
		localPathHint = fmt.Sprintf("\nRepo: %s", p.RepoURL)
	}

	nudge := fmt.Sprintf(`[AUTO-DRIVE] Project: %s | Phase: %s
You are autonomously working on this project. Continue from where you left off.
Current phase: %s
All phases: %s%s

Instructions:
- If current phase is complete, update phase and move to next
- Use sub-agents for implementation work
- Record decisions with structured output
- If blocked, report the blocker
- Do NOT wait for user input — keep working autonomously
- Read/write files LOCALLY (do not use git API for file access)
- Use git.commit and git.create-branch for git operations only
- Branch naming: project/%s/{description} (NEVER commit directly to main/master)`,
		p.Name, p.CurrentPhase, p.CurrentPhase, p.Phases, localPathHint, p.Slug)

	d.logger.Info().Str("project", p.Slug).Str("phase", p.CurrentPhase).Msg("sending drive nudge")

	// Switch model for this phase if configured, otherwise use session default
	if p.PhaseModels != nil {
		if model, ok := p.PhaseModels[p.CurrentPhase]; ok && model != "" {
			modelCmd := fmt.Sprintf("/model %s", model)
			d.bridge.HandleMessageWithSession(ctx, p.ReportChannelID, "auto-drive", modelCmd, p.ReportThreadTS, "", p.ActiveSession)
			d.logger.Info().Str("phase", p.CurrentPhase).Str("model", model).Msg("switching model for phase")
		}
	}

	d.bridge.HandleMessageWithSession(ctx, p.ReportChannelID, "auto-drive", nudge, p.ReportThreadTS, "", p.ActiveSession)

	_ = d.store.TouchProject(p.Slug)
}

func (d *Driver) reportTick(ctx context.Context, projectID string) {
	d.mu.Lock()
	dt, ok := d.tickers[projectID]
	if !ok {
		d.mu.Unlock()
		return
	}
	if dt.busy != 0 {
		d.mu.Unlock()
		return
	}
	dt.busy = 1
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		if dt, ok := d.tickers[projectID]; ok {
			dt.busy = 0
		}
		d.mu.Unlock()
	}()

	p, err := d.store.GetProjectByID(projectID)
	if err != nil || p == nil || !p.AutoDrive || p.Status != "active" {
		return
	}

	prompt := fmt.Sprintf(`[STATUS-REPORT] Provide a brief status update for project %s.
Format:
📊 Phase: %s
✅ Done: (what you completed)
🔨 Working: (what you're doing now)
🚧 Blockers: (any blockers)
⏭️ Next: (what's next)`, p.Name, p.CurrentPhase)

	d.logger.Info().Str("project", p.Slug).Msg("requesting status report")

	// Send report request through the project session
	d.bridge.HandleMessageWithSession(ctx, p.ReportChannelID, "auto-drive", prompt, p.ReportThreadTS, "", p.ActiveSession)

	// Record event
	_ = d.store.AddEvent(&ProjectEvent{
		ProjectID: p.ID,
		EventType: "status_report",
		ActorID:   "auto-drive",
		Summary:   "Auto-drive status report requested",
	})
}

// StopDriving stops tickers for a project.
func (d *Driver) StopDriving(projectID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if dt, ok := d.tickers[projectID]; ok {
		dt.cancel()
		dt.driveTimer.Stop()
		if dt.reportTimer != nil {
			dt.reportTimer.Stop()
		}
		delete(d.tickers, projectID)
		d.logger.Info().Str("project_id", projectID).Msg("auto-drive stopped")
	}
}

// RestoreDriving restores auto-drive for all active auto-drive projects (called on startup).
func (d *Driver) RestoreDriving() {
	projects, err := d.store.ListAutoDriveProjects()
	if err != nil {
		d.logger.Error().Err(err).Msg("failed to restore auto-drive projects")
		return
	}

	for _, p := range projects {
		// Check expiry
		if p.AutoDriveUntil > 0 && time.Now().UnixMilli() > p.AutoDriveUntil {
			d.logger.Info().Str("project", p.Slug).Msg("auto-drive expired, disabling")
			_ = d.store.UpdateAutoDrive(p.ID, false, p.DriveIntervalMs, p.ReportIntervalMs,
				p.ReportChannelID, p.ReportThreadTS, p.CurrentPhase, p.Phases, p.AutoDriveUntil)
			continue
		}
		d.StartDriving(p)
	}

	d.logger.Info().Int("count", len(projects)).Msg("restored auto-drive projects")
}

// CheckExpiry checks for expired auto-drive projects and stops them.
func (d *Driver) CheckExpiry() {
	projects, err := d.store.ListAutoDriveProjects()
	if err != nil {
		d.logger.Error().Err(err).Msg("failed to check auto-drive expiry")
		return
	}

	now := time.Now().UnixMilli()
	for _, p := range projects {
		if p.AutoDriveUntil > 0 && now > p.AutoDriveUntil {
			d.logger.Info().Str("project", p.Slug).Msg("auto-drive expired")
			d.StopDriving(p.ID)
			_ = d.store.UpdateAutoDrive(p.ID, false, p.DriveIntervalMs, p.ReportIntervalMs,
				p.ReportChannelID, p.ReportThreadTS, p.CurrentPhase, p.Phases, p.AutoDriveUntil)

			// Notify
			if p.ReportChannelID != "" && d.poster != nil {
				_, _ = d.poster.PostMessage(p.ReportChannelID,
					fmt.Sprintf("⏰ Auto-drive expired for project `%s`. Use `drive %s` to resume.", p.Slug, p.Slug),
					p.ReportThreadTS)
			}
		}
	}
}

// IsActive returns true if the project is currently being auto-driven.
func (d *Driver) IsActive(projectID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.tickers[projectID]
	return ok
}

// ActiveCount returns the number of actively driven projects.
func (d *Driver) ActiveCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.tickers)
}

// ParseDuration parses a human-friendly duration string like "10m", "1h", "24h".
func ParseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	return time.ParseDuration(s)
}

// DurationToMs converts a duration string to milliseconds.
func DurationToMs(s string) (int64, error) {
	d, err := ParseDuration(s)
	if err != nil {
		return 0, err
	}
	return d.Milliseconds(), nil
}

// FormatDurationMs formats milliseconds as a human-readable duration.
func FormatDurationMs(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d >= time.Hour {
		hours := int(d.Hours())
		mins := int(d.Minutes()) % 60
		if mins > 0 {
			return fmt.Sprintf("%dh%dm", hours, mins)
		}
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// TimeLeftStr returns a human-readable string for time remaining.
func TimeLeftStr(untilMs int64) string {
	if untilMs <= 0 {
		return "∞"
	}
	left := time.Until(time.UnixMilli(untilMs))
	if left <= 0 {
		return "expired"
	}
	if left >= time.Hour {
		return fmt.Sprintf("%dh left", int(left.Hours()))
	}
	return fmt.Sprintf("%dm left", int(left.Minutes()))
}

// ParsePhasesString splits comma-separated phases into a clean string.
func ParsePhasesString(s string) string {
	parts := strings.Split(s, ",")
	var clean []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			clean = append(clean, p)
		}
	}
	return strings.Join(clean, ",")
}
