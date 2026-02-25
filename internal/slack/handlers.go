package slack

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// MessageForwarder receives inbound Slack messages and forwards them to Kog-2.
type MessageForwarder interface {
	HandleMessage(ctx context.Context, channelID, userID, text, threadTS, messageTS string)
	IsActiveThread(channelID, threadTS string) bool
}

// ApprovalHandler processes approval/denial callbacks from interactive buttons.
type ApprovalHandler interface {
	OnApproval(requestID, approverID string, approved bool)
}

// ProjectInteractionHandler processes project button callbacks.
type ProjectInteractionHandler interface {
	OnProjectContinue(channelID, threadTS, userID, slug string)
	OnProjectArchive(channelID, threadTS, userID, slug string)
}

// SessionCleanupHandler processes session cleanup button callbacks.
type SessionCleanupHandler interface {
	KeepSession(sessionKey string) error
	CloseSession(sessionKey string) error
}

// Handler processes Slack events.
// Interactive callbacks (approval buttons) are handled inline.
// Regular messages are forwarded to Kog-2 via the MessageForwarder (bridge).
type Handler struct {
	api             BotAPI
	socket          *socketmode.Client
	logger          zerolog.Logger
	middleware      *Middleware
	forwarder       MessageForwarder
	approvalHandler ApprovalHandler
	projectHandler  ProjectInteractionHandler
	cleanupHandler  SessionCleanupHandler
}

// NewHandler creates a new event handler.
func NewHandler(logger zerolog.Logger, middleware *Middleware) *Handler {
	return &Handler{
		logger:     logger.With().Str("component", "slack.handler").Logger(),
		middleware: middleware,
	}
}

// SetForwarder sets the message forwarder (bridge) for routing messages to Kog-2.
func (h *Handler) SetForwarder(f MessageForwarder) {
	h.forwarder = f
}

// SetApprovalHandler sets the handler for approval/denial callbacks.
func (h *Handler) SetApprovalHandler(ah ApprovalHandler) {
	h.approvalHandler = ah
}

// SetProjectHandler sets the handler for project button callbacks.
func (h *Handler) SetProjectHandler(ph ProjectInteractionHandler) {
	h.projectHandler = ph
}

// SetCleanupHandler sets the handler for session cleanup button callbacks.
func (h *Handler) SetCleanupHandler(ch SessionCleanupHandler) {
	h.cleanupHandler = ch
}

// SetSocket sets the Socket Mode client for acknowledging events.
func (h *Handler) SetSocket(s *socketmode.Client) {
	h.socket = s
}

// HandleEvent routes Socket Mode events to the appropriate handler.
func (h *Handler) HandleEvent(ctx context.Context, evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		h.handleEventsAPI(ctx, evt)
	case socketmode.EventTypeInteractive:
		h.handleInteraction(ctx, evt)
	default:
		h.logger.Debug().Str("type", string(evt.Type)).Msg("unhandled event type")
	}
}

// handleEventsAPI processes Events API payloads (messages, app_mention, etc.).
func (h *Handler) handleEventsAPI(ctx context.Context, evt socketmode.Event) {
	// Acknowledge the event first — Slack requires this within 3 seconds
	if h.socket != nil && evt.Request != nil {
		h.socket.Ack(*evt.Request)
	}

	eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		h.logger.Warn().Str("type", string(evt.Type)).Msg("failed to cast events_api data")
		return
	}

	switch eventsAPIEvent.Type {
	case slackevents.CallbackEvent:
		h.handleCallbackEvent(ctx, eventsAPIEvent.InnerEvent)
	}
}

func (h *Handler) handleCallbackEvent(ctx context.Context, innerEvent slackevents.EventsAPIInnerEvent) {
	switch ev := innerEvent.Data.(type) {
	case *slackevents.AppMentionEvent:
		h.logger.Info().
			Str("user", ev.User).
			Str("channel", ev.Channel).
			Str("text", ev.Text).
			Msg("app mention received")

		if h.forwarder != nil {
			h.forwarder.HandleMessage(ctx, ev.Channel, ev.User, ev.Text, ev.ThreadTimeStamp, ev.TimeStamp)
		}

	case *slackevents.MessageEvent:
		// Skip bot messages and message_changed/deleted subtypes
		if ev.User == "" || ev.SubType != "" {
			return
		}

		// Handle DMs
		if ev.ChannelType == "im" {
			h.logger.Info().
				Str("user", ev.User).
				Str("channel", ev.Channel).
				Msg("DM received")

			if h.forwarder != nil {
				h.forwarder.HandleMessage(ctx, ev.Channel, ev.User, ev.Text, ev.ThreadTimeStamp, ev.TimeStamp)
			}
			return
		}

		// Handle thread replies in active threads (no @mention needed)
		if ev.ThreadTimeStamp != "" && h.forwarder != nil && h.forwarder.IsActiveThread(ev.Channel, ev.ThreadTimeStamp) {
			h.logger.Info().
				Str("user", ev.User).
				Str("channel", ev.Channel).
				Str("thread", ev.ThreadTimeStamp).
				Msg("thread reply in active thread")

			h.forwarder.HandleMessage(ctx, ev.Channel, ev.User, ev.Text, ev.ThreadTimeStamp, ev.TimeStamp)
		}

	default:
		h.logger.Debug().
			Str("inner_type", innerEvent.Type).
			Msg("unhandled callback event type")
	}
}

func (h *Handler) handleInteraction(ctx context.Context, evt socketmode.Event) {
	// Acknowledge interactive event
	if h.socket != nil && evt.Request != nil {
		h.socket.Ack(*evt.Request)
	}

	callback, ok := evt.Data.(slack.InteractionCallback)
	if !ok {
		return
	}

	for _, action := range callback.ActionCallback.BlockActions {
		h.logger.Info().
			Str("action", action.ActionID).
			Str("user", callback.User.ID).
			Msg("interaction received")

		switch {
		case strings.HasPrefix(action.ActionID, "approve_"):
			h.handleApproval(ctx, callback, action, true)
		case strings.HasPrefix(action.ActionID, "deny_"):
			h.handleApproval(ctx, callback, action, false)
		case strings.HasPrefix(action.ActionID, "policy_approve_"):
			h.handleApproval(ctx, callback, action, true)
		case strings.HasPrefix(action.ActionID, "policy_deny_"):
			h.handleApproval(ctx, callback, action, false)
		case strings.HasPrefix(action.ActionID, "project_continue_"):
			slug := strings.TrimPrefix(action.ActionID, "project_continue_")
			if h.projectHandler != nil {
				h.projectHandler.OnProjectContinue(callback.Channel.ID, callback.Message.Timestamp, callback.User.ID, slug)
			}
		case strings.HasPrefix(action.ActionID, "project_archive_"):
			slug := strings.TrimPrefix(action.ActionID, "project_archive_")
			if h.projectHandler != nil {
				h.projectHandler.OnProjectArchive(callback.Channel.ID, callback.Message.Timestamp, callback.User.ID, slug)
			}
		case strings.HasPrefix(action.ActionID, "project_start_"):
			slug := strings.TrimPrefix(action.ActionID, "project_start_")
			if h.projectHandler != nil {
				h.projectHandler.OnProjectContinue(callback.Channel.ID, callback.Message.Timestamp, callback.User.ID, slug)
			}
		case strings.HasPrefix(action.ActionID, "session_keep_"):
			sessionKey := strings.TrimPrefix(action.ActionID, "session_keep_")
			if h.cleanupHandler != nil {
				if err := h.cleanupHandler.KeepSession(sessionKey); err != nil {
					h.logger.Warn().Err(err).Str("session", sessionKey).Msg("failed to keep session")
				}
			}
		case strings.HasPrefix(action.ActionID, "session_close_"):
			sessionKey := strings.TrimPrefix(action.ActionID, "session_close_")
			if h.cleanupHandler != nil {
				if err := h.cleanupHandler.CloseSession(sessionKey); err != nil {
					h.logger.Warn().Err(err).Str("session", sessionKey).Msg("failed to close session")
				}
			}
		}
	}
}

func (h *Handler) handleApproval(_ context.Context, callback slack.InteractionCallback, action *slack.BlockAction, approved bool) {
	status := "✅ Approved"
	if !approved {
		status = "❌ Denied"
	}

	h.logger.Info().
		Str("action_id", action.ActionID).
		Str("user", callback.User.ID).
		Bool("approved", approved).
		Msg("approval action")

	// Extract request ID from action ID (format: "approve_<requestID>" or "deny_<requestID>")
	requestID := ""
	if parts := strings.SplitN(action.ActionID, "_", 2); len(parts) == 2 {
		requestID = parts[1]
	}

	// Replace the original approval message — remove buttons, show result
	if h.api != nil {
		// Extract the original context text from the first section block
		originalText := ""
		if callback.Message.Msg.Blocks.BlockSet != nil {
			for _, block := range callback.Message.Msg.Blocks.BlockSet {
				if section, ok := block.(*slack.SectionBlock); ok && section.Text != nil {
					originalText = section.Text.Text
					break
				}
			}
		}

		updatedText := fmt.Sprintf("%s\n\n%s by <@%s>", originalText, status, callback.User.ID)

		_, _, _, _ = h.api.UpdateMessage(
			callback.Channel.ID,
			callback.Message.Timestamp,
			slack.MsgOptionText(updatedText, false),
			// No blocks = buttons removed
		)
	}

	// Trigger approval callback (grant permission + re-queue task)
	if h.approvalHandler != nil && requestID != "" {
		h.approvalHandler.OnApproval(requestID, callback.User.ID, approved)
	}
}

// SendApprovalRequest sends an interactive approval message to the supervisor channel.
func (h *Handler) SendApprovalRequest(ctx context.Context, channelID, requestID, userID, action, resource string) error {
	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("🔐 *Access Request*\n*User:* <@%s>\n*Action:* %s\n*Resource:* %s",
					userID, action, resource),
				false, false),
			nil, nil,
		),
		slack.NewActionBlock(
			"approval_actions",
			slack.NewButtonBlockElement(
				fmt.Sprintf("approve_%s", requestID),
				"approve",
				slack.NewTextBlockObject("plain_text", "✅ Approve", false, false),
			),
			slack.NewButtonBlockElement(
				fmt.Sprintf("deny_%s", requestID),
				"deny",
				slack.NewTextBlockObject("plain_text", "❌ Deny", false, false),
			),
		),
	}

	_, _, err := h.api.PostMessage(
		channelID,
		slack.MsgOptionBlocks(blocks...),
	)
	if err != nil {
		return fmt.Errorf("sending approval request: %w", err)
	}
	return nil
}
