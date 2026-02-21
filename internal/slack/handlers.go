package slack

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// Handler processes Slack events.
// In the new architecture, the handler only processes interactive callbacks
// (button clicks for approvals). Message interpretation is handled by Kog-2
// via the Management API.
type Handler struct {
	api        BotAPI
	logger     zerolog.Logger
	middleware *Middleware
}

// NewHandler creates a new event handler.
func NewHandler(logger zerolog.Logger, middleware *Middleware) *Handler {
	return &Handler{
		logger:     logger.With().Str("component", "slack.handler").Logger(),
		middleware: middleware,
	}
}

// HandleEvent routes Socket Mode events to the appropriate handler.
// Only interactive callbacks (button clicks) are processed.
func (h *Handler) HandleEvent(ctx context.Context, evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeInteractive:
		h.handleInteraction(ctx, evt)
	default:
		h.logger.Debug().Str("type", string(evt.Type)).Msg("unhandled event type (messages handled by Kog-2)")
	}
}

func (h *Handler) handleInteraction(ctx context.Context, evt socketmode.Event) {
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

	if h.api == nil {
		return
	}

	_, _, _ = h.api.PostMessage(
		callback.Channel.ID,
		slack.MsgOptionText(
			fmt.Sprintf("%s by <@%s>", status, callback.User.ID),
			false,
		),
		slack.MsgOptionTS(callback.MessageTs),
	)
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
