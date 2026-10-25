// Package mapper converts REST response bodies into MQTT messages.
package mapper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

type Message struct {
	Topic   string
	Payload []byte
	Retain  bool
}

type Mapper interface {
	Map(body []byte) ([]Message, error)
}

type Func func([]byte) ([]Message, error)

func (f Func) Map(body []byte) ([]Message, error) { return f(body) }

type Passthrough struct {
	Topic  string
	Retain bool
}

func (p Passthrough) Map(body []byte) ([]Message, error) {
	return []Message{{Topic: p.Topic, Payload: body, Retain: p.Retain}}, nil
}

type JSONFields struct {
	Prefix string
	Retain bool
}

func (m JSONFields) Map(body []byte) ([]Message, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("decode json object: %w", err)
	}
	if object == nil {
		return nil, fmt.Errorf("expected json object")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("expected exactly one json object")
	}
	var messages []Message
	seen := make(map[string]bool)
	if err := m.walk(object, strings.TrimRight(m.Prefix, "/"), &messages, seen); err != nil {
		return nil, err
	}
	return messages, nil
}

var topicReplacer = strings.NewReplacer("/", "_", "+", "_", "#", "_", " ", "_", "\x00", "_")

func (m JSONFields) walk(object map[string]any, prefix string, messages *[]Message, seen map[string]bool) error {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		part := topicReplacer.Replace(key)
		if part == "" {
			part = "_"
		}
		topic := part
		if prefix != "" {
			topic = prefix + "/" + part
		}
		if !utf8.ValidString(topic) || len(topic) > 65535 || strings.ContainsAny(topic, "+#\x00") {
			return fmt.Errorf("invalid mapped topic")
		}
		if seen[topic] {
			return fmt.Errorf("mapped topic collision at %q", topic)
		}
		seen[topic] = true
		value := object[key]
		if nested, ok := value.(map[string]any); ok {
			if err := m.walk(nested, topic, messages, seen); err != nil {
				return err
			}
			continue
		}
		var payload []byte
		if text, ok := value.(string); ok {
			payload = []byte(text)
		} else {
			var err error
			payload, err = json.Marshal(value)
			if err != nil {
				return fmt.Errorf("encode field %q: %w", key, err)
			}
		}
		*messages = append(*messages, Message{Topic: topic, Payload: payload, Retain: m.Retain})
	}
	return nil
}
