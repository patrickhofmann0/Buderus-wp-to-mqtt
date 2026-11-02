// Package web stellt eine kleine Weboberfläche mit den zuletzt abgefragten Werten bereit.
package web

import (
	"encoding/json"
	"sync"
	"time"
)

// HeatingCircuit beschreibt einen Heizkreis für die Anzeige.
type HeatingCircuit struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	HasMixer bool   `json:"hasMixer"`
}

// Snapshot ist der aktuelle Zustand, wie er an den Browser geht.
type Snapshot struct {
	Version         string                     `json:"version"`
	PollInterval    float64                    `json:"pollIntervalSeconds"`
	LastPoll        *time.Time                 `json:"lastPoll,omitempty"`
	GatewayOK       bool                       `json:"gatewayOk"`
	MQTTOK          bool                       `json:"mqttOk"`
	MQTTError       string                     `json:"mqttError,omitempty"`
	HeatingCircuits []HeatingCircuit           `json:"heatingCircuits"`
	Values          map[string]json.RawMessage `json:"values"`
}

// Store hält den letzten Zustand threadsicher im Speicher und benachrichtigt Abonnenten.
// Er erfüllt app.StateRecorder.
//
// Für Java-Entwickler: Das Mutex entspricht einem synchronized-Block,
// die Abonnenten-Channels einem einfachen Observer-Pattern.
type Store struct {
	mu          sync.RWMutex
	snapshot    Snapshot
	pollRecords int
	pollMQTTErr error
	subscribers map[chan struct{}]struct{}
}

// NewStore erzeugt einen leeren Store.
func NewStore(version string, pollInterval time.Duration, circuits []HeatingCircuit) *Store {
	return &Store{
		snapshot: Snapshot{
			Version:         version,
			PollInterval:    pollInterval.Seconds(),
			HeatingCircuits: circuits,
			Values:          map[string]json.RawMessage{},
		},
		subscribers: map[chan struct{}]struct{}{},
	}
}

// Record merkt sich das Payload einer Gruppe. Werte bleiben erhalten, bis neue kommen.
func (s *Store) Record(key string, payload []byte, publishErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.Values[key] = append(json.RawMessage(nil), payload...)
	s.pollRecords++
	if publishErr != nil {
		s.pollMQTTErr = publishErr
	}
}

// PollCompleted schließt einen Abfragedurchlauf ab und benachrichtigt alle Abonnenten.
func (s *Store) PollCompleted(at time.Time) {
	s.mu.Lock()
	s.snapshot.LastPoll = &at
	s.snapshot.GatewayOK = s.pollRecords > 0
	// Ohne Daten wurde nichts versendet; dann bleibt der MQTT-Status unverändert.
	if s.pollRecords > 0 {
		s.snapshot.MQTTOK = s.pollMQTTErr == nil
		s.snapshot.MQTTError = ""
		if s.pollMQTTErr != nil {
			s.snapshot.MQTTError = s.pollMQTTErr.Error()
		}
	}
	s.pollRecords = 0
	s.pollMQTTErr = nil
	for ch := range s.subscribers {
		// Nicht blockieren: Ein ausstehendes Signal reicht, der Leser holt sich dann den neuesten Stand.
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	s.mu.Unlock()
}

// Snapshot liefert den aktuellen Zustand als JSON.
func (s *Store) Snapshot() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.Marshal(s.snapshot)
}

// Subscribe liefert einen Channel, der bei jeder Aktualisierung ein Signal erhält.
// Die zurückgegebene Funktion meldet das Abonnement wieder ab.
func (s *Store) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subscribers, ch)
		s.mu.Unlock()
	}
}
