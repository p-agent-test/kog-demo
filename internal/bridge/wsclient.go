// Package bridge provides WebSocket and CLI bridges to OpenClaw gateway.
// WSClient implements persistent WebSocket communication with the OpenClaw
// gateway protocol v3 (challenge-response, token auth, agent turns).
package bridge

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
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

	// Token is the gateway auth token. For device auth, this is the device token.
	Token string

	// DeviceID is the device identifier (hex, 64 chars). Required for device auth.
	DeviceID string

	// PublicKey is the Ed25519 public key in base64url format (raw 32 bytes, no padding).
	PublicKey string

	// PrivateKeyPEM is the Ed25519 private key in PEM format. Required for signing.
	PrivateKeyPEM string

	// ClientID identifies this client. Must be a known gateway client ID.
	// Default: "gateway-client"
	ClientID string

	// Scopes requested from the gateway.
	// Default: ["operator.admin", "operator.write", "operator.read"]
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
	MinProtocol int             `json:"minProtocol"`
	MaxProtocol int             `json:"maxProtocol"`
	Client      connectClient   `json:"client"`
	Auth        *connectAuth    `json:"auth,omitempty"`
	Device      *connectDevice  `json:"device,omitempty"`
	Role        string          `json:"role"`
	Scopes      []string        `json:"scopes"`
	Caps        []string        `json:"caps"`
	UserAgent   string          `json:"userAgent,omitempty"`
	Locale      string          `json:"locale,omitempty"`
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

type connectDevice struct {
	ID        string `json:"id"`
	PublicKey string `json:"publicKey"`
	Signature string `json:"signature"`
	SignedAt  int64  `json:"signedAt"`
	Nonce     string `json:"nonce,omitempty"`
}

// chatSendParams is the "chat.send" request params.
type chatSendParams struct {
	SessionKey     string `json:"sessionKey"`
	Message        string `json:"message"`
	Deliver        bool   `json:"deliver"`
	IdempotencyKey string `json:"idempotencyKey"`
}

// chatSendResult is the "chat.send" response payload.
type chatSendResult struct {
	RunID      string `json:"runId"`
	Status     string `json:"status"`
	AcceptedAt int64  `json:"acceptedAt"`
}

// chatEvent is a chat stream event payload.
type chatEvent struct {
	RunID      string       `json:"runId"`
	SessionKey string       `json:"sessionKey"`
	State      string       `json:"state"` // "delta", "final", "error", "aborted"
	Message    *chatMessage `json:"message,omitempty"`
	Error      string       `json:"errorMessage,omitempty"`
}

type chatMessage struct {
	Role    string        `json:"role"`
	Content []chatContent `json:"content"`
}

type chatContent struct {
	Type string `json:"type"` // "text", "tool_use", etc.
	Text string `json:"text,omitempty"`
}

// agentParams is the legacy "agent" request params (kept for compatibility).
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

	mu            sync.Mutex
	conn          *websocket.Conn
	connected     bool
	pending       map[string]chan wsFrame     // request ID → response channel
	chatListeners map[string]chan chatEvent   // runID → chat event channel
	stopCh        chan struct{}
	done          chan struct{}
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

	var challenge challengePayload
	if err := json.Unmarshal(frame.Payload, &challenge); err != nil {
		return fmt.Errorf("parsing challenge payload: %w", err)
	}

	noncePreview := challenge.Nonce
	if len(noncePreview) > 16 {
		noncePreview = noncePreview[:16] + "..."
	}
	c.logger.Debug().Str("nonce", noncePreview).Msg("received connect.challenge")

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
		Role:      "operator",
		Scopes:    c.cfg.Scopes,
		Caps:      []string{},
		UserAgent: "platform-agent-bridge/1.0",
		Locale:    "en",
	}

	if c.cfg.Token != "" {
		params.Auth = &connectAuth{Token: c.cfg.Token}
	}

	// Device auth: Ed25519 signing
	if c.cfg.DeviceID != "" && c.cfg.PrivateKeyPEM != "" {
		signedAt := time.Now().UnixMilli()
		device, err := c.buildDeviceAuth(challenge.Nonce, signedAt)
		if err != nil {
			return fmt.Errorf("device auth: %w", err)
		}
		params.Device = device
		c.logger.Debug().Msg("device auth signature attached")
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

// buildDeviceAuth creates the device auth payload with Ed25519 signature.
// Sign payload format (v2): "v2|deviceId|clientId|clientMode|role|scopes|signedAtMs|token|nonce"
func (c *WSClient) buildDeviceAuth(nonce string, signedAtMs int64) (*connectDevice, error) {
	// Parse PEM private key
	block, _ := pem.Decode([]byte(c.cfg.PrivateKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM private key")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing private key: %w", err)
	}

	edKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not Ed25519")
	}

	// Build v2 sign payload
	scopesStr := strings.Join(c.cfg.Scopes, ",")
	token := c.cfg.Token
	payload := fmt.Sprintf("v2|%s|%s|backend|operator|%s|%d|%s|%s",
		c.cfg.DeviceID, c.cfg.ClientID, scopesStr, signedAtMs, token, nonce)

	// Sign with Ed25519
	sig, err := edKey.Sign(nil, []byte(payload), crypto.Hash(0))
	if err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}

	// Encode signature as base64url (no padding) — gateway expects this format
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return &connectDevice{
		ID:        c.cfg.DeviceID,
		PublicKey: c.cfg.PublicKey,
		Signature: sigB64,
		SignedAt:  signedAtMs,
		Nonce:     nonce,
	}, nil
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
			if frame.Event == "chat" {
				var evt chatEvent
				if err := json.Unmarshal(frame.Payload, &evt); err == nil {
					c.mu.Lock()
					ch, ok := c.chatListeners[evt.RunID]
					c.mu.Unlock()
					if ok {
						select {
						case ch <- evt:
						default:
							c.logger.Warn().Str("runId", evt.RunID).Msg("chat listener full, dropping event")
						}
					}
				}
			}
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

// SendChat sends a message via chat.send and streams the response via chat events.
// Returns the final response text when the chat turn completes.
func (c *WSClient) SendChat(ctx context.Context, sessionKey, message string) (string, error) {
	c.mu.Lock()
	if !c.connected || c.conn == nil {
		c.mu.Unlock()
		return "", fmt.Errorf("not connected to gateway")
	}
	c.mu.Unlock()

	idempotencyKey := uuid.New().String()
	params := chatSendParams{
		SessionKey:     sessionKey,
		Message:        message,
		Deliver:        false,
		IdempotencyKey: idempotencyKey,
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("marshaling chat params: %w", err)
	}

	reqID := uuid.New().String()
	req := wsFrame{
		Type:   "req",
		ID:     reqID,
		Method: "chat.send",
		Params: paramsJSON,
	}

	// Register for response
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
		return "", fmt.Errorf("sending chat.send: %w", err)
	}

	c.logger.Debug().
		Str("session", sessionKey).
		Str("reqId", reqID).
		Msg("chat.send request sent")

	// Wait for accepted response
	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return "", fmt.Errorf("chat.send error: %s", resp.Error.Message)
		}
		var chatResp chatSendResult
		if err := json.Unmarshal(resp.Payload, &chatResp); err != nil {
			return "", fmt.Errorf("parsing chat.send response: %w", err)
		}
		c.logger.Debug().Str("runId", chatResp.RunID).Msg("chat.send accepted")

		// Now wait for the final chat event with this runId
		return c.waitForChatFinal(ctx, sessionKey, chatResp.RunID)

	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, reqID)
		c.mu.Unlock()
		return "", ctx.Err()
	}
}

// waitForChatFinal waits for a chat event with state="final" for the given runId.
// Chat events are delivered via the readLoop's event handling.
func (c *WSClient) waitForChatFinal(ctx context.Context, sessionKey, runID string) (string, error) {
	// Register a chat event listener
	eventCh := make(chan chatEvent, 32)

	c.mu.Lock()
	if c.chatListeners == nil {
		c.chatListeners = make(map[string]chan chatEvent)
	}
	c.chatListeners[runID] = eventCh
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.chatListeners, runID)
		c.mu.Unlock()
	}()

	var lastDelta string
	for {
		select {
		case evt := <-eventCh:
			switch evt.State {
			case "delta":
				// Extract text from message content
				if evt.Message != nil {
					for _, c := range evt.Message.Content {
						if c.Type == "text" && c.Text != "" {
							lastDelta = c.Text
						}
					}
				}
			case "final":
				if evt.Message != nil {
					var texts []string
					for _, c := range evt.Message.Content {
						if c.Type == "text" && c.Text != "" {
							texts = append(texts, c.Text)
						}
					}
					if len(texts) > 0 {
						return strings.Join(texts, "\n"), nil
					}
				}
				if lastDelta != "" {
					return lastDelta, nil
				}
				return "", nil
			case "error":
				return "", fmt.Errorf("chat error: %s", evt.Error)
			case "aborted":
				if lastDelta != "" {
					return lastDelta, nil
				}
				return "", fmt.Errorf("chat aborted")
			}
		case <-ctx.Done():
			return "", ctx.Err()
		}
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
