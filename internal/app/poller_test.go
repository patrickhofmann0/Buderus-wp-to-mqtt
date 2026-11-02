package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/internal/buderus"
	"github.com/patrickhofmann0/Buderus-wp-to-mqtt/internal/mqtt"
)

// fakeFetcher liefert vordefinierte Antworten pro Pfad (≈ Mockito-Mock, nur handgeschrieben).
type fakeFetcher struct {
	responses    map[string]buderus.EmonResponse
	counters     map[string]buderus.CounterResponse
	values       map[string]buderus.ValueResponse
	strings      map[string]buderus.StringResponse
	errs         map[string]error
	valueCalls   []string
	counterCalls map[string]int
}

func (f *fakeFetcher) FetchEmon(_ context.Context, path string) (buderus.EmonResponse, error) {
	if err := f.errs[path]; err != nil {
		return buderus.EmonResponse{}, err
	}
	return f.responses[path], nil
}

func (f *fakeFetcher) FetchCounter(_ context.Context, path string) (buderus.CounterResponse, error) {
	if f.counterCalls == nil {
		f.counterCalls = make(map[string]int)
	}
	f.counterCalls[path]++
	if err := f.errs[path]; err != nil {
		return buderus.CounterResponse{}, err
	}
	return f.counters[path], nil
}

func (f *fakeFetcher) FetchValue(_ context.Context, path string) (buderus.ValueResponse, error) {
	f.valueCalls = append(f.valueCalls, path)
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
		counters: map[string]buderus.CounterResponse{
			buderus.PathNumberOfStarts: {
				Values: buderus.CounterValues{"ch": 737, "dhw": 194, "cooling": 0, "total": 931},
			},
			buderus.PathWorkingTime: {
				Unit:   "s",
				Values: buderus.CounterValues{"ch": 19515812, "dhw": 1135643, "cooling": 0, "total": 20651456},
			},
		},
		values: map[string]buderus.ValueResponse{
			buderus.PathCompressorPower:                              {Value: ptr(460), Unit: "W"},
			buderus.PathSystemPressure:                               {Value: ptr(1.7), UnitOfMeasure: "bar", State: []map[string]float64{{"invalid": 255}}},
			buderus.PathModulation:                                   {Value: ptr(13), Unit: "%"},
			buderus.PathOutdoorTemperature:                           {Value: ptr(7.5), UnitOfMeasure: "C", State: []map[string]float64{{"open": -32768}, {"short": 32767}}},
			buderus.PathSupplyTemperature:                            {Value: ptr(30.6), UnitOfMeasure: "C", State: []map[string]float64{{"open": -32768}, {"short": 32767}}},
			buderus.PathReturnTemperature:                            {Value: ptr(30), UnitOfMeasure: "C", State: []map[string]float64{{"open": -32768}, {"short": 32767}}},
			buderus.DHWCircuitPath("dhw1", "actualTemp"):             {Value: ptr(43), UnitOfMeasure: "C", State: []map[string]float64{{"open": -32768}, {"short": 32767}}},
			buderus.DHWCircuitPath("dhw1", "currentSetpoint"):        {Value: ptr(48), UnitOfMeasure: "C"},
			buderus.HeatingCircuitPath("hc2", "mixerPosition"):       {Value: ptr(49), UnitOfMeasure: "%"},
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

	heatingCircuits := []HeatingCircuit{{Key: "hc1", Label: "HK1"}, {Key: "hc2", Label: "HK2", HasMixer: true}}
	poller := NewPoller(fetcher, publisher, "buderus/wp", DefaultCircuits, heatingCircuits, discardLogger())
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
	if got := publisher.published["buderus/wp/pressure"]; got != `{"system":1.7}` {
		t.Errorf("pressure payload = %s, want system pressure", got)
	}
	if got := publisher.published["buderus/wp/temperatures"]; got != `{"outdoor":7.5,"supply":30.6,"return":30,"dhwActual":43,"dhwSetpoint":48}` {
		t.Errorf("temperature payload = %s, want all valid temperatures", got)
	}
	if !publisher.retained["buderus/wp/temperatures"] {
		t.Error("temperature state should be retained")
	}
	wantMaintenance := `{"startsHeating":737,"startsDhw":194,"startsCooling":0,"startsTotal":931,"workingTimeHeating":19515812,"workingTimeDhw":1135643,"workingTimeCooling":0,"workingTimeTotal":20651456}`
	if got := publisher.published["buderus/wp/maintenance"]; got != wantMaintenance {
		t.Errorf("maintenance payload = %s, want %s", got, wantMaintenance)
	}
	if !publisher.retained["buderus/wp/maintenance"] {
		t.Error("maintenance state should be retained")
	}
	if got := publisher.published["buderus/wp/hc1"]; got != `{"summerWinterMode":"Winter","roomSetpoint":21}` {
		t.Errorf("hc1 payload = %s, want Winter mode and 21 °C setpoint", got)
	}
	if got := publisher.published["buderus/wp/hc2"]; got != `{"summerWinterMode":"Sommer","mixerPosition":49}` {
		t.Errorf("hc2 payload = %s, want Sommer mode and mixer position", got)
	}
	if !publisher.retained["buderus/wp/hc1"] || !publisher.retained["buderus/wp/hc2"] {
		t.Error("heating circuit mode states should be retained")
	}
	for _, path := range fetcher.valueCalls {
		if path == buderus.HeatingCircuitPath("hc1", "mixerPosition") {
			t.Error("mixer position should not be queried for hc1 without a mixer")
		}
	}
}

func TestMaintenanceStartsErrorStillPublishesWorkingTime(t *testing.T) {
	fetcher := &fakeFetcher{
		counters: map[string]buderus.CounterResponse{
			buderus.PathWorkingTime: {
				Unit:   "s",
				Values: buderus.CounterValues{"ch": 10, "dhw": 20, "total": 30},
			},
		},
		errs: map[string]error{
			buderus.PathNumberOfStarts: errors.New("starts endpoint unavailable"),
		},
	}
	publisher := &fakePublisher{published: map[string]string{}, retained: map[string]bool{}}
	poller := NewPoller(fetcher, publisher, "buderus/wp", nil, nil, discardLogger())

	if err := poller.pollMaintenance(context.Background()); err != nil {
		t.Fatalf("pollMaintenance() error = %v", err)
	}
	if got := publisher.published["buderus/wp/maintenance"]; got != `{"workingTimeHeating":10,"workingTimeDhw":20,"workingTimeTotal":30}` {
		t.Errorf("maintenance payload = %s, want working times only", got)
	}
}

func TestMaintenanceMissingCoolingIsOmitted(t *testing.T) {
	fetcher := &fakeFetcher{
		counters: map[string]buderus.CounterResponse{
			buderus.PathNumberOfStarts: {
				Values: buderus.CounterValues{"ch": 737, "dhw": 194, "pool": 5, "total": 931},
			},
			buderus.PathWorkingTime: {
				Values: buderus.CounterValues{"ch": 100, "dhw": 200, "pool": 300, "total": 400},
			},
		},
	}
	publisher := &fakePublisher{published: map[string]string{}, retained: map[string]bool{}}
	poller := NewPoller(fetcher, publisher, "buderus/wp", nil, nil, discardLogger())

	if err := poller.pollMaintenance(context.Background()); err != nil {
		t.Fatalf("pollMaintenance() error = %v", err)
	}
	want := `{"startsHeating":737,"startsDhw":194,"startsTotal":931,"workingTimeHeating":100,"workingTimeDhw":200,"workingTimeTotal":400}`
	if got := publisher.published["buderus/wp/maintenance"]; got != want {
		t.Errorf("maintenance payload = %s, want %s", got, want)
	}
}

func TestMaintenanceBothFailNothingPublished(t *testing.T) {
	fetcher := &fakeFetcher{
		values: map[string]buderus.ValueResponse{
			buderus.PathCompressorPower: {Value: ptr(460)},
		},
		errs: map[string]error{
			buderus.PathNumberOfStarts: errors.New("starts endpoint unavailable"),
			buderus.PathWorkingTime:    buderus.ErrNotFound,
		},
	}
	publisher := &fakePublisher{published: map[string]string{}, retained: map[string]bool{}}
	poller := NewPoller(fetcher, publisher, "buderus/wp", nil, nil, discardLogger())

	poller.PollOnce(context.Background())
	if _, ok := publisher.published["buderus/wp/maintenance"]; ok {
		t.Error("maintenance should not be published when both counter requests fail")
	}
	if _, ok := publisher.published["buderus/wp/power"]; !ok {
		t.Error("other groups should still be published when maintenance requests fail")
	}
	poller.PollOnce(context.Background())
	for _, path := range []string{buderus.PathNumberOfStarts, buderus.PathWorkingTime} {
		if got := fetcher.counterCalls[path]; got != 2 {
			t.Errorf("counter calls for %s = %d, want 2 after consecutive polls", path, got)
		}
	}
}

func TestMaintenanceUnexpectedUnitSkipsWorkingTime(t *testing.T) {
	fetcher := &fakeFetcher{
		counters: map[string]buderus.CounterResponse{
			buderus.PathNumberOfStarts: {
				Values: buderus.CounterValues{"ch": 7, "total": 7},
			},
			buderus.PathWorkingTime: {
				Unit:   "h",
				Values: buderus.CounterValues{"ch": 10, "total": 10},
			},
		},
	}
	publisher := &fakePublisher{published: map[string]string{}, retained: map[string]bool{}}
	poller := NewPoller(fetcher, publisher, "buderus/wp", nil, nil, discardLogger())

	if err := poller.pollMaintenance(context.Background()); err != nil {
		t.Fatalf("pollMaintenance() error = %v", err)
	}
	if got := publisher.published["buderus/wp/maintenance"]; got != `{"startsHeating":7,"startsTotal":7}` {
		t.Errorf("maintenance payload = %s, want starts only", got)
	}
}

func TestDetectHeatingCircuits(t *testing.T) {
	hc1MixerPath := buderus.HeatingCircuitPath("hc1", "mixerPosition")
	hc2MixerPath := buderus.HeatingCircuitPath("hc2", "mixerPosition")
	fetcher := &fakeFetcher{
		values: map[string]buderus.ValueResponse{hc2MixerPath: {Value: ptr(49)}},
		errs:   map[string]error{hc1MixerPath: buderus.ErrNotFound},
	}

	circuits := DetectHeatingCircuits(context.Background(), fetcher, discardLogger())
	if len(circuits) != 2 {
		t.Fatalf("got %d heating circuits, want 2", len(circuits))
	}
	if circuits[0].Key != "hc1" || circuits[0].HasMixer {
		t.Errorf("hc1 detection = %+v, want no mixer", circuits[0])
	}
	if circuits[1].Key != "hc2" || !circuits[1].HasMixer {
		t.Errorf("hc2 detection = %+v, want mixer present", circuits[1])
	}
}

func TestDetectHeatingCircuitsAssumesMixerOnOtherErrors(t *testing.T) {
	hc1MixerPath := buderus.HeatingCircuitPath("hc1", "mixerPosition")
	hc2MixerPath := buderus.HeatingCircuitPath("hc2", "mixerPosition")
	fetcher := &fakeFetcher{
		errs: map[string]error{
			hc1MixerPath: errors.New("gateway unavailable"),
			hc2MixerPath: buderus.ErrNotFound,
		},
	}

	circuits := DetectHeatingCircuits(context.Background(), fetcher, discardLogger())
	if !circuits[0].HasMixer || circuits[1].HasMixer {
		t.Errorf("detected circuits = %+v, want hc1 assumed and hc2 absent", circuits)
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

	poller := NewPoller(fetcher, publisher, "buderus/wp", []Circuit{DefaultCircuits[0]}, nil, discardLogger())
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

	poller := NewPoller(fetcher, publisher, "buderus/wp", nil, nil, discardLogger())
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

	poller := NewPoller(fetcher, publisher, "buderus/wp", nil, nil, discardLogger())
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

	poller := NewPoller(fetcher, publisher, "buderus/wp", nil, nil, discardLogger())
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

	NewPoller(fetcher, publisher, "buderus/wp", nil, DefaultHeatingCircuits, discardLogger()).PollOnce(context.Background())

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
			poller := NewPoller(tt.fetcher, publisher, "buderus/wp", nil, nil, discardLogger())
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

	NewPoller(fetcher, publisher, "buderus/wp", nil, nil, discardLogger()).PollOnce(context.Background())

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
	heatingCircuits := []HeatingCircuit{{Key: "hc1", Label: "Heizkreis 1"}, {Key: "hc2", Label: "Heizkreis 2", HasMixer: true}}
	sensors := Sensors("buderus/wp", DefaultCircuits, heatingCircuits, VolumeFlows{})
	if len(sensors) != 27 {
		t.Fatalf("got %d sensors, want 27", len(sensors))
	}
	seenSubDevice := false
	for _, sensor := range sensors {
		if sensor.SubDevice != nil {
			seenSubDevice = true
		} else if seenSubDevice {
			t.Errorf("main-device sensor %q appears after a sub-device sensor", sensor.Key)
		}
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
		"system_pressure",
		"supply_temperature",
		"return_temperature",
		"dhw_temperature",
		"dhw_setpoint",
		"hc1_summer_winter_mode",
		"hc2_summer_winter_mode",
		"hc1_room_setpoint",
		"hc2_room_setpoint",
		"hc2_mixer_position",
		"compressor_starts_total",
		"working_time_total",
		"compressor_starts_heating",
		"working_time_heating",
		"compressor_starts_dhw",
		"working_time_dhw",
	} {
		if !keys[key] {
			t.Errorf("missing sensor %q", key)
		}
	}
	maintenanceSensors := map[string]struct {
		name        string
		jsonField   string
		unit        string
		deviceClass string
		stateClass  string
		icon        string
		subDevice   *mqtt.SubDevice
	}{
		"compressor_starts_total": {name: "Verdichterstarts", jsonField: "startsTotal", stateClass: "total_increasing", icon: "mdi:counter"},
		"working_time_total":      {name: "Betriebszeit", jsonField: "workingTimeTotal", unit: "s", deviceClass: "duration", stateClass: "total_increasing", icon: "mdi:timer-outline"},
		"compressor_starts_heating": {
			name: "Verdichterstarts", jsonField: "startsHeating", stateClass: "total_increasing", icon: "mdi:counter",
			subDevice: &mqtt.SubDevice{Key: "heating", Name: "Heizung"},
		},
		"working_time_heating": {
			name: "Betriebszeit", jsonField: "workingTimeHeating", unit: "s", deviceClass: "duration", stateClass: "total_increasing", icon: "mdi:timer-outline",
			subDevice: &mqtt.SubDevice{Key: "heating", Name: "Heizung"},
		},
		"compressor_starts_dhw": {
			name: "Verdichterstarts", jsonField: "startsDhw", stateClass: "total_increasing", icon: "mdi:counter",
			subDevice: &mqtt.SubDevice{Key: "dhw", Name: "Warmwasser"},
		},
		"working_time_dhw": {
			name: "Betriebszeit", jsonField: "workingTimeDhw", unit: "s", deviceClass: "duration", stateClass: "total_increasing", icon: "mdi:timer-outline",
			subDevice: &mqtt.SubDevice{Key: "dhw", Name: "Warmwasser"},
		},
	}
	foundMaintenanceSensors := make(map[string]bool, len(maintenanceSensors))
	foundCompressorPower, foundModulation, foundOutdoorTemperature := false, false, false
	foundTemperatures := map[string]bool{}
	foundHeatingModes := map[string]bool{}
	foundRoomSetpoints := map[string]bool{}
	foundMixer := false
	foundHeatingDevice, foundDHWDevice, foundHeatingCircuitDevice := false, false, false
	foundMainDevices := map[string]bool{}
	roomSetpointTopics := map[string]string{
		"hc1_room_setpoint": "buderus/wp/hc1",
		"hc2_room_setpoint": "buderus/wp/hc2",
	}
	for _, s := range sensors {
		if want, ok := maintenanceSensors[s.Key]; ok {
			foundMaintenanceSensors[s.Key] = true
			if s.StateTopic != "buderus/wp/maintenance" || s.Name != want.name || s.JSONField != want.jsonField || s.Unit != want.unit || s.DeviceClass != want.deviceClass || s.StateClass != want.stateClass || s.Icon != want.icon {
				t.Errorf("unexpected maintenance sensor %q: %+v", s.Key, s)
			}
			if want.subDevice == nil {
				if s.SubDevice != nil {
					t.Errorf("maintenance sensor %q has sub-device %+v, want main device", s.Key, s.SubDevice)
				}
			} else if s.SubDevice == nil || s.SubDevice.Key != want.subDevice.Key || s.SubDevice.Name != want.subDevice.Name {
				t.Errorf("maintenance sensor %q sub-device = %+v, want %+v", s.Key, s.SubDevice, want.subDevice)
			}
		}
		if s.Key == "heating_electricity" {
			foundHeatingDevice = s.Name == "Stromverbrauch" && s.SubDevice != nil && s.SubDevice.Key == "heating" && s.SubDevice.Name == "Heizung"
		}
		if s.Key == "dhw_temperature" {
			foundDHWDevice = s.Name == "Ist-Temperatur" && s.SubDevice != nil && s.SubDevice.Key == "dhw" && s.SubDevice.Name == "Warmwasser"
		}
		if s.Key == "outdoor_temperature" || s.Key == "compressor_power" {
			foundMainDevices[s.Key] = s.SubDevice == nil
		}
		if s.Key == "system_pressure" {
			foundMainDevices[s.Key] = s.SubDevice == nil
			if s.Name != "Systemdruck" || s.StateTopic != "buderus/wp/pressure" || s.JSONField != "system" || s.Unit != "bar" ||
				s.DeviceClass != "pressure" || s.StateClass != "measurement" || s.Icon != "mdi:gauge" {
				t.Errorf("unexpected system pressure sensor: %+v", s)
			}
		}
		if s.Key == "hc1_summer_winter_mode" {
			foundHeatingCircuitDevice = s.Name == "Sommer/Winter" && s.SubDevice != nil && s.SubDevice.Key == "hc1" && s.SubDevice.Name == "Heizkreis 1"
		}
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
		if s.Key == "hc2_mixer_position" {
			foundMixer = true
			if s.StateTopic != "buderus/wp/hc2" || s.JSONField != "mixerPosition" || s.Unit != "%" || s.StateClass != "measurement" {
				t.Errorf("unexpected mixer position sensor: %+v", s)
			}
		}
		if s.Key == "hc1_mixer_position" {
			t.Error("mixer sensor should not be created for hc1")
		}
	}
	for key := range maintenanceSensors {
		if !foundMaintenanceSensors[key] {
			t.Errorf("maintenance sensor %q not found", key)
		}
	}
	if keys["compressor_starts_cooling"] || keys["working_time_cooling"] {
		t.Error("cooling maintenance values must not have Home Assistant sensors")
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
	if !foundMixer {
		t.Error("hc2_mixer_position sensor not found")
	}
	if !foundHeatingDevice {
		t.Error("heating energy sensor should use the Heizung sub-device and a short name")
	}
	if !foundDHWDevice {
		t.Error("DHW temperature should use the Warmwasser sub-device and a short name")
	}
	for _, key := range []string{"outdoor_temperature", "compressor_power", "system_pressure"} {
		if !foundMainDevices[key] {
			t.Errorf("%s should remain on the main device", key)
		}
	}
	if !foundHeatingCircuitDevice {
		t.Error("heating circuit sensor should use its sub-device and a short name")
	}
}

// fakeRecorder merkt sich die an den StateRecorder gemeldeten Payloads.
type fakeRecorder struct {
	records   map[string]string
	errs      map[string]error
	completed int
}

func (f *fakeRecorder) Record(key string, payload []byte, publishErr error) {
	f.records[key] = string(payload)
	f.errs[key] = publishErr
}

func (f *fakeRecorder) PollCompleted(time.Time) {
	f.completed++
}

// failingPublisher simuliert einen nicht erreichbaren Broker.
type failingPublisher struct{}

func (failingPublisher) Publish(context.Context, string, []byte, bool) error {
	return errors.New("broker down")
}

func TestPollOnceRecordsStateEvenIfPublishFails(t *testing.T) {
	fetcher := &fakeFetcher{
		values: map[string]buderus.ValueResponse{
			buderus.PathCompressorPower: {Value: ptr(460), UnitOfMeasure: "W"},
		},
	}
	recorder := &fakeRecorder{records: map[string]string{}, errs: map[string]error{}}
	poller := NewPoller(fetcher, failingPublisher{}, "buderus/wp", nil, nil, discardLogger())
	poller.SetRecorder(recorder)
	poller.PollOnce(context.Background())

	if got := recorder.records["power"]; got != `{"compressorPower":460}` {
		t.Errorf("recorded power = %s, want compressor power", got)
	}
	if recorder.errs["power"] == nil {
		t.Error("publish error should be passed to recorder")
	}
	if recorder.completed != 1 {
		t.Errorf("PollCompleted calls = %d, want 1", recorder.completed)
	}
}

func TestLitersPerHourToLitersPerMinute(t *testing.T) {
	for _, tt := range []struct{ in, want float64 }{
		{0, 0}, {1134, 18.9}, {1000, 16.67}, {100, 1.67}, {61, 1.02}, {30, 0.5},
	} {
		if got := litersPerHourToLitersPerMinute(tt.in); got != tt.want {
			t.Errorf("litersPerHourToLitersPerMinute(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

var flowFaultStates = []map[string]float64{{"open": -32768}, {"short": 32767}}

// newFlowPoller baut einen Poller mit beiden Volumenströmen und frischem Fake-Publisher.
func newFlowPoller(fetcher *fakeFetcher) (*Poller, *fakePublisher) {
	publisher := &fakePublisher{published: map[string]string{}, retained: map[string]bool{}}
	poller := NewPoller(fetcher, publisher, "buderus/wp", nil, nil, discardLogger())
	poller.SetVolumeFlows(VolumeFlows{Heating: true, DHW: true})
	return poller, publisher
}

func TestPollFlowPublishesBothValues(t *testing.T) {
	fetcher := &fakeFetcher{values: map[string]buderus.ValueResponse{
		buderus.PathHeatingVolumeFlow: {Value: ptr(1134), UnitOfMeasure: "l/h", State: []map[string]float64{{"lowflow": 65535}}},
		buderus.PathDHWVolumeFlow:     {Value: ptr(20), UnitOfMeasure: "l/min", State: flowFaultStates},
	}}
	poller, publisher := newFlowPoller(fetcher)

	if err := poller.pollFlow(context.Background()); err != nil {
		t.Fatalf("pollFlow() error = %v", err)
	}
	if got := publisher.published["buderus/wp/flow"]; got != `{"heating":18.9,"dhw":20}` {
		t.Errorf("flow payload = %s", got)
	}
	if !publisher.retained["buderus/wp/flow"] {
		t.Error("flow state should be retained")
	}
}

func TestPollFlowLowflowOmitsOnlyHeating(t *testing.T) {
	fetcher := &fakeFetcher{values: map[string]buderus.ValueResponse{
		buderus.PathHeatingVolumeFlow: {Value: ptr(65535), UnitOfMeasure: "l/h", State: []map[string]float64{{"lowflow": 65535}}},
		buderus.PathDHWVolumeFlow:     {Value: ptr(12), UnitOfMeasure: "l/min"},
	}}
	poller, publisher := newFlowPoller(fetcher)

	if err := poller.pollFlow(context.Background()); err != nil {
		t.Fatalf("pollFlow() error = %v", err)
	}
	if got := publisher.published["buderus/wp/flow"]; got != `{"dhw":12}` {
		t.Errorf("flow payload = %s, want dhw only", got)
	}
}

func TestPollFlowHeatingLowflowWithoutStateOmitsOnlyHeating(t *testing.T) {
	for name, state := range map[string][]map[string]float64{"ohne state": nil, "leeres state": {}} {
		t.Run(name, func(t *testing.T) {
			fetcher := &fakeFetcher{values: map[string]buderus.ValueResponse{
				buderus.PathHeatingVolumeFlow: {Value: ptr(65535), UnitOfMeasure: "l/h", State: state},
				buderus.PathDHWVolumeFlow:     {Value: ptr(12), UnitOfMeasure: "l/min"},
			}}
			poller, publisher := newFlowPoller(fetcher)
			if err := poller.pollFlow(context.Background()); err != nil {
				t.Fatalf("pollFlow() error = %v", err)
			}
			if got := publisher.published["buderus/wp/flow"]; got != `{"dhw":12}` {
				t.Errorf("flow payload = %s, want dhw only", got)
			}
		})
	}
}

func TestPollFlowDHWFaultValuesWithoutStateOmitOnlyDHW(t *testing.T) {
	for _, value := range []float64{-32768, 32767} {
		fetcher := &fakeFetcher{values: map[string]buderus.ValueResponse{
			buderus.PathHeatingVolumeFlow: {Value: ptr(600), UnitOfMeasure: "l/h"},
			buderus.PathDHWVolumeFlow:     {Value: ptr(value), UnitOfMeasure: "l/min"},
		}}
		poller, publisher := newFlowPoller(fetcher)
		if err := poller.pollFlow(context.Background()); err != nil {
			t.Fatalf("pollFlow() error = %v", err)
		}
		if got := publisher.published["buderus/wp/flow"]; got != `{"heating":10}` {
			t.Errorf("value %v: flow payload = %s, want heating only", value, got)
		}
	}
}

func TestPollFlowDHWSensorFaultOmitsOnlyDHW(t *testing.T) {
	for _, value := range []float64{-32768, 32767} {
		fetcher := &fakeFetcher{values: map[string]buderus.ValueResponse{
			buderus.PathHeatingVolumeFlow: {Value: ptr(600), UnitOfMeasure: "l/h"},
			buderus.PathDHWVolumeFlow:     {Value: ptr(value), UnitOfMeasure: "l/min", State: flowFaultStates},
		}}
		poller, publisher := newFlowPoller(fetcher)

		if err := poller.pollFlow(context.Background()); err != nil {
			t.Fatalf("pollFlow() error = %v", err)
		}
		if got := publisher.published["buderus/wp/flow"]; got != `{"heating":10}` {
			t.Errorf("value %v: flow payload = %s, want heating only", value, got)
		}
	}
}

func TestPollFlowUnexpectedUnitOmitsField(t *testing.T) {
	tests := []struct {
		name    string
		heating buderus.ValueResponse
		dhw     buderus.ValueResponse
		want    string
	}{
		{
			name:    "falsche Einheit",
			heating: buderus.ValueResponse{Value: ptr(600), UnitOfMeasure: "m3/h"},
			dhw:     buderus.ValueResponse{Value: ptr(5), UnitOfMeasure: "l/min"},
			want:    `{"dhw":5}`,
		},
		{
			name:    "leere UnitOfMeasure, Unit als Rückfall",
			heating: buderus.ValueResponse{Value: ptr(600), Unit: "l/h"},
			dhw:     buderus.ValueResponse{Value: ptr(5), UnitOfMeasure: "L/min"},
			want:    `{"heating":10,"dhw":5}`,
		},
		{
			name:    "beide Einheiten leer",
			heating: buderus.ValueResponse{Value: ptr(600)},
			dhw:     buderus.ValueResponse{Value: ptr(5), Unit: "l/h"},
			want:    `{"heating":10}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcher := &fakeFetcher{values: map[string]buderus.ValueResponse{
				buderus.PathHeatingVolumeFlow: tt.heating,
				buderus.PathDHWVolumeFlow:     tt.dhw,
			}}
			poller, publisher := newFlowPoller(fetcher)
			if err := poller.pollFlow(context.Background()); err != nil {
				t.Fatalf("pollFlow() error = %v", err)
			}
			if got := publisher.published["buderus/wp/flow"]; got != tt.want {
				t.Errorf("flow payload = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestPollFlowNilValueOmitsField(t *testing.T) {
	fetcher := &fakeFetcher{values: map[string]buderus.ValueResponse{
		buderus.PathHeatingVolumeFlow: {UnitOfMeasure: "l/h"},
		buderus.PathDHWVolumeFlow:     {Value: ptr(7), UnitOfMeasure: "l/min"},
	}}
	poller, publisher := newFlowPoller(fetcher)
	if err := poller.pollFlow(context.Background()); err != nil {
		t.Fatalf("pollFlow() error = %v", err)
	}
	if got := publisher.published["buderus/wp/flow"]; got != `{"dhw":7}` {
		t.Errorf("flow payload = %s, want dhw only", got)
	}
}

func TestPollFlowFetchErrorOmitsOnlyThatField(t *testing.T) {
	for _, err := range []error{errors.New("gateway unreachable"), buderus.ErrNotFound} {
		fetcher := &fakeFetcher{
			values: map[string]buderus.ValueResponse{
				buderus.PathDHWVolumeFlow: {Value: ptr(9), UnitOfMeasure: "l/min"},
			},
			errs: map[string]error{buderus.PathHeatingVolumeFlow: err},
		}
		poller, publisher := newFlowPoller(fetcher)
		if pollErr := poller.pollFlow(context.Background()); pollErr != nil {
			t.Fatalf("pollFlow() error = %v", pollErr)
		}
		if got := publisher.published["buderus/wp/flow"]; got != `{"dhw":9}` {
			t.Errorf("error %v: flow payload = %s, want dhw only", err, got)
		}
	}
}

func TestPollFlowNothingPublishedWhenBothMissing(t *testing.T) {
	fetcher := &fakeFetcher{errs: map[string]error{
		buderus.PathHeatingVolumeFlow: errors.New("down"),
		buderus.PathDHWVolumeFlow:     buderus.ErrNotFound,
	}}
	poller, publisher := newFlowPoller(fetcher)
	if err := poller.pollFlow(context.Background()); err != nil {
		t.Fatalf("pollFlow() error = %v", err)
	}
	if _, ok := publisher.published["buderus/wp/flow"]; ok {
		t.Error("flow should not be published when both values are missing")
	}
}

func TestPollFlowSkipsUndetectedEndpoints(t *testing.T) {
	fetcher := &fakeFetcher{values: map[string]buderus.ValueResponse{
		buderus.PathHeatingVolumeFlow: {Value: ptr(600), UnitOfMeasure: "l/h"},
		buderus.PathDHWVolumeFlow:     {Value: ptr(5), UnitOfMeasure: "l/min"},
	}}
	publisher := &fakePublisher{published: map[string]string{}, retained: map[string]bool{}}
	poller := NewPoller(fetcher, publisher, "buderus/wp", nil, nil, discardLogger())

	if err := poller.pollFlow(context.Background()); err != nil {
		t.Fatalf("pollFlow() error = %v", err)
	}
	if len(fetcher.valueCalls) != 0 {
		t.Errorf("value calls = %v, want none without SetVolumeFlows", fetcher.valueCalls)
	}
	if _, ok := publisher.published["buderus/wp/flow"]; ok {
		t.Error("flow should not be published without detected endpoints")
	}

	poller.SetVolumeFlows(VolumeFlows{DHW: true})
	if err := poller.pollFlow(context.Background()); err != nil {
		t.Fatalf("pollFlow() error = %v", err)
	}
	for _, path := range fetcher.valueCalls {
		if path == buderus.PathHeatingVolumeFlow {
			t.Error("heating volume flow should not be queried when it was not detected")
		}
	}
	if got := publisher.published["buderus/wp/flow"]; got != `{"dhw":5}` {
		t.Errorf("flow payload = %s, want dhw only", got)
	}
}

func TestPollFlowRecordsFlowKey(t *testing.T) {
	fetcher := &fakeFetcher{values: map[string]buderus.ValueResponse{
		buderus.PathHeatingVolumeFlow: {Value: ptr(1134), UnitOfMeasure: "l/h"},
		buderus.PathDHWVolumeFlow:     {Value: ptr(12), UnitOfMeasure: "l/min"},
	}}
	poller, publisher := newFlowPoller(fetcher)
	recorder := &fakeRecorder{records: map[string]string{}, errs: map[string]error{}}
	poller.SetRecorder(recorder)

	poller.PollOnce(context.Background())
	want := `{"heating":18.9,"dhw":12}`
	if got := recorder.records["flow"]; got != want {
		t.Errorf("recorded flow = %s, want %s", got, want)
	}
	if got := publisher.published["buderus/wp/flow"]; got != want {
		t.Errorf("published flow = %s, want %s", got, want)
	}
}

func TestDetectVolumeFlows(t *testing.T) {
	tests := []struct {
		name string
		errs map[string]error
		want VolumeFlows
	}{
		{"beide vorhanden", nil, VolumeFlows{Heating: true, DHW: true}},
		{
			"Heizung 404",
			map[string]error{buderus.PathHeatingVolumeFlow: buderus.ErrNotFound},
			VolumeFlows{DHW: true},
		},
		{
			"Warmwasser 404",
			map[string]error{buderus.PathDHWVolumeFlow: fmt.Errorf("GET: %w", buderus.ErrNotFound)},
			VolumeFlows{Heating: true},
		},
		{
			"anderer Fehler nimmt Vorhandensein an",
			map[string]error{
				buderus.PathHeatingVolumeFlow: errors.New("timeout"),
				buderus.PathDHWVolumeFlow:     buderus.ErrNotFound,
			},
			VolumeFlows{Heating: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcher := &fakeFetcher{errs: tt.errs}
			if got := DetectVolumeFlows(context.Background(), fetcher, discardLogger()); got != tt.want {
				t.Errorf("DetectVolumeFlows() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSensorsVolumeFlow(t *testing.T) {
	sensors := Sensors("buderus/wp", DefaultCircuits, nil, VolumeFlows{Heating: true, DHW: true})
	base := Sensors("buderus/wp", DefaultCircuits, nil, VolumeFlows{})
	if len(sensors) != len(base)+2 {
		t.Fatalf("got %d sensors, want %d", len(sensors), len(base)+2)
	}

	want := map[string]struct {
		field, icon string
		subDevice   mqtt.SubDevice
	}{
		"heating_volume_flow": {"heating", "mdi:water-pump", mqtt.SubDevice{Key: "heating", Name: "Heizung"}},
		"dhw_volume_flow":     {"dhw", "mdi:water-sync", mqtt.SubDevice{Key: "dhw", Name: "Warmwasser"}},
	}
	seenSubDevice := false
	found := map[string]bool{}
	for _, s := range sensors {
		if s.SubDevice != nil {
			seenSubDevice = true
		} else if seenSubDevice {
			t.Errorf("main-device sensor %q appears after a sub-device sensor", s.Key)
		}
		w, ok := want[s.Key]
		if !ok {
			continue
		}
		found[s.Key] = true
		if s.Name != "Volumenstrom" || s.StateTopic != "buderus/wp/flow" || s.JSONField != w.field ||
			s.Unit != "L/min" || s.DeviceClass != "volume_flow_rate" || s.StateClass != "measurement" || s.Icon != w.icon {
			t.Errorf("unexpected volume flow sensor %q: %+v", s.Key, s)
		}
		if s.SubDevice == nil || *s.SubDevice != w.subDevice {
			t.Errorf("sensor %q sub-device = %+v, want %+v", s.Key, s.SubDevice, w.subDevice)
		}
	}
	if len(found) != 2 {
		t.Errorf("found volume flow sensors = %v, want both", found)
	}

	for _, tt := range []struct {
		flows VolumeFlows
		keys  []string
	}{
		{VolumeFlows{}, nil},
		{VolumeFlows{Heating: true}, []string{"heating_volume_flow"}},
		{VolumeFlows{DHW: true}, []string{"dhw_volume_flow"}},
	} {
		got := map[string]bool{}
		for _, s := range Sensors("buderus/wp", DefaultCircuits, nil, tt.flows) {
			if s.Key == "heating_volume_flow" || s.Key == "dhw_volume_flow" {
				got[s.Key] = true
			}
		}
		if len(got) != len(tt.keys) {
			t.Errorf("flows %+v: got sensors %v, want %v", tt.flows, got, tt.keys)
		}
		for _, k := range tt.keys {
			if !got[k] {
				t.Errorf("flows %+v: missing sensor %q", tt.flows, k)
			}
		}
	}
}

func TestSensorsVolumeFlowDiscovery(t *testing.T) {
	sensors := Sensors("buderus/wp", DefaultCircuits, nil, VolumeFlows{Heating: true})
	device := mqtt.Device{ID: "wp", Name: "Wärmepumpe"}
	messages, err := mqtt.BuildDiscoveryMessages("homeassistant", "buderus/wp/status", device, sensors)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantTopic := "homeassistant/sensor/wp/heating_volume_flow/config"
	for _, m := range messages {
		if m.Topic != wantTopic {
			continue
		}
		var payload struct {
			Name            string `json:"name"`
			UniqueID        string `json:"unique_id"`
			DefaultEntityID string `json:"default_entity_id"`
			StateTopic      string `json:"state_topic"`
			ValueTemplate   string `json:"value_template"`
			Unit            string `json:"unit_of_measurement"`
			DeviceClass     string `json:"device_class"`
			Device          struct {
				Identifiers []string `json:"identifiers"`
				Name        string   `json:"name"`
				ViaDevice   string   `json:"via_device"`
			} `json:"device"`
		}
		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			t.Fatalf("invalid discovery JSON: %v", err)
		}
		if payload.UniqueID != "wp_heating_volume_flow" || payload.DefaultEntityID != "sensor.wp_heating_volume_flow" ||
			payload.Unit != "L/min" || payload.DeviceClass != "volume_flow_rate" || payload.Name != "Volumenstrom" ||
			payload.StateTopic != "buderus/wp/flow" || payload.ValueTemplate != "{{ value_json.heating }}" {
			t.Errorf("unexpected discovery payload: %+v", payload)
		}
		if len(payload.Device.Identifiers) != 1 || payload.Device.Identifiers[0] != "wp_heating" ||
			payload.Device.Name != "Heizung" || payload.Device.ViaDevice != "wp" {
			t.Errorf("unexpected discovery device: %+v", payload.Device)
		}
		return
	}
	t.Errorf("discovery message %q not found", wantTopic)
}

// newPressurePoller baut einen Poller ohne Heizkreise und Circuits mit frischem Fake-Publisher.
func newPressurePoller(fetcher *fakeFetcher) (*Poller, *fakePublisher) {
	publisher := &fakePublisher{published: map[string]string{}, retained: map[string]bool{}}
	return NewPoller(fetcher, publisher, "buderus/wp", nil, nil, discardLogger()), publisher
}

func pressureFetcher(resp buderus.ValueResponse) *fakeFetcher {
	return &fakeFetcher{values: map[string]buderus.ValueResponse{buderus.PathSystemPressure: resp}}
}

func TestPollPressurePublishesValue(t *testing.T) {
	poller, publisher := newPressurePoller(pressureFetcher(buderus.ValueResponse{Value: ptr(1.7), UnitOfMeasure: "bar"}))
	if err := poller.pollPressure(context.Background()); err != nil {
		t.Fatalf("pollPressure() error = %v", err)
	}
	if got := publisher.published["buderus/wp/pressure"]; got != `{"system":1.7}` {
		t.Errorf("pressure payload = %s", got)
	}
	if !publisher.retained["buderus/wp/pressure"] {
		t.Error("pressure state should be retained")
	}
}

func TestPollPressureUnitAccepted(t *testing.T) {
	for name, resp := range map[string]buderus.ValueResponse{
		"BAR":           {Value: ptr(1.7), UnitOfMeasure: "BAR"},
		"unit-Fallback": {Value: ptr(1.7), Unit: "bar"},
		"beide leer":    {Value: ptr(1.7)},
	} {
		t.Run(name, func(t *testing.T) {
			poller, publisher := newPressurePoller(pressureFetcher(resp))
			if err := poller.pollPressure(context.Background()); err != nil {
				t.Fatalf("pollPressure() error = %v", err)
			}
			if got := publisher.published["buderus/wp/pressure"]; got != `{"system":1.7}` {
				t.Errorf("pressure payload = %q", got)
			}
		})
	}
}

func TestPollPressureUnexpectedUnitOmitted(t *testing.T) {
	poller, publisher := newPressurePoller(pressureFetcher(buderus.ValueResponse{Value: ptr(1.7), UnitOfMeasure: "kPa"}))
	if err := poller.pollPressure(context.Background()); err != nil {
		t.Fatalf("pollPressure() error = %v", err)
	}
	if _, ok := publisher.published["buderus/wp/pressure"]; ok {
		t.Error("pressure with unexpected unit should not be published")
	}
}

func TestPollPressureInvalidStateNotPublished(t *testing.T) {
	// 255 ist zusätzlich unplausibel; der Fall 1,7 bar mit passendem Fehlerzustand
	// isoliert dagegen den SensorFaultState-Zweig (Plausibilitätsprüfung würde 1,7 akzeptieren).
	for _, value := range []float64{255, 1.7} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			publisher := &fakePublisher{published: map[string]string{}, retained: map[string]bool{}}
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			fetcher := pressureFetcher(buderus.ValueResponse{
				Value: ptr(value), UnitOfMeasure: "bar", State: []map[string]float64{{"invalid": value}},
			})
			poller := NewPoller(fetcher, publisher, "buderus/wp", nil, nil, logger)
			if err := poller.pollPressure(context.Background()); err != nil {
				t.Fatalf("pollPressure() error = %v", err)
			}
			if _, ok := publisher.published["buderus/wp/pressure"]; ok {
				t.Error("invalid pressure should not be published")
			}
			if out := logs.String(); !strings.Contains(out, "system pressure unavailable") || !strings.Contains(out, "state=invalid") {
				t.Errorf("expected warning with state=invalid, got %q", out)
			}
		})
	}
}

func TestPollPressureImplausibleNotPublished(t *testing.T) {
	for _, tt := range []struct {
		value float64
		want  string // leer = nicht veröffentlicht
	}{
		{0, ""}, {-0.5, ""}, {10.1, ""}, {255, ""}, {10, `{"system":10}`},
	} {
		t.Run(fmt.Sprint(tt.value), func(t *testing.T) {
			poller, publisher := newPressurePoller(pressureFetcher(buderus.ValueResponse{Value: ptr(tt.value), UnitOfMeasure: "bar"}))
			if err := poller.pollPressure(context.Background()); err != nil {
				t.Fatalf("pollPressure() error = %v", err)
			}
			got, ok := publisher.published["buderus/wp/pressure"]
			if tt.want == "" && ok {
				t.Errorf("value %v should not be published, got %s", tt.value, got)
			}
			if tt.want != "" && got != tt.want {
				t.Errorf("pressure payload = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPollPressureNilValueNotPublished(t *testing.T) {
	poller, publisher := newPressurePoller(pressureFetcher(buderus.ValueResponse{UnitOfMeasure: "bar"}))
	if err := poller.pollPressure(context.Background()); err != nil {
		t.Fatalf("pollPressure() error = %v", err)
	}
	if _, ok := publisher.published["buderus/wp/pressure"]; ok {
		t.Error("pressure without value should not be published")
	}
}

func TestPollPressureFetchErrorDoesNotStopOtherTopics(t *testing.T) {
	for name, fetchErr := range map[string]error{
		"timeout":  context.DeadlineExceeded,
		"notFound": buderus.ErrNotFound,
	} {
		t.Run(name, func(t *testing.T) {
			fetcher := &fakeFetcher{
				values: map[string]buderus.ValueResponse{
					buderus.PathCompressorPower:    {Value: ptr(460), Unit: "W"},
					buderus.PathOutdoorTemperature: {Value: ptr(7.5), UnitOfMeasure: "C"},
				},
				errs: map[string]error{buderus.PathSystemPressure: fetchErr},
			}
			poller, publisher := newPressurePoller(fetcher)
			poller.PollOnce(context.Background())

			if _, ok := publisher.published["buderus/wp/pressure"]; ok {
				t.Error("pressure should not be published on fetch error")
			}
			if got := publisher.published["buderus/wp/power"]; got != `{"compressorPower":460}` {
				t.Errorf("power payload = %q", got)
			}
			if got := publisher.published["buderus/wp/temperatures"]; got != `{"outdoor":7.5}` {
				t.Errorf("temperatures payload = %q", got)
			}
		})
	}
}

func TestPollPressureQueriedEveryCycle(t *testing.T) {
	fetcher := &fakeFetcher{errs: map[string]error{buderus.PathSystemPressure: buderus.ErrNotFound}}
	poller, _ := newPressurePoller(fetcher)
	poller.PollOnce(context.Background())
	poller.PollOnce(context.Background())

	calls := 0
	for _, path := range fetcher.valueCalls {
		if path == buderus.PathSystemPressure {
			calls++
		}
	}
	if calls != 2 {
		t.Errorf("pressure queried %d times, want 2", calls)
	}
}

func TestPollPressureRecordsPressureKey(t *testing.T) {
	poller, _ := newPressurePoller(pressureFetcher(buderus.ValueResponse{Value: ptr(1.7), UnitOfMeasure: "bar"}))
	recorder := &fakeRecorder{records: map[string]string{}, errs: map[string]error{}}
	poller.SetRecorder(recorder)
	poller.PollOnce(context.Background())

	if got := recorder.records["pressure"]; got != `{"system":1.7}` {
		t.Errorf("recorded pressure = %q", got)
	}
}

func TestSensorsSystemPressureDiscovery(t *testing.T) {
	sensors := Sensors("buderus/wp", DefaultCircuits, nil, VolumeFlows{})
	device := mqtt.Device{ID: "wp", Name: "Wärmepumpe"}
	messages, err := mqtt.BuildDiscoveryMessages("homeassistant", "buderus/wp/status", device, sensors)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantTopic := "homeassistant/sensor/wp/system_pressure/config"
	for _, m := range messages {
		if m.Topic != wantTopic {
			continue
		}
		var payload struct {
			Name            string `json:"name"`
			UniqueID        string `json:"unique_id"`
			DefaultEntityID string `json:"default_entity_id"`
			StateTopic      string `json:"state_topic"`
			ValueTemplate   string `json:"value_template"`
			Unit            string `json:"unit_of_measurement"`
			DeviceClass     string `json:"device_class"`
			StateClass      string `json:"state_class"`
			Device          struct {
				Identifiers []string `json:"identifiers"`
				ViaDevice   string   `json:"via_device"`
			} `json:"device"`
		}
		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			t.Fatalf("invalid discovery JSON: %v", err)
		}
		if payload.UniqueID != "wp_system_pressure" || payload.DefaultEntityID != "sensor.wp_system_pressure" ||
			payload.Name != "Systemdruck" || payload.Unit != "bar" || payload.DeviceClass != "pressure" ||
			payload.StateClass != "measurement" || payload.StateTopic != "buderus/wp/pressure" ||
			payload.ValueTemplate != "{{ value_json.system }}" {
			t.Errorf("unexpected discovery payload: %+v", payload)
		}
		if len(payload.Device.Identifiers) != 1 || payload.Device.Identifiers[0] != "wp" || payload.Device.ViaDevice != "" {
			t.Errorf("unexpected discovery device: %+v", payload.Device)
		}
		return
	}
	t.Errorf("discovery message %q not found", wantTopic)
}
