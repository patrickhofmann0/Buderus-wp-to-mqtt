package buderus

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// newTestClient startet einen lokalen HTTPS-Server (≈ MockWebServer) und einen Client dafür.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	// t.Cleanup ≈ @AfterEach, nur pro Test registriert.
	t.Cleanup(server.Close)

	host, portStr, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	return NewClient(host, port, "test-token", slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestFetchEmon(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PathHeatingConsumption {
			t.Errorf("path = %q, want %q", r.URL.Path, PathHeatingConsumption)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "/heatSources/emon/chConsumption",
			"type": "emonValue",
			"writeable": 0,
			"values": [{"outputProduced": 30}, {"eheater": 2}, {"electricity": 7.5}],
			"unit": "kWh"
		}`))
	})

	got, err := client.FetchEmon(context.Background(), PathHeatingConsumption)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Unit != "kWh" {
		t.Errorf("Unit = %q, want kWh", got.Unit)
	}
	if got.Values.Electricity == nil || *got.Values.Electricity != 7.5 {
		t.Errorf("Electricity = %v, want 7.5", got.Values.Electricity)
	}
	if got.Values.OutputProduced == nil || *got.Values.OutputProduced != 30 {
		t.Errorf("OutputProduced = %v, want 30", got.Values.OutputProduced)
	}
}

func TestFetchCounter(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PathWorkingTime {
			t.Errorf("path = %q, want %q", r.URL.Path, PathWorkingTime)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"values":[{"ch":19515812},{"dhw":1135643},{"cooling":0},{"total":20651456}],"unit":"s"}`))
	})

	got, err := client.FetchCounter(context.Background(), PathWorkingTime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Unit != "s" {
		t.Errorf("Unit = %q, want s", got.Unit)
	}
	for key, want := range map[string]float64{
		"ch":      19515812,
		"dhw":     1135643,
		"cooling": 0,
		"total":   20651456,
	} {
		if value := got.Values.Get(key); value == nil || *value != want {
			t.Errorf("Values.Get(%q) = %v, want %v", key, value, want)
		}
	}
}

func TestFetchCounterMissingAndUnknownKeys(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"values":[{"ch":737},{"dhw":194},{"pool":42},{"total":931}]}`))
	})

	got, err := client.FetchCounter(context.Background(), PathNumberOfStarts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value := got.Values.Get("cooling"); value != nil {
		t.Errorf("Values.Get(cooling) = %v, want nil", value)
	}
	if value := got.Values.Get("pool"); value == nil || *value != 42 {
		t.Errorf("Values.Get(pool) = %v, want 42", value)
	}
}

func TestFetchCounterNotFound(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	if _, err := client.FetchCounter(context.Background(), PathNumberOfStarts); !errors.Is(err, ErrNotFound) {
		t.Errorf("FetchCounter() error = %v, want ErrNotFound", err)
	}
}

func TestFetchCounterInvalidValues(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"values":"x"}`))
	})
	if _, err := client.FetchCounter(context.Background(), PathNumberOfStarts); err == nil {
		t.Fatal("expected error for invalid counter values")
	}
}

func TestFetchValue(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PathCompressorPower {
			t.Errorf("path = %q, want %q", r.URL.Path, PathCompressorPower)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"/heatSources/compressor/powerElecActual","type":"floatValue","value":460.0,"unit":"W"}`))
	})

	got, err := client.FetchValue(context.Background(), PathCompressorPower)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Value == nil || *got.Value != 460 {
		t.Errorf("Value = %v, want 460", got.Value)
	}
	if got.Unit != "W" {
		t.Errorf("Unit = %q, want W", got.Unit)
	}
}

func TestFetchModulationValue(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PathModulation {
			t.Errorf("path = %q, want %q", r.URL.Path, PathModulation)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"/heatSources/actualModulation","type":"floatValue","value":13.0,"unit":"%"}`))
	})

	got, err := client.FetchValue(context.Background(), PathModulation)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Value == nil || *got.Value != 13 {
		t.Errorf("Value = %v, want 13", got.Value)
	}
	if got.Unit != "%" {
		t.Errorf("Unit = %q, want %%", got.Unit)
	}
}

func TestFetchOutdoorTemperature(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PathOutdoorTemperature {
			t.Errorf("path = %q, want %q", r.URL.Path, PathOutdoorTemperature)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"/system/sensors/temperatures/outdoor_t1","type":"floatValue","value":7.5,"unitOfMeasure":"C","state":[{"open":-32768},{"short":32767}]}`))
	})

	got, err := client.FetchValue(context.Background(), PathOutdoorTemperature)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Value == nil || *got.Value != 7.5 {
		t.Errorf("Value = %v, want 7.5", got.Value)
	}
	if got.UnitOfMeasure != "C" {
		t.Errorf("UnitOfMeasure = %q, want C", got.UnitOfMeasure)
	}
	if got.SensorFault() {
		t.Errorf("valid outdoor temperature reported as sensor fault (%q)", got.SensorFaultState())
	}
}

func TestFetchStringHeatingCircuitMode(t *testing.T) {
	path := HeatingCircuitPath("hc1", "currentSuWiMode")
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			t.Errorf("path = %q, want %q", r.URL.Path, path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"/heatingCircuits/hc1/currentSuWiMode","type":"stringValue","value":"forced","allowedValues":["forced","off","cooling"]}`))
	})

	got, err := client.FetchString(context.Background(), path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Value != "forced" {
		t.Errorf("Value = %q, want forced", got.Value)
	}
	wantValues := []string{"forced", "off", "cooling"}
	if len(got.AllowedValues) != len(wantValues) {
		t.Fatalf("AllowedValues = %v, want %v", got.AllowedValues, wantValues)
	}
	for i, want := range wantValues {
		if got.AllowedValues[i] != want {
			t.Errorf("AllowedValues[%d] = %q, want %q", i, got.AllowedValues[i], want)
		}
	}
}

func TestFetchCurrentRoomSetpointOffState(t *testing.T) {
	path := HeatingCircuitPath("hc1", "currentRoomSetpoint")
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			t.Errorf("path = %q, want %q", r.URL.Path, path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"/heatingCircuits/hc1/currentRoomSetpoint","type":"floatValue","value":0,"unitOfMeasure":"C","state":[{"off":0}]}`))
	})

	got, err := client.FetchValue(context.Background(), path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Value == nil || *got.Value != 0 {
		t.Fatalf("Value = %v, want 0", got.Value)
	}
	if got.SensorFaultState() != "off" {
		t.Errorf("SensorFaultState() = %q, want off", got.SensorFaultState())
	}
}

func TestTemperatureEndpointPaths(t *testing.T) {
	paths := []string{
		PathOutdoorTemperature,
		PathSupplyTemperature,
		PathReturnTemperature,
		DHWCircuitPath("dhw1", "actualTemp"),
		DHWCircuitPath("dhw1", "currentSetpoint"),
	}
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		found := false
		for _, path := range paths {
			if r.URL.Path == path {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("unexpected temperature path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"floatValue","value":20,"unitOfMeasure":"C"}`))
	})

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			if _, err := client.FetchValue(context.Background(), path); err != nil {
				t.Fatalf("FetchValue(%q) error = %v", path, err)
			}
		})
	}
}

func TestValueResponseSensorFault(t *testing.T) {
	ptr := func(value float64) *float64 { return &value }
	response := ValueResponse{
		State: []map[string]float64{{"open": -32768}, {"short": 32767}},
	}
	tests := []struct {
		name      string
		value     *float64
		wantFault string
	}{
		{name: "normal", value: ptr(7.5)},
		{name: "open sensor", value: ptr(-32768), wantFault: "open"},
		{name: "short sensor", value: ptr(32767), wantFault: "short"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response.Value = tt.value
			if got := response.SensorFaultState(); got != tt.wantFault {
				t.Errorf("SensorFaultState() = %q, want %q", got, tt.wantFault)
			}
			if got := response.SensorFault(); got != (tt.wantFault != "") {
				t.Errorf("SensorFault() = %t, want %t", got, tt.wantFault != "")
			}
		})
	}
}

func TestFetchEmonErrorStatus(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	if _, err := client.FetchEmon(context.Background(), PathDHWConsumption); err == nil {
		t.Fatal("expected error for status 401")
	}
}

func TestFetchValueNotFoundSentinel(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	if _, err := client.FetchValue(context.Background(), HeatingCircuitPath("hc1", "mixerPosition")); !errors.Is(err, ErrNotFound) {
		t.Errorf("FetchValue() error = %v, want ErrNotFound", err)
	}
}

func TestFetchValueOtherStatusIsNotNotFound(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if _, err := client.FetchValue(context.Background(), HeatingCircuitPath("hc1", "mixerPosition")); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("FetchValue() error = %v, want non-404 error", err)
	}
}

func TestEmonValuesMX400(t *testing.T) {
	// Echte Antwort eines MX400: kein "electricity", nur Verdichter und Zuheizer.
	var resp EmonResponse
	body := `{"id":"/heatSources/emon/chConsumption","type":"emonValue","writeable":0,"unit":"kWh",
		"values":[{"outputProduced":24797.11},{"eheater":0.0},{"compressor":6265.99}]}`
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v := resp.Values
	if v.Electricity != nil {
		t.Errorf("Electricity = %v, want nil", *v.Electricity)
	}
	if v.Compressor == nil || *v.Compressor != 6265.99 {
		t.Errorf("Compressor = %v, want 6265.99", v.Compressor)
	}
	if v.EHeater == nil || *v.EHeater != 0 {
		t.Errorf("EHeater = %v, want 0", v.EHeater)
	}
	if total := v.TotalElectricity(); total == nil || *total != 6265.99 {
		t.Errorf("TotalElectricity = %v, want 6265.99", total)
	}
}

func TestTotalElectricity(t *testing.T) {
	ptr := func(f float64) *float64 { return &f }

	tests := []struct {
		name   string
		values EmonValues
		want   *float64
	}{
		{"electricity provided", EmonValues{Electricity: ptr(10), Compressor: ptr(3)}, ptr(10)},
		{"compressor and eheater", EmonValues{Compressor: ptr(1.1), EHeater: ptr(2.2)}, ptr(3.3)},
		{"only eheater", EmonValues{EHeater: ptr(2)}, ptr(2)},
		{"nothing", EmonValues{OutputProduced: ptr(5)}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.values.TotalElectricity()
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("got %v, want nil", *got)
			case tt.want != nil && (got == nil || *got != *tt.want):
				t.Errorf("got %v, want %v", got, *tt.want)
			}
		})
	}
}

func TestEmonValuesMissingFields(t *testing.T) {
	var values EmonValues
	if err := json.Unmarshal([]byte(`[{"unknown": 2}]`), &values); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if values.Electricity != nil || values.OutputProduced != nil || values.Compressor != nil {
		t.Errorf("expected nil values, got %+v", values)
	}
}

func TestEmonValuesInvalidJSON(t *testing.T) {
	var values EmonValues
	if err := json.Unmarshal([]byte(`{"electricity": 2}`), &values); err == nil {
		t.Fatal("expected error for non-array values")
	}
}
