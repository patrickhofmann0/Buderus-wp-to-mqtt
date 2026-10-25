// Package mqttpub provides a bounded, reconnecting MQTT publisher.
package mqttpub

import (
	"bytes"
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
	"github.com/eclipse/paho.mqtt.golang/packets"
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
		return errors.New("mqtt broker must be a valid url with a host")
	}
	switch u.Scheme {
	case "tcp", "mqtt", "ssl", "tls", "mqtts", "ws", "wss":
	default:
		return errors.New("mqtt broker scheme must be tcp, mqtt, ssl, tls, mqtts, ws, or wss")
	}
	if u.Fragment != "" || strings.HasSuffix(u.Host, ":") {
		return errors.New("invalid mqtt broker url")
	}
	if strings.Contains(u.Hostname(), ":") && net.ParseIP(strings.Split(u.Hostname(), "%")[0]) == nil {
		return errors.New("invalid mqtt broker host")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("invalid mqtt broker port")
		}
	}
	if cfg.QoS > 2 {
		return errors.New("mqtt qos must be 0, 1, or 2")
	}
	if cfg.Timeout < 0 || cfg.KeepAlive < 0 {
		return errors.New("mqtt timeout and keepalive must not be negative")
	}
	if cfg.KeepAlive/time.Second > 65535 {
		return errors.New("mqtt keepalive must not exceed 65535 seconds")
	}
	if !validString(cfg.ClientID) || !validString(cfg.Username) || len(cfg.Password) > 65535 {
		return errors.New("invalid mqtt client id or credentials")
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
		return errors.New("mqtt publish topic must be nonempty valid utf-8, at most 65535 bytes, without wildcards or nul")
	}
	return nil
}

// mqttClient is the consumer-owned subset of Paho used by Publisher.
type mqttClient interface {
	Connect() mqtt.Token
	IsConnectionOpen() bool
	Publish(string, byte, bool, interface{}) mqtt.Token
	Disconnect(uint)
}

// Paho 1.5.1's memory store aliases outgoing PUBLISH packets. Its reconnect
// replay mutates DUP while the writer can still be using that same packet.
// Isolate stored packets from both the outgoing writer and each replay.
type publishStore struct {
	mqtt.Store
	mu                sync.Mutex
	pending           map[string]bool // Outbound IDs: true for data, false for availability.
	availabilityTopic string
}

func newPublishStore(availabilityTopic string) *publishStore {
	return &publishStore{
		Store: mqtt.NewMemoryStore(), pending: make(map[string]bool),
		availabilityTopic: availabilityTopic,
	}
}

func copyPublish(packet packets.ControlPacket) packets.ControlPacket {
	switch pub := packet.(type) {
	case *packets.PublishPacket:
		copied := *pub
		copied.Payload = bytes.Clone(pub.Payload)
		return &copied
	case *packets.PubrelPacket:
		copied := *pub
		return &copied
	}
	return packet
}

func (s *publishStore) Put(key string, packet packets.ControlPacket) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pub, ok := packet.(*packets.PublishPacket); ok && strings.HasPrefix(key, "o.") {
		s.pending[key] = pub.TopicName != s.availabilityTopic
	}
	s.Store.Put(key, copyPublish(packet))
}

func (s *publishStore) Get(key string) packets.ControlPacket {
	return copyPublish(s.Store.Get(key))
}

func (s *publishStore) Del(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, key)
	s.Store.Del(key)
}

func (s *publishStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.pending)
	s.Store.Reset()
}

func (s *publishStore) hasPending(data bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, isData := range s.pending {
		if isData == data {
			return true
		}
	}
	return false
}

// ErrPending means an earlier data publication is still queued or unacknowledged.
// A timeout cancels waiting, not MQTT delivery; callers must not enqueue a retry.
var ErrPending = errors.New("mqtt publication is still pending")

// Publisher owns its MQTT connection. Close is safe to call more than once.
type Publisher struct {
	client mqttClient
	cfg    Config
	mu     sync.Mutex // Orders availability publications before graceful shutdown.
	closed atomic.Bool
	done   chan struct{}
	once   sync.Once
	err    error
	store  *publishStore
	sendMu sync.Mutex
	// Reserve before entering Paho, which may enqueue after our context expires.
	enqueuing bool
	pending   mqtt.Token
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
	p := &Publisher{cfg: cfg, done: make(chan struct{}), store: newPublishStore(cfg.AvailabilityTopic)}
	retryInterval := min(100*time.Millisecond, cfg.Timeout)
	opts := mqtt.NewClientOptions().
		AddBroker(u.String()).
		SetClientID(cfg.ClientID).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(retryInterval).
		SetConnectTimeout(cfg.Timeout).
		SetWriteTimeout(cfg.Timeout).
		SetStore(p.store).
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
		opts.SetWill(cfg.AvailabilityTopic, "offline", min(cfg.QoS, 1), true)
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
		return nil, &operationError{"mqtt connect failed", err}
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
		return errors.New("mqtt publisher is closed")
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
	if !client.IsConnectionOpen() {
		return mqtt.ErrNotConnected
	}
	if payload == "online" && p.store.hasPending(false) {
		// Paho replays the already queued online message on this connection.
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.Timeout)
	defer cancel()
	// QoS 1 retains a replayable PUBLISH until ACK. QoS 2 could retain only
	// PUBREL, which cannot restore online after the broker emits our last will.
	token := client.Publish(p.cfg.AvailabilityTopic, min(p.cfg.QoS, 1), true, []byte(payload))
	return waitToken(ctx, token, p.cfg.Timeout, nil)
}

func (p *Publisher) publish(ctx context.Context, client mqttClient, topic string, payload []byte, retain bool) error {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	p.sendMu.Lock()
	if p.enqueuing || p.store.hasPending(true) {
		p.sendMu.Unlock()
		return ErrPending
	}
	if p.pending != nil {
		select {
		case <-p.pending.Done():
			p.pending = nil
		default:
			p.sendMu.Unlock()
			return ErrPending
		}
	}
	// IsConnected also reports true during reconnect; that would let Paho retain
	// arbitrary application payloads offline, beyond the caller's deadline.
	if !client.IsConnectionOpen() {
		p.sendMu.Unlock()
		return mqtt.ErrNotConnected
	}
	p.enqueuing = true
	p.sendMu.Unlock()
	// Paho's enqueue itself may block until WriteTimeout. This buffered handoff
	// permits immediate context cancellation without any goroutine waiting on a
	// token; the enqueue goroutine is bounded by Paho's configured WriteTimeout.
	tokens := make(chan mqtt.Token, 1)
	payload = bytes.Clone(payload)
	go func() {
		token := client.Publish(topic, p.cfg.QoS, retain, payload)
		p.sendMu.Lock()
		p.pending = token
		p.enqueuing = false
		p.sendMu.Unlock()
		tokens <- token
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return errors.New("mqtt publisher is closed")
	case token := <-tokens:
		return waitToken(ctx, token, p.cfg.Timeout, p.done)
	}
}

// Publish attempts each message and joins all failures. Each message is bounded
// by Timeout and the caller's context; a cancelled context prevents new sends.
// Reconnecting clients reject data. At most one data publication may remain
// queued or unacknowledged, even after a timeout; later messages return ErrPending
// until it is acknowledged. Paho can still deliver that earlier message later.
func (p *Publisher) Publish(ctx context.Context, msgs ...mapper.Message) error {
	var errs []error
	for i, msg := range msgs {
		err := validateTopic(msg.Topic)
		if err == nil && p.cfg.AvailabilityTopic != "" && msg.Topic == p.cfg.AvailabilityTopic {
			err = errors.New("mqtt availability topic is reserved")
		}
		if err == nil {
			if p.closed.Load() {
				err = errors.New("mqtt publisher is closed")
			} else {
				err = p.publish(ctx, p.client, msg.Topic, msg.Payload, msg.Retain)
			}
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("message %d: %w", i, &operationError{"mqtt publish failed", err}))
		}
	}
	return errors.Join(errs...)
}

// Close publishes retained offline availability before disconnecting. It waits
// at most Timeout for that publication and Timeout for Paho's graceful shutdown,
// after any already-running availability callback has finished.
func (p *Publisher) Close() error {
	p.once.Do(func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.closed.Store(true)
		if p.cfg.AvailabilityTopic != "" {
			if err := p.availability(p.client, "offline"); err != nil {
				p.err = &operationError{"mqtt offline publication failed", err}
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
