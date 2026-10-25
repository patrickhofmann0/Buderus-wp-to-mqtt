// Package config loads and validates environment configuration.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/mqttpub"
	"github.com/robfig/cron/v3"
)

type Endpoint struct{ Name, URL string }

type Config struct {
	Endpoints                      []Endpoint
	APIToken, APIUser, APIPassword string
	APITimeout                     time.Duration
	APIMaxBodyBytes                int64
	APIInsecureSkipVerify          bool
	MQTT                           mqttpub.Config
	MQTTTopic                      string
	MQTTRetain, MQTTAvailability   bool
	Mapping, Schedule              string
	PollOnStart                    bool
	Location                       *time.Location
	LogLevel                       slog.Level
	LogFormat                      string
}

// Load reads only documented variables. Nonempty API_URLS takes precedence over
// API_URL. KeepAlive is the library default (30 seconds), not an environment setting.
func Load() (Config, error) {
	var c Config
	var err error
	get := func(key, fallback string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		return fallback
	}
	fail := func(key string) (Config, error) { return Config{}, fmt.Errorf("invalid %s", key) }
	if values, ok := os.LookupEnv("API_URLS"); ok && values != "" {
		seen := make(map[string]bool)
		for _, entry := range strings.Split(values, ",") {
			name, endpoint, found := strings.Cut(strings.TrimSpace(entry), "=")
			name, endpoint = strings.TrimSpace(name), strings.TrimSpace(endpoint)
			if !found || !validName(name) || seen[name] || !validAPIURL(endpoint) {
				return fail("API_URLS")
			}
			seen[name] = true
			c.Endpoints = append(c.Endpoints, Endpoint{Name: name, URL: endpoint})
		}
	} else {
		endpoint := get("API_URL", "")
		if !validAPIURL(endpoint) {
			return fail("API_URL")
		}
		c.Endpoints = []Endpoint{{URL: endpoint}}
	}
	c.APIToken, c.APIUser, c.APIPassword = get("API_TOKEN", ""), get("API_USER", ""), get("API_PASSWORD", "")
	if c.APIToken != "" && (c.APIUser != "" || c.APIPassword != "") {
		return fail("api authentication")
	}
	if strings.ContainsAny(c.APIToken, " \t") || strings.ContainsRune(c.APIUser, ':') || hasControl(c.APIToken) || hasControl(c.APIUser) || hasControl(c.APIPassword) || (c.APIPassword != "" && c.APIUser == "") {
		return fail("api authentication")
	}
	for _, item := range []struct {
		key, fallback string
		target        *time.Duration
	}{
		{"API_TIMEOUT", "15s", &c.APITimeout},
		{"MQTT_PUBLISH_TIMEOUT", "5s", &c.MQTT.Timeout},
	} {
		*item.target, err = time.ParseDuration(get(item.key, item.fallback))
		if err != nil || *item.target <= 0 {
			return fail(item.key)
		}
	}
	c.APIMaxBodyBytes, err = strconv.ParseInt(get("API_MAX_BODY_BYTES", "1048576"), 10, 64)
	if err != nil || c.APIMaxBodyBytes <= 0 || c.APIMaxBodyBytes == math.MaxInt64 {
		return fail("API_MAX_BODY_BYTES")
	}
	for _, item := range []struct {
		key, fallback string
		target        *bool
	}{
		{"API_INSECURE_SKIP_VERIFY", "false", &c.APIInsecureSkipVerify},
		{"MQTT_RETAIN", "false", &c.MQTTRetain},
		{"MQTT_AVAILABILITY", "true", &c.MQTTAvailability},
		{"POLL_ON_START", "true", &c.PollOnStart},
	} {
		*item.target, err = strconv.ParseBool(get(item.key, item.fallback))
		if err != nil {
			return fail(item.key)
		}
	}
	c.MQTT.Broker, c.MQTT.ClientID = get("MQTT_BROKER", "tcp://localhost:1883"), get("MQTT_CLIENT_ID", "buderus-wp-to-mqtt")
	c.MQTT.Username, c.MQTT.Password = get("MQTT_USER", ""), get("MQTT_PASSWORD", "")
	c.MQTT.KeepAlive = 30 * time.Second
	broker, parseErr := url.Parse(c.MQTT.Broker)
	if parseErr != nil || broker.Hostname() == "" || broker.User != nil || broker.Fragment != "" {
		return fail("MQTT_BROKER")
	}
	switch broker.Scheme {
	case "tcp", "ssl", "tls", "mqtt", "mqtts", "ws", "wss":
	default:
		return fail("MQTT_BROKER")
	}
	if c.MQTT.ClientID == "" || strings.ContainsRune(c.MQTT.ClientID, 0) {
		return fail("MQTT_CLIENT_ID")
	}
	qos, err := strconv.ParseUint(get("MQTT_QOS", "0"), 10, 8)
	if err != nil || qos > 2 {
		return fail("MQTT_QOS")
	}
	c.MQTT.QoS = byte(qos)
	c.MQTTTopic = get("MQTT_TOPIC", "buderus/wp")
	if !validTopic(c.MQTTTopic) {
		return fail("MQTT_TOPIC")
	}
	if c.MQTTAvailability {
		c.MQTT.AvailabilityTopic = c.MQTTTopic + "/status"
		if len(c.MQTT.AvailabilityTopic) > 65535 {
			return fail("MQTT_TOPIC")
		}
	}
	if err := mqttpub.Validate(c.MQTT); err != nil {
		return Config{}, fmt.Errorf("invalid mqtt configuration: %w", err)
	}
	c.Mapping = get("MAPPING", "fields")
	if c.Mapping != "fields" && c.Mapping != "passthrough" {
		return fail("MAPPING")
	}
	c.Schedule = get("SCHEDULE", "@every 60s")
	if err := validateSchedule(c.Schedule); err != nil {
		return fail("SCHEDULE")
	}
	c.Location, err = time.LoadLocation(get("TZ", "UTC"))
	if err != nil || get("TZ", "UTC") == "" {
		return fail("TZ")
	}
	c.LogFormat = get("LOG_FORMAT", "json")
	if c.LogFormat != "json" && c.LogFormat != "text" {
		return fail("LOG_FORMAT")
	}
	switch get("LOG_LEVEL", "info") {
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "info":
		c.LogLevel = slog.LevelInfo
	case "warn":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		return fail("LOG_LEVEL")
	}
	return c, nil
}

func hasControl(value string) bool {
	return strings.IndexFunc(value, func(c rune) bool { return c < 32 || c == 127 }) >= 0
}

func validName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		allowed := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
		if !allowed {
			return false
		}
	}
	return true
}

func validTopic(topic string) bool {
	return topic != "" && len(topic) <= 65535 && utf8.ValidString(topic) && !strings.ContainsAny(topic, "+#\x00")
}

func validAPIURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.HasSuffix(u.Host, ":") {
		return false
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		return err == nil && number > 0 && number <= 65535
	}
	return true
}

func validateSchedule(schedule string) error {
	fields := strings.Fields(schedule)
	if len(fields) > 0 && fields[0] == "@every" {
		if len(fields) != 2 {
			return errors.New("invalid interval")
		}
		d, err := time.ParseDuration(fields[1])
		if err != nil || d <= 0 {
			return errors.New("interval must be positive")
		}
	}
	_, err := cron.ParseStandard(schedule)
	return err
}
