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
	ID           uint64      `json:"id"`
	Schema       int         `json:"schema"`
	Kind         string      `json:"kind"`
	Frame        int         `json:"frame"`
	Cycle        uint64      `json:"cycle,omitempty"`
	PC           *PC         `json:"pc,omitempty"`
	CPU          *CPUContext `json:"cpu,omitempty"`
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
	w     *json.Encoder
	next  uint64
	kinds map[string]int
}

func NewWriter(w io.Writer) *Writer {
	return &Writer{w: json.NewEncoder(w), kinds: map[string]int{}}
}

func (w *Writer) Emit(e Event) error {
	e.ID = w.next
	w.next++
	e.Schema = SchemaVersion
	w.kinds[e.Kind]++
	return w.w.Encode(e)
}

func (w *Writer) Count() int {
	return int(w.next)
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
