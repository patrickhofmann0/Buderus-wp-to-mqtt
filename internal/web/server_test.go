package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func decodeSnapshot(t *testing.T, data []byte) Snapshot {
	t.Helper()
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("invalid snapshot JSON %q: %v", data, err)
	}
	return s
}

func TestStoreRecordAndPollCompleted(t *testing.T) {
	store := NewStore("1.2.3", time.Minute, []HeatingCircuit{{Key: "hc1", Label: "Heizkreis 1"}})
	updates, unsubscribe := store.Subscribe()
	defer unsubscribe()

	store.Record("power", []byte(`{"compressorPower":460}`), nil)
	store.Record("temperatures", []byte(`{"outdoor":7.5}`), errors.New("broker down"))
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	store.PollCompleted(at)

	select {
	case <-updates:
	default:
		t.Fatal("subscriber was not notified")
	}

	data, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	s := decodeSnapshot(t, data)
	if s.Version != "1.2.3" || s.PollInterval != 60 {
		t.Errorf("version/interval = %q/%v", s.Version, s.PollInterval)
	}
	if s.LastPoll == nil || !s.LastPoll.Equal(at) {
		t.Errorf("LastPoll = %v, want %v", s.LastPoll, at)
	}
	if !s.GatewayOK {
		t.Error("GatewayOK = false, want true")
	}
	if s.MQTTOK || s.MQTTError != "broker down" {
		t.Errorf("MQTT = %v/%q, want false/broker down", s.MQTTOK, s.MQTTError)
	}
	if string(s.Values["power"]) != `{"compressorPower":460}` {
		t.Errorf("power = %s", s.Values["power"])
	}

	// Ein Durchlauf ohne Daten: Gateway-Fehler, Werte bleiben erhalten.
	store.PollCompleted(at.Add(time.Minute))
	s = decodeSnapshot(t, must(store.Snapshot()))
	if s.GatewayOK {
		t.Error("GatewayOK = true after empty poll, want false")
	}
	if _, ok := s.Values["power"]; !ok {
		t.Error("previous values were dropped")
	}
}

func must(data []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return data
}

func TestHandlerStateAndStatic(t *testing.T) {
	store := NewStore("dev", time.Minute, nil)
	store.Record("power", []byte(`{"modulation":13}`), nil)
	store.PollCompleted(time.Now())
	srv := httptest.NewServer(NewHandler(store, discardLogger()))
	defer srv.Close()

	tests := []struct {
		path        string
		contentType string
		contains    string
	}{
		{"/api/state", "application/json", `"modulation":13`},
		{"/", "text/html", "Wärmepumpe"},
		{"/app.js", "javascript", "EventSource"},
		{"/style.css", "text/css", "prefers-color-scheme"},
		{"/healthz", "text/plain", "ok"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+tt.path, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, tt.contentType) {
				t.Errorf("Content-Type = %q, want %q", ct, tt.contentType)
			}
			if !strings.Contains(string(body), tt.contains) {
				t.Errorf("body does not contain %q", tt.contains)
			}
		})
	}
}

func TestEventsSendsInitialAndUpdates(t *testing.T) {
	store := NewStore("dev", time.Minute, nil)
	srv := httptest.NewServer(NewHandler(store, discardLogger()))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}

	reader := bufio.NewReader(resp.Body)
	readData := func() Snapshot {
		t.Helper()
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("read event: %v", err)
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				return decodeSnapshot(t, []byte(data))
			}
		}
	}

	if s := readData(); s.LastPoll != nil {
		t.Errorf("initial LastPoll = %v, want nil", s.LastPoll)
	}

	store.Record("power", []byte(`{"modulation":42}`), nil)
	store.PollCompleted(time.Now())
	if s := readData(); string(s.Values["power"]) != `{"modulation":42}` {
		t.Errorf("update power = %s", s.Values["power"])
	}
}
