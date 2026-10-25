package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/mapper"
)

type fakeFetcher struct{ err error }

func (f fakeFetcher) Fetch(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []byte(`{"temp":21}`), f.err
}

type fakePublisher struct {
	messages []mapper.Message
	err      error
}

func (p *fakePublisher) Publish(_ context.Context, msgs ...mapper.Message) error {
	p.messages = append(p.messages, msgs...)
	return p.err
}

func TestTransfer(t *testing.T) {
	sentinel := errors.New("test failure")
	tests := []struct {
		name    string
		src     source
		pubErr  error
		wantErr error
		fail    bool
	}{
		{"success", source{client: fakeFetcher{}, mapper: mapper.JSONFields{Prefix: "wp"}}, nil, nil, false},
		{"fetch error", source{client: fakeFetcher{sentinel}}, nil, sentinel, true},
		{"map error", source{client: fakeFetcher{}, mapper: mapper.Func(func([]byte) ([]mapper.Message, error) {
			return nil, sentinel
		})}, nil, sentinel, true},
		{"publish error", source{client: fakeFetcher{}, mapper: mapper.Passthrough{Topic: "wp"}}, sentinel, sentinel, true},
		{"reserved status", source{client: fakeFetcher{}, mapper: mapper.Passthrough{Topic: "wp/status"}}, nil, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := &fakePublisher{err: tt.pubErr}
			err := transfer(context.Background(), tt.src, pub, "wp/status")
			if (err != nil) != tt.fail || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) {
				t.Fatalf("transfer() = %v", err)
			}
			if !tt.fail && (len(pub.messages) != 1 || pub.messages[0].Topic != "wp/temp" || string(pub.messages[0].Payload) != "21") {
				t.Fatalf("messages = %v", pub.messages)
			}
			if tt.name == "reserved status" && len(pub.messages) != 0 {
				t.Fatal("availability overwritten")
			}
		})
	}
}

func TestPollContinuesAndCancels(t *testing.T) {
	pub := &fakePublisher{}
	sources := []source{
		{name: "broken", client: fakeFetcher{errors.New("unavailable")}},
		{name: "working", client: fakeFetcher{}, mapper: mapper.Passthrough{Topic: "wp"}},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	poll(context.Background(), sources, pub, "", logger)
	if len(pub.messages) != 1 {
		t.Fatal("failure prevented remaining endpoints")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	poll(ctx, sources, pub, "", logger)
	if len(pub.messages) != 1 {
		t.Fatal("canceled poll published messages")
	}
}
