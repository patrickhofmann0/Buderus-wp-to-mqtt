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
	FetchString(ctx context.Context, path string) (buderus.StringResponse, error)
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

// HeatingCircuit ist ein unterstützter Heizkreis für Statuswerte.
type HeatingCircuit struct {
	Key   string
	Label string
}

// DefaultCircuits sind die aktuell unterstützten Energiezähler.
var DefaultCircuits = []Circuit{
	{Key: "heating", Label: "Heizung", Path: buderus.PathHeatingConsumption},
	{Key: "dhw", Label: "Warmwasser", Path: buderus.PathDHWConsumption},
}

// DefaultHeatingCircuits sind die fest unterstützten Heizkreise.
var DefaultHeatingCircuits = []HeatingCircuit{
	{Key: "hc1", Label: "HK1"},
	{Key: "hc2", Label: "HK2"},
}

var summerWinterModes = map[string]string{
	"forced":  "Winter",
	"off":     "Sommer",
	"cooling": "Kühlen",
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
	Modulation      *float64 `json:"modulation,omitempty"`
}

// TemperaturesPayload ist der JSON-Inhalt des Temperaturtopics.
type TemperaturesPayload struct {
	Outdoor     *float64 `json:"outdoor,omitempty"`
	Supply      *float64 `json:"supply,omitempty"`
	Return      *float64 `json:"return,omitempty"`
	DHWActual   *float64 `json:"dhwActual,omitempty"`
	DHWSetpoint *float64 `json:"dhwSetpoint,omitempty"`
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

// TemperaturesTopic liefert das Topic für Temperaturwerte.
func TemperaturesTopic(topicPrefix string) string {
	return topicPrefix + "/temperatures"
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
		p.logger.Error("poll failed", "group", "power", "error", err)
	}
	if err := p.pollTemperatures(ctx); err != nil {
		p.logger.Error("poll failed", "group", "temperatures", "error", err)
	}
	for _, circuit := range DefaultHeatingCircuits {
		if err := p.pollHeatingCircuit(ctx, circuit); err != nil {
			p.logger.Error("poll failed", "heatingCircuit", circuit.Key, "error", err)
		}
	}
}

func translateSummerWinterMode(mode string) (string, bool) {
	translated, ok := summerWinterModes[mode]
	return translated, ok
}

// HeatingCircuitPayload ist der JSON-Inhalt des Topics eines Heizkreises.
type HeatingCircuitPayload struct {
	SummerWinterMode string   `json:"summerWinterMode,omitempty"`
	RoomSetpoint     *float64 `json:"roomSetpoint,omitempty"`
}

func (p *Poller) pollHeatingCircuit(ctx context.Context, circuit HeatingCircuit) error {
	var state HeatingCircuitPayload

	modePath := buderus.HeatingCircuitPath(circuit.Key, "currentSuWiMode")
	modeResponse, err := p.fetcher.FetchString(ctx, modePath)
	if err != nil {
		p.logger.Error("poll failed", "path", modePath, "error", err)
	} else if mode, ok := translateSummerWinterMode(modeResponse.Value); ok {
		state.SummerWinterMode = mode
	} else {
		p.logger.Warn("unknown heating circuit summer/winter mode", "heatingCircuit", circuit.Key, "value", modeResponse.Value)
	}

	setpointPath := buderus.HeatingCircuitPath(circuit.Key, "currentRoomSetpoint")
	setpointResponse, err := p.fetcher.FetchValue(ctx, setpointPath)
	if err != nil {
		p.logger.Error("poll failed", "path", setpointPath, "error", err)
	} else if setpointResponse.SensorFaultState() == "off" {
		p.logger.Warn("heating circuit is off; skipping room setpoint", "heatingCircuit", circuit.Key, "value", setpointResponse.Value)
	} else {
		state.RoomSetpoint = setpointResponse.Value
	}

	if state.SummerWinterMode == "" && state.RoomSetpoint == nil {
		return nil
	}

	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal heating circuit state: %w", err)
	}

	topic := StateTopic(p.topicPrefix, Circuit{Key: circuit.Key})
	if err := p.publisher.Publish(ctx, topic, payload, true); err != nil {
		return err
	}
	p.logger.Debug("published", "topic", topic, "payload", string(payload))
	return nil
}

func (p *Poller) pollPower(ctx context.Context) error {
	var payloadState PowerPayload

	compressorPower, err := p.fetcher.FetchValue(ctx, buderus.PathCompressorPower)
	if err != nil {
		p.logger.Error("poll failed", "path", buderus.PathCompressorPower, "error", err)
	} else {
		payloadState.CompressorPower = compressorPower.Value
	}

	modulation, err := p.fetcher.FetchValue(ctx, buderus.PathModulation)
	if err != nil {
		p.logger.Error("poll failed", "path", buderus.PathModulation, "error", err)
	} else {
		payloadState.Modulation = modulation.Value
	}

	if payloadState.CompressorPower == nil && payloadState.Modulation == nil {
		return nil
	}

	payload, err := json.Marshal(payloadState)
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

const (
	minPlausibleTemperature = -50
	maxPlausibleTemperature = 150
)

func (p *Poller) fetchTemperature(ctx context.Context, path string) *float64 {
	resp, err := p.fetcher.FetchValue(ctx, path)
	if err != nil {
		p.logger.Error("temperature fetch failed", "path", path, "error", err)
		return nil
	}
	if resp.SensorFault() {
		p.logger.Warn("temperature sensor fault", "path", path, "state", resp.SensorFaultState(), "value", *resp.Value)
		return nil
	}
	if resp.Value == nil {
		return nil
	}
	if *resp.Value < minPlausibleTemperature || *resp.Value > maxPlausibleTemperature {
		p.logger.Warn("temperature outside plausible range", "path", path, "value", *resp.Value, "minimum", minPlausibleTemperature, "maximum", maxPlausibleTemperature)
		return nil
	}
	return resp.Value
}

func (p *Poller) pollTemperatures(ctx context.Context) error {
	state := TemperaturesPayload{
		Outdoor:     p.fetchTemperature(ctx, buderus.PathOutdoorTemperature),
		Supply:      p.fetchTemperature(ctx, buderus.PathSupplyTemperature),
		Return:      p.fetchTemperature(ctx, buderus.PathReturnTemperature),
		DHWActual:   p.fetchTemperature(ctx, buderus.DHWCircuitPath("dhw1", "actualTemp")),
		DHWSetpoint: p.fetchTemperature(ctx, buderus.DHWCircuitPath("dhw1", "currentSetpoint")),
	}
	if state.Outdoor == nil && state.Supply == nil && state.Return == nil && state.DHWActual == nil && state.DHWSetpoint == nil {
		return nil
	}

	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal temperature state: %w", err)
	}

	topic := TemperaturesTopic(p.topicPrefix)
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
	sensors = append(sensors, mqtt.Sensor{
		Key:        "modulation",
		Name:       "Modulation",
		StateTopic: PowerTopic(topicPrefix),
		JSONField:  "modulation",
		Unit:       "%",
		StateClass: "measurement",
		Icon:       "mdi:sine-wave",
	})
	sensors = append(sensors, mqtt.Sensor{
		Key:         "outdoor_temperature",
		Name:        "Außentemperatur",
		StateTopic:  TemperaturesTopic(topicPrefix),
		JSONField:   "outdoor",
		Unit:        "°C",
		DeviceClass: "temperature",
		StateClass:  "measurement",
		Icon:        "mdi:thermometer",
	})
	for _, temperature := range []struct {
		key   string
		name  string
		field string
		icon  string
	}{
		{key: "supply_temperature", name: "Vorlauftemperatur", field: "supply", icon: "mdi:thermometer-water"},
		{key: "return_temperature", name: "Rücklauftemperatur", field: "return", icon: "mdi:thermometer-water"},
		{key: "dhw_temperature", name: "Warmwasser Ist", field: "dhwActual", icon: "mdi:water-thermometer"},
		{key: "dhw_setpoint", name: "Warmwasser Soll", field: "dhwSetpoint", icon: "mdi:thermometer-chevron-up"},
	} {
		sensors = append(sensors, mqtt.Sensor{
			Key:         temperature.key,
			Name:        temperature.name,
			StateTopic:  TemperaturesTopic(topicPrefix),
			JSONField:   temperature.field,
			Unit:        "°C",
			DeviceClass: "temperature",
			StateClass:  "measurement",
			Icon:        temperature.icon,
		})
	}
	for _, circuit := range DefaultHeatingCircuits {
		sensors = append(sensors, mqtt.Sensor{
			Key:         circuit.Key + "_summer_winter_mode",
			Name:        circuit.Label + " Sommer/Winter",
			StateTopic:  topicPrefix + "/" + circuit.Key,
			JSONField:   "summerWinterMode",
			DeviceClass: "enum",
			Options:     []string{"Winter", "Sommer", "Kühlen"},
			Icon:        "mdi:sun-snowflake-variant",
		})
		sensors = append(sensors, mqtt.Sensor{
			Key:         circuit.Key + "_room_setpoint",
			Name:        circuit.Label + " Raumsollwert",
			StateTopic:  topicPrefix + "/" + circuit.Key,
			JSONField:   "roomSetpoint",
			Unit:        "°C",
			DeviceClass: "temperature",
			StateClass:  "measurement",
			Icon:        "mdi:home-thermometer",
		})
	}
	return sensors
}
