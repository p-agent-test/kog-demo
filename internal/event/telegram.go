package event

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// TelegramSource polls the Telegram Bot API for updates and emits Events.
// Uses long-polling (getUpdates with timeout) — no webhook needed.
type TelegramSource struct {
	token   string
	baseURL string
	offset  int
	timeout int // long-poll timeout in seconds
	logger  *slog.Logger
	client  *http.Client
}

// TelegramSourceOption configures TelegramSource.
type TelegramSourceOption func(*TelegramSource)

func TelegramWithLogger(l *slog.Logger) TelegramSourceOption {
	return func(s *TelegramSource) { s.logger = l }
}

func TelegramWithPollTimeout(secs int) TelegramSourceOption {
	return func(s *TelegramSource) { s.timeout = secs }
}

// NewTelegramSource creates a Telegram polling source.
// token is the Bot API token (e.g. "12345:ABCDEF...").
func NewTelegramSource(token string, opts ...TelegramSourceOption) *TelegramSource {
	s := &TelegramSource{
		token:   token,
		baseURL: "https://api.telegram.org/bot" + token,
		timeout: 30,
		logger:  slog.Default(),
		client:  &http.Client{Timeout: 60 * time.Second},
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *TelegramSource) Name() string { return SourceTelegram }

// Subscribe starts long-polling in a goroutine. Emits TypeMessage events.
func (s *TelegramSource) Subscribe(ctx context.Context, out chan<- Event) error {
	go s.poll(ctx, out)
	return nil
}

// Ack is a no-op for Telegram (offset tracks ack implicitly).
func (s *TelegramSource) Ack(_ context.Context, _ string) error { return nil }

func (s *TelegramSource) poll(ctx context.Context, out chan<- Event) {
	for {
		if ctx.Err() != nil {
			return
		}

		updates, err := s.getUpdates(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.logger.Error("telegram getUpdates", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		for _, upd := range updates {
			if upd.Message == nil {
				s.offset = maxInt(s.offset, upd.UpdateID+1)
				continue
			}

			payload := TelegramPayload{
				ChatID:    upd.Message.Chat.ID,
				MessageID: upd.Message.MessageID,
				UserID:    upd.Message.From.ID,
				Username:  upd.Message.From.Username,
				Text:      upd.Message.Text,
			}
			if upd.Message.ReplyToMessage != nil {
				payload.ReplyTo = upd.Message.ReplyToMessage.MessageID
			}

			ev, err := NewEvent(SourceTelegram, TypeMessage, payload, map[string]string{
				"chat_id": strconv.FormatInt(payload.ChatID, 10),
				"user_id": strconv.FormatInt(payload.UserID, 10),
			})
			if err != nil {
				s.logger.Error("telegram event marshal", "err", err)
			} else {
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}

			s.offset = maxInt(s.offset, upd.UpdateID+1)
		}
	}
}

// ---- Telegram API wire types ----

type tgGetUpdatesResponse struct {
	OK     bool        `json:"ok"`
	Result []tgUpdate  `json:"result"`
}

type tgUpdate struct {
	UpdateID int        `json:"update_id"`
	Message  *tgMessage `json:"message,omitempty"`
}

type tgMessage struct {
	MessageID      int      `json:"message_id"`
	From           tgUser   `json:"from"`
	Chat           tgChat   `json:"chat"`
	Text           string   `json:"text"`
	ReplyToMessage *tgMessage `json:"reply_to_message,omitempty"`
}

type tgUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type tgChat struct {
	ID int64 `json:"id"`
}

func (s *TelegramSource) getUpdates(ctx context.Context) ([]tgUpdate, error) {
	endpoint := fmt.Sprintf("%s/getUpdates", s.baseURL)
	params := url.Values{
		"offset":  []string{strconv.Itoa(s.offset)},
		"timeout": []string{strconv.Itoa(s.timeout)},
		"allowed_updates": []string{`["message"]`},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result tgGetUpdatesResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	if !result.OK {
		return nil, fmt.Errorf("telegram api returned ok=false")
	}
	return result.Result, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
