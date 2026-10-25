package mapper

import (
	"reflect"
	"testing"
)

func TestJSONFields(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []Message
		fail bool
	}{
		{"sorted nested values", `{"z":null,"b":{"temp":21.5},"a":"warm","list":[1,"x"],"bool":true}`,
			[]Message{
				{"wp/a", []byte("warm"), true},
				{"wp/b/temp", []byte("21.5"), true},
				{"wp/bool", []byte("true"), true},
				{"wp/list", []byte(`[1,"x"]`), true},
				{"wp/z", []byte("null"), true},
			}, false},
		{"sanitize", `{"a/b +#":1}`, []Message{{"wp/a_b___", []byte("1"), true}}, false},
		{"large integer", `{"n":9007199254740993}`, []Message{{"wp/n", []byte("9007199254740993"), true}}, false},
		{"empty object", `{}`, nil, false},
		{"empty nested object", `{"empty":{}}`, nil, false},
		{"empty key", `{"":1}`, []Message{{"wp/_", []byte("1"), true}}, false},
		{"nul key", `{"\u0000":1}`, []Message{{"wp/_", []byte("1"), true}}, false},
		{"collision", `{"a/b":1,"a_b":2}`, nil, true},
		{"object collision", `{"a/b":{},"a_b":2}`, nil, true},
		{"invalid json", `{`, nil, true},
		{"array", `[]`, nil, true},
		{"null", `null`, nil, true},
		{"scalar", `"value"`, nil, true},
		{"extra json", `{} {}`, nil, true},
		{"trailing junk", `{} junk`, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (JSONFields{Prefix: "wp/", Retain: true}).Map([]byte(tt.body))
			if (err != nil) != tt.fail {
				t.Fatalf("Map() error = %v, want error %v", err, tt.fail)
			}
			if !tt.fail && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Map() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestMapperAdapters(t *testing.T) {
	body := []byte("not json")
	var m Mapper = Passthrough{Topic: "wp", Retain: true}
	got, err := m.Map(body)
	if err != nil || len(got) != 1 || got[0].Topic != "wp" || !got[0].Retain || string(got[0].Payload) != string(body) {
		t.Fatalf("passthrough = %v, %v", got, err)
	}
	var custom Mapper = Func(func(b []byte) ([]Message, error) {
		return []Message{{Topic: "custom", Payload: b}}, nil
	})
	got, err = custom.Map(body)
	if err != nil || got[0].Topic != "custom" {
		t.Fatalf("custom mapper = %v, %v", got, err)
	}
}
