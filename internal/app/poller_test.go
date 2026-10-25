package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/internal/buderus"
)

// fakeFetcher liefert vordefinierte Antworten pro Pfad (≈ Mockito-Mock, nur handgeschrieben).
type fakeFetcher struct {
	responses map[string]buderus.EmonResponse
	errs      map[string]error
}

func (f *fakeFetcher) FetchEmon(_ context.Context, path string) (buderus.EmonResponse, error) {
	if err := f.errs[path]; err != nil {
		return buderus.EmonResponse{}, err
	}
	return f.responses[path], nil
}

// fakePublisher merkt sich alle veröffentlichten Nachrichten.
type fakePublisher struct {
	published map[string]string
}

func (f *fakePublisher) Publish(_ context.Context, topic string, payload []byte, _ bool) error {
	f.published[topic] = string(payload)
	return nil
}

func ptr(v float64) *float64 { return &v }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPollOnce(t *testing.T) {
	fetcher := &fakeFetcher{
		responses: map[string]buderus.EmonResponse{
			buderus.PathHeatingConsumption: {
				Unit:   "kWh",
				Values: buderus.EmonValues{Compressor: ptr(12.5), EHeater: ptr(0), OutputProduced: ptr(40)},
			},
		},
		errs: map[string]error{
			buderus.PathDHWConsumption: errors.New("gateway unreachable"),
		},
	}
	publisher := &fakePublisher{published: map[string]string{}}

	poller := NewPoller(fetcher, publisher, "buderus/wp", DefaultCircuits, discardLogger())
	poller.PollOnce(context.Background())

	// electricity wird aus compressor + eheater berechnet.
	want := `{"electricity":12.5,"compressor":12.5,"eheater":0,"outputProduced":40,"unit":"kWh"}`
	if got := publisher.published["buderus/wp/heating"]; got != want {
		t.Errorf("heating payload = %s, want %s", got, want)
	}
	// Der Fehler bei Warmwasser darf Heizung nicht verhindern und nichts veröffentlichen.
	if _, ok := publisher.published["buderus/wp/dhw"]; ok {
		t.Error("dhw should not be published on fetch error")
	}
}

func TestSensors(t *testing.T) {
	sensors := Sensors("buderus/wp", DefaultCircuits)
	if len(sensors) != 8 {
		t.Fatalf("got %d sensors, want 8", len(sensors))
	}

	keys := map[string]bool{}
	for _, s := range sensors {
		keys[s.Key] = true
	}
	for _, key := range []string{
		"heating_electricity", "heating_compressor", "heating_eheater", "heating_output_produced",
		"dhw_electricity", "dhw_compressor", "dhw_eheater", "dhw_output_produced",
	} {
		if !keys[key] {
			t.Errorf("missing sensor %q", key)
		}
	}
}
