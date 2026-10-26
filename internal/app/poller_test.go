package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

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
	sensors := Sensors("buderus/wp", DefaultCircuits, heatingCircuits)
	if len(sensors) != 26 {
		t.Fatalf("got %d sensors, want 26", len(sensors))
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
	for _, key := range []string{"outdoor_temperature", "compressor_power"} {
		if !foundMainDevices[key] {
			t.Errorf("%s should remain on the main device", key)
		}
	}
	if !foundHeatingCircuitDevice {
		t.Error("heating circuit sensor should use its sub-device and a short name")
	}
}
