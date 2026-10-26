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
