package project

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/slack-go/slack"
)

// MessageForwarder is the basic interface for forwarding messages.
type MessageForwarder interface {
	HandleMessage(ctx context.Context, channelID, userID, text, threadTS, messageTS string)
	IsActiveThread(channelID, threadTS string) bool
}

// SessionAwareForwarder extends MessageForwarder with explicit session routing.
type SessionAwareForwarder interface {
	MessageForwarder
	HandleMessageWithSession(ctx context.Context, channelID, userID, text, threadTS, messageTS, sessionKey string)
}

// SlackResponder abstracts posting messages to Slack for the router.
type SlackResponder interface {
	PostMessage(channelID string, text string, threadTS string) (string, error)
	PostBlocks(channelID string, threadTS string, fallbackText string, blocks ...slack.Block) (string, error)
}

// Router sits between Slack handlers and the bridge, resolving project routing.
type Router struct {
	store   *Store
	manager *Manager
	bridge  SessionAwareForwarder
	poster  SlackResponder
	driver  *Driver
	botUID  string
	logger  zerolog.Logger
}

// NewRouter creates a new project router.
func NewRouter(store *Store, manager *Manager, bridge SessionAwareForwarder, poster SlackResponder, botUserID string, logger zerolog.Logger) *Router {
	return &Router{
		store:   store,
		manager: manager,
		bridge:  bridge,
		poster:  poster,
		botUID:  botUserID,
		logger:  logger.With().Str("component", "project.router").Logger(),
	}
}

// SetDriver sets the auto-drive engine on the router.
func (r *Router) SetDriver(d *Driver) {
	r.driver = d
}

// HandleMessage implements the MessageForwarder interface with project-aware routing.
func (r *Router) HandleMessage(ctx context.Context, channelID, userID, text, threadTS, messageTS string) {
	// 1. Check thread binding
	if threadTS != "" {
		proj, err := r.store.GetProjectByThread(channelID, threadTS)
		if err == nil && proj != nil {
			r.logger.Debug().Str("slug", proj.Slug).Str("thread", threadTS).Msg("thread bound to project")
			if proj.Status == "archived" {
				r.respond(channelID, threadTS, messageTS, fmt.Sprintf("Project `%s` is archived. Run `resume %s` to reactivate.", proj.Slug, proj.Slug))
				return
			}
			_ = r.store.TouchProject(proj.Slug)
			r.bridge.HandleMessageWithSession(ctx, channelID, userID, text, threadTS, messageTS, proj.ActiveSession)
			return
		}
	}

	// 2. Parse command from text (strip bot mention first)
	cleanText := text
	if r.botUID != "" {
		mention := fmt.Sprintf("<@%s>", r.botUID)
		cleanText = strings.TrimPrefix(cleanText, mention)
		cleanText = strings.TrimSpace(cleanText)
	}

	cmd := ParseCommand(cleanText)
	if cmd != nil {
		switch cmd.Type {
		case CmdListProjects:
			r.handleListProjects(channelID, userID, threadTS, messageTS)
			return

		case CmdNewProject:
			r.handleNewProject(channelID, userID, threadTS, messageTS, cmd)
			return

		case CmdDecide:
			r.handleDecide(channelID, userID, threadTS, messageTS, cmd)
			return

		case CmdBlocker:
			r.handleBlocker(channelID, userID, threadTS, messageTS, cmd)
			return

		case CmdArchive:
			r.handleArchive(channelID, userID, threadTS, messageTS, cmd)
			return

		case CmdResume:
			r.handleResume(channelID, userID, threadTS, messageTS, cmd)
			return

		case CmdDrive:
			r.handleDrive(channelID, userID, threadTS, messageTS, cmd)
			return

		case CmdPause:
			r.handlePause(channelID, userID, threadTS, messageTS, cmd)
			return

		case CmdPhase:
			r.handlePhase(channelID, userID, threadTS, messageTS, cmd)
			return

		case CmdReport:
			r.handleReport(channelID, userID, threadTS, messageTS, cmd)
			return

		case CmdContinueProject, CmdMessageProject:
			// Check if slug matches a known project
			proj, _ := r.store.GetProject(cmd.Slug)
			if proj != nil {
				if proj.Status == "archived" {
					r.respond(channelID, threadTS, messageTS, fmt.Sprintf("Project `%s` is archived. Run `resume %s` to reactivate.", proj.Slug, proj.Slug))
					return
				}
				if cmd.Type == CmdContinueProject {
					r.handleContinueProject(ctx, channelID, userID, threadTS, messageTS, proj)
				} else {
					r.bridge.HandleMessageWithSession(ctx, channelID, userID, cmd.Message, threadTS, messageTS, proj.ActiveSession)
				}
				return
			}
			// Not a known slug → fall through to default
		}
	}

	// 3. Default: existing behavior
	r.bridge.HandleMessage(ctx, channelID, userID, text, threadTS, messageTS)
}

// IsActiveThread delegates to the underlying bridge.
func (r *Router) IsActiveThread(channelID, threadTS string) bool {
	// Check project thread binding first
	proj, _ := r.store.GetProjectByThread(channelID, threadTS)
	if proj != nil {
		return true
	}
	return r.bridge.IsActiveThread(channelID, threadTS)
}

func (r *Router) respond(channelID, threadTS, messageTS, text string) {
	if r.poster == nil {
		return
	}
	replyThread := threadTS
	if replyThread == "" {
		replyThread = messageTS
	}
	_, _ = r.poster.PostMessage(channelID, text, replyThread)
}

func (r *Router) respondBlocks(channelID, threadTS, messageTS, fallback string, blocks ...slack.Block) {
	if r.poster == nil {
		return
	}
	replyThread := threadTS
	if replyThread == "" {
		replyThread = messageTS
	}
	_, _ = r.poster.PostBlocks(channelID, replyThread, fallback, blocks...)
}

func (r *Router) handleListProjects(channelID, userID, threadTS, messageTS string) {
	projects, err := r.store.ListProjects("active", "")
	if err != nil {
		r.respond(channelID, threadTS, messageTS, "⚠️ Failed to list projects.")
		return
	}
	if len(projects) == 0 {
		r.respond(channelID, threadTS, messageTS, "No active projects. Create one with `new project \"Name\"`")
		return
	}

	statsMap := make(map[string]*ProjectStats)
	eventsMap := make(map[string]*ProjectEvent)
	for _, p := range projects {
		stats, _ := r.store.GetProjectStats(p.ID)
		statsMap[p.ID] = stats
		events, _ := r.store.ListEvents(p.ID, 1)
		if len(events) > 0 {
			eventsMap[p.ID] = events[0]
		}
	}

	blocks := DashboardBlocks(projects, statsMap, eventsMap)
	r.respondBlocks(channelID, threadTS, messageTS, fmt.Sprintf("📂 %d Active Projects", len(projects)), blocks...)
}

func (r *Router) handleNewProject(channelID, userID, threadTS, messageTS string, cmd *ProjectCommand) {
	if cmd.Name == "" {
		r.respond(channelID, threadTS, messageTS, "Usage: `new project \"Project Name\" [--repo URL]`")
		return
	}

	p, err := r.store.CreateProject(CreateProjectInput{
		Name:    cmd.Name,
		RepoURL: cmd.RepoURL,
		OwnerID: userID,
	})
	if err != nil {
		r.respond(channelID, threadTS, messageTS, fmt.Sprintf("⚠️ %s", err.Error()))
		return
	}

	// If --auto-drive was specified, enable it
	if cmd.DriveInterval != "" && r.driver != nil {
		driveCmd := &ProjectCommand{
			Type:           CmdDrive,
			Slug:           p.Slug,
			DriveInterval:  cmd.DriveInterval,
			ReportInterval: cmd.ReportInterval,
			Phases:         cmd.Phases,
			Duration:       cmd.Duration,
		}
		r.handleDrive(channelID, userID, threadTS, messageTS, driveCmd)
		return
	}

	blocks := ProjectCreatedBlocks(p)
	r.respondBlocks(channelID, threadTS, messageTS, fmt.Sprintf("✅ Project created: %s", p.Slug), blocks...)
}

func (r *Router) handleDecide(channelID, userID, threadTS, messageTS string, cmd *ProjectCommand) {
	proj, _ := r.store.GetProject(cmd.Slug)
	if proj == nil {
		r.respond(channelID, threadTS, messageTS, fmt.Sprintf("Project `%s` not found.", cmd.Slug))
		return
	}

	_ = r.store.AddMemory(&ProjectMemory{
		ProjectID: proj.ID,
		Type:      "decision",
		Content:   cmd.Message,
	})
	stats, _ := r.store.GetProjectStats(proj.ID)
	total := 0
	if stats != nil {
		total = stats.Decisions
	}
	blocks := DecisionRecordedBlocks(proj.Slug, cmd.Message, total)
	r.respondBlocks(channelID, threadTS, messageTS, fmt.Sprintf("📌 Decision recorded for %s", proj.Slug), blocks...)
}

func (r *Router) handleBlocker(channelID, userID, threadTS, messageTS string, cmd *ProjectCommand) {
	proj, _ := r.store.GetProject(cmd.Slug)
	if proj == nil {
		r.respond(channelID, threadTS, messageTS, fmt.Sprintf("Project `%s` not found.", cmd.Slug))
		return
	}

	_ = r.store.AddMemory(&ProjectMemory{
		ProjectID: proj.ID,
		Type:      "blocker",
		Content:   cmd.Message,
	})
	stats, _ := r.store.GetProjectStats(proj.ID)
	total := 0
	if stats != nil {
		total = stats.Blockers
	}
	blocks := BlockerRecordedBlocks(proj.Slug, cmd.Message, total)
	r.respondBlocks(channelID, threadTS, messageTS, fmt.Sprintf("🚧 Blocker recorded for %s", proj.Slug), blocks...)
}

func (r *Router) handleArchive(channelID, userID, threadTS, messageTS string, cmd *ProjectCommand) {
	// Stop auto-drive if active
	proj, _ := r.store.GetProject(cmd.Slug)
	if proj != nil && r.driver != nil {
		r.driver.StopDriving(proj.ID)
	}

	if err := r.store.ArchiveProject(cmd.Slug, userID); err != nil {
		r.respond(channelID, threadTS, messageTS, fmt.Sprintf("⚠️ %s", err.Error()))
		return
	}
	r.respond(channelID, threadTS, messageTS, fmt.Sprintf("📦 Project `%s` archived.", cmd.Slug))
}

func (r *Router) handleDrive(channelID, userID, threadTS, messageTS string, cmd *ProjectCommand) {
	if r.driver == nil {
		r.respond(channelID, threadTS, messageTS, "⚠️ Auto-drive not available.")
		return
	}

	proj, _ := r.store.GetProject(cmd.Slug)
	if proj == nil {
		r.respond(channelID, threadTS, messageTS, fmt.Sprintf("Project `%s` not found.", cmd.Slug))
		return
	}
	if proj.Status != "active" {
		r.respond(channelID, threadTS, messageTS, fmt.Sprintf("Project `%s` is %s. Resume it first.", cmd.Slug, proj.Status))
		return
	}

	// Auto-bind thread + inject project preamble if not already bound
	replyThread := threadTS
	if replyThread == "" {
		replyThread = messageTS
	}
	if replyThread != "" {
		existing, _ := r.store.GetProjectByThread(channelID, replyThread)
		if existing == nil {
			_ = r.store.BindThread(channelID, replyThread, proj.ID, proj.ActiveSession)
			// Send preamble to initialize the session with project context
			preamble, err := r.manager.BuildContextPreamble(proj)
			if err == nil && preamble != "" {
				r.bridge.HandleMessageWithSession(context.Background(), channelID, userID, preamble, threadTS, messageTS, proj.ActiveSession)
			}
		}
	}

	// Parse intervals
	driveMs := proj.DriveIntervalMs
	if cmd.DriveInterval != "" {
		ms, err := DurationToMs(cmd.DriveInterval)
		if err != nil {
			r.respond(channelID, threadTS, messageTS, fmt.Sprintf("⚠️ Invalid drive interval: %s", cmd.DriveInterval))
			return
		}
		driveMs = ms
	}
	if driveMs <= 0 {
		driveMs = 600000 // default 10m
	}

	reportMs := proj.ReportIntervalMs
	if cmd.ReportInterval != "" {
		ms, err := DurationToMs(cmd.ReportInterval)
		if err != nil {
			r.respond(channelID, threadTS, messageTS, fmt.Sprintf("⚠️ Invalid report interval: %s", cmd.ReportInterval))
			return
		}
		reportMs = ms
	}

	phases := proj.Phases
	if cmd.Phases != "" {
		phases = ParsePhasesString(cmd.Phases)
	}

	currentPhase := proj.CurrentPhase
	if currentPhase == "" && phases != "" {
		// Set first phase
		parts := strings.Split(phases, ",")
		if len(parts) > 0 {
			currentPhase = strings.TrimSpace(parts[0])
		}
	}

	var autoDriveUntil int64
	if cmd.Duration != "" {
		dur, err := ParseDuration(cmd.Duration)
		if err != nil {
			r.respond(channelID, threadTS, messageTS, fmt.Sprintf("⚠️ Invalid duration: %s", cmd.Duration))
			return
		}
		autoDriveUntil = time.Now().Add(dur).UnixMilli()
	} else if proj.AutoDriveUntil > 0 {
		autoDriveUntil = proj.AutoDriveUntil
	}

	reportChannel := proj.ReportChannelID
	if reportChannel == "" {
		reportChannel = channelID
	}
	reportThread := proj.ReportThreadTS
	if reportThread == "" {
		if threadTS != "" {
			reportThread = threadTS
		} else {
			reportThread = messageTS
		}
	}

	// Update DB
	if err := r.store.UpdateAutoDrive(proj.ID, true, driveMs, reportMs,
		reportChannel, reportThread, currentPhase, phases, autoDriveUntil); err != nil {
		r.respond(channelID, threadTS, messageTS, fmt.Sprintf("⚠️ %s", err.Error()))
		return
	}

	// Refresh project and start driving
	proj, _ = r.store.GetProjectByID(proj.ID)
	if proj != nil {
		r.driver.StartDriving(proj)
	}

	msg := fmt.Sprintf("🔄 Auto-drive enabled for `%s`\n⏱️ Drive: every %s", cmd.Slug, FormatDurationMs(driveMs))
	if reportMs > 0 {
		msg += fmt.Sprintf(" · Report: every %s", FormatDurationMs(reportMs))
	}
	if currentPhase != "" {
		msg += fmt.Sprintf("\n📍 Phase: %s", currentPhase)
	}
	if autoDriveUntil > 0 {
		msg += fmt.Sprintf("\n⏰ Expires: %s", TimeLeftStr(autoDriveUntil))
	}
	r.respond(channelID, threadTS, messageTS, msg)

	_ = r.store.AddEvent(&ProjectEvent{
		ProjectID: proj.ID,
		EventType: "auto_drive_started",
		ActorID:   userID,
		Summary:   fmt.Sprintf("Auto-drive enabled (every %s)", FormatDurationMs(driveMs)),
	})
}

func (r *Router) handlePause(channelID, userID, threadTS, messageTS string, cmd *ProjectCommand) {
	proj, _ := r.store.GetProject(cmd.Slug)
	if proj == nil {
		r.respond(channelID, threadTS, messageTS, fmt.Sprintf("Project `%s` not found.", cmd.Slug))
		return
	}

	if r.driver != nil {
		r.driver.StopDriving(proj.ID)
	}

	_ = r.store.UpdateAutoDrive(proj.ID, false, proj.DriveIntervalMs, proj.ReportIntervalMs,
		proj.ReportChannelID, proj.ReportThreadTS, proj.CurrentPhase, proj.Phases, proj.AutoDriveUntil)

	r.respond(channelID, threadTS, messageTS, fmt.Sprintf("⏸️ Auto-drive paused for `%s`. Use `drive %s` to resume.", cmd.Slug, cmd.Slug))

	_ = r.store.AddEvent(&ProjectEvent{
		ProjectID: proj.ID,
		EventType: "auto_drive_paused",
		ActorID:   userID,
		Summary:   "Auto-drive paused",
	})
}

func (r *Router) handlePhase(channelID, userID, threadTS, messageTS string, cmd *ProjectCommand) {
	proj, _ := r.store.GetProject(cmd.Slug)
	if proj == nil {
		r.respond(channelID, threadTS, messageTS, fmt.Sprintf("Project `%s` not found.", cmd.Slug))
		return
	}

	oldPhase := proj.CurrentPhase

	if err := r.store.UpdatePhase(proj.ID, cmd.Message); err != nil {
		r.respond(channelID, threadTS, messageTS, fmt.Sprintf("⚠️ %s", err.Error()))
		return
	}

	r.respond(channelID, threadTS, messageTS, fmt.Sprintf("📍 Phase updated to `%s` for project `%s`.", cmd.Message, cmd.Slug))

	_ = r.store.AddEvent(&ProjectEvent{
		ProjectID: proj.ID,
		EventType: "phase_updated",
		ActorID:   userID,
		Summary:   fmt.Sprintf("Phase updated to %s", cmd.Message),
	})

	// Trigger phase completion report if auto-drive is active
	if proj.AutoDrive && r.driver != nil && proj.ReportChannelID != "" {
		report := fmt.Sprintf("📍 *Phase Transition: `%s` → `%s`*\nProject: `%s`\n\nProvide a summary of what was accomplished in the `%s` phase.",
			oldPhase, cmd.Message, proj.Slug, oldPhase)
		r.bridge.HandleMessageWithSession(context.Background(), proj.ReportChannelID, "auto-drive", report, proj.ReportThreadTS, "", proj.ActiveSession)
	}
}

func (r *Router) handleReport(channelID, userID, threadTS, messageTS string, cmd *ProjectCommand) {
	proj, _ := r.store.GetProject(cmd.Slug)
	if proj == nil {
		r.respond(channelID, threadTS, messageTS, fmt.Sprintf("Project `%s` not found.", cmd.Slug))
		return
	}

	// If interval provided, update report interval
	if cmd.ReportInterval != "" {
		ms, err := DurationToMs(cmd.ReportInterval)
		if err != nil {
			r.respond(channelID, threadTS, messageTS, fmt.Sprintf("⚠️ Invalid interval: %s", cmd.ReportInterval))
			return
		}

		if err := r.store.UpdateAutoDrive(proj.ID, proj.AutoDrive, proj.DriveIntervalMs, ms,
			proj.ReportChannelID, proj.ReportThreadTS, proj.CurrentPhase, proj.Phases, proj.AutoDriveUntil); err != nil {
			r.respond(channelID, threadTS, messageTS, fmt.Sprintf("⚠️ %s", err.Error()))
			return
		}

		// Restart driver with new interval if active
		if proj.AutoDrive && r.driver != nil {
			updated, _ := r.store.GetProjectByID(proj.ID)
			if updated != nil {
				r.driver.StartDriving(updated)
			}
		}

		r.respond(channelID, threadTS, messageTS, fmt.Sprintf("📊 Report interval updated to `%s` for `%s`.", FormatDurationMs(ms), cmd.Slug))
		return
	}

	// No interval → trigger immediate report
	if !proj.AutoDrive {
		r.respond(channelID, threadTS, messageTS, fmt.Sprintf("Project `%s` is not in auto-drive mode.", cmd.Slug))
		return
	}

	prompt := fmt.Sprintf(`[STATUS-REPORT] Provide a brief status update for project %s.
Format:
📊 Phase: %s
✅ Done: (what you completed)
🔨 Working: (what you're doing now)
🚧 Blockers: (any blockers)
⏭️ Next: (what's next)`, proj.Name, proj.CurrentPhase)

	replyThread := threadTS
	if replyThread == "" {
		replyThread = messageTS
	}
	r.bridge.HandleMessageWithSession(context.Background(), channelID, userID, prompt, replyThread, "", proj.ActiveSession)
}

func (r *Router) handleResume(channelID, userID, threadTS, messageTS string, cmd *ProjectCommand) {
	p, err := r.manager.ResumeProject(cmd.Slug, userID)
	if err != nil {
		r.respond(channelID, threadTS, messageTS, fmt.Sprintf("⚠️ %s", err.Error()))
		return
	}
	r.respond(channelID, threadTS, messageTS, fmt.Sprintf("▶️ Project `%s` resumed (session v%d).", p.Slug, p.SessionVersion))
}

// OnProjectContinue handles the "Continue" button callback from Slack.
func (r *Router) OnProjectContinue(channelID, threadTS, userID, slug string) {
	proj, _ := r.store.GetProject(slug)
	if proj == nil {
		r.respond(channelID, threadTS, "", fmt.Sprintf("Project `%s` not found.", slug))
		return
	}
	if proj.Status == "archived" {
		r.respond(channelID, threadTS, "", fmt.Sprintf("Project `%s` is archived. Run `resume %s` to reactivate.", slug, slug))
		return
	}
	ctx := context.Background()
	r.handleContinueProject(ctx, channelID, userID, threadTS, "", proj)
}

// OnProjectArchive handles the "Archive" button callback from Slack.
func (r *Router) OnProjectArchive(channelID, threadTS, userID, slug string) {
	if err := r.store.ArchiveProject(slug, userID); err != nil {
		r.respond(channelID, threadTS, "", fmt.Sprintf("⚠️ %s", err.Error()))
		return
	}
	r.respond(channelID, threadTS, "", fmt.Sprintf("📦 Project `%s` archived.", slug))
}

func (r *Router) handleContinueProject(ctx context.Context, channelID, userID, threadTS, messageTS string, proj *Project) {
	// Build context preamble and send to project session
	preamble, err := r.manager.BuildContextPreamble(proj)
	if err != nil {
		r.logger.Warn().Err(err).Str("slug", proj.Slug).Msg("failed to build preamble")
		preamble = fmt.Sprintf("Continuing project: %s (%s)", proj.Name, proj.Slug)
	}

	// Send the preamble as context to the project session
	r.bridge.HandleMessageWithSession(ctx, channelID, userID, preamble, threadTS, messageTS, proj.ActiveSession)

	// Bind this thread to the project if we have a threadTS
	replyThread := threadTS
	if replyThread == "" {
		replyThread = messageTS
	}
	if replyThread != "" {
		_ = r.store.BindThread(channelID, replyThread, proj.ID, proj.ActiveSession)
	}
}
