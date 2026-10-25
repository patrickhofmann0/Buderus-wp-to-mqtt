package config

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

var environmentKeys = []string{
	"API_URL", "API_URLS", "API_TOKEN", "API_USER", "API_PASSWORD", "API_TIMEOUT", "API_MAX_BODY_BYTES", "API_INSECURE_SKIP_VERIFY",
	"MQTT_BROKER", "MQTT_CLIENT_ID", "MQTT_USER", "MQTT_PASSWORD", "MQTT_TOPIC", "MQTT_QOS", "MQTT_RETAIN", "MQTT_PUBLISH_TIMEOUT", "MQTT_AVAILABILITY",
	"MAPPING", "SCHEDULE", "POLL_ON_START", "TZ", "LOG_LEVEL", "LOG_FORMAT",
}

func cleanEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range environmentKeys {
		value, exists := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if exists {
				if err := os.Setenv(key, value); err != nil {
					t.Error(err)
				}
			} else {
				if err := os.Unsetenv(key); err != nil {
					t.Error(err)
				}
			}
		})
	}
	t.Setenv("API_URL", "https://example.com/api")
}

func TestDefaults(t *testing.T) {
	cleanEnvironment(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Endpoints) != 1 || c.Endpoints[0].URL != "https://example.com/api" || c.Endpoints[0].Name != "" {
		t.Fatalf("endpoints = %#v", c.Endpoints)
	}
	if c.APITimeout != 15*time.Second || c.APIMaxBodyBytes != 1048576 || c.APIInsecureSkipVerify {
		t.Fatal("incorrect API defaults")
	}
	if c.MQTT.Broker != "tcp://localhost:1883" || c.MQTT.ClientID != "buderus-wp-to-mqtt" || c.MQTT.Username != "" || c.MQTT.Password != "" || c.MQTT.QoS != 0 || c.MQTT.Timeout != 5*time.Second || c.MQTT.KeepAlive != 30*time.Second {
		t.Fatalf("MQTT defaults = %#v", c.MQTT)
	}
	if c.MQTTTopic != "buderus/wp" || c.MQTTRetain || !c.MQTTAvailability || c.MQTT.AvailabilityTopic != "buderus/wp/status" {
		t.Fatal("incorrect MQTT topic defaults")
	}
	if c.Mapping != "fields" || c.Schedule != "@every 60s" || !c.PollOnStart || c.Location.String() != "UTC" || c.LogLevel != slog.LevelInfo || c.LogFormat != "json" {
		t.Fatal("incorrect application defaults")
	}
}

func TestOverrides(t *testing.T) {
	cleanEnvironment(t)
	values := map[string]string{
		"API_URLS":  " upstairs=https://example.com/api?key=a=b,basement-2=http://example.org ",
		"API_TOKEN": "token", "API_TIMEOUT": "2s", "API_MAX_BODY_BYTES": "123", "API_INSECURE_SKIP_VERIFY": "true",
		"MQTT_BROKER": "wss://broker.example/ws", "MQTT_CLIENT_ID": "custom", "MQTT_USER": "user", "MQTT_PASSWORD": "pass",
		"MQTT_TOPIC": "custom/topic", "MQTT_QOS": "2", "MQTT_RETAIN": "true", "MQTT_PUBLISH_TIMEOUT": "3s", "MQTT_AVAILABILITY": "false",
		"MAPPING": "passthrough", "SCHEDULE": "*/5 * * * *", "POLL_ON_START": "false", "TZ": "Europe/Berlin", "LOG_LEVEL": "debug", "LOG_FORMAT": "text",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Endpoints) != 2 || c.Endpoints[0].Name != "upstairs" || c.Endpoints[0].URL != "https://example.com/api?key=a=b" || c.Endpoints[1].Name != "basement-2" {
		t.Fatalf("endpoints = %#v", c.Endpoints)
	}
	if c.APIToken != "token" || c.APITimeout != 2*time.Second || c.APIMaxBodyBytes != 123 || !c.APIInsecureSkipVerify {
		t.Fatal("API overrides lost")
	}
	if c.MQTT.Broker != values["MQTT_BROKER"] || c.MQTT.ClientID != "custom" || c.MQTT.Username != "user" || c.MQTT.Password != "pass" || c.MQTT.Timeout != 3*time.Second || c.MQTT.QoS != 2 || !c.MQTTRetain || c.MQTTAvailability || c.MQTT.AvailabilityTopic != "" || c.MQTTTopic != "custom/topic" {
		t.Fatal("MQTT overrides lost")
	}
	if c.Mapping != "passthrough" || c.Schedule != values["SCHEDULE"] || c.PollOnStart || c.Location.String() != "Europe/Berlin" || c.LogLevel != slog.LevelDebug || c.LogFormat != "text" {
		t.Fatal("application overrides lost")
	}
}

func TestInvalidValues(t *testing.T) {
	cases := map[string][]string{
		"API_URL":     {"", "/relative", "ftp://example.com", "https://", "http://" + "user@example.com", "http://[", "https://example.com/#fragment", "http://host:", "http://host:0", "http://host:65536"},
		"API_URLS":    {" ", "a=http://example.com,", ",a=http://example.com", "missing", "=http://example.com", "a=", "a=http://one,a=http://two", "a/b=http://example.com", "a+=http://example.com", "a#=http://example.com", "a b=http://example.com", "a=ftp://example.com"},
		"API_TIMEOUT": {"", "0", "-1s", "bad"}, "API_MAX_BODY_BYTES": {"", "0", "-1", "1.5", "9223372036854775807", "9223372036854775808"},
		"API_INSECURE_SKIP_VERIFY": {"", "yes"}, "MQTT_BROKER": {"", "ftp://example.com", "tcp://", "tcp://" + "user@host", "tcp://[", "tcp://host:0", "tcp://host:65536"},
		"MQTT_CLIENT_ID": {"", "\xff"}, "MQTT_TOPIC": {"", "a/+", "a/#", "\xff"},
		"MQTT_QOS": {"", "-1", "3", "256", "bad"}, "MQTT_RETAIN": {"", "yes"}, "MQTT_AVAILABILITY": {"", "yes"},
		"MQTT_PUBLISH_TIMEOUT": {"", "0s", "-1s", "bad"}, "MAPPING": {"", "unknown"}, "POLL_ON_START": {"", "yes"},
		"SCHEDULE": {"", "@every 0s", "@every -1s", "@every nonsense", "@every", "* * * * * *", "61 * * * *", "nonsense"},
		"TZ":       {"", "Missing/Timezone"}, "LOG_LEVEL": {"", "trace"}, "LOG_FORMAT": {"", "xml"},
		"API_TOKEN": {"bad\r\n", "bad token", "bad\x01", "bad\x7f"}, "API_USER": {"a:b", "bad\x01"}, "API_PASSWORD": {"secret", "bad\x01"},
	}
	for key, values := range cases {
		for _, value := range values {
			t.Run(key+"/"+value, func(t *testing.T) {
				cleanEnvironment(t)
				t.Setenv(key, value)
				if _, err := Load(); err == nil {
					t.Fatalf("accepted %s=%q", key, value)
				} else if strings.Contains(err.Error(), "secret") {
					t.Fatalf("credentials leaked: %v", err)
				}
			})
		}
	}
}

func TestAuth(t *testing.T) {
	cleanEnvironment(t)
	t.Setenv("API_USER", "user")
	t.Setenv("API_PASSWORD", "password")
	c, err := Load()
	if err != nil || c.APIUser != "user" || c.APIPassword != "password" {
		t.Fatalf("basic auth = %#v, %v", c, err)
	}
	t.Setenv("API_TOKEN", "token")
	if _, err := Load(); err == nil {
		t.Fatal("conflicting auth accepted")
	}
}

func TestScheduleDescriptorsAndSchemes(t *testing.T) {
	cleanEnvironment(t)
	for _, schedule := range []string{"@hourly", "@daily", "@weekly", "@monthly", "@yearly", "@every 500ms", "0 12 * * MON"} {
		t.Setenv("SCHEDULE", schedule)
		if _, err := Load(); err != nil {
			t.Fatalf("%s: %v", schedule, err)
		}
	}
	for _, scheme := range []string{"tcp", "ssl", "tls", "mqtt", "mqtts", "ws", "wss"} {
		t.Setenv("MQTT_BROKER", scheme+"://localhost:1883")
		if _, err := Load(); err != nil {
			t.Fatalf("%s: %v", scheme, err)
		}
	}
	for _, level := range []string{"debug", "info", "warn", "error"} {
		t.Setenv("LOG_LEVEL", level)
		if _, err := Load(); err != nil {
			t.Fatalf("%s: %v", level, err)
		}
	}
}

func TestURLListPrecedenceAndLimits(t *testing.T) {
	cleanEnvironment(t)
	t.Setenv("API_URL", "not a URL")
	t.Setenv("API_URLS", "a=https://example.com")
	t.Setenv("API_MAX_BODY_BYTES", "9223372036854775806")
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MQTT_TOPIC", strings.Repeat("a", 65530))
	if _, err := Load(); err == nil {
		t.Fatal("availability topic exceeding MQTT limit accepted")
	}
	t.Setenv("MQTT_AVAILABILITY", "false")
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MQTT_TOPIC", strings.Repeat("a", 65536))
	if _, err := Load(); err == nil {
		t.Fatal("oversized MQTT topic accepted")
	}
}

func TestTopicValidation(t *testing.T) {
	for _, topic := range []string{"", "a\x00b", "a/+", "a/#", "\xff", strings.Repeat("a", 65536)} {
		if validTopic(topic) {
			t.Errorf("invalid topic accepted: %q", topic)
		}
	}
	if !validTopic("buderus/wp") {
		t.Fatal("valid topic rejected")
	}
}

func TestEmptyURLListFallback(t *testing.T) {
	cleanEnvironment(t)
	t.Setenv("API_URLS", "")
	c, err := Load()
	if err != nil || len(c.Endpoints) != 1 || c.Endpoints[0].URL != "https://example.com/api" {
		t.Fatalf("empty API_URLS did not fall back to API_URL: %#v, %v", c.Endpoints, err)
	}
	t.Setenv("API_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("empty API_URL and API_URLS accepted")
	}
}

func TestInvalidUTF8TopicWithoutAvailability(t *testing.T) {
	cleanEnvironment(t)
	t.Setenv("MQTT_AVAILABILITY", "false")
	t.Setenv("MQTT_TOPIC", "\xff")
	if _, err := Load(); err == nil {
		t.Fatal("invalid UTF-8 topic accepted without availability")
	}
}
