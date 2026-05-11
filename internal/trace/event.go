// Package trace defines the JSONL event format used by SNES provenance tools.
package trace

import (
	"encoding/json"
	"fmt"
	"io"
)

const SchemaVersion = 1

type PC struct {
	Bank uint8  `json:"bank"`
	Addr uint16 `json:"addr"`
}

func (p PC) String() string {
	return fmt.Sprintf("%02x:%04x", p.Bank, p.Addr)
}

type Event struct {
	ID      uint64 `json:"id"`
	Schema  int    `json:"schema"`
	Kind    string `json:"kind"`
	Frame   int    `json:"frame"`
	Cycle   uint64 `json:"cycle,omitempty"`
	PC      *PC    `json:"pc,omitempty"`
	Name    string `json:"name,omitempty"`
	Space   string `json:"space,omitempty"`
	Addr    uint32 `json:"addr,omitempty"`
	End     uint32 `json:"end,omitempty"`
	Width   int    `json:"width,omitempty"`
	Value   uint64 `json:"value,omitempty"`
	Before  uint64 `json:"before,omitempty"`
	After   uint64 `json:"after,omitempty"`
	Op      string `json:"op,omitempty"`
	Channel int    `json:"channel,omitempty"`
	Mode    uint8  `json:"mode,omitempty"`
	Source  Range  `json:"source,omitempty"`
	Dest    Range  `json:"dest,omitempty"`
	Hash    string `json:"hash,omitempty"`
}

type Range struct {
	Space string `json:"space"`
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
}

type Writer struct {
	w    *json.Encoder
	next uint64
}

func NewWriter(w io.Writer) *Writer {
	return &Writer{w: json.NewEncoder(w)}
}

func (w *Writer) Emit(e Event) error {
	e.ID = w.next
	w.next++
	e.Schema = SchemaVersion
	return w.w.Encode(e)
}

func Decode(r io.Reader) ([]Event, error) {
	dec := json.NewDecoder(r)
	var events []Event
	for {
		var e Event
		if err := dec.Decode(&e); err != nil {
			if err == io.EOF {
				return events, nil
			}
			return nil, err
		}
		events = append(events, e)
	}
}
