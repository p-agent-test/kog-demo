package project

import (
	"context"
	"fmt"
	"strings"

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
	if err := r.store.ArchiveProject(cmd.Slug, userID); err != nil {
		r.respond(channelID, threadTS, messageTS, fmt.Sprintf("⚠️ %s", err.Error()))
		return
	}
	r.respond(channelID, threadTS, messageTS, fmt.Sprintf("📦 Project `%s` archived.", cmd.Slug))
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
