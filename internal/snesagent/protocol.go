// Package snesagent defines the live SNES control socket protocol.
package snesagent

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// Format names a socket wire format.
type Format string

const (
	// FormatJSONL is newline-delimited JSON.
	FormatJSONL Format = "jsonl"
	// FormatProto is length-delimited protobuf.
	FormatProto Format = "proto"
)

// ParseFormat parses a socket wire format.
func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case "", FormatJSONL:
		return FormatJSONL, nil
	case FormatProto:
		return FormatProto, nil
	default:
		return "", fmt.Errorf("unknown snes agent socket format %q", s)
	}
}

// Command is a control command sent by an agent.
type Command struct {
	Type string `json:"type,omitempty"`
	Path string `json:"path,omitempty"`
}

// ClientMessage is sent by an agent to control input or state.
type ClientMessage struct {
	Type       string `json:"type,omitempty"`
	Action     any    `json:"action,omitempty"`
	ActionName string `json:"action_name,omitempty"`
	Path       string `json:"path,omitempty"`
	action     *uint32
}

// ActionIndex returns the numeric action if one was supplied.
func (m ClientMessage) ActionIndex() (*uint32, bool) {
	if m.action != nil {
		return m.action, true
	}
	switch v := m.Action.(type) {
	case uint32:
		return &v, true
	case float64:
		u := uint32(v)
		return &u, true
	case int:
		u := uint32(v)
		return &u, true
	}
	return nil, false
}

// ActionText returns the string action if one was supplied in action.
func (m ClientMessage) ActionText() (string, bool) {
	s, ok := m.Action.(string)
	return s, ok
}

// UnmarshalJSON accepts both the current action_name field and the legacy
// action string/number field.
func (m *ClientMessage) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type       string          `json:"type,omitempty"`
		Action     json.RawMessage `json:"action,omitempty"`
		ActionName string          `json:"action_name,omitempty"`
		Path       string          `json:"path,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Type = raw.Type
	m.ActionName = raw.ActionName
	m.Path = raw.Path
	m.Action = nil
	m.action = nil
	if len(raw.Action) == 0 {
		return nil
	}
	var u uint32
	if err := json.Unmarshal(raw.Action, &u); err == nil {
		m.Action = u
		m.action = &u
		return nil
	}
	var s string
	if err := json.Unmarshal(raw.Action, &s); err == nil {
		m.Action = s
		return nil
	}
	return fmt.Errorf("action must be string or number")
}

// Observation is streamed by the emulator to connected agents.
type Observation struct {
	Type            string   `json:"type"`
	Frame           int      `json:"frame"`
	Width           int      `json:"width"`
	Height          int      `json:"height"`
	FrameBuffer     []byte   `json:"framebuffer_u16le,omitempty"`
	RAMOffset       int64    `json:"ram_offset,omitempty"`
	RAM             []byte   `json:"ram,omitempty"`
	Actor           string   `json:"actor,omitempty"`
	HumanActive     bool     `json:"human_active"`
	HumanButtons    []string `json:"human_buttons,omitempty"`
	ExecutedButtons []string `json:"executed_buttons,omitempty"`
	ExecutedAction  string   `json:"executed_action_name,omitempty"`
}

// MarshalJSON emits the protobuf JSON field names plus the legacy base64
// aliases used by the original JSONL socket.
func (o Observation) MarshalJSON() ([]byte, error) {
	type observationJSON struct {
		Type              string   `json:"type"`
		Frame             int      `json:"frame"`
		Width             int      `json:"width"`
		Height            int      `json:"height"`
		FrameBuffer       []byte   `json:"framebuffer_u16le,omitempty"`
		FrameBufferLegacy []byte   `json:"framebuffer_u16le_base64,omitempty"`
		RAMOffset         int64    `json:"ram_offset,omitempty"`
		RAM               []byte   `json:"ram,omitempty"`
		RAMLegacy         []byte   `json:"ram_base64,omitempty"`
		Actor             string   `json:"actor,omitempty"`
		HumanActive       bool     `json:"human_active"`
		HumanButtons      []string `json:"human_buttons,omitempty"`
		ExecutedButtons   []string `json:"executed_buttons,omitempty"`
		ExecutedAction    string   `json:"executed_action_name,omitempty"`
	}
	return json.Marshal(observationJSON{
		Type:              o.Type,
		Frame:             o.Frame,
		Width:             o.Width,
		Height:            o.Height,
		FrameBuffer:       o.FrameBuffer,
		FrameBufferLegacy: o.FrameBuffer,
		RAMOffset:         o.RAMOffset,
		RAM:               o.RAM,
		RAMLegacy:         o.RAM,
		Actor:             o.Actor,
		HumanActive:       o.HumanActive,
		HumanButtons:      o.HumanButtons,
		ExecutedButtons:   o.ExecutedButtons,
		ExecutedAction:    o.ExecutedAction,
	})
}

// JSONReader reads newline-delimited JSON client messages.
type JSONReader struct {
	scanner *bufio.Scanner
}

// NewJSONReader returns a JSONL client-message reader.
func NewJSONReader(r io.Reader) *JSONReader {
	return &JSONReader{scanner: bufio.NewScanner(r)}
}

// Read reads the next client message.
func (r *JSONReader) Read() (ClientMessage, error) {
	if !r.scanner.Scan() {
		if err := r.scanner.Err(); err != nil {
			return ClientMessage{}, err
		}
		return ClientMessage{}, io.EOF
	}
	var msg ClientMessage
	if err := json.Unmarshal(r.scanner.Bytes(), &msg); err != nil {
		return ClientMessage{}, err
	}
	return msg, nil
}

// JSONWriter writes newline-delimited JSON observations.
type JSONWriter struct {
	enc *json.Encoder
}

// NewJSONWriter returns a JSONL observation writer.
func NewJSONWriter(w io.Writer) *JSONWriter {
	return &JSONWriter{enc: json.NewEncoder(w)}
}

// Write writes obs.
func (w *JSONWriter) Write(obs Observation) error {
	return w.enc.Encode(obs)
}

// ProtoReader reads length-delimited protobuf client messages.
type ProtoReader struct {
	r *bufio.Reader
}

// NewProtoReader returns a length-delimited protobuf client-message reader.
func NewProtoReader(r io.Reader) *ProtoReader {
	return &ProtoReader{r: bufio.NewReader(r)}
}

// Read reads the next client message.
func (r *ProtoReader) Read() (ClientMessage, error) {
	data, err := readDelimited(r.r)
	if err != nil {
		return ClientMessage{}, err
	}
	return unmarshalClientMessage(data)
}

// ProtoWriter writes length-delimited protobuf observations.
type ProtoWriter struct {
	w *bufio.Writer
}

// NewProtoWriter returns a length-delimited protobuf observation writer.
func NewProtoWriter(w io.Writer) *ProtoWriter {
	return &ProtoWriter{w: bufio.NewWriter(w)}
}

// Write writes obs.
func (w *ProtoWriter) Write(obs Observation) error {
	data := marshalObservation(obs)
	if err := writeDelimited(w.w, data); err != nil {
		return err
	}
	return w.w.Flush()
}

// MarshalClientMessageProto returns the protobuf encoding of msg.
func MarshalClientMessageProto(msg ClientMessage) []byte {
	var out []byte
	out = appendString(out, 1, msg.Type)
	if action, ok := msg.ActionIndex(); ok {
		out = appendVarint(out, 2, uint64(*action))
	}
	out = appendString(out, 3, msg.ActionName)
	out = appendString(out, 4, msg.Path)
	return out
}

// MarshalObservationProto returns the protobuf encoding of obs.
func MarshalObservationProto(obs Observation) []byte {
	return marshalObservation(obs)
}

func unmarshalClientMessage(data []byte) (ClientMessage, error) {
	var msg ClientMessage
	for len(data) > 0 {
		field, wire, rest, err := consumeTag(data)
		if err != nil {
			return ClientMessage{}, err
		}
		data = rest
		switch field {
		case 1:
			s, r, err := consumeString(data, wire)
			if err != nil {
				return ClientMessage{}, err
			}
			msg.Type = s
			data = r
		case 2:
			v, r, err := consumeVarint(data, wire)
			if err != nil {
				return ClientMessage{}, err
			}
			u := uint32(v)
			msg.Action = u
			msg.action = &u
			data = r
		case 3:
			s, r, err := consumeString(data, wire)
			if err != nil {
				return ClientMessage{}, err
			}
			msg.ActionName = s
			data = r
		case 4:
			s, r, err := consumeString(data, wire)
			if err != nil {
				return ClientMessage{}, err
			}
			msg.Path = s
			data = r
		default:
			r, err := skipField(data, wire)
			if err != nil {
				return ClientMessage{}, err
			}
			data = r
		}
	}
	return msg, nil
}

func marshalObservation(obs Observation) []byte {
	var out []byte
	out = appendString(out, 1, obs.Type)
	out = appendVarint(out, 2, uint64(obs.Frame))
	out = appendVarint(out, 3, uint64(obs.Width))
	out = appendVarint(out, 4, uint64(obs.Height))
	out = appendBytes(out, 5, obs.FrameBuffer)
	out = appendVarint(out, 6, uint64(obs.RAMOffset))
	out = appendBytes(out, 7, obs.RAM)
	out = appendString(out, 8, obs.Actor)
	if obs.HumanActive {
		out = appendVarint(out, 9, 1)
	}
	for _, s := range obs.HumanButtons {
		out = appendString(out, 10, s)
	}
	for _, s := range obs.ExecutedButtons {
		out = appendString(out, 11, s)
	}
	out = appendString(out, 12, obs.ExecutedAction)
	return out
}

func readDelimited(r *bufio.Reader) ([]byte, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return data, nil
}

func writeDelimited(w io.Writer, data []byte) error {
	var lenbuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenbuf[:], uint64(len(data)))
	if _, err := w.Write(lenbuf[:n]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func appendTag(out []byte, field int, wire int) []byte {
	return binary.AppendUvarint(out, uint64(field<<3|wire))
}

func appendVarint(out []byte, field int, v uint64) []byte {
	out = appendTag(out, field, 0)
	return binary.AppendUvarint(out, v)
}

func appendString(out []byte, field int, s string) []byte {
	if s == "" {
		return out
	}
	return appendBytes(out, field, []byte(s))
}

func appendBytes(out []byte, field int, b []byte) []byte {
	if len(b) == 0 {
		return out
	}
	out = appendTag(out, field, 2)
	out = binary.AppendUvarint(out, uint64(len(b)))
	return append(out, b...)
}

func consumeTag(data []byte) (int, int, []byte, error) {
	tag, n := binary.Uvarint(data)
	if n <= 0 {
		return 0, 0, nil, fmt.Errorf("decode protobuf tag")
	}
	return int(tag >> 3), int(tag & 7), data[n:], nil
}

func consumeVarint(data []byte, wire int) (uint64, []byte, error) {
	if wire != 0 {
		return 0, nil, fmt.Errorf("protobuf field: got wire %d, want varint", wire)
	}
	v, n := binary.Uvarint(data)
	if n <= 0 {
		return 0, nil, fmt.Errorf("decode protobuf varint")
	}
	return v, data[n:], nil
}

func consumeString(data []byte, wire int) (string, []byte, error) {
	b, rest, err := consumeBytes(data, wire)
	if err != nil {
		return "", nil, err
	}
	return string(b), rest, nil
}

func consumeBytes(data []byte, wire int) ([]byte, []byte, error) {
	if wire != 2 {
		return nil, nil, fmt.Errorf("protobuf field: got wire %d, want bytes", wire)
	}
	n, c := binary.Uvarint(data)
	if c <= 0 {
		return nil, nil, fmt.Errorf("decode protobuf length")
	}
	data = data[c:]
	if uint64(len(data)) < n {
		return nil, nil, fmt.Errorf("short protobuf bytes field")
	}
	return data[:n], data[n:], nil
}

func skipField(data []byte, wire int) ([]byte, error) {
	switch wire {
	case 0:
		_, rest, err := consumeVarint(data, wire)
		return rest, err
	case 2:
		_, rest, err := consumeBytes(data, wire)
		return rest, err
	default:
		return nil, fmt.Errorf("unsupported protobuf wire type %d", wire)
	}
}
