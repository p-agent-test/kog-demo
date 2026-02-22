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
