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
	strings   map[string]buderus.StringResponse
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

func (f *fakeFetcher) FetchString(_ context.Context, path string) (buderus.StringResponse, error) {
	if err := f.errs[path]; err != nil {
		return buderus.StringResponse{}, err
	}
	return f.strings[path], nil
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
			buderus.PathCompressorPower:                              {Value: ptr(460), Unit: "W"},
			buderus.PathModulation:                                   {Value: ptr(13), Unit: "%"},
			buderus.PathOutdoorTemperature:                           {Value: ptr(7.5), UnitOfMeasure: "C", State: []map[string]float64{{"open": -32768}, {"short": 32767}}},
			buderus.PathSupplyTemperature:                            {Value: ptr(30.6), UnitOfMeasure: "C", State: []map[string]float64{{"open": -32768}, {"short": 32767}}},
			buderus.PathReturnTemperature:                            {Value: ptr(30), UnitOfMeasure: "C", State: []map[string]float64{{"open": -32768}, {"short": 32767}}},
			buderus.DHWCircuitPath("dhw1", "actualTemp"):             {Value: ptr(43), UnitOfMeasure: "C", State: []map[string]float64{{"open": -32768}, {"short": 32767}}},
			buderus.DHWCircuitPath("dhw1", "currentSetpoint"):        {Value: ptr(48), UnitOfMeasure: "C"},
			buderus.HeatingCircuitPath("hc1", "currentRoomSetpoint"): {Value: ptr(21), UnitOfMeasure: "C", State: []map[string]float64{{"off": 0}}},
			buderus.HeatingCircuitPath("hc2", "currentRoomSetpoint"): {Value: ptr(0), UnitOfMeasure: "C", State: []map[string]float64{{"off": 0}}},
		},
		strings: map[string]buderus.StringResponse{
			buderus.HeatingCircuitPath("hc1", "currentSuWiMode"): {Value: "forced"},
			buderus.HeatingCircuitPath("hc2", "currentSuWiMode"): {Value: "off"},
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
	if got := publisher.published["buderus/wp/temperatures"]; got != `{"outdoor":7.5,"supply":30.6,"return":30,"dhwActual":43,"dhwSetpoint":48}` {
		t.Errorf("temperature payload = %s, want all valid temperatures", got)
	}
	if !publisher.retained["buderus/wp/temperatures"] {
		t.Error("temperature state should be retained")
	}
	if got := publisher.published["buderus/wp/hc1"]; got != `{"summerWinterMode":"Winter","roomSetpoint":21}` {
		t.Errorf("hc1 payload = %s, want Winter mode and 21 °C setpoint", got)
	}
	if got := publisher.published["buderus/wp/hc2"]; got != `{"summerWinterMode":"Sommer"}` {
		t.Errorf("hc2 payload = %s, want Sommer mode without an off setpoint", got)
	}
	if !publisher.retained["buderus/wp/hc1"] || !publisher.retained["buderus/wp/hc2"] {
		t.Error("heating circuit mode states should be retained")
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

func TestTemperatureFaultAndOutlierOmitOnlyInvalidFields(t *testing.T) {
	fetcher := &fakeFetcher{
		values: map[string]buderus.ValueResponse{
			buderus.PathOutdoorTemperature: {
				Value: ptr(-32768),
				State: []map[string]float64{{"open": -32768}, {"short": 32767}},
			},
			buderus.PathSupplyTemperature: {Value: ptr(31)},
			buderus.PathReturnTemperature: {
				Value: ptr(-3276.8),
				State: []map[string]float64{{"open": -32768}, {"short": 32767}},
			},
			buderus.DHWCircuitPath("dhw1", "actualTemp"):      {Value: ptr(44)},
			buderus.DHWCircuitPath("dhw1", "currentSetpoint"): {Value: ptr(50)},
		},
	}
	publisher := &fakePublisher{published: map[string]string{}, retained: map[string]bool{}}

	poller := NewPoller(fetcher, publisher, "buderus/wp", nil, discardLogger())
	poller.PollOnce(context.Background())

	want := `{"supply":31,"dhwActual":44,"dhwSetpoint":50}`
	if got := publisher.published["buderus/wp/temperatures"]; got != want {
		t.Errorf("temperature payload = %s, want %s", got, want)
	}
}

func TestHeatingCircuitModeErrorsAreIndependent(t *testing.T) {
	hc1Path := buderus.HeatingCircuitPath("hc1", "currentSuWiMode")
	hc2Path := buderus.HeatingCircuitPath("hc2", "currentSuWiMode")
	fetcher := &fakeFetcher{
		strings: map[string]buderus.StringResponse{
			hc2Path: {Value: "cooling"},
		},
		errs: map[string]error{
			hc1Path: errors.New("hc1 unavailable"),
		},
	}
	publisher := &fakePublisher{published: map[string]string{}, retained: map[string]bool{}}

	NewPoller(fetcher, publisher, "buderus/wp", nil, discardLogger()).PollOnce(context.Background())

	if _, ok := publisher.published["buderus/wp/hc1"]; ok {
		t.Error("hc1 should not be published after fetch error")
	}
	if got := publisher.published["buderus/wp/hc2"]; got != `{"summerWinterMode":"Kühlen"}` {
		t.Errorf("hc2 payload = %s, want Kühlen mode", got)
	}
}

func TestHeatingCircuitPublishesAvailableFieldWhenOtherFetchFails(t *testing.T) {
	modePath := buderus.HeatingCircuitPath("hc1", "currentSuWiMode")
	setpointPath := buderus.HeatingCircuitPath("hc1", "currentRoomSetpoint")
	tests := []struct {
		name      string
		fetcher   *fakeFetcher
		wantState string
	}{
		{
			name: "room setpoint when mode fetch fails",
			fetcher: &fakeFetcher{
				values: map[string]buderus.ValueResponse{setpointPath: {Value: ptr(20.5)}},
				errs:   map[string]error{modePath: errors.New("mode endpoint unavailable")},
			},
			wantState: `{"roomSetpoint":20.5}`,
		},
		{
			name: "mode when room setpoint fetch fails",
			fetcher: &fakeFetcher{
				strings: map[string]buderus.StringResponse{modePath: {Value: "forced"}},
				errs:    map[string]error{setpointPath: errors.New("setpoint endpoint unavailable")},
			},
			wantState: `{"summerWinterMode":"Winter"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publisher := &fakePublisher{published: map[string]string{}, retained: map[string]bool{}}
			poller := NewPoller(tt.fetcher, publisher, "buderus/wp", nil, discardLogger())
			if err := poller.pollHeatingCircuit(context.Background(), DefaultHeatingCircuits[0]); err != nil {
				t.Fatalf("pollHeatingCircuit() error = %v", err)
			}
			if got := publisher.published["buderus/wp/hc1"]; got != tt.wantState {
				t.Errorf("state = %s, want %s", got, tt.wantState)
			}
		})
	}
}

func TestUnknownHeatingCircuitModeIsNotPublished(t *testing.T) {
	fetcher := &fakeFetcher{
		strings: map[string]buderus.StringResponse{
			buderus.HeatingCircuitPath("hc1", "currentSuWiMode"): {Value: "unexpected"},
		},
	}
	publisher := &fakePublisher{published: map[string]string{}, retained: map[string]bool{}}

	NewPoller(fetcher, publisher, "buderus/wp", nil, discardLogger()).PollOnce(context.Background())

	if _, ok := publisher.published["buderus/wp/hc1"]; ok {
		t.Error("unknown mode should not be published")
	}
}

func TestTranslateSummerWinterMode(t *testing.T) {
	tests := map[string]string{
		"forced":  "Winter",
		"off":     "Sommer",
		"cooling": "Kühlen",
	}
	for input, want := range tests {
		got, ok := translateSummerWinterMode(input)
		if !ok || got != want {
			t.Errorf("translateSummerWinterMode(%q) = %q, %t; want %q, true", input, got, ok, want)
		}
	}
	if got, ok := translateSummerWinterMode("unknown"); ok || got != "" {
		t.Errorf("translateSummerWinterMode(unknown) = %q, %t; want empty, false", got, ok)
	}
}

func TestSensors(t *testing.T) {
	sensors := Sensors("buderus/wp", DefaultCircuits)
	if len(sensors) != 19 {
		t.Fatalf("got %d sensors, want 19", len(sensors))
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
		"supply_temperature",
		"return_temperature",
		"dhw_temperature",
		"dhw_setpoint",
		"hc1_summer_winter_mode",
		"hc2_summer_winter_mode",
		"hc1_room_setpoint",
		"hc2_room_setpoint",
	} {
		if !keys[key] {
			t.Errorf("missing sensor %q", key)
		}
	}
	foundCompressorPower, foundModulation, foundOutdoorTemperature := false, false, false
	foundTemperatures := map[string]bool{}
	foundHeatingModes := map[string]bool{}
	foundRoomSetpoints := map[string]bool{}
	roomSetpointTopics := map[string]string{
		"hc1_room_setpoint": "buderus/wp/hc1",
		"hc2_room_setpoint": "buderus/wp/hc2",
	}
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
		if s.Key == "supply_temperature" || s.Key == "return_temperature" || s.Key == "dhw_temperature" || s.Key == "dhw_setpoint" {
			foundTemperatures[s.Key] = true
			if s.StateTopic != "buderus/wp/temperatures" || s.Unit != "°C" || s.DeviceClass != "temperature" || s.StateClass != "measurement" {
				t.Errorf("unexpected temperature sensor: %+v", s)
			}
		}
		if s.Key == "hc1_summer_winter_mode" || s.Key == "hc2_summer_winter_mode" {
			foundHeatingModes[s.Key] = true
			if s.DeviceClass != "enum" || s.StateClass != "" || s.Unit != "" || len(s.Options) != 3 || s.Options[0] != "Winter" || s.Options[1] != "Sommer" || s.Options[2] != "Kühlen" {
				t.Errorf("unexpected heating circuit mode sensor: %+v", s)
			}
		}
		if s.Key == "hc1_room_setpoint" || s.Key == "hc2_room_setpoint" {
			foundRoomSetpoints[s.Key] = true
			if s.StateTopic != roomSetpointTopics[s.Key] || s.JSONField != "roomSetpoint" || s.Unit != "°C" || s.DeviceClass != "temperature" || s.StateClass != "measurement" {
				t.Errorf("unexpected room setpoint sensor: %+v", s)
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
	for _, key := range []string{"supply_temperature", "return_temperature", "dhw_temperature", "dhw_setpoint"} {
		if !foundTemperatures[key] {
			t.Errorf("%s sensor not found", key)
		}
	}
	for _, key := range []string{"hc1_summer_winter_mode", "hc2_summer_winter_mode"} {
		if !foundHeatingModes[key] {
			t.Errorf("%s sensor not found", key)
		}
	}
	for _, key := range []string{"hc1_room_setpoint", "hc2_room_setpoint"} {
		if !foundRoomSetpoints[key] {
			t.Errorf("%s sensor not found", key)
		}
	}
}
