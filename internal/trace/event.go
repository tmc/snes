// Package trace defines the JSONL event format used by SNES provenance tools.
package trace

import (
	"encoding/json"
	"fmt"
	"io"
)

// SchemaVersion is the version written in every record.
//
// Version 2 adds A and E to CPU contexts, fetched instruction bytes in
// cpu_step and cpu_block, and the run, cpu_insn, cpu_transition and gap
// records. Version 1 streams lack these and cannot be upgraded.
const SchemaVersion = 2

type PC struct {
	Bank uint8  `json:"bank"`
	Addr uint16 `json:"addr"`
}

func (p PC) String() string {
	return fmt.Sprintf("%02x:%04x", p.Bank, p.Addr)
}

type Event struct {
	ID           uint64      `json:"id"`
	Schema       int         `json:"schema"`
	Kind         string      `json:"kind"`
	Frame        int         `json:"frame"`
	Cycle        uint64      `json:"cycle,omitempty"`
	PC           *PC         `json:"pc,omitempty"`
	CPU          *CPUContext `json:"cpu,omitempty"`
	CPUAfter     *CPUContext `json:"cpu_after,omitempty"`
	Name         string      `json:"name,omitempty"`
	Register     string      `json:"register,omitempty"`
	Category     string      `json:"category,omitempty"`
	Space        string      `json:"space,omitempty"`
	Addr         uint32      `json:"addr,omitempty"`
	End          uint32      `json:"end,omitempty"`
	Width        int         `json:"width,omitempty"`
	Value        uint64      `json:"value,omitempty"`
	Before       *uint64     `json:"before,omitempty"`
	After        *uint64     `json:"after,omitempty"`
	Op           string      `json:"op,omitempty"`
	Channel      int         `json:"channel,omitempty"`
	Mode         uint8       `json:"mode,omitempty"`
	Count        int         `json:"count,omitempty"`
	Direction    string      `json:"direction,omitempty"`
	Target       uint8       `json:"target,omitempty"`
	DestRegister uint16      `json:"dest_register,omitempty"`
	DMA          *DMAContext `json:"dma,omitempty"`
	Source       Range       `json:"source,omitempty"`
	Dest         Range       `json:"dest,omitempty"`
	Hash         string      `json:"hash,omitempty"`
	EndPC        *PC         `json:"end_pc,omitempty"`
	SuccessorPC  *PC         `json:"successor_pc,omitempty"`
	BranchKind   string      `json:"branch_kind,omitempty"`
	Run          *RunInfo    `json:"run,omitempty"`
	Insn         *Insn       `json:"insn,omitempty"`
	Transition   *Transition `json:"transition,omitempty"`
	Gap          *Gap        `json:"gap,omitempty"`
}

type DMAContext struct {
	Channel      int    `json:"channel"`
	Mode         uint8  `json:"mode"`
	Count        int    `json:"count"`
	Direction    string `json:"direction"`
	Target       uint8  `json:"target"`
	DestRegister uint16 `json:"dest_register"`
}

type CPUContext struct {
	A             uint16   `json:"a"`
	E             bool     `json:"e"`
	PBR           uint8    `json:"pbr"`
	PC            uint16   `json:"pc"`
	DBR           uint8    `json:"dbr"`
	DP            uint16   `json:"dp"`
	X             uint16   `json:"x"`
	Y             uint16   `json:"y"`
	S             uint16   `json:"s"`
	P             uint8    `json:"p"`
	MWidth        int      `json:"m_width,omitempty"`
	XWidth        int      `json:"x_width,omitempty"`
	Opcode        uint8    `json:"opcode"`
	Bytes         []uint16 `json:"bytes,omitempty"`
	Disasm        string   `json:"disasm,omitempty"`
	Addressing    string   `json:"addressing,omitempty"`
	EffectiveAddr *uint32  `json:"effective_addr,omitempty"`
	EffectiveExpr string   `json:"effective_expr,omitempty"`
}

type Range struct {
	Space string `json:"space"`
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
}

type Writer struct {
	w         io.Writer
	next      uint64
	kinds     map[string]int
	limit     int
	byteLimit int
	bytes     int
	truncated string // reason, or empty
	err       error
}

func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w, kinds: map[string]int{}}
}

func (w *Writer) SetLimit(n int) {
	w.limit = n
}

func (w *Writer) SetByteLimit(n int) {
	w.byteLimit = n
}

// Emit writes e. After the first write error, Emit writes nothing and
// returns that error. After a limit is reached, Emit writes nothing and
// Truncated reports the reason.
func (w *Writer) Emit(e Event) error {
	if w.err != nil {
		return w.err
	}
	if w.truncated != "" {
		return nil
	}
	if w.limit > 0 && int(w.next) >= w.limit {
		w.truncated = "event_limit"
		return nil
	}
	e.ID = w.next
	w.next++
	e.Schema = SchemaVersion
	w.kinds[e.Kind]++
	data, err := json.Marshal(e)
	if err != nil {
		w.err = fmt.Errorf("encode trace event: %w", err)
		return w.err
	}
	data = append(data, '\n')
	if w.byteLimit > 0 && w.bytes+len(data) > w.byteLimit {
		w.next--
		w.kinds[e.Kind]--
		if w.kinds[e.Kind] == 0 {
			delete(w.kinds, e.Kind)
		}
		w.truncated = "byte_limit"
		return nil
	}
	if _, err := w.w.Write(data); err != nil {
		w.err = fmt.Errorf("write trace event: %w", err)
		return w.err
	}
	w.bytes += len(data)
	return nil
}

func (w *Writer) Count() int {
	return int(w.next)
}

func (w *Writer) Bytes() int {
	return w.bytes
}

// Truncated reports whether a limit stopped output.
func (w *Writer) Truncated() bool {
	return w.truncated != ""
}

// TruncationReason returns "event_limit", "byte_limit", or "".
func (w *Writer) TruncationReason() string {
	return w.truncated
}

// Err returns the first write error.
func (w *Writer) Err() error {
	return w.err
}

func (w *Writer) Kinds() map[string]int {
	out := make(map[string]int, len(w.kinds))
	for k, v := range w.kinds {
		out[k] = v
	}
	return out
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
