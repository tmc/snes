package trace

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// An Outcome says how a run ended. Only OutcomeComplete describes a
// full replay.
type Outcome string

const (
	OutcomeComplete  Outcome = "complete"
	OutcomeCPUFault  Outcome = "cpu_fault"
	OutcomeTimeout   Outcome = "timeout"
	OutcomeSinkError Outcome = "sink_error"
	OutcomeLimit     Outcome = "limit"
)

// A Receipt is written after a stream is closed. A stream without a
// receipt is incomplete.
type Receipt struct {
	Schema           int     `json:"schema"`
	Outcome          Outcome `json:"outcome"`
	LastSeq          uint64  `json:"last_seq"`
	EventCount       int     `json:"event_count"`
	StreamSHA256     string  `json:"stream_sha256"`
	TruncationReason string  `json:"truncation_reason,omitempty"`
	Error            string  `json:"error,omitempty"`
}

// WriteReceipt writes r to path. The file appears only when complete.
func WriteReceipt(path string, r Receipt) error {
	r.Schema = SchemaVersion
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode receipt: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".receipt-*")
	if err != nil {
		return fmt.Errorf("write receipt: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write receipt: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write receipt: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write receipt: %w", err)
	}
	return nil
}

// DecodeStrict decodes a stream and rejects records whose schema is
// not SchemaVersion.
func DecodeStrict(r io.Reader) ([]Event, error) {
	events, err := Decode(r)
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		if e.Schema != SchemaVersion {
			return nil, fmt.Errorf("trace event %d: unsupported schema %d, want %d", e.ID, e.Schema, SchemaVersion)
		}
	}
	return events, nil
}
