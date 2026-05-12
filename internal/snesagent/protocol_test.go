package snesagent

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestJSONProtocol(t *testing.T) {
	var buf bytes.Buffer
	w := NewJSONWriter(&buf)
	obs := Observation{
		Type:            "observation",
		Frame:           7,
		Width:           256,
		Height:          224,
		FrameBuffer:     []byte{1, 2, 3},
		RAMOffset:       16,
		RAM:             []byte{4, 5},
		Actor:           "policy",
		HumanActive:     true,
		HumanButtons:    []string{"left"},
		ExecutedButtons: []string{"a"},
		ExecutedAction:  "left_a",
	}
	if err := w.Write(obs); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var got Observation
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Frame != obs.Frame || got.Width != obs.Width || !bytes.Equal(got.FrameBuffer, obs.FrameBuffer) || !bytes.Equal(got.RAM, obs.RAM) {
		t.Fatalf("observation = %+v, want %+v", got, obs)
	}

	r := NewJSONReader(bytes.NewBufferString(`{"type":"action","action_name":"right_a"}` + "\n"))
	msg, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if msg.Type != "action" || msg.ActionName != "right_a" {
		t.Fatalf("message = %+v", msg)
	}
}

func TestProtoProtocol(t *testing.T) {
	action := uint32(3)
	var buf bytes.Buffer
	pw := NewProtoWriter(&buf)
	obs := Observation{
		Type:            "observation",
		Frame:           9,
		Width:           256,
		Height:          224,
		FrameBuffer:     []byte{1, 2, 3, 4},
		RAMOffset:       32,
		RAM:             []byte{9, 8, 7},
		Actor:           "policy",
		HumanActive:     true,
		HumanButtons:    []string{"right"},
		ExecutedButtons: []string{"right", "a"},
		ExecutedAction:  "right_a",
	}
	if err := pw.Write(obs); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty proto observation")
	}

	msg := ClientMessage{Type: "action", Action: action, ActionName: "down"}
	data := MarshalClientMessageProto(msg)
	var framed bytes.Buffer
	if err := writeDelimited(&framed, data); err != nil {
		t.Fatalf("writeDelimited: %v", err)
	}
	got, err := NewProtoReader(&framed).Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	gotAction, ok := got.ActionIndex()
	if got.Type != msg.Type || !ok || *gotAction != action || got.ActionName != msg.ActionName {
		t.Fatalf("message = %+v, want %+v", got, msg)
	}
}

func TestParseFormat(t *testing.T) {
	tests := []struct {
		in      string
		want    Format
		wantErr bool
	}{
		{"", FormatJSONL, false},
		{"jsonl", FormatJSONL, false},
		{"proto", FormatProto, false},
		{"xml", "", true},
	}
	for _, tt := range tests {
		got, err := ParseFormat(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("ParseFormat(%q) succeeded", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseFormat(%q): %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("ParseFormat(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
