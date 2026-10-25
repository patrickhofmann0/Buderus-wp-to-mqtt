// Package mqtt kapselt die Verbindung zum MQTT-Broker und die Home-Assistant-Discovery.
package mqtt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// Payloads für das Availability-Topic (Online-Status des Programms).
const (
	PayloadOnline  = "online"
	PayloadOffline = "offline"
)

const publishTimeout = 10 * time.Second

// Options sind die Verbindungsdaten für den Broker.
type Options struct {
	Broker            string
	Username          string
	Password          string
	ClientID          string
	AvailabilityTopic string
}

// Client ist eine dünne Hülle um den Paho-Client.
type Client struct {
	paho              paho.Client
	availabilityTopic string
	logger            *slog.Logger
}

// Connect verbindet sich mit dem Broker und meldet das Programm als "online".
// Bei Verbindungsabbruch verbindet Paho sich automatisch neu.
func Connect(ctx context.Context, opts Options, logger *slog.Logger) (*Client, error) {
	c := &Client{availabilityTopic: opts.AvailabilityTopic, logger: logger}

	pahoOpts := paho.NewClientOptions().
		AddBroker(opts.Broker).
		SetClientID(opts.ClientID).
		SetUsername(opts.Username).
		SetPassword(opts.Password).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(10*time.Second).
		// Last Will: Der Broker setzt "offline", wenn die Verbindung unerwartet abbricht.
		SetWill(opts.AvailabilityTopic, PayloadOffline, 1, true).
		SetOnConnectHandler(c.onConnect).
		SetConnectionLostHandler(func(_ paho.Client, err error) {
			logger.Warn("mqtt connection lost", "error", err)
		})

	c.paho = paho.NewClient(pahoOpts)

	if err := waitForToken(ctx, c.paho.Connect()); err != nil {
		return nil, fmt.Errorf("connect to %s: %w", opts.Broker, err)
	}
	return c, nil
}

// onConnect wird bei jeder (Wieder-)Verbindung aufgerufen.
func (c *Client) onConnect(client paho.Client) {
	c.logger.Info("mqtt connected")
	token := client.Publish(c.availabilityTopic, 1, true, PayloadOnline)
	// Im Callback nicht blockierend warten, sondern Ergebnis asynchron loggen.
	go func() {
		if token.WaitTimeout(publishTimeout) && token.Error() != nil {
			c.logger.Error("publish availability failed", "error", token.Error())
		}
	}()
}

// Publish sendet eine Nachricht mit QoS 1.
func (c *Client) Publish(ctx context.Context, topic string, payload []byte, retain bool) error {
	if err := waitForToken(ctx, c.paho.Publish(topic, 1, retain, payload)); err != nil {
		return fmt.Errorf("publish %s: %w", topic, err)
	}
	return nil
}

// Close meldet das Programm als "offline" und trennt die Verbindung.
func (c *Client) Close(ctx context.Context) {
	if err := c.Publish(ctx, c.availabilityTopic, []byte(PayloadOffline), true); err != nil {
		c.logger.Warn("publish offline status failed", "error", err)
	}
	c.paho.Disconnect(250)
}

// waitForToken wartet auf ein Paho-Token, bricht aber ab, wenn der Context endet.
func waitForToken(ctx context.Context, token paho.Token) error {
	select {
	case <-token.Done():
		return token.Error()
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(publishTimeout):
		return errors.New("timeout waiting for mqtt broker")
	}
}
