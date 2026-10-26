package mqtt

import (
	"encoding/json"
	"testing"
)

func TestBuildDiscoveryMessages(t *testing.T) {
	device := Device{ID: "buderus_wp", Name: "Buderus Wärmepumpe", Manufacturer: "Buderus"}
	sensors := []Sensor{{
		Key:         "heating_electricity",
		Name:        "Heizung Strom",
		StateTopic:  "buderus/wp/heating",
		JSONField:   "electricity",
		Unit:        "kWh",
		DeviceClass: "energy",
		StateClass:  "total_increasing",
	}}

	messages, err := BuildDiscoveryMessages("homeassistant", "buderus/wp/status", device, sensors)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(messages))
	}

	msg := messages[0]
	wantTopic := "homeassistant/sensor/buderus_wp/heating_electricity/config"
	if msg.Topic != wantTopic {
		t.Errorf("Topic = %q, want %q", msg.Topic, wantTopic)
	}
	if !msg.Retain {
		t.Error("discovery message must be retained")
	}

	// Payload zurücklesen und die wichtigsten Felder prüfen.
	var payload map[string]any
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	want := map[string]string{
		"unique_id":           "buderus_wp_heating_electricity",
		"state_topic":         "buderus/wp/heating",
		"value_template":      "{{ value_json.electricity }}",
		"unit_of_measurement": "kWh",
		"device_class":        "energy",
		"state_class":         "total_increasing",
		"availability_topic":  "buderus/wp/status",
	}
	for key, value := range want {
		if payload[key] != value {
			t.Errorf("%s = %v, want %q", key, payload[key], value)
		}
	}
	if _, ok := payload["icon"]; ok {
		t.Error("empty icon should be omitted")
	}
	if _, ok := payload["options"]; ok {
		t.Error("empty options should be omitted")
	}
	if _, ok := payload["via_device"]; ok {
		t.Error("via_device should be omitted from the top-level payload")
	}
}

func TestBuildDiscoveryMessagesIncludesEnumOptions(t *testing.T) {
	messages, err := BuildDiscoveryMessages("homeassistant", "buderus/wp/status", Device{ID: "buderus_wp", Name: "Buderus"}, []Sensor{{
		Key:         "hc1_summer_winter_mode",
		Name:        "HK1 Sommer/Winter",
		StateTopic:  "buderus/wp/hc1",
		JSONField:   "summerWinterMode",
		DeviceClass: "enum",
		Options:     []string{"Winter", "Sommer", "Kühlen"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var payload struct {
		DeviceClass string   `json:"device_class"`
		Options     []string `json:"options"`
	}
	if err := json.Unmarshal(messages[0].Payload, &payload); err != nil {
		t.Fatalf("invalid discovery JSON: %v", err)
	}
	if payload.DeviceClass != "enum" {
		t.Errorf("device_class = %q, want enum", payload.DeviceClass)
	}
	want := []string{"Winter", "Sommer", "Kühlen"}
	if len(payload.Options) != len(want) {
		t.Fatalf("options = %v, want %v", payload.Options, want)
	}
	for i, value := range want {
		if payload.Options[i] != value {
			t.Errorf("options[%d] = %q, want %q", i, payload.Options[i], value)
		}
	}
}

func TestBuildDiscoveryMessagesForSubDeviceKeepsEntityIdentity(t *testing.T) {
	device := Device{ID: "buderus_wp", Name: "Buderus Wärmepumpe", Manufacturer: "Buderus", Model: "Wärmepumpe"}
	sensor := Sensor{
		Key:        "heating_electricity",
		Name:       "Stromverbrauch",
		StateTopic: "buderus/wp/heating",
		JSONField:  "electricity",
		SubDevice:  &SubDevice{Key: "heating", Name: "Heizung"},
	}

	messages, err := BuildDiscoveryMessages("homeassistant", "buderus/wp/status", device, []Sensor{sensor})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	message := messages[0]
	if want := "homeassistant/sensor/buderus_wp/heating_electricity/config"; message.Topic != want {
		t.Errorf("Topic = %q, want %q", message.Topic, want)
	}

	var payload struct {
		UniqueID        string `json:"unique_id"`
		DefaultEntityID string `json:"default_entity_id"`
		Device          struct {
			Identifiers []string `json:"identifiers"`
			Name        string   `json:"name"`
			ViaDevice   string   `json:"via_device"`
		} `json:"device"`
	}
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		t.Fatalf("invalid discovery JSON: %v", err)
	}
	if payload.UniqueID != "buderus_wp_heating_electricity" {
		t.Errorf("unique_id = %q, want stable root-based ID", payload.UniqueID)
	}
	if payload.DefaultEntityID != "sensor.buderus_wp_heating_electricity" {
		t.Errorf("default_entity_id = %q, want stable entity ID", payload.DefaultEntityID)
	}
	if payload.Device.ViaDevice != "buderus_wp" {
		t.Errorf("device.via_device = %q, want buderus_wp", payload.Device.ViaDevice)
	}
	if len(payload.Device.Identifiers) != 1 || payload.Device.Identifiers[0] != "buderus_wp_heating" {
		t.Errorf("device identifiers = %v, want [buderus_wp_heating]", payload.Device.Identifiers)
	}
	if payload.Device.Name != "Heizung" {
		t.Errorf("device name = %q, want Heizung", payload.Device.Name)
	}
}
