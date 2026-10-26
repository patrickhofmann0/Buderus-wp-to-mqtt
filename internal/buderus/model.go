package buderus

import (
	"encoding/json"
	"fmt"
	"math"
)

// EmonResponse ist die Antwort der Endpunkte /heatSources/emon/*Consumption.
//
// Beispiel (MX400):
//
//	{"id":"/heatSources/emon/chConsumption","type":"emonValue","unit":"kWh",
//	 "values":[{"outputProduced":24797.11},{"eheater":0.0},{"compressor":6265.99}]}
//
// Die Struct-Tags `json:"..."` entsprechen @JsonProperty in Jackson.
type EmonResponse struct {
	ID     string     `json:"id"`
	Type   string     `json:"type"`
	Values EmonValues `json:"values"`
	Unit   string     `json:"unit"`
}

// ValueResponse ist die Antwort eines Endpunkts, der einen einzelnen Messwert liefert.
type ValueResponse struct {
	ID    string   `json:"id"`
	Type  string   `json:"type"`
	Value *float64 `json:"value"`
	Unit  string   `json:"unit"`
}

// EmonValues sind die Energiewerte in der Einheit aus EmonResponse.Unit.
// Pointer (*float64) unterscheiden "nicht geliefert" (nil) von "0".
type EmonValues struct {
	// Electricity ist der gesamte Stromverbrauch. Nicht jedes Gateway liefert ihn (z. B. MX400 nicht).
	Electricity *float64
	// Compressor ist der Stromverbrauch des Verdichters.
	Compressor *float64
	// EHeater ist der Stromverbrauch des elektrischen Zuheizers.
	EHeater *float64
	// Burner ist der Verbrauch eines Brenners (Hybridsysteme).
	Burner *float64
	// OutputProduced ist die erzeugte Wärme.
	OutputProduced *float64
}

// UnmarshalJSON wandelt die Liste einzelner Objekte in ein Struct um.
// Die API liefert [{"compressor":2},{"outputProduced":30}] statt eines einzelnen Objekts.
//
// Für Java-Entwickler: Das entspricht einem eigenen Jackson-JsonDeserializer.
// encoding/json ruft diese Methode automatisch auf, weil EmonValues das
// Interface json.Unmarshaler implementiert (implizit, ohne "implements").
func (v *EmonValues) UnmarshalJSON(data []byte) error {
	var entries []map[string]float64
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("decode emon values: %w", err)
	}

	for _, entry := range entries {
		for key, value := range entry {
			// Kopie anlegen, damit jeder Pointer auf einen eigenen Wert zeigt.
			val := value
			switch key {
			case "electricity":
				v.Electricity = &val
			case "compressor":
				v.Compressor = &val
			case "eheater":
				v.EHeater = &val
			case "burner":
				v.Burner = &val
			case "outputProduced":
				v.OutputProduced = &val
			}
		}
	}
	return nil
}

// TotalElectricity liefert den gesamten Stromverbrauch.
// Fehlt "electricity", wird er aus Verdichter + Zuheizer berechnet.
// Ist keiner der Werte vorhanden, ist das Ergebnis nil.
func (v EmonValues) TotalElectricity() *float64 {
	if v.Electricity != nil {
		return v.Electricity
	}
	if v.Compressor == nil && v.EHeater == nil {
		return nil
	}

	var sum float64
	for _, part := range []*float64{v.Compressor, v.EHeater} {
		if part != nil {
			sum += *part
		}
	}
	// Auf 3 Nachkommastellen runden, um Artefakte wie 3.3000000000000003 zu vermeiden.
	sum = math.Round(sum*1000) / 1000
	return &sum
}
