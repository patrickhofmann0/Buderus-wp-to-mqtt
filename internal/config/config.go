// Package config liest die Konfiguration aus Umgebungsvariablen.
//
// Für Java-Entwickler: Vergleichbar mit einer @ConfigurationProperties-Klasse,
// nur ohne Framework – die Werte werden explizit in Load() gelesen.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Config enthält alle Einstellungen der Anwendung.
type Config struct {
	BuderusHost  string
	BuderusPort  int
	BuderusToken string

	MQTTBroker   string
	MQTTUsername string
	MQTTPassword string
	TopicPrefix  string

	DiscoveryEnabled  bool
	HADiscoveryPrefix string

	DeviceName string
	DeviceID   string

	PollInterval time.Duration
	LogLevel     slog.Level
}

// Load liest die Konfiguration aus den Umgebungsvariablen des Prozesses.
func Load() (Config, error) {
	return load(os.Getenv)
}

// load ist von der Quelle der Variablen entkoppelt, damit Tests eine Map übergeben können.
// In Go übergibt man dafür einfach eine Funktion statt ein Interface zu definieren.
func load(getenv func(string) string) (Config, error) {
	// errs sammelt alle Fehler, damit der Nutzer alle Probleme auf einmal sieht.
	var errs []error

	cfg := Config{
		BuderusHost:       getenv("BUDERUS_HOST"),
		BuderusToken:      getenv("BUDERUS_TOKEN"),
		MQTTBroker:        getenv("MQTT_BROKER"),
		MQTTUsername:      getenv("MQTT_USERNAME"),
		MQTTPassword:      getenv("MQTT_PASSWORD"),
		TopicPrefix:       normalizePrefix(withDefault(getenv("MQTT_TOPIC_PREFIX"), "buderus/wp")),
		HADiscoveryPrefix: normalizePrefix(withDefault(getenv("MQTT_HA_DISCOVERY_PREFIX"), "homeassistant")),
		DeviceName:        withDefault(getenv("DEVICE_NAME"), "Buderus Wärmepumpe"),
	}

	cfg.DeviceID = getenv("DEVICE_ID")
	if cfg.DeviceID == "" {
		cfg.DeviceID = Slugify(cfg.DeviceName)
	}

	required := map[string]string{
		"BUDERUS_HOST":  cfg.BuderusHost,
		"BUDERUS_TOKEN": cfg.BuderusToken,
		"MQTT_BROKER":   cfg.MQTTBroker,
	}
	for _, name := range []string{"BUDERUS_HOST", "BUDERUS_TOKEN", "MQTT_BROKER"} {
		if required[name] == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
	}

	port, err := strconv.Atoi(withDefault(getenv("BUDERUS_PORT"), "9443"))
	if err != nil {
		errs = append(errs, fmt.Errorf("BUDERUS_PORT: %w", err))
	}
	cfg.BuderusPort = port

	cfg.DiscoveryEnabled, err = strconv.ParseBool(withDefault(getenv("MQTT_DISCOVERY_ENABLED"), "true"))
	if err != nil {
		errs = append(errs, fmt.Errorf("MQTT_DISCOVERY_ENABLED: %w", err))
	}

	cfg.PollInterval, err = time.ParseDuration(withDefault(getenv("POLL_INTERVAL"), "5m"))
	if err != nil {
		errs = append(errs, fmt.Errorf("POLL_INTERVAL: %w", err))
	} else if cfg.PollInterval < 10*time.Second {
		errs = append(errs, errors.New("POLL_INTERVAL must be at least 10s"))
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(withDefault(getenv("LOG_LEVEL"), "info"))); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %w", err))
	}

	if cfg.TopicPrefix == "" {
		errs = append(errs, errors.New("MQTT_TOPIC_PREFIX must not be empty"))
	}

	// errors.Join liefert nil, wenn die Liste leer ist.
	return cfg, errors.Join(errs...)
}

func withDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// normalizePrefix entfernt führende und abschließende Slashes, z. B. "buderus/wp/" -> "buderus/wp".
func normalizePrefix(prefix string) string {
	return strings.Trim(strings.TrimSpace(prefix), "/")
}

// Slugify erzeugt aus einem Anzeigenamen eine ID aus [a-z0-9_], z. B. "Buderus Wärmepumpe" -> "buderus_warmepumpe".
func Slugify(name string) string {
	replacer := strings.NewReplacer("ä", "a", "ö", "o", "ü", "u", "ß", "ss", "Ä", "a", "Ö", "o", "Ü", "u")
	name = strings.ToLower(replacer.Replace(name))

	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastUnderscore = false
		} else if !lastUnderscore && b.Len() > 0 {
			b.WriteRune('_')
			lastUnderscore = true
		}
	}
	return strings.TrimSuffix(b.String(), "_")
}
