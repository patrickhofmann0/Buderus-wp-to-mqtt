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
	values    map[string]buderus.ValueResponse
	errs      map[string]error
}

func (f *fakeFetcher) FetchEmon(_ context.Context, path string) (buderus.EmonResponse, error) {
	if err := f.errs[path]; err != nil {
		return buderus.EmonResponse{}, err
	}
	return f.responses[path], nil
}

func (f *fakeFetcher) FetchValue(_ context.Context, path string) (buderus.ValueResponse, error) {
	if err := f.errs[path]; err != nil {
		return buderus.ValueResponse{}, err
	}
	return f.values[path], nil
}

// fakePublisher merkt sich alle veröffentlichten Nachrichten.
type fakePublisher struct {
	published map[string]string
	retained  map[string]bool
}

func (f *fakePublisher) Publish(_ context.Context, topic string, payload []byte, retain bool) error {
	f.published[topic] = string(payload)
	f.retained[topic] = retain
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
		values: map[string]buderus.ValueResponse{
			buderus.PathCompressorPower:    {Value: ptr(460), Unit: "W"},
			buderus.PathModulation:         {Value: ptr(13), Unit: "%"},
			buderus.PathOutdoorTemperature: {Value: ptr(7.5), UnitOfMeasure: "C", State: []map[string]float64{{"open": -32768}, {"short": 32767}}},
		},
		errs: map[string]error{
			buderus.PathDHWConsumption: errors.New("gateway unreachable"),
		},
	}
	publisher := &fakePublisher{published: map[string]string{}, retained: map[string]bool{}}

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
	if got := publisher.published["buderus/wp/power"]; got != `{"compressorPower":460,"modulation":13}` {
		t.Errorf("power payload = %s, want %s", got, `{"compressorPower":460,"modulation":13}`)
	}
	if !publisher.retained["buderus/wp/power"] {
		t.Error("power state should be retained")
	}
	if got := publisher.published["buderus/wp/temperatures"]; got != `{"outdoor":7.5}` {
		t.Errorf("temperature payload = %s, want %s", got, `{"outdoor":7.5}`)
	}
	if !publisher.retained["buderus/wp/temperatures"] {
		t.Error("temperature state should be retained")
	}
}

func TestPowerFetchErrorDoesNotStopEnergyPublishing(t *testing.T) {
	fetcher := &fakeFetcher{
		responses: map[string]buderus.EmonResponse{
			buderus.PathHeatingConsumption: {Unit: "kWh", Values: buderus.EmonValues{Compressor: ptr(12)}},
		},
		errs: map[string]error{
			buderus.PathCompressorPower: errors.New("power endpoint unavailable"),
		},
	}
	publisher := &fakePublisher{published: map[string]string{}, retained: map[string]bool{}}

	poller := NewPoller(fetcher, publisher, "buderus/wp", []Circuit{DefaultCircuits[0]}, discardLogger())
	poller.PollOnce(context.Background())

	if _, ok := publisher.published["buderus/wp/heating"]; !ok {
		t.Error("heating should still be published when the power endpoint fails")
	}
	if _, ok := publisher.published["buderus/wp/power"]; ok {
		t.Error("power should not be published when its fetch fails")
	}
}

func TestModulationFetchErrorStillPublishesCompressorPower(t *testing.T) {
	fetcher := &fakeFetcher{
		values: map[string]buderus.ValueResponse{
			buderus.PathCompressorPower: {Value: ptr(460), Unit: "W"},
		},
		errs: map[string]error{
			buderus.PathModulation: errors.New("modulation endpoint unavailable"),
		},
	}
	publisher := &fakePublisher{published: map[string]string{}, retained: map[string]bool{}}

	poller := NewPoller(fetcher, publisher, "buderus/wp", nil, discardLogger())
	poller.PollOnce(context.Background())

	if got := publisher.published["buderus/wp/power"]; got != `{"compressorPower":460}` {
		t.Errorf("power payload = %s, want compressor power only", got)
	}
}

func TestOutdoorSensorFaultIsNotPublished(t *testing.T) {
	fetcher := &fakeFetcher{
		values: map[string]buderus.ValueResponse{
			buderus.PathOutdoorTemperature: {
				Value: ptr(-32768),
				State: []map[string]float64{{"open": -32768}, {"short": 32767}},
			},
		},
	}
	publisher := &fakePublisher{published: map[string]string{}, retained: map[string]bool{}}

	poller := NewPoller(fetcher, publisher, "buderus/wp", nil, discardLogger())
	poller.PollOnce(context.Background())

	if _, ok := publisher.published["buderus/wp/temperatures"]; ok {
		t.Error("outdoor temperature sensor fault should not be published")
	}
}

func TestSensors(t *testing.T) {
	sensors := Sensors("buderus/wp", DefaultCircuits)
	if len(sensors) != 11 {
		t.Fatalf("got %d sensors, want 11", len(sensors))
	}

	keys := map[string]bool{}
	for _, s := range sensors {
		keys[s.Key] = true
	}
	for _, key := range []string{
		"heating_electricity", "heating_compressor", "heating_eheater", "heating_output_produced",
		"dhw_electricity", "dhw_compressor", "dhw_eheater", "dhw_output_produced",
		"compressor_power",
		"modulation",
		"outdoor_temperature",
	} {
		if !keys[key] {
			t.Errorf("missing sensor %q", key)
		}
	}
	foundCompressorPower, foundModulation, foundOutdoorTemperature := false, false, false
	for _, s := range sensors {
		if s.Key == "compressor_power" {
			foundCompressorPower = true
			if s.StateTopic != "buderus/wp/power" || s.JSONField != "compressorPower" || s.Unit != "W" || s.DeviceClass != "power" || s.StateClass != "measurement" {
				t.Errorf("unexpected compressor power sensor: %+v", s)
			}
		}
		if s.Key == "modulation" {
			foundModulation = true
			if s.StateTopic != "buderus/wp/power" || s.JSONField != "modulation" || s.Unit != "%" || s.DeviceClass != "" || s.StateClass != "measurement" {
				t.Errorf("unexpected modulation sensor: %+v", s)
			}
		}
		if s.Key == "outdoor_temperature" {
			foundOutdoorTemperature = true
			if s.StateTopic != "buderus/wp/temperatures" || s.JSONField != "outdoor" || s.Unit != "°C" || s.DeviceClass != "temperature" || s.StateClass != "measurement" {
				t.Errorf("unexpected outdoor temperature sensor: %+v", s)
			}
		}
	}
	if !foundCompressorPower {
		t.Error("compressor_power sensor not found")
	}
	if !foundModulation {
		t.Error("modulation sensor not found")
	}
	if !foundOutdoorTemperature {
		t.Error("outdoor_temperature sensor not found")
	}
}
