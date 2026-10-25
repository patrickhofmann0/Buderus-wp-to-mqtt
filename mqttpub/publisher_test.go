package mqttpub

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/mapper"
)

type fakeToken struct {
	done chan struct{}
	err  error
}

func completed(err error) *fakeToken {
	token := &fakeToken{done: make(chan struct{}), err: err}
	close(token.done)
	return token
}

func (t *fakeToken) Done() <-chan struct{} { return t.done }
func (t *fakeToken) Error() error          { return t.err }
func (t *fakeToken) Wait() bool            { panic("must select on Done, not Wait") }
func (t *fakeToken) WaitTimeout(time.Duration) bool {
	panic("must select on Done, not WaitTimeout")
}

type publication struct {
	topic   string
	qos     byte
	retain  bool
	payload string
}

type fakeClient struct {
	mu           sync.Mutex
	connect      mqtt.Token
	publish      func(publication) mqtt.Token
	publications []publication
	disconnects  int
	onDisconnect func()
}

func (c *fakeClient) Connect() mqtt.Token { return c.connect }
func (c *fakeClient) Publish(topic string, qos byte, retained bool, payload interface{}) mqtt.Token {
	pub := publication{topic, qos, retained, string(payload.([]byte))}
	c.mu.Lock()
	c.publications = append(c.publications, pub)
	c.mu.Unlock()
	if c.publish != nil {
		return c.publish(pub)
	}
	return completed(nil)
}
func (c *fakeClient) Disconnect(uint) {
	c.mu.Lock()
	c.disconnects++
	c.mu.Unlock()
	if c.onDisconnect != nil {
		c.onDisconnect()
	}
}

func fakePublisher(t *testing.T, cfg Config, client *fakeClient) (*Publisher, *mqtt.ClientOptions) {
	t.Helper()
	cfg.Broker = "tcp://broker"
	if cfg.Timeout == 0 {
		cfg.Timeout = time.Second
	}
	client.connect = completed(nil)
	var opts *mqtt.ClientOptions
	p, err := newPublisher(cfg, func(o *mqtt.ClientOptions) mqttClient {
		opts = o
		return client
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, opts
}

func TestValidate(t *testing.T) {
	for _, scheme := range []string{"tcp", "mqtt", "ssl", "tls", "mqtts", "ws", "wss"} {
		if err := Validate(Config{Broker: scheme + "://localhost"}); err != nil {
			t.Errorf("%s: %v", scheme, err)
		}
	}
	for _, cfg := range []Config{
		{}, {Broker: "localhost:1883"}, {Broker: "http://broker"},
		{Broker: "tcp://"}, {Broker: "tcp://host:"}, {Broker: "tcp://host:0"},
		{Broker: "tcp://host:65536"}, {Broker: "tcp://host:bad"},
		{Broker: "tcp://host/#fragment"}, {Broker: "tcp://host", QoS: 3},
		{Broker: "tcp://host", Timeout: -1}, {Broker: "tcp://host", KeepAlive: -1},
		{Broker: "tcp://host", KeepAlive: 65536 * time.Second},
		{Broker: "tcp://host", AvailabilityTopic: "a/+"},
		{Broker: "tcp://host", ClientID: "\x00"},
	} {
		if err := Validate(cfg); err == nil {
			t.Errorf("accepted invalid config: %+v", cfg)
		}
	}
	secret := "super-secret"
	if err := Validate(Config{Broker: "tcp://user:" + secret + "@host:bad"}); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe or absent validation error: %v", err)
	}
}

func TestDefaultsAndOptions(t *testing.T) {
	client := &fakeClient{connect: completed(nil)}
	p, err := newPublisher(Config{Broker: "tcp://broker", AvailabilityTopic: "prefix/status", QoS: 2}, func(o *mqtt.ClientOptions) mqttClient {
		if !o.AutoReconnect || !o.ConnectRetry || o.ConnectTimeout != defaultTimeout ||
			o.WriteTimeout != defaultTimeout || o.KeepAlive != 30 || o.ClientID != defaultClientID {
			t.Errorf("incorrect connection defaults: %+v", o)
		}
		if o.Servers[0].Host != "broker:1883" {
			t.Errorf("missing default port: %s", o.Servers[0])
		}
		if !o.WillEnabled || !o.WillRetained || o.WillQos != 2 ||
			o.WillTopic != "prefix/status" || string(o.WillPayload) != "offline" || o.OnConnect == nil {
			t.Error("incorrect last will")
		}
		return client
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	_, opts := fakePublisher(t, Config{}, &fakeClient{})
	if opts.WillEnabled || opts.OnConnect != nil {
		t.Error("availability must be opt-in")
	}
}

func TestPublishJoinsFailuresAndPreservesMessages(t *testing.T) {
	broken := errors.New("transport failed with ******host")
	client := &fakeClient{publish: func(pub publication) mqtt.Token {
		if pub.topic == "fail" {
			return completed(broken)
		}
		return completed(nil)
	}}
	p, _ := fakePublisher(t, Config{QoS: 2}, client)
	err := p.Publish(context.Background(),
		mapper.Message{Topic: "good", Payload: []byte("body"), Retain: true},
		mapper.Message{Topic: "fail"}, mapper.Message{Topic: "bad/#"},
		mapper.Message{Topic: "last"})
	if !errors.Is(err, broken) || strings.Contains(err.Error(), "password") {
		t.Fatalf("incorrect error: %v", err)
	}
	if errs, ok := err.(interface{ Unwrap() []error }); !ok || len(errs.Unwrap()) != 2 {
		t.Fatalf("expected joined failures: %v", err)
	}
	want := []publication{{"good", 2, true, "body"}, {"fail", 2, false, ""}, {"last", 2, false, ""}}
	if !reflect.DeepEqual(client.publications, want) {
		t.Fatalf("got %#v, want %#v", client.publications, want)
	}
}

func TestPublishTopicValidation(t *testing.T) {
	client := &fakeClient{}
	p, _ := fakePublisher(t, Config{}, client)
	for _, topic := range []string{"", "a/+", "a/#", "a\x00b", "\xff", strings.Repeat("a", 65536)} {
		if err := p.Publish(context.Background(), mapper.Message{Topic: topic}); err == nil {
			t.Errorf("accepted invalid topic length %d", len(topic))
		}
	}
	for _, topic := range []string{"/", "a//b", "日本語", strings.Repeat("a", 65535)} {
		if err := p.Publish(context.Background(), mapper.Message{Topic: topic}); err != nil {
			t.Errorf("rejected valid topic length %d: %v", len(topic), err)
		}
	}
	if len(client.publications) != 4 {
		t.Fatal("invalid topics reached MQTT client")
	}
}

func TestPublishCancellationAndTimeout(t *testing.T) {
	for _, test := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		want error
	}{
		{"cancelled", func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, cancel
		}, context.Canceled},
		{"deadline", func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 10*time.Millisecond)
		}, context.DeadlineExceeded},
		{"timeout", func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		}, context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{publish: func(publication) mqtt.Token {
				return &fakeToken{done: make(chan struct{})}
			}}
			p, _ := fakePublisher(t, Config{Timeout: 40 * time.Millisecond}, client)
			ctx, cancel := test.ctx()
			defer cancel()
			start := time.Now()
			err := p.Publish(ctx, mapper.Message{Topic: "test"})
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if time.Since(start) > 500*time.Millisecond {
				t.Fatal("publish did not return promptly")
			}
			if test.name == "cancelled" && len(client.publications) != 0 {
				t.Error("cancelled context sent a message")
			}
		})
	}
}

func TestCancellationDuringEnqueue(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	client := &fakeClient{publish: func(publication) mqtt.Token {
		close(entered)
		<-release
		return completed(nil)
	}}
	p, _ := fakePublisher(t, Config{}, client)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- p.Publish(ctx, mapper.Message{Topic: "test"}) }()
	<-entered
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Error("cancellation blocked behind enqueue")
	}
	close(release)
}

func TestConnectFailureCleanupAndSafeErrors(t *testing.T) {
	for _, pending := range []bool{false, true} {
		token := completed(errors.New("******host"))
		if pending {
			token = &fakeToken{done: make(chan struct{})}
		}
		client := &fakeClient{connect: token}
		if pending {
			client.onDisconnect = func() { close(token.done) }
		}
		p, err := newPublisher(Config{Broker: "tcp://host", Timeout: 10 * time.Millisecond},
			func(*mqtt.ClientOptions) mqttClient { return client })
		if p != nil || err == nil || strings.Contains(err.Error(), "secret") || client.disconnects != 1 {
			t.Fatalf("failed connect was not safely cleaned up: publisher=%v error=%v disconnects=%d", p, err, client.disconnects)
		}
		if pending && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout not preserved: %v", err)
		}
	}
}

func TestCloseBoundedAndIdempotent(t *testing.T) {
	client := &fakeClient{publish: func(publication) mqtt.Token {
		return &fakeToken{done: make(chan struct{})}
	}}
	p, _ := fakePublisher(t, Config{AvailabilityTopic: "status", Timeout: 20 * time.Millisecond}, client)
	start := time.Now()
	err := p.Close()
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("unbounded or missing close error: %v", err)
	}
	if p.Close() != err || client.disconnects != 1 {
		t.Fatal("close must be idempotent")
	}
	if err := p.Publish(context.Background(), mapper.Message{Topic: "test"}); err == nil {
		t.Fatal("publish after close succeeded")
	}
	if len(client.publications) != 1 || client.publications[0].payload != "offline" || !client.publications[0].retain {
		t.Fatal("close must first publish retained offline")
	}
}

// The mini broker uses only the standard library and inspects the actual wire
// format, rather than accepting assumptions shared with the MQTT library.
type wirePacket struct {
	header byte
	body   []byte
}

func readPacket(r *bufio.Reader) (wirePacket, error) {
	header, err := r.ReadByte()
	if err != nil {
		return wirePacket{}, err
	}
	size, multiplier := 0, 1
	for i := 0; i < 4; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return wirePacket{}, err
		}
		size += int(b&127) * multiplier
		if b&128 == 0 {
			body := make([]byte, size)
			_, err = io.ReadFull(r, body)
			return wirePacket{header, body}, err
		}
		multiplier *= 128
	}
	return wirePacket{}, errors.New("invalid remaining length")
}

func wireString(body *[]byte) (string, error) {
	if len(*body) < 2 {
		return "", io.ErrUnexpectedEOF
	}
	size := int(binary.BigEndian.Uint16((*body)[:2]))
	if len(*body) < 2+size {
		return "", io.ErrUnexpectedEOF
	}
	value := string((*body)[2 : 2+size])
	*body = (*body)[2+size:]
	return value, nil
}

func inspectConnect(packet wirePacket, availability bool) error {
	if packet.header != 0x10 || len(packet.body) < 10 {
		return errors.New("expected CONNECT")
	}
	body := packet.body
	protocol, err := wireString(&body)
	if err != nil || protocol != "MQTT" || len(body) < 4 || body[0] != 4 {
		return errors.New("incorrect MQTT protocol")
	}
	flags := body[1]
	body = body[4:]
	id, err := wireString(&body)
	if err != nil || id != defaultClientID {
		return errors.New("incorrect client ID")
	}
	if availability {
		if flags&0x2c != 0x2c {
			return errors.New("will must be retained QoS 1")
		}
		topic, err := wireString(&body)
		if err != nil || topic != "prefix/status" {
			return errors.New("incorrect will topic")
		}
		payload, err := wireString(&body)
		if err != nil || payload != "offline" {
			return errors.New("incorrect will payload")
		}
	} else if flags&4 != 0 {
		return errors.New("availability unexpectedly enabled")
	}
	username, err := wireString(&body)
	if err != nil || username != "user" {
		return errors.New("incorrect username")
	}
	password, err := wireString(&body)
	if err != nil || password != "password" {
		return errors.New("incorrect password")
	}
	return nil
}

func TestMQTTWireAvailabilityAndReconnect(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	events := make(chan publication, 8)
	finished := make(chan error, 1)
	go func() {
		for attempt := 0; attempt < 2; attempt++ {
			conn, err := listener.Accept()
			if err != nil {
				finished <- err
				return
			}
			err = serveConnection(conn, events, attempt == 0)
			_ = conn.Close()
			if err != nil {
				finished <- err
				return
			}
		}
		finished <- nil
	}()
	p, err := New(Config{
		Broker: "tcp://" + listener.Addr().String(), Username: "user", Password: "password",
		QoS: 1, Timeout: time.Second, AvailabilityTopic: "prefix/status",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	next := func(want publication) {
		t.Helper()
		select {
		case got := <-events:
			if got != want {
				t.Fatalf("got %#v, want %#v", got, want)
			}
		case err := <-finished:
			t.Fatalf("broker exited early: %v", err)
		case <-time.After(4 * time.Second):
			t.Fatal("timed out waiting for MQTT publication")
		}
	}
	next(publication{"prefix/status", 1, true, "online"})
	next(publication{"prefix/status", 1, true, "online"})
	if err := p.Publish(context.Background(), mapper.Message{Topic: "data", Payload: []byte("value"), Retain: true}); err != nil {
		t.Fatal(err)
	}
	next(publication{"data", 1, true, "value"})
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	// The broker may have already finished after receiving DISCONNECT.
	select {
	case got := <-events:
		if got != (publication{"prefix/status", 1, true, "offline"}) {
			t.Fatalf("unexpected shutdown publication: %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("missing offline publication")
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("broker did not receive DISCONNECT")
	}
}

func serveConnection(conn net.Conn, events chan<- publication, dropAfterOnline bool) error {
	if err := conn.SetDeadline(time.Now().Add(6 * time.Second)); err != nil {
		return err
	}
	r := bufio.NewReader(conn)
	packet, err := readPacket(r)
	if err != nil {
		return err
	}
	if err := inspectConnect(packet, true); err != nil {
		return err
	}
	if _, err := conn.Write([]byte{0x20, 2, 0, 0}); err != nil {
		return err
	}
	for {
		packet, err := readPacket(r)
		if err != nil {
			return err
		}
		switch packet.header >> 4 {
		case 3:
			body := packet.body
			topic, err := wireString(&body)
			if err != nil || len(body) < 2 {
				return errors.New("invalid PUBLISH")
			}
			if (packet.header>>1)&3 != 1 {
				return errors.New("expected QoS 1")
			}
			ack := []byte{0x40, 2, body[0], body[1]}
			pub := publication{topic, 1, packet.header&1 != 0, string(body[2:])}
			if _, err := conn.Write(ack); err != nil {
				return err
			}
			events <- pub
			if dropAfterOnline {
				return nil
			}
		case 12:
			if _, err := conn.Write([]byte{0xd0, 0}); err != nil {
				return err
			}
		case 14:
			return nil
		default:
			return fmt.Errorf("unexpected MQTT packet type %d", packet.header>>4)
		}
	}
}

func TestInitialHandshakeTimeoutStopsRetrying(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		if _, err := readPacket(bufio.NewReader(conn)); err != nil {
			serverDone <- err
			return
		}
		// Withhold CONNACK. A timed-out handshake must close the socket.
		buffer := make([]byte, 1)
		_, err = conn.Read(buffer)
		if !errors.Is(err, io.EOF) {
			serverDone <- fmt.Errorf("connection not closed: %w", err)
			return
		}
		tcp := listener.(*net.TCPListener)
		_ = tcp.SetDeadline(time.Now().Add(150 * time.Millisecond))
		extra, err := tcp.Accept()
		if err == nil {
			_ = extra.Close()
			serverDone <- errors.New("initial retries continued after failure")
			return
		}
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			serverDone <- err
			return
		}
		serverDone <- nil
	}()
	start := time.Now()
	p, err := New(Config{Broker: "tcp://" + listener.Addr().String(), Timeout: 40 * time.Millisecond})
	if p != nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("initial connection was not bounded: %v, %v", p, err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retry cleanup did not finish")
	}
}
