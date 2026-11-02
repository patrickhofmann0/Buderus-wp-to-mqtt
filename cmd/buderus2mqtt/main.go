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
	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/internal/web"
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
	buderusClient := buderus.NewClient(cfg.BuderusHost, cfg.BuderusPort, cfg.BuderusToken, logger)
	heatingCircuits := app.DetectHeatingCircuits(ctx, buderusClient, logger)
	volumeFlows := app.DetectVolumeFlows(ctx, buderusClient, logger)

	if cfg.DiscoveryEnabled {
		if err := publishDiscovery(ctx, cfg, availabilityTopic, mqttClient, heatingCircuits, volumeFlows); err != nil {
			return err
		}
	}

	poller := app.NewPoller(buderusClient, mqttClient, cfg.TopicPrefix, app.DefaultCircuits, heatingCircuits, logger)
	poller.SetVolumeFlows(volumeFlows)

	// pollCtx endet bei Programmende oder wenn der Webserver ausfällt (z. B. Port belegt).
	pollCtx, cancelPoll := context.WithCancel(ctx)
	defer cancelPoll()

	// Der Webserver läuft parallel als Goroutine; webErr liefert sein Ergebnis.
	webErr := make(chan error, 1)
	if cfg.WebEnabled {
		store := web.NewStore(version, cfg.PollInterval, webHeatingCircuits(heatingCircuits))
		poller.SetRecorder(store)
		go func() {
			err := web.Serve(ctx, cfg.WebListen, web.NewHandler(store, logger), logger)
			if err != nil {
				cancelPoll()
			}
			webErr <- err
		}()
	} else {
		webErr <- nil
	}

	logger.Info("started", "version", version, "gateway", cfg.BuderusHost, "interval", cfg.PollInterval, "topicPrefix", cfg.TopicPrefix, "web", cfg.WebEnabled)
	poller.Run(pollCtx, cfg.PollInterval)
	// Auf das geordnete Herunterfahren des Webservers warten.
	err = <-webErr
	logger.Info("stopped")
	return err
}

func webHeatingCircuits(circuits []app.HeatingCircuit) []web.HeatingCircuit {
	result := make([]web.HeatingCircuit, 0, len(circuits))
	for _, c := range circuits {
		result = append(result, web.HeatingCircuit{Key: c.Key, Label: c.Label, HasMixer: c.HasMixer})
	}
	return result
}

func publishDiscovery(ctx context.Context, cfg config.Config, availabilityTopic string, client *mqtt.Client, heatingCircuits []app.HeatingCircuit, volumeFlows app.VolumeFlows) error {
	device := mqtt.Device{
		ID:           cfg.DeviceID,
		Name:         cfg.DeviceName,
		Manufacturer: "Buderus",
		Model:        "Wärmepumpe",
	}
	messages, err := mqtt.BuildDiscoveryMessages(cfg.HADiscoveryPrefix, availabilityTopic, device, app.Sensors(cfg.TopicPrefix, app.DefaultCircuits, heatingCircuits, volumeFlows))
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
