package bridge

import (
	"testing"

	"github.com/rs/zerolog"
)

// TestWSClient_IsConnected verifies the IsConnected() helper.
func TestWSClient_IsConnected(t *testing.T) {
	cfg := DefaultWSConfig()
	logger := zerolog.New(nil)
	ws := NewWSClient(cfg, logger)

	// Initially not connected
	if ws.IsConnected() {
		t.Error("expected not connected initially")
	}

	// Set connected
	ws.connected.Store(true)
	if !ws.IsConnected() {
		t.Error("expected connected after Store(true)")
	}

	// Set not connected
	ws.connected.Store(false)
	if ws.IsConnected() {
		t.Error("expected not connected after Store(false)")
	}
}

// TestWSClient_Close_StopsReconnect verifies that Close() stops the reconnect loop
// by checking the closed flag and stopReconnect channel.
func TestWSClient_Close_StopsReconnect(t *testing.T) {
	cfg := DefaultWSConfig()
	logger := zerolog.New(nil)
	ws := NewWSClient(cfg, logger)

	// Verify initial state
	if ws.closed.Load() {
		t.Error("closed flag should be false initially")
	}

	// Call Close()
	err := ws.Close()
	if err != nil && err.Error() != "use of closed network connection" {
		// Close might fail if connection is already closed, that's ok
		_ = err
	}

	// Verify closed flag is set
	if !ws.closed.Load() {
		t.Error("closed flag should be true after Close()")
	}

	// Verify stopReconnect was closed (can receive from it)
	select {
	case <-ws.stopReconnect:
		// Good, it was closed by Close()
	default:
		t.Error("stopReconnect channel should be closed by Close()")
	}

	// stopCh should also be closed
	select {
	case <-ws.stopCh:
		// Good, it was closed
	default:
		t.Error("stopCh should be closed by Close()")
	}
}

// TestWSClient_ReconnectingCAS verifies the CAS (compare-and-swap) logic
// for the reconnecting flag prevents concurrent reconnect attempts.
func TestWSClient_ReconnectingCAS(t *testing.T) {
	cfg := DefaultWSConfig()
	logger := zerolog.New(nil)
	ws := NewWSClient(cfg, logger)

	// Initially not reconnecting
	if ws.reconnecting.Load() {
		t.Error("reconnecting flag should be false initially")
	}

	// Test CAS: should succeed the first time
	ok := ws.reconnecting.CompareAndSwap(false, true)
	if !ok {
		t.Error("CompareAndSwap should succeed when value is false")
	}

	// Should be true now
	if !ws.reconnecting.Load() {
		t.Error("reconnecting flag should be true after CAS")
	}

	// CAS should fail the second time
	ok = ws.reconnecting.CompareAndSwap(false, true)
	if ok {
		t.Error("CompareAndSwap should fail when value is true")
	}

	// Reset
	ws.reconnecting.Store(false)
	if ws.reconnecting.Load() {
		t.Error("reconnecting flag should be false after Store(false)")
	}
}

// TestWSClient_AtomicFields verifies all atomic fields are properly initialized.
func TestWSClient_AtomicFields(t *testing.T) {
	cfg := DefaultWSConfig()
	logger := zerolog.New(nil)
	ws := NewWSClient(cfg, logger)

	// connected should be false
	if ws.connected.Load() {
		t.Error("connected should be false initially")
	}

	// reconnecting should be false
	if ws.reconnecting.Load() {
		t.Error("reconnecting should be false initially")
	}

	// closed should be false
	if ws.closed.Load() {
		t.Error("closed should be false initially")
	}

	// Verify we can store to them without panicking
	ws.connected.Store(true)
	ws.reconnecting.Store(true)
	ws.closed.Store(true)

	if !ws.connected.Load() || !ws.reconnecting.Load() || !ws.closed.Load() {
		t.Error("atomic fields should reflect stored values")
	}
}

// TestWSClient_StopReconnectChannel verifies the stopReconnect channel
// is initialized and can be used for signaling.
func TestWSClient_StopReconnectChannel(t *testing.T) {
	cfg := DefaultWSConfig()
	logger := zerolog.New(nil)
	ws := NewWSClient(cfg, logger)

	// Channel should not be nil
	if ws.stopReconnect == nil {
		t.Error("stopReconnect channel should be initialized")
	}

	// Should be able to receive from it (it's not closed yet)
	select {
	case <-ws.stopReconnect:
		t.Error("stopReconnect should not be closed initially")
	default:
		// Good, it's open
	}

	// Close it
	close(ws.stopReconnect)

	// Now we should be able to receive from it
	select {
	case <-ws.stopReconnect:
		// Good, it's closed
	default:
		t.Error("stopReconnect should be closed now")
	}
}

// TestWSClient_MinIntHelper verifies the minInt helper function.
func TestWSClient_MinIntHelper(t *testing.T) {
	tests := []struct {
		a, b, expected int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{5, 5, 5},
		{0, 10, 0},
		{10, 0, 0},
		{-5, 3, -5},
	}

	for _, tt := range tests {
		result := minInt(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("minInt(%d, %d) = %d, expected %d", tt.a, tt.b, result, tt.expected)
		}
	}
}

// TestWSClient_ConnectedStateTransitions verifies the connected flag
// transitions correctly in the intended lifecycle.
func TestWSClient_ConnectedStateTransitions(t *testing.T) {
	cfg := DefaultWSConfig()
	logger := zerolog.New(nil)
	ws := NewWSClient(cfg, logger)

	// Start: not connected
	if ws.IsConnected() {
		t.Error("should start not connected")
	}

	// Simulate successful connection
	ws.connected.Store(true)
	if !ws.IsConnected() {
		t.Error("should be connected after Store(true)")
	}

	// Simulate disconnection (readLoop would do this)
	ws.connected.Store(false)
	if ws.IsConnected() {
		t.Error("should be disconnected after Store(false)")
	}

	// Simulate reconnection
	ws.connected.Store(true)
	if !ws.IsConnected() {
		t.Error("should be reconnected after Store(true)")
	}
}
