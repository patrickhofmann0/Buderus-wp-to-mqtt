// Package mqttpub provides a bounded, reconnecting MQTT publisher.
package mqttpub

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/mapper"
)

const (
	defaultTimeout   = 5 * time.Second
	defaultKeepAlive = 30 * time.Second
	defaultClientID  = "buderus-wp-to-mqtt"
)

type Config struct {
	Broker            string
	ClientID          string
	Username          string
	Password          string
	QoS               byte
	Timeout           time.Duration
	KeepAlive         time.Duration
	AvailabilityTopic string
}

// Validate checks configuration without connecting. Zero durations and an empty
// ClientID select New's defaults; an empty AvailabilityTopic disables availability.
func Validate(cfg Config) error {
	u, err := url.Parse(cfg.Broker)
	if err != nil || u == nil || u.Hostname() == "" || u.Opaque != "" {
		return errors.New("MQTT broker must be a valid URL with a host")
	}
	switch u.Scheme {
	case "tcp", "mqtt", "ssl", "tls", "mqtts", "ws", "wss":
	default:
		return errors.New("MQTT broker scheme must be tcp, mqtt, ssl, tls, mqtts, ws, or wss")
	}
	if u.Fragment != "" || strings.HasSuffix(u.Host, ":") {
		return errors.New("invalid MQTT broker URL")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("invalid MQTT broker port")
		}
	}
	if cfg.QoS > 2 {
		return errors.New("MQTT QoS must be 0, 1, or 2")
	}
	if cfg.Timeout < 0 || cfg.KeepAlive < 0 {
		return errors.New("MQTT timeout and keepalive must not be negative")
	}
	if cfg.KeepAlive/time.Second > 65535 {
		return errors.New("MQTT keepalive must not exceed 65535 seconds")
	}
	if !validString(cfg.ClientID) || !validString(cfg.Username) || len(cfg.Password) > 65535 {
		return errors.New("invalid MQTT client ID or credentials")
	}
	if cfg.AvailabilityTopic != "" {
		return validateTopic(cfg.AvailabilityTopic)
	}
	return nil
}

func validString(s string) bool {
	return len(s) <= 65535 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

func validateTopic(topic string) error {
	if topic == "" || !validString(topic) || strings.ContainsAny(topic, "+#") {
		return errors.New("MQTT publish topic must be nonempty valid UTF-8, at most 65535 bytes, without wildcards or NUL")
	}
	return nil
}

// mqttClient is the consumer-owned subset of Paho used by Publisher.
type mqttClient interface {
	Connect() mqtt.Token
	Publish(string, byte, bool, interface{}) mqtt.Token
	Disconnect(uint)
}

// Publisher owns its MQTT connection. Close is safe to call more than once.
type Publisher struct {
	client mqttClient
	cfg    Config
	mu     sync.Mutex // Orders availability publications before graceful shutdown.
	closed atomic.Bool
	done   chan struct{}
	once   sync.Once
	err    error
}

// New connects within Timeout. Defaults are a 5s timeout, 30s keepalive and
// client ID "buderus-wp-to-mqtt". Brokers without a port use the scheme's default.
// Failed connections are disconnected, cancelling Paho's initial retry loop.
func New(cfg Config) (*Publisher, error) {
	return newPublisher(cfg, func(opts *mqtt.ClientOptions) mqttClient {
		return mqtt.NewClient(opts)
	})
}

func newPublisher(cfg Config, factory func(*mqtt.ClientOptions) mqttClient) (*Publisher, error) {
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.KeepAlive == 0 {
		cfg.KeepAlive = defaultKeepAlive
	}
	if cfg.ClientID == "" {
		cfg.ClientID = defaultClientID
	}
	u, _ := url.Parse(cfg.Broker)
	if u.Port() == "" {
		port := "1883"
		switch u.Scheme {
		case "ssl", "tls", "mqtts":
			port = "8883"
		case "ws":
			port = "80"
		case "wss":
			port = "443"
		}
		u.Host = net.JoinHostPort(u.Hostname(), port)
	}
	p := &Publisher{cfg: cfg, done: make(chan struct{})}
	retryInterval := min(100*time.Millisecond, cfg.Timeout)
	opts := mqtt.NewClientOptions().
		AddBroker(u.String()).
		SetClientID(cfg.ClientID).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(retryInterval).
		SetConnectTimeout(cfg.Timeout).
		SetWriteTimeout(cfg.Timeout).
		SetDialer(&net.Dialer{Timeout: cfg.Timeout}).
		SetProtocolVersion(4).
		SetKeepAlive(max(time.Second, cfg.KeepAlive))
	if cfg.Username != "" {
		opts.SetUsername(cfg.Username)
	}
	if cfg.Password != "" {
		opts.SetPassword(cfg.Password)
	}
	if cfg.AvailabilityTopic != "" {
		opts.SetWill(cfg.AvailabilityTopic, "offline", cfg.QoS, true)
		opts.SetOnConnectHandler(func(client mqtt.Client) {
			p.mu.Lock()
			defer p.mu.Unlock()
			if !p.closed.Load() {
				_ = p.availability(client, "online")
			}
		})
	}
	p.client = factory(opts)
	token := p.client.Connect()
	if err := waitToken(context.Background(), token, cfg.Timeout, nil); err != nil {
		p.mu.Lock()
		p.closed.Store(true)
		close(p.done)
		p.mu.Unlock()
		// Paho's Disconnect changes its state even during an initial handshake.
		// The retry goroutine then exits after its bounded dial and retry sleep.
		p.client.Disconnect(1)
		_ = waitToken(context.Background(), token, cfg.Timeout+retryInterval, nil)
		return nil, &operationError{"MQTT connect failed", err}
	}
	return p, nil
}

// operationError deliberately does not print transport errors, which can contain
// broker URLs (including passwords). Unwrap retains errors.Is/As semantics.
type operationError struct {
	message string
	err     error
}

func (e *operationError) Error() string { return e.message }
func (e *operationError) Unwrap() error { return e.err }

func waitToken(ctx context.Context, token mqtt.Token, timeout time.Duration, closed <-chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-closed:
		return errors.New("MQTT publisher is closed")
	case <-timer.C:
		return context.DeadlineExceeded
	case <-token.Done():
		if err := ctx.Err(); err != nil {
			return err
		}
		return token.Error()
	}
}

func (p *Publisher) availability(client mqttClient, payload string) error {
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.Timeout)
	defer cancel()
	token := client.Publish(p.cfg.AvailabilityTopic, p.cfg.QoS, true, []byte(payload))
	return waitToken(ctx, token, p.cfg.Timeout, nil)
}

func (p *Publisher) publish(ctx context.Context, client mqttClient, topic string, payload []byte, retain bool) error {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	// Paho's enqueue itself may block until WriteTimeout. This buffered handoff
	// permits immediate context cancellation without any goroutine waiting on a
	// token; the enqueue goroutine is bounded by Paho's configured WriteTimeout.
	tokens := make(chan mqtt.Token, 1)
	go func() { tokens <- client.Publish(topic, p.cfg.QoS, retain, payload) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return errors.New("MQTT publisher is closed")
	case token := <-tokens:
		return waitToken(ctx, token, p.cfg.Timeout, p.done)
	}
}

// Publish attempts each message and joins all failures. Each message is bounded
// by Timeout and the caller's context; a cancelled context prevents new sends.
func (p *Publisher) Publish(ctx context.Context, msgs ...mapper.Message) error {
	var errs []error
	for i, msg := range msgs {
		err := validateTopic(msg.Topic)
		if err == nil {
			if p.closed.Load() {
				err = errors.New("MQTT publisher is closed")
			} else {
				err = p.publish(ctx, p.client, msg.Topic, msg.Payload, msg.Retain)
			}
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("message %d: %w", i, &operationError{"MQTT publish failed", err}))
		}
	}
	return errors.Join(errs...)
}

// Close publishes retained offline availability before disconnecting. It waits
// at most Timeout for that publication and Timeout for Paho's graceful shutdown.
func (p *Publisher) Close() error {
	p.once.Do(func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.closed.Store(true)
		if p.cfg.AvailabilityTopic != "" {
			if err := p.availability(p.client, "offline"); err != nil {
				p.err = &operationError{"MQTT offline publication failed", err}
			}
		}
		close(p.done)
		quiesce := uint64(max(time.Millisecond, p.cfg.Timeout) / time.Millisecond)
		// Paho accepts a uint millisecond count; cap it before conversion.
		quiesce = min(quiesce, uint64(^uint(0)))
		p.client.Disconnect(uint(quiesce))
	})
	return p.err
}
