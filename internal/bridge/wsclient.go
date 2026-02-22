// Package bridge provides WebSocket and CLI bridges to OpenClaw gateway.
// WSClient implements persistent WebSocket communication with the OpenClaw
// gateway protocol v3 (challenge-response, token auth, agent turns).
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

// WSConfig holds WebSocket bridge configuration.
type WSConfig struct {
	// GatewayURL is the WebSocket URL, e.g. "ws://localhost:18789/ws/gateway"
	GatewayURL string

	// Token is the gateway auth token (from openclaw.json gateway.auth.token).
	Token string

	// ClientID identifies this client. Must be a known gateway client ID.
	// Default: "gateway-client"
	ClientID string

	// Scopes requested from the gateway.
	// Default: ["operator.admin"]
	Scopes []string

	// AgentTimeout is the max wait for an agent response.
	DefaultTimeout time.Duration

	// ReconnectInterval is the delay between reconnection attempts.
	ReconnectInterval time.Duration

	// MaxReconnectInterval caps the exponential backoff.
	MaxReconnectInterval time.Duration
}

// DefaultWSConfig returns sane defaults.
func DefaultWSConfig() WSConfig {
	return WSConfig{
		GatewayURL:           "ws://localhost:18789/ws/gateway",
		ClientID:             "gateway-client",
		Scopes:               []string{"operator.admin"},
		DefaultTimeout:       120 * time.Second,
		ReconnectInterval:    1 * time.Second,
		MaxReconnectInterval: 30 * time.Second,
	}
}

// --- Protocol frames ---

// wsFrame is a raw protocol frame.
type wsFrame struct {
	Type    string          `json:"type"`              // "req", "res", "event"
	ID      string          `json:"id,omitempty"`      // request/response ID
	Method  string          `json:"method,omitempty"`  // request method
	Params  json.RawMessage `json:"params,omitempty"`  // request params
	OK      *bool           `json:"ok,omitempty"`      // response ok
	Payload json.RawMessage `json:"payload,omitempty"` // response/event payload
	Event   string          `json:"event,omitempty"`   // event name
	Error   *wsError        `json:"error,omitempty"`   // response error
}

type wsError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// challengePayload is the connect.challenge event payload.
type challengePayload struct {
	Nonce string `json:"nonce"`
	TS    int64  `json:"ts"`
}

// connectParams is sent as the "connect" request.
type connectParams struct {
	MinProtocol int            `json:"minProtocol"`
	MaxProtocol int            `json:"maxProtocol"`
	Client      connectClient  `json:"client"`
	Auth        *connectAuth   `json:"auth,omitempty"`
	Role        string         `json:"role"`
	Scopes      []string       `json:"scopes"`
	Caps        []string       `json:"caps"`
}

type connectClient struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Mode     string `json:"mode"`
}

type connectAuth struct {
	Token string `json:"token,omitempty"`
}

// agentParams is the "agent" request params.
type agentParams struct {
	Message   string `json:"message"`
	SessionID string `json:"sessionId,omitempty"`
	Timeout   int    `json:"timeout,omitempty"`
}

// agentResult is the "agent" response payload.
type agentResult struct {
	RunID   string `json:"runId"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
	Result  struct {
		Payloads []struct {
			Text     string  `json:"text"`
			MediaURL *string `json:"mediaUrl"`
		} `json:"payloads"`
	} `json:"result"`
}

// --- WSClient ---

// WSClient is a persistent WebSocket client for the OpenClaw gateway.
type WSClient struct {
	cfg    WSConfig
	logger zerolog.Logger

	mu        sync.Mutex
	conn      *websocket.Conn
	connected bool
	pending   map[string]chan wsFrame // request ID → response channel
	stopCh    chan struct{}
	done      chan struct{}
}

// NewWSClient creates a new WebSocket client.
func NewWSClient(cfg WSConfig, logger zerolog.Logger) *WSClient {
	if cfg.ClientID == "" {
		cfg.ClientID = "gateway-client"
	}
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = 120 * time.Second
	}
	if cfg.ReconnectInterval == 0 {
		cfg.ReconnectInterval = 1 * time.Second
	}
	if cfg.MaxReconnectInterval == 0 {
		cfg.MaxReconnectInterval = 30 * time.Second
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"operator.admin"}
	}

	return &WSClient{
		cfg:     cfg,
		logger:  logger.With().Str("component", "ws-bridge").Logger(),
		pending: make(map[string]chan wsFrame),
		stopCh:  make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Connect establishes the WebSocket connection and completes the handshake.
func (c *WSClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.connected {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	c.logger.Info().Str("url", c.cfg.GatewayURL).Msg("connecting to gateway")

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, c.cfg.GatewayURL, nil)
	if err != nil {
		return fmt.Errorf("ws dial failed: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	// Wait for connect.challenge
	if err := c.handleChallenge(ctx); err != nil {
		conn.Close()
		return fmt.Errorf("challenge handshake failed: %w", err)
	}

	// Start read loop
	go c.readLoop()

	c.logger.Info().Msg("connected to gateway")
	return nil
}

// handleChallenge reads the challenge event and sends the connect request.
func (c *WSClient) handleChallenge(ctx context.Context) error {
	// Read challenge frame
	c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, msg, err := c.conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("reading challenge: %w", err)
	}
	c.conn.SetReadDeadline(time.Time{})

	var frame wsFrame
	if err := json.Unmarshal(msg, &frame); err != nil {
		return fmt.Errorf("parsing challenge: %w", err)
	}

	if frame.Type != "event" || frame.Event != "connect.challenge" {
		return fmt.Errorf("expected connect.challenge, got %s/%s", frame.Type, frame.Event)
	}

	c.logger.Debug().Msg("received connect.challenge")

	// Send connect request
	params := connectParams{
		MinProtocol: 3,
		MaxProtocol: 3,
		Client: connectClient{
			ID:       c.cfg.ClientID,
			Version:  "platform-agent/1.0",
			Platform: "linux",
			Mode:     "backend",
		},
		Role:   "operator",
		Scopes: c.cfg.Scopes,
		Caps:   []string{},
	}

	if c.cfg.Token != "" {
		params.Auth = &connectAuth{Token: c.cfg.Token}
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshaling connect params: %w", err)
	}

	reqID := uuid.New().String()
	req := wsFrame{
		Type:   "req",
		ID:     reqID,
		Method: "connect",
		Params: paramsJSON,
	}

	// Create response channel before sending
	respCh := make(chan wsFrame, 1)
	c.mu.Lock()
	c.pending[reqID] = respCh
	c.mu.Unlock()

	reqBytes, _ := json.Marshal(req)
	if err := c.conn.WriteMessage(websocket.TextMessage, reqBytes); err != nil {
		return fmt.Errorf("sending connect: %w", err)
	}

	// Read response (may come with other events, so read in loop)
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			return fmt.Errorf("connect response timeout")
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("reading connect response: %w", err)
		}
		c.conn.SetReadDeadline(time.Time{})

		var resp wsFrame
		if err := json.Unmarshal(msg, &resp); err != nil {
			continue
		}

		// Skip events during handshake
		if resp.Type == "event" {
			continue
		}

		if resp.Type == "res" && resp.ID == reqID {
			if resp.OK != nil && *resp.OK {
				c.mu.Lock()
				c.connected = true
				delete(c.pending, reqID)
				c.mu.Unlock()
				return nil
			}
			errMsg := "unknown error"
			if resp.Error != nil {
				errMsg = resp.Error.Message
			}
			return fmt.Errorf("connect rejected: %s", errMsg)
		}
	}
}

// readLoop reads messages from the WebSocket and dispatches responses.
func (c *WSClient) readLoop() {
	defer func() {
		c.mu.Lock()
		c.connected = false
		// Fail all pending requests
		for id, ch := range c.pending {
			ch <- wsFrame{
				Type:  "res",
				ID:    id,
				Error: &wsError{Code: "DISCONNECTED", Message: "connection lost"},
			}
			delete(c.pending, id)
		}
		c.mu.Unlock()
		close(c.done)
	}()

	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			if !errors.Is(err, websocket.ErrCloseSent) {
				c.logger.Warn().Err(err).Msg("ws read error")
			}
			return
		}

		var frame wsFrame
		if err := json.Unmarshal(msg, &frame); err != nil {
			c.logger.Warn().Err(err).Msg("ws parse error")
			continue
		}

		switch frame.Type {
		case "res":
			c.mu.Lock()
			ch, ok := c.pending[frame.ID]
			if ok {
				delete(c.pending, frame.ID)
			}
			c.mu.Unlock()
			if ok {
				ch <- frame
			}
		case "event":
			// Handle tick/health/etc silently
			c.logger.Trace().Str("event", frame.Event).Msg("event received")
		}
	}
}

// SendAgent sends a message to the agent and waits for a response.
func (c *WSClient) SendAgent(ctx context.Context, sessionID, message string) (*AgentResponse, error) {
	c.mu.Lock()
	if !c.connected || c.conn == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("not connected to gateway")
	}
	c.mu.Unlock()

	timeout := c.cfg.DefaultTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}

	params := agentParams{
		Message:   message,
		SessionID: sessionID,
		Timeout:   int(timeout.Seconds()),
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshaling agent params: %w", err)
	}

	reqID := uuid.New().String()
	req := wsFrame{
		Type:   "req",
		ID:     reqID,
		Method: "agent",
		Params: paramsJSON,
	}

	respCh := make(chan wsFrame, 1)
	c.mu.Lock()
	c.pending[reqID] = respCh
	c.mu.Unlock()

	reqBytes, _ := json.Marshal(req)

	c.mu.Lock()
	err = c.conn.WriteMessage(websocket.TextMessage, reqBytes)
	c.mu.Unlock()

	if err != nil {
		c.mu.Lock()
		delete(c.pending, reqID)
		c.mu.Unlock()
		return nil, fmt.Errorf("sending agent request: %w", err)
	}

	c.logger.Debug().
		Str("session", sessionID).
		Str("reqId", reqID).
		Msg("agent request sent")

	// Wait for response
	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return nil, fmt.Errorf("agent error: %s", resp.Error.Message)
		}
		if resp.OK == nil || !*resp.OK {
			return nil, fmt.Errorf("agent request failed")
		}

		var result agentResult
		if err := json.Unmarshal(resp.Payload, &result); err != nil {
			return nil, fmt.Errorf("parsing agent response: %w", err)
		}

		// Convert to AgentResponse (same struct as CLI bridge)
		agentResp := &AgentResponse{
			RunID:   result.RunID,
			Status:  result.Status,
			Summary: result.Summary,
		}
		for _, p := range result.Result.Payloads {
			agentResp.Result.Payloads = append(agentResp.Result.Payloads, struct {
				Text     string  `json:"text"`
				MediaURL *string `json:"mediaUrl"`
			}{Text: p.Text, MediaURL: p.MediaURL})
		}

		c.logger.Info().
			Str("session", sessionID).
			Str("runId", result.RunID).
			Int("payloads", len(result.Result.Payloads)).
			Msg("agent response received")

		return agentResp, nil

	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, reqID)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Close gracefully shuts down the WebSocket connection.
func (c *WSClient) Close() error {
	close(c.stopCh)

	c.mu.Lock()
	conn := c.conn
	c.connected = false
	c.mu.Unlock()

	if conn != nil {
		// Send close frame
		_ = conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		return conn.Close()
	}
	return nil
}

// IsConnected returns true if the client is connected.
func (c *WSClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}
