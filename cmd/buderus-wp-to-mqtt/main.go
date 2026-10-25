package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/config"
	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/mapper"
	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/mqttpub"
	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/restclient"
	"github.com/robfig/cron/v3"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("service stopped", "error", err)
		os.Exit(1)
	}
}

type fetcher interface {
	Fetch(context.Context) ([]byte, error)
}

type publisher interface {
	Publish(context.Context, ...mapper.Message) error
}

type source struct {
	name   string
	client fetcher
	mapper mapper.Mapper
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	options := &slog.HandlerOptions{Level: cfg.LogLevel}
	var handler slog.Handler = slog.NewJSONHandler(os.Stdout, options)
	if cfg.LogFormat == "text" {
		handler = slog.NewTextHandler(os.Stdout, options)
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)
	sources := make([]source, 0, len(cfg.Endpoints))
	for _, endpoint := range cfg.Endpoints {
		clientOptions := []restclient.Option{
			restclient.WithTimeout(cfg.APITimeout),
			restclient.WithMaxBodySize(cfg.APIMaxBodyBytes),
			restclient.WithInsecureSkipVerify(cfg.APIInsecureSkipVerify),
		}
		if cfg.APIToken != "" {
			clientOptions = append(clientOptions, restclient.WithBearerToken(cfg.APIToken))
		}
		if cfg.APIUser != "" {
			clientOptions = append(clientOptions, restclient.WithBasicAuth(cfg.APIUser, cfg.APIPassword))
		}
		client, err := restclient.New(endpoint.URL, clientOptions...)
		if err != nil {
			return fmt.Errorf("create rest client: %w", err)
		}
		topic := cfg.MQTTTopic
		if endpoint.Name != "" {
			topic += "/" + endpoint.Name
		}
		var mapping mapper.Mapper = mapper.JSONFields{Prefix: topic, Retain: cfg.MQTTRetain}
		if cfg.Mapping == "passthrough" {
			mapping = mapper.Passthrough{Topic: topic, Retain: cfg.MQTTRetain}
		}
		sources = append(sources, source{name: endpoint.Name, client: client, mapper: mapping})
	}
	if err := ctx.Err(); err != nil {
		return nil
	}
	pub, err := mqttpub.New(cfg.MQTT)
	if err != nil {
		return fmt.Errorf("create mqtt publisher: %w", err)
	}
	defer func() {
		if err := pub.Close(); err != nil {
			logger.Error("close mqtt publisher", "error", err)
		}
	}()

	cronLog := cronLogger{logger}
	scheduler := cron.New(cron.WithLocation(cfg.Location), cron.WithLogger(cronLog))
	job := cron.NewChain(cron.SkipIfStillRunning(cronLog)).Then(cron.FuncJob(func() {
		poll(ctx, sources, pub, cfg.MQTT.AvailabilityTopic, logger)
	}))
	schedule, err := cron.ParseStandard(cfg.Schedule)
	if err != nil {
		return fmt.Errorf("parse schedule: %w", err)
	}
	scheduler.Schedule(schedule, job)
	scheduler.Start()
	logger.Info("service started", "endpoints", len(sources), "schedule", cfg.Schedule, "timezone", cfg.Location.String())
	if cfg.PollOnStart {
		job.Run()
	}
	<-ctx.Done()
	<-scheduler.Stop().Done()
	logger.Info("service stopped")
	return nil
}

func poll(ctx context.Context, sources []source, pub publisher, availabilityTopic string, logger *slog.Logger) {
	for _, src := range sources {
		if ctx.Err() != nil {
			return
		}
		if err := transfer(ctx, src, pub, availabilityTopic); err != nil {
			if ctx.Err() == nil {
				logger.Error("poll failed", "endpoint", src.name, "error", err)
			}
			continue
		}
		logger.Debug("poll completed", "endpoint", src.name)
	}
}

func transfer(ctx context.Context, src source, pub publisher, availabilityTopic string) error {
	body, err := src.client.Fetch(ctx)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	messages, err := src.mapper.Map(body)
	if err != nil {
		return fmt.Errorf("map response: %w", err)
	}
	for _, msg := range messages {
		if availabilityTopic != "" && msg.Topic == availabilityTopic {
			return errors.New("mapped topic conflicts with mqtt availability topic")
		}
	}
	if err := pub.Publish(ctx, messages...); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	return nil
}

type cronLogger struct{ logger *slog.Logger }

func (l cronLogger) Info(msg string, keysAndValues ...interface{}) {
	l.logger.Debug(msg, keysAndValues...)
}

func (l cronLogger) Error(err error, msg string, keysAndValues ...interface{}) {
	l.logger.Error(msg, append(keysAndValues, "error", err)...)
}
