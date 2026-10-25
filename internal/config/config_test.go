package config

import (
	"testing"
	"time"
)

// envFrom baut aus einer Map eine getenv-Funktion für die Tests.
func envFrom(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(envFrom(map[string]string{
		"BUDERUS_HOST":  "gateway.local",
		"BUDERUS_TOKEN": "secret",
		"MQTT_BROKER":   "tcp://localhost:1883",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.BuderusPort != 9443 {
		t.Errorf("BuderusPort = %d, want 9443", cfg.BuderusPort)
	}
	if cfg.TopicPrefix != "buderus/wp" {
		t.Errorf("TopicPrefix = %q, want %q", cfg.TopicPrefix, "buderus/wp")
	}
	if cfg.PollInterval != 5*time.Minute {
		t.Errorf("PollInterval = %v, want 5m", cfg.PollInterval)
	}
	if !cfg.DiscoveryEnabled {
		t.Error("DiscoveryEnabled = false, want true")
	}
	if cfg.DeviceID != "buderus_warmepumpe" {
		t.Errorf("DeviceID = %q, want %q", cfg.DeviceID, "buderus_warmepumpe")
	}
}

func TestLoadMissingRequired(t *testing.T) {
	_, err := load(envFrom(map[string]string{}))
	if err == nil {
		t.Fatal("expected error for missing required variables")
	}
}

func TestLoadInvalidValues(t *testing.T) {
	base := map[string]string{
		"BUDERUS_HOST":  "gateway.local",
		"BUDERUS_TOKEN": "secret",
		"MQTT_BROKER":   "tcp://localhost:1883",
	}

	// Table-driven Test: entspricht @ParameterizedTest in JUnit.
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"invalid port", "BUDERUS_PORT", "abc"},
		{"invalid interval", "POLL_INTERVAL", "five minutes"},
		{"interval too short", "POLL_INTERVAL", "1s"},
		{"invalid bool", "MQTT_DISCOVERY_ENABLED", "maybe"},
		{"invalid log level", "LOG_LEVEL", "verbose"},
		{"empty prefix", "MQTT_TOPIC_PREFIX", "/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{tt.key: tt.value}
			for k, v := range base {
				env[k] = v
			}
			if _, err := load(envFrom(env)); err == nil {
				t.Errorf("expected error for %s=%q", tt.key, tt.value)
			}
		})
	}
}

func TestNormalizePrefix(t *testing.T) {
	tests := map[string]string{
		"buderus/wp/":  "buderus/wp",
		"/buderus/wp":  "buderus/wp",
		"buderus":      "buderus",
		" buderus/wp ": "buderus/wp",
	}
	for input, want := range tests {
		if got := normalizePrefix(input); got != want {
			t.Errorf("normalizePrefix(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSlugify(t *testing.T) {
	tests := map[string]string{
		"Buderus Wärmepumpe": "buderus_warmepumpe",
		"WP Keller #1":       "wp_keller_1",
		"  Heizung  ":        "heizung",
		"Straße":             "strasse",
	}
	for input, want := range tests {
		if got := Slugify(input); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", input, got, want)
		}
	}
}
