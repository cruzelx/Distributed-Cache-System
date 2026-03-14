package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthHandler(t *testing.T) {
	m := NewMaster("primary", "")
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	m.HealthHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestStateHandler(t *testing.T) {
	m := NewMaster("primary", "")
	m.activeAuxServers["aux1:3001"] = true
	m.activeAuxServers["aux2:3002"] = false

	req := httptest.NewRequest(http.MethodGet, "/state", nil)
	w := httptest.NewRecorder()

	m.StateHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var state map[string]bool
	err := json.NewDecoder(w.Body).Decode(&state)
	require.NoError(t, err)
	assert.True(t, state["aux1:3001"])
	assert.False(t, state["aux2:3002"])
}

func TestRingUpdateHandler_Add(t *testing.T) {
	m := NewMaster("standby", "")
	body := `{"action":"add","aux":"aux1:3001"}`

	req := httptest.NewRequest(http.MethodPost, "/ring-update", strings.NewReader(body))
	w := httptest.NewRecorder()

	m.RingUpdateHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, m.activeAuxServers["aux1:3001"])
	_, err := m.hashring.GetNode("any-key")
	assert.NoError(t, err, "ring should have a node after add")
}

func TestRingUpdateHandler_Remove(t *testing.T) {
	m := NewMaster("standby", "")
	m.hashring.AddNode("aux1:3001")
	m.activeAuxServers["aux1:3001"] = true

	body := `{"action":"remove","aux":"aux1:3001"}`
	req := httptest.NewRequest(http.MethodPost, "/ring-update", strings.NewReader(body))
	w := httptest.NewRecorder()

	m.RingUpdateHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, m.activeAuxServers["aux1:3001"])
	assert.Equal(t, 0, len(m.hashring.hashmap), "ring should be empty after removing the only node")
}

func TestRingUpdateHandler_InvalidAction(t *testing.T) {
	m := NewMaster("standby", "")
	body := `{"action":"noop","aux":"aux1:3001"}`

	req := httptest.NewRequest(http.MethodPost, "/ring-update", strings.NewReader(body))
	w := httptest.NewRecorder()

	m.RingUpdateHandler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRingUpdateHandler_InvalidBody(t *testing.T) {
	m := NewMaster("standby", "")

	req := httptest.NewRequest(http.MethodPost, "/ring-update", strings.NewReader("not-json"))
	w := httptest.NewRecorder()

	m.RingUpdateHandler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPushRingUpdate(t *testing.T) {
	var received RingUpdate

	standby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/ring-update", r.URL.Path)
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer standby.Close()

	standbyAddr := strings.TrimPrefix(standby.URL, "http://")
	m := NewMaster("primary", standbyAddr)

	m.pushRingUpdate("remove", "aux2:3002")

	assert.Equal(t, "remove", received.Action)
	assert.Equal(t, "aux2:3002", received.Aux)
}

func TestPushRingUpdate_NoStandby(t *testing.T) {
	// pushRingUpdate should be a no-op when no standby is configured
	m := NewMaster("primary", "")
	assert.NotPanics(t, func() {
		m.pushRingUpdate("remove", "aux1:3001")
	})
}

func TestInitFromPrimary(t *testing.T) {
	state := map[string]bool{
		"aux1:3001": true,
		"aux2:3002": false,
		"aux3:3003": true,
	}

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/state", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(state)
	}))
	defer primary.Close()

	m := NewMaster("standby", "")
	primaryAddr := strings.TrimPrefix(primary.URL, "http://")

	m.initFromPrimary(primaryAddr)

	assert.Equal(t, state, m.activeAuxServers)

	// Only active nodes should be reachable in the ring
	node, err := m.hashring.GetNode("some-key")
	require.NoError(t, err)
	assert.True(t, node == "aux1:3001" || node == "aux3:3003",
		"expected an active node, got %s", node)
}

func TestInitFromPrimary_RetriesOnFailure(t *testing.T) {
	attempts := 0
	state := map[string]bool{"aux1:3001": true}

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(state)
	}))
	defer primary.Close()

	m := NewMaster("standby", "")
	primaryAddr := strings.TrimPrefix(primary.URL, "http://")

	// Override the client timeout to speed up retries
	m.client.Timeout = 100 * time.Millisecond

	m.initFromPrimary(primaryAddr)

	assert.Equal(t, 3, attempts, "expected 3 attempts before success")
	assert.True(t, m.activeAuxServers["aux1:3001"])
}

func TestMonitorPrimary_Promotes(t *testing.T) {
	// Use a closed server so health checks immediately get connection refused
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	primary.Close()

	m := NewMaster("standby", "")
	primaryAddr := strings.TrimPrefix(primary.URL, "http://")

	promote := make(chan struct{}, 1)
	stop := make(chan interface{})
	defer close(stop)

	go m.monitorPrimary(primaryAddr, promote, stop)

	// 3 failures × 5s sleep ≈ 15s; allow 20s
	select {
	case <-promote:
		// promoted as expected
	case <-time.After(20 * time.Second):
		t.Fatal("standby did not promote within 20s")
	}
}

func TestMonitorPrimary_StopsOnSignal(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer primary.Close()

	m := NewMaster("standby", "")
	primaryAddr := strings.TrimPrefix(primary.URL, "http://")

	promote := make(chan struct{}, 1)
	stop := make(chan interface{})

	done := make(chan struct{})
	go func() {
		m.monitorPrimary(primaryAddr, promote, stop)
		close(done)
	}()

	close(stop)

	select {
	case <-done:
		// goroutine exited cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("monitorPrimary did not stop after stop signal")
	}
	assert.Empty(t, promote)
}

func TestHandleDeadAuxServer_PushesToStandby(t *testing.T) {
	received := make(chan RingUpdate, 1)

	standby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var update RingUpdate
		json.NewDecoder(r.Body).Decode(&update)
		received <- update
		w.WriteHeader(http.StatusOK)
	}))
	defer standby.Close()

	standbyAddr := strings.TrimPrefix(standby.URL, "http://")
	m := NewMaster("primary", standbyAddr)
	m.hashring.AddNode("aux1:3001")
	m.activeAuxServers["aux1:3001"] = true

	m.handleDeadAuxServer("aux1:3001")

	select {
	case update := <-received:
		assert.Equal(t, "remove", update.Action)
		assert.Equal(t, "aux1:3001", update.Aux)
	case <-time.After(2 * time.Second):
		t.Fatal("standby did not receive ring update within 2s")
	}

	assert.False(t, m.activeAuxServers["aux1:3001"])
}
