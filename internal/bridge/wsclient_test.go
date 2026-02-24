package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockGateway simulates the OpenClaw gateway WS protocol.
type mockGateway struct {
	t         *testing.T
	server    *httptest.Server
	upgrader  websocket.Upgrader
	token     string
	agentFunc func(params agentParams) agentResult
	chatFunc  func(conn *websocket.Conn, params chatSendParams) string // returns final text
	mu        sync.Mutex
	conns     []*websocket.Conn
}

func newMockGateway(t *testing.T, token string) *mockGateway {
	mg := &mockGateway{
		t:     t,
		token: token,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/gateway", mg.handleWS)
	mg.server = httptest.NewServer(mux)

	return mg
}

func (mg *mockGateway) url() string {
	return "ws" + strings.TrimPrefix(mg.server.URL, "http") + "/ws/gateway"
}

func (mg *mockGateway) close() {
	mg.mu.Lock()
	for _, conn := range mg.conns {
		conn.Close()
	}
	mg.mu.Unlock()
	mg.server.Close()
}

func (mg *mockGateway) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := mg.upgrader.Upgrade(w, r, nil)
	if err != nil {
		mg.t.Logf("upgrade error: %v", err)
		return
	}
	mg.mu.Lock()
	mg.conns = append(mg.conns, conn)
	mg.mu.Unlock()

	defer conn.Close()

	// Send challenge
	challenge := wsFrame{
		Type:  "event",
		Event: "connect.challenge",
	}
	challengePayload, _ := json.Marshal(challengePayload{
		Nonce: "test-nonce-123",
		TS:    time.Now().UnixMilli(),
	})
	challenge.Payload = challengePayload
	conn.WriteJSON(challenge)

	// Read and handle messages
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var frame wsFrame
		if err := json.Unmarshal(msg, &frame); err != nil {
			continue
		}

		if frame.Type != "req" {
			continue
		}

		switch frame.Method {
		case "connect":
			mg.handleConnect(conn, frame)
		case "agent":
			mg.handleAgent(conn, frame)
		case "chat.send":
			mg.handleChatSend(conn, frame)
		}
	}
}

func (mg *mockGateway) handleConnect(conn *websocket.Conn, req wsFrame) {
	var params connectParams
	json.Unmarshal(req.Params, &params)

	// Check token
	if mg.token != "" && (params.Auth == nil || params.Auth.Token != mg.token) {
		ok := false
		conn.WriteJSON(wsFrame{
			Type: "res",
			ID:   req.ID,
			OK:   &ok,
			Error: &wsError{
				Code:    "UNAUTHORIZED",
				Message: "invalid token",
			},
		})
		return
	}

	ok := true
	payload, _ := json.Marshal(map[string]interface{}{
		"type":     "hello-ok",
		"protocol": 3,
	})
	conn.WriteJSON(wsFrame{
		Type:    "res",
		ID:      req.ID,
		OK:      &ok,
		Payload: payload,
	})
}

func (mg *mockGateway) handleAgent(conn *websocket.Conn, req wsFrame) {
	var params agentParams
	json.Unmarshal(req.Params, &params)

	var result agentResult
	if mg.agentFunc != nil {
		result = mg.agentFunc(params)
	} else {
		result = agentResult{
			RunID:   "test-run-123",
			Status:  "ok",
			Summary: "completed",
		}
		result.Result.Payloads = []struct {
			Text     string  `json:"text"`
			MediaURL *string `json:"mediaUrl"`
		}{{Text: "Hello from mock gateway!"}}
	}

	ok := true
	payload, _ := json.Marshal(result)
	conn.WriteJSON(wsFrame{
		Type:    "res",
		ID:      req.ID,
		OK:      &ok,
		Payload: payload,
	})
}

func (mg *mockGateway) handleChatSend(conn *websocket.Conn, req wsFrame) {
	var params chatSendParams
	json.Unmarshal(req.Params, &params)

	// Send chat.send response with runId
	runID := "chat-run-" + strings.TrimSuffix(req.ID, "-req")
	chatResp := chatSendResult{
		RunID:      runID,
		Status:     "accepted",
		AcceptedAt: time.Now().UnixMilli(),
	}

	ok := true
	payload, _ := json.Marshal(chatResp)
	conn.WriteJSON(wsFrame{
		Type:    "res",
		ID:      req.ID,
		OK:      &ok,
		Payload: payload,
	})

	// Simulate streaming: send delta then final event
	finalText := "Chat response"
	if mg.chatFunc != nil {
		finalText = mg.chatFunc(conn, params)
	}

	// Send delta event
	deltaEvent := chatEvent{
		RunID:      runID,
		SessionKey: params.SessionKey,
		State:      "delta",
		Message: &chatMessage{
			Role: "assistant",
			Content: []chatContent{
				{Type: "text", Text: "Streaming response..."},
			},
		},
	}
	deltaPayload, _ := json.Marshal(deltaEvent)
	conn.WriteJSON(wsFrame{
		Type:    "event",
		Event:   "chat",
		Payload: deltaPayload,
	})

	// Small delay to simulate processing
	time.Sleep(10 * time.Millisecond)

	// Send final event
	finalEvent := chatEvent{
		RunID:      runID,
		SessionKey: params.SessionKey,
		State:      "final",
		Message: &chatMessage{
			Role: "assistant",
			Content: []chatContent{
				{Type: "text", Text: finalText},
			},
		},
	}
	finalPayload, _ := json.Marshal(finalEvent)
	conn.WriteJSON(wsFrame{
		Type:    "event",
		Event:   "chat",
		Payload: finalPayload,
	})
}

// helper to create a fast test config
func testWSConfig(url string) WSConfig {
	cfg := DefaultWSConfig()
	cfg.GatewayURL = url
	cfg.PingInterval = 100 * time.Millisecond
	cfg.PongTimeout = 50 * time.Millisecond
	return cfg
}

func TestWSClient_ConnectAndSend(t *testing.T) {
	gw := newMockGateway(t, "test-token")
	defer gw.close()

	logger := zerolog.Nop()
	cfg := DefaultWSConfig()
	cfg.GatewayURL = gw.url()
	cfg.Token = "test-token"

	client := NewWSClient(cfg, logger)
	ctx := context.Background()

	err := client.Connect(ctx)
	require.NoError(t, err)
	assert.True(t, client.IsConnected())

	resp, err := client.SendAgent(ctx, "test-session", "hello")
	require.NoError(t, err)
	assert.Equal(t, "test-run-123", resp.RunID)
	assert.Equal(t, "ok", resp.Status)
	require.Len(t, resp.Result.Payloads, 1)
	assert.Equal(t, "Hello from mock gateway!", resp.Result.Payloads[0].Text)

	err = client.Close()
	require.NoError(t, err)
}

func TestWSClient_InvalidToken(t *testing.T) {
	gw := newMockGateway(t, "correct-token")
	defer gw.close()

	logger := zerolog.Nop()
	cfg := DefaultWSConfig()
	cfg.GatewayURL = gw.url()
	cfg.Token = "wrong-token"

	client := NewWSClient(cfg, logger)
	err := client.Connect(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid token")
}

func TestWSClient_CustomAgentResponse(t *testing.T) {
	gw := newMockGateway(t, "")
	gw.agentFunc = func(params agentParams) agentResult {
		result := agentResult{
			RunID:   "custom-run",
			Status:  "ok",
			Summary: "custom response",
		}
		result.Result.Payloads = []struct {
			Text     string  `json:"text"`
			MediaURL *string `json:"mediaUrl"`
		}{
			{Text: "Response to: " + params.Message},
		}
		return result
	}
	defer gw.close()

	logger := zerolog.Nop()
	cfg := DefaultWSConfig()
	cfg.GatewayURL = gw.url()

	client := NewWSClient(cfg, logger)
	ctx := context.Background()

	require.NoError(t, client.Connect(ctx))

	resp, err := client.SendAgent(ctx, "s1", "test message")
	require.NoError(t, err)
	assert.Equal(t, "Response to: test message", resp.Result.Payloads[0].Text)

	client.Close()
}

func TestWSClient_NotConnected(t *testing.T) {
	logger := zerolog.Nop()
	cfg := DefaultWSConfig()
	cfg.GatewayURL = "ws://localhost:1/nonexistent"

	client := NewWSClient(cfg, logger)
	_, err := client.SendAgent(context.Background(), "s1", "hello")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not connected")
}

func TestWSClient_ConcurrentRequests(t *testing.T) {
	gw := newMockGateway(t, "")
	gw.agentFunc = func(params agentParams) agentResult {
		// Small delay to simulate processing
		time.Sleep(10 * time.Millisecond)
		result := agentResult{
			RunID:  "run-" + params.SessionID,
			Status: "ok",
		}
		result.Result.Payloads = []struct {
			Text     string  `json:"text"`
			MediaURL *string `json:"mediaUrl"`
		}{{Text: "reply-" + params.SessionID}}
		return result
	}
	defer gw.close()

	logger := zerolog.Nop()
	cfg := DefaultWSConfig()
	cfg.GatewayURL = gw.url()

	client := NewWSClient(cfg, logger)
	ctx := context.Background()
	require.NoError(t, client.Connect(ctx))

	var wg sync.WaitGroup
	results := make([]*AgentResponse, 5)
	errs := make([]error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sessionID := strings.Replace("session-X", "X", string(rune('A'+idx)), 1)
			results[idx], errs[idx] = client.SendAgent(ctx, sessionID, "msg")
		}(i)
	}

	wg.Wait()
	for i := 0; i < 5; i++ {
		assert.NoError(t, errs[i], "request %d", i)
		assert.NotNil(t, results[i], "result %d", i)
	}

	client.Close()
}

func TestWSClient_ContextCancellation(t *testing.T) {
	gw := newMockGateway(t, "")
	// Agent never responds
	gw.agentFunc = func(params agentParams) agentResult {
		time.Sleep(10 * time.Second)
		return agentResult{}
	}
	defer gw.close()

	logger := zerolog.Nop()
	cfg := DefaultWSConfig()
	cfg.GatewayURL = gw.url()

	client := NewWSClient(cfg, logger)
	require.NoError(t, client.Connect(context.Background()))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := client.SendAgent(ctx, "s1", "hello")
	assert.Error(t, err)

	client.Close()
}

func TestWSClient_SendChat(t *testing.T) {
	gw := newMockGateway(t, "")
	defer gw.close()

	logger := zerolog.Nop()
	cfg := DefaultWSConfig()
	cfg.GatewayURL = gw.url()

	client := NewWSClient(cfg, logger)
	ctx := context.Background()

	require.NoError(t, client.Connect(ctx))

	result, err := client.SendChat(ctx, "test-session", "hello")
	require.NoError(t, err)
	assert.NotEmpty(t, result)

	client.Close()
}

func TestWSClient_SendChatStream(t *testing.T) {
	gw := newMockGateway(t, "")
	gw.chatFunc = func(conn *websocket.Conn, params chatSendParams) string {
		return "Streamed: " + params.Message
	}
	defer gw.close()

	logger := zerolog.Nop()
	cfg := DefaultWSConfig()
	cfg.GatewayURL = gw.url()

	client := NewWSClient(cfg, logger)
	ctx := context.Background()

	require.NoError(t, client.Connect(ctx))

	var mu sync.Mutex
	var deltaTexts []string
	var finalText string
	var finalCalled bool

	callback := func(text string, isFinal bool) {
		mu.Lock()
		defer mu.Unlock()
		if isFinal {
			finalCalled = true
			finalText = text
		} else {
			deltaTexts = append(deltaTexts, text)
		}
	}

	result, err := client.SendChatStream(ctx, "test-session", "test message", callback)
	require.NoError(t, err)
	assert.NotEmpty(t, result)

	mu.Lock()
	assert.True(t, finalCalled, "final callback should have been called")
	assert.Equal(t, finalText, result)
	mu.Unlock()

	client.Close()
}

func TestWSClient_SendChatRaceCondition(t *testing.T) {
	gw := newMockGateway(t, "")
	gw.chatFunc = func(conn *websocket.Conn, params chatSendParams) string {
		return "Response for: " + params.Message
	}
	defer gw.close()

	logger := zerolog.Nop()
	cfg := DefaultWSConfig()
	cfg.GatewayURL = gw.url()

	client := NewWSClient(cfg, logger)
	ctx := context.Background()

	require.NoError(t, client.Connect(ctx))

	// Run multiple concurrent chat sends to increase chance of race condition
	var wg sync.WaitGroup
	results := make([]string, 5)
	errs := make([]error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			msg := strings.Replace("message-X", "X", string(rune('A'+idx)), 1)
			results[idx], errs[idx] = client.SendChat(ctx, "session", msg)
		}(i)
	}

	wg.Wait()
	for i := 0; i < 5; i++ {
		assert.NoError(t, errs[i], "request %d", i)
		assert.NotEmpty(t, results[i], "result %d", i)
	}

	client.Close()
}

// --- New tests for bulletproof features ---

func TestWSClient_PingPongKeepalive(t *testing.T) {
	// Use a mock gateway that responds to pings (gorilla/websocket does this automatically)
	gw := newMockGateway(t, "")
	defer gw.close()

	logger := zerolog.Nop()
	cfg := testWSConfig(gw.url())

	client := NewWSClient(cfg, logger)
	ctx := context.Background()

	require.NoError(t, client.Connect(ctx))
	assert.True(t, client.IsConnected())

	// Wait for a few ping cycles — connection should stay alive
	time.Sleep(350 * time.Millisecond)
	assert.True(t, client.IsConnected(), "connection should survive ping/pong cycles")

	// Verify we can still send messages after pings
	resp, err := client.SendAgent(ctx, "s1", "after-ping")
	require.NoError(t, err)
	assert.NotNil(t, resp)

	client.Close()
}

func TestWSClient_ConcurrentWrites(t *testing.T) {
	// This test verifies that concurrent writes don't cause a race condition
	// Run with -race to detect
	gw := newMockGateway(t, "")
	gw.agentFunc = func(params agentParams) agentResult {
		result := agentResult{RunID: "r1", Status: "ok"}
		result.Result.Payloads = []struct {
			Text     string  `json:"text"`
			MediaURL *string `json:"mediaUrl"`
		}{{Text: "ok"}}
		return result
	}
	defer gw.close()

	logger := zerolog.Nop()
	cfg := testWSConfig(gw.url())

	client := NewWSClient(cfg, logger)
	ctx := context.Background()
	require.NoError(t, client.Connect(ctx))

	// Hammer concurrent writes while ping loop is also writing
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = client.SendAgent(ctx, "s", "msg")
		}()
	}
	wg.Wait()

	client.Close()
}

func TestWSClient_ReconnectPreservesListeners(t *testing.T) {
	// Scenario: client sends chat.send, gets response + delta on conn1,
	// conn1 dies, client reconnects, server sends final on conn2.
	// The chat listener should survive the reconnect and receive the final.

	runID := "run-preserved"
	var gatewayMu sync.Mutex
	connCount := 0
	// Channel to signal server to send final on conn2
	sendFinalCh := make(chan *websocket.Conn, 1)

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		gatewayMu.Lock()
		connCount++
		myConnNum := connCount
		gatewayMu.Unlock()

		// Send challenge
		cp, _ := json.Marshal(challengePayload{Nonce: "n", TS: time.Now().UnixMilli()})
		conn.WriteJSON(wsFrame{Type: "event", Event: "connect.challenge", Payload: cp})

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var frame wsFrame
			if err := json.Unmarshal(msg, &frame); err != nil {
				continue
			}
			if frame.Type != "req" {
				continue
			}

			switch frame.Method {
			case "connect":
				ok := true
				p, _ := json.Marshal(map[string]interface{}{"protocol": 3})
				conn.WriteJSON(wsFrame{Type: "res", ID: frame.ID, OK: &ok, Payload: p})

				if myConnNum == 2 {
					// Second connection established — send final event for preserved listener
					sendFinalCh <- conn
				}

			case "chat.send":
				ok := true
				p, _ := json.Marshal(chatSendResult{RunID: runID, Status: "accepted"})
				conn.WriteJSON(wsFrame{Type: "res", ID: frame.ID, OK: &ok, Payload: p})

				// First connection: send delta then close
				de := chatEvent{RunID: runID, State: "delta", Message: &chatMessage{
					Role:    "assistant",
					Content: []chatContent{{Type: "text", Text: "partial..."}},
				}}
				dp, _ := json.Marshal(de)
				conn.WriteJSON(wsFrame{Type: "event", Event: "chat", Payload: dp})
				time.Sleep(20 * time.Millisecond)
				conn.Close()
				return
			}
		}
	}))
	defer server.Close()

	// Goroutine to send final on second connection
	go func() {
		conn := <-sendFinalCh
		time.Sleep(50 * time.Millisecond) // let readLoop start
		fe := chatEvent{RunID: runID, State: "final", Message: &chatMessage{
			Role:    "assistant",
			Content: []chatContent{{Type: "text", Text: "final after reconnect"}},
		}}
		fp, _ := json.Marshal(fe)
		conn.WriteJSON(wsFrame{Type: "event", Event: "chat", Payload: fp})
	}()

	logger := zerolog.Nop()
	cfg := DefaultWSConfig()
	cfg.GatewayURL = "ws" + strings.TrimPrefix(server.URL, "http")
	cfg.ReconnectInterval = 50 * time.Millisecond
	cfg.PingInterval = 5 * time.Second
	cfg.PongTimeout = 5 * time.Second

	client := NewWSClient(cfg, logger)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, client.Connect(ctx))

	result, err := client.SendChat(ctx, "sess", "test")
	require.NoError(t, err)
	assert.Equal(t, "final after reconnect", result)

	client.Close()
}

func TestWSClient_ChatListenerPreRegistration(t *testing.T) {
	// Test that events arriving immediately after response don't get dropped
	// The mock gateway sends events RIGHT AFTER the chat.send response (no delay)
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		cp, _ := json.Marshal(challengePayload{Nonce: "n", TS: time.Now().UnixMilli()})
		conn.WriteJSON(wsFrame{Type: "event", Event: "connect.challenge", Payload: cp})

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var frame wsFrame
			json.Unmarshal(msg, &frame)
			if frame.Type != "req" {
				continue
			}

			switch frame.Method {
			case "connect":
				ok := true
				p, _ := json.Marshal(map[string]interface{}{"protocol": 3})
				conn.WriteJSON(wsFrame{Type: "res", ID: frame.ID, OK: &ok, Payload: p})

			case "chat.send":
				runID := "fast-run"
				ok := true
				p, _ := json.Marshal(chatSendResult{RunID: runID, Status: "accepted"})
				// Send response, then IMMEDIATELY send events (no sleep)
				conn.WriteJSON(wsFrame{Type: "res", ID: frame.ID, OK: &ok, Payload: p})

				fe := chatEvent{RunID: runID, State: "final", Message: &chatMessage{
					Role:    "assistant",
					Content: []chatContent{{Type: "text", Text: "instant response"}},
				}}
				fp, _ := json.Marshal(fe)
				conn.WriteJSON(wsFrame{Type: "event", Event: "chat", Payload: fp})
			}
		}
	}))
	defer server.Close()

	logger := zerolog.Nop()
	cfg := DefaultWSConfig()
	cfg.GatewayURL = "ws" + strings.TrimPrefix(server.URL, "http")
	cfg.PingInterval = 5 * time.Second
	cfg.PongTimeout = 5 * time.Second

	// Run multiple times to increase chance of hitting the race
	for i := 0; i < 10; i++ {
		client := NewWSClient(cfg, logger)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

		require.NoError(t, client.Connect(ctx))

		result, err := client.SendChat(ctx, "sess", "fast")
		assert.NoError(t, err, "iteration %d", i)
		assert.Equal(t, "instant response", result, "iteration %d", i)

		client.Close()
		cancel()
	}
}

func TestWSClient_DefaultTimeouts(t *testing.T) {
	cfg := DefaultWSConfig()
	assert.Equal(t, 300*time.Second, cfg.DefaultTimeout)
	assert.Equal(t, 10*time.Minute, cfg.ChatTimeout)
	assert.Equal(t, 30*time.Second, cfg.PingInterval)
	assert.Equal(t, 10*time.Second, cfg.PongTimeout)
}
