package mqtt

import (
	"encoding/json"
	"fmt"
)

// Device beschreibt das Gerät, unter dem Home Assistant die Sensoren gruppiert.
type Device struct {
	ID           string
	Name         string
	Manufacturer string
	Model        string
}

// Sensor beschreibt einen Messwert, für den eine Discovery-Nachricht erzeugt wird.
type Sensor struct {
	// Key ist eindeutig pro Gerät, z. B. "heating_electricity".
	Key  string
	Name string
	// StateTopic ist das Topic, auf dem der Wert (als Teil eines JSON-Objekts) veröffentlicht wird.
	StateTopic string
	// JSONField ist das Feld im JSON-Payload, z. B. "electricity".
	JSONField   string
	Unit        string
	DeviceClass string
	StateClass  string
	Icon        string
	Options     []string
}

// Message ist eine fertige MQTT-Nachricht.
type Message struct {
	Topic   string
	Payload []byte
	Retain  bool
}

// discoveryPayload entspricht dem JSON-Format der Home-Assistant-MQTT-Discovery.
// "omitempty" lässt leere Felder weg (≈ @JsonInclude(NON_EMPTY)).
type discoveryPayload struct {
	Name              string          `json:"name"`
	UniqueID          string          `json:"unique_id"`
	DefaultEntityID   string          `json:"default_entity_id"`
	StateTopic        string          `json:"state_topic"`
	ValueTemplate     string          `json:"value_template"`
	UnitOfMeasurement string          `json:"unit_of_measurement,omitempty"`
	DeviceClass       string          `json:"device_class,omitempty"`
	StateClass        string          `json:"state_class,omitempty"`
	Icon              string          `json:"icon,omitempty"`
	Options           []string        `json:"options,omitempty"`
	AvailabilityTopic string          `json:"availability_topic"`
	Device            discoveryDevice `json:"device"`
}

type discoveryDevice struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer,omitempty"`
	Model        string   `json:"model,omitempty"`
}

// BuildDiscoveryMessages erzeugt eine retained Discovery-Nachricht pro Sensor.
// Topic-Schema: <discoveryPrefix>/sensor/<deviceID>/<sensorKey>/config
func BuildDiscoveryMessages(discoveryPrefix, availabilityTopic string, device Device, sensors []Sensor) ([]Message, error) {
	// make(..., 0, n) legt eine leere Liste mit Kapazität n an (≈ new ArrayList<>(n)).
	messages := make([]Message, 0, len(sensors))

	for _, s := range sensors {
		uniqueID := device.ID + "_" + s.Key
		payload := discoveryPayload{
			Name:              s.Name,
			UniqueID:          uniqueID,
			DefaultEntityID:   "sensor." + uniqueID,
			StateTopic:        s.StateTopic,
			ValueTemplate:     fmt.Sprintf("{{ value_json.%s }}", s.JSONField),
			UnitOfMeasurement: s.Unit,
			DeviceClass:       s.DeviceClass,
			StateClass:        s.StateClass,
			Icon:              s.Icon,
			Options:           s.Options,
			AvailabilityTopic: availabilityTopic,
			Device: discoveryDevice{
				Identifiers:  []string{device.ID},
				Name:         device.Name,
				Manufacturer: device.Manufacturer,
				Model:        device.Model,
			},
		}

		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal discovery for %s: %w", s.Key, err)
		}

		messages = append(messages, Message{
			Topic:   fmt.Sprintf("%s/sensor/%s/%s/config", discoveryPrefix, device.ID, s.Key),
			Payload: data,
			Retain:  true,
		})
	}
	return messages, nil
}
