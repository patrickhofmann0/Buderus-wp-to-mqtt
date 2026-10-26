// Command buderus2mqtt liest Messwerte einer Buderus-Wärmepumpe und veröffentlicht sie per MQTT.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/internal/app"
	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/internal/buderus"
	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/internal/config"
	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/internal/mqtt"
)

// version wird beim Build per -ldflags "-X main.version=..." gesetzt
// (≈ Implementation-Version im MANIFEST.MF).
var version = "dev"

func main() {
	// main bleibt minimal; die Logik steckt in run(), damit Fehler einheitlich behandelt werden.
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	// ctx wird bei SIGINT (Strg+C) oder SIGTERM (docker stop) beendet.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	availabilityTopic := cfg.TopicPrefix + "/status"
	mqttClient, err := mqtt.Connect(ctx, mqtt.Options{
		Broker:            cfg.MQTTBroker,
		Username:          cfg.MQTTUsername,
		Password:          cfg.MQTTPassword,
		ClientID:          "buderus2mqtt-" + cfg.DeviceID,
		AvailabilityTopic: availabilityTopic,
	}, logger)
	if err != nil {
		return err
	}
	// Beim Beenden "offline" melden; eigener Context, weil ctx dann schon beendet ist.
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		mqttClient.Close(closeCtx)
	}()

	if cfg.DiscoveryEnabled {
		if err := publishDiscovery(ctx, cfg, availabilityTopic, mqttClient); err != nil {
			return err
		}
	}

	buderusClient := buderus.NewClient(cfg.BuderusHost, cfg.BuderusPort, cfg.BuderusToken, logger)
	poller := app.NewPoller(buderusClient, mqttClient, cfg.TopicPrefix, app.DefaultCircuits, logger)

	logger.Info("started", "version", version, "gateway", cfg.BuderusHost, "interval", cfg.PollInterval, "topicPrefix", cfg.TopicPrefix)
	poller.Run(ctx, cfg.PollInterval)
	logger.Info("stopped")
	return nil
}

func publishDiscovery(ctx context.Context, cfg config.Config, availabilityTopic string, client *mqtt.Client) error {
	device := mqtt.Device{
		ID:           cfg.DeviceID,
		Name:         cfg.DeviceName,
		Manufacturer: "Buderus",
		Model:        "Wärmepumpe",
	}
	messages, err := mqtt.BuildDiscoveryMessages(cfg.HADiscoveryPrefix, availabilityTopic, device, app.Sensors(cfg.TopicPrefix, app.DefaultCircuits))
	if err != nil {
		return err
	}
	for _, m := range messages {
		if err := client.Publish(ctx, m.Topic, m.Payload, m.Retain); err != nil {
			return err
		}
	}
	return nil
}
