// Package app enthält die eigentliche Anwendungslogik: Werte abfragen und veröffentlichen.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/internal/buderus"
	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/internal/mqtt"
)

// Fetcher liest Energiezähler und einzelne Messwerte. *buderus.Client erfüllt dieses Interface automatisch.
//
// Für Java-Entwickler: In Go definiert der Nutzer das Interface (hier der Poller),
// nicht der Anbieter. Eine Klasse muss es nicht mit "implements" deklarieren –
// es reicht, die Methoden zu haben. Dadurch lässt sich im Test leicht ein Fake einsetzen.
type Fetcher interface {
	FetchEmon(ctx context.Context, path string) (buderus.EmonResponse, error)
	FetchValue(ctx context.Context, path string) (buderus.ValueResponse, error)
}

// Publisher veröffentlicht MQTT-Nachrichten. *mqtt.Client erfüllt dieses Interface.
type Publisher interface {
	Publish(ctx context.Context, topic string, payload []byte, retain bool) error
}

// Circuit ist ein abgefragter Energiezähler, z. B. Heizung oder Warmwasser.
type Circuit struct {
	// Key wird für Topic und Sensor-IDs verwendet, z. B. "heating".
	Key string
	// Label ist der Anzeigename in Home Assistant, z. B. "Heizung".
	Label string
	// Path ist der REST-Pfad am Gateway.
	Path string
}

// DefaultCircuits sind die aktuell unterstützten Energiezähler.
var DefaultCircuits = []Circuit{
	{Key: "heating", Label: "Heizung", Path: buderus.PathHeatingConsumption},
	{Key: "dhw", Label: "Warmwasser", Path: buderus.PathDHWConsumption},
}

// StatePayload ist der JSON-Inhalt, der pro Circuit veröffentlicht wird.
// Nicht gelieferte Werte (nil) werden weggelassen.
type StatePayload struct {
	// Electricity ist der gesamte Stromverbrauch (ggf. berechnet aus Compressor + EHeater).
	Electricity    *float64 `json:"electricity,omitempty"`
	Compressor     *float64 `json:"compressor,omitempty"`
	EHeater        *float64 `json:"eheater,omitempty"`
	Burner         *float64 `json:"burner,omitempty"`
	OutputProduced *float64 `json:"outputProduced,omitempty"`
	Unit           string   `json:"unit"`
}

// PowerPayload ist der JSON-Inhalt des Leistungstopics.
type PowerPayload struct {
	CompressorPower *float64 `json:"compressorPower,omitempty"`
}

// Poller fragt die Werte regelmäßig ab und veröffentlicht sie.
type Poller struct {
	fetcher     Fetcher
	publisher   Publisher
	topicPrefix string
	circuits    []Circuit
	logger      *slog.Logger
}

// NewPoller erzeugt einen Poller. Abhängigkeiten werden übergeben (≈ Constructor Injection).
func NewPoller(fetcher Fetcher, publisher Publisher, topicPrefix string, circuits []Circuit, logger *slog.Logger) *Poller {
	return &Poller{
		fetcher:     fetcher,
		publisher:   publisher,
		topicPrefix: topicPrefix,
		circuits:    circuits,
		logger:      logger,
	}
}

// StateTopic liefert das Topic, auf dem die Werte eines Circuits veröffentlicht werden.
func StateTopic(topicPrefix string, c Circuit) string {
	return topicPrefix + "/" + c.Key
}

// PowerTopic liefert das Topic für aktuelle Leistungswerte.
func PowerTopic(topicPrefix string) string {
	return topicPrefix + "/power"
}

// Run fragt sofort und danach im angegebenen Intervall ab, bis ctx beendet wird.
// Fehler werden geloggt; das Programm läuft weiter.
func (p *Poller) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		p.PollOnce(ctx)

		// select wartet auf das erste Ereignis: nächster Tick oder Programmende.
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// PollOnce fragt alle Circuits einmal ab. Ein Fehler bei einem Circuit stoppt die anderen nicht.
func (p *Poller) PollOnce(ctx context.Context) {
	for _, c := range p.circuits {
		if err := p.pollCircuit(ctx, c); err != nil {
			p.logger.Error("poll failed", "circuit", c.Key, "error", err)
		}
	}
	if err := p.pollPower(ctx); err != nil {
		p.logger.Error("poll failed", "path", buderus.PathCompressorPower, "error", err)
	}
}

func (p *Poller) pollPower(ctx context.Context) error {
	resp, err := p.fetcher.FetchValue(ctx, buderus.PathCompressorPower)
	if err != nil {
		return err
	}

	payload, err := json.Marshal(PowerPayload{CompressorPower: resp.Value})
	if err != nil {
		return fmt.Errorf("marshal power state: %w", err)
	}

	topic := PowerTopic(p.topicPrefix)
	if err := p.publisher.Publish(ctx, topic, payload, true); err != nil {
		return err
	}
	p.logger.Debug("published", "topic", topic, "payload", string(payload))
	return nil
}

func (p *Poller) pollCircuit(ctx context.Context, c Circuit) error {
	resp, err := p.fetcher.FetchEmon(ctx, c.Path)
	if err != nil {
		return err
	}

	payload, err := json.Marshal(StatePayload{
		Electricity:    resp.Values.TotalElectricity(),
		Compressor:     resp.Values.Compressor,
		EHeater:        resp.Values.EHeater,
		Burner:         resp.Values.Burner,
		OutputProduced: resp.Values.OutputProduced,
		Unit:           resp.Unit,
	})
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	topic := StateTopic(p.topicPrefix, c)
	if err := p.publisher.Publish(ctx, topic, payload, true); err != nil {
		return err
	}
	p.logger.Debug("published", "topic", topic, "payload", string(payload))
	return nil
}

// Sensors liefert die Sensor-Definitionen für die Home-Assistant-Discovery.
func Sensors(topicPrefix string, circuits []Circuit) []mqtt.Sensor {
	// Felder, für die pro Circuit ein Sensor angelegt wird.
	fields := []struct {
		keySuffix string
		label     string
		jsonField string
		icon      string
	}{
		{"electricity", "Stromverbrauch", "electricity", "mdi:lightning-bolt"},
		{"compressor", "Strom Verdichter", "compressor", "mdi:heat-pump"},
		{"eheater", "Strom Zuheizer", "eheater", "mdi:heating-coil"},
		{"output_produced", "erzeugte Wärme", "outputProduced", "mdi:fire"},
	}

	var sensors []mqtt.Sensor
	for _, c := range circuits {
		for _, f := range fields {
			sensors = append(sensors, mqtt.Sensor{
				Key:         c.Key + "_" + f.keySuffix,
				Name:        c.Label + " " + f.label,
				StateTopic:  StateTopic(topicPrefix, c),
				JSONField:   f.jsonField,
				Unit:        "kWh",
				DeviceClass: "energy",
				StateClass:  "total_increasing",
				Icon:        f.icon,
			})
		}
	}
	sensors = append(sensors, mqtt.Sensor{
		Key:         "compressor_power",
		Name:        "Leistung Verdichter",
		StateTopic:  PowerTopic(topicPrefix),
		JSONField:   "compressorPower",
		Unit:        "W",
		DeviceClass: "power",
		StateClass:  "measurement",
		Icon:        "mdi:heat-pump",
	})
	return sensors
}
