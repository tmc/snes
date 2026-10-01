package extractor

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// HistoryWrite represents a historical write to WRAM.
type HistoryWrite struct {
	Cycle     uint64
	ID        uint64
	Value     uint8
	Actor     string // "cpu", "dma", "unknown"
	CPUPBR    byte
	CPUPC     uint16
	CPUOpcode byte
	CPUBytes  []byte
	CPUS      uint16
	CPUA      uint16
	CPUX      uint16
	CPUY      uint16
	CPUD      uint16
	CPUDB     byte
	CPUP      byte
	CPUE      bool
}

// WriteHistory stores past writes indexed by canonical WRAM address.
type WriteHistory struct {
	writesByAddr map[uint32][]HistoryWrite
	minCycle     uint64
	pinnedWRAM   []byte
}

// LineScanner abstracts line scanning for history ingestion.
type LineScanner interface {
	Scan() bool
	Bytes() []byte
	Err() error
}

// SetPinnedWRAM attaches deep-copied pinned initial WRAM bytes from a validated checkpoint state.
func (h *WriteHistory) SetPinnedWRAM(wram []byte) {
	if wram == nil {
		h.pinnedWRAM = nil
		return
	}
	h.pinnedWRAM = make([]byte, len(wram))
	copy(h.pinnedWRAM, wram)
}

// LoadCheckpointWRAM extracts the verified 128KB WRAM slice from a gob-encoded checkpoint state file
// and returns both the deep-copied slice and the SHA-256 digest from a single read.
func LoadCheckpointWRAM(path string, expectedROMHashHex string) ([]byte, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read checkpoint state %q: %w", path, err)
	}
	cpSum := sha256.Sum256(data)
	cpSHA := hex.EncodeToString(cpSum[:])

	var state struct {
		Version   uint32
		ROMHash   [32]byte
		BusMDR    uint8
		BusMEMSEL uint8
		WRAM      []byte
	}
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&state); err != nil {
		return nil, "", fmt.Errorf("decode checkpoint state %q: %w", path, err)
	}
	if state.Version == 0 {
		return nil, "", fmt.Errorf("checkpoint state %q has invalid version %d", path, state.Version)
	}
	if expectedROMHashHex != "" {
		gotROM := hex.EncodeToString(state.ROMHash[:])
		if !strings.EqualFold(gotROM, expectedROMHashHex) {
			return nil, "", fmt.Errorf("checkpoint ROM hash mismatch: got %s, want %s", gotROM, expectedROMHashHex)
		}
	}
	const WRAMSize = 128 * 1024
	if len(state.WRAM) != WRAMSize {
		return nil, "", fmt.Errorf("checkpoint state %q has invalid WRAM size %d (want %d)", path, len(state.WRAM), WRAMSize)
	}
	wram := make([]byte, WRAMSize)
	copy(wram, state.WRAM)
	return wram, cpSHA, nil
}

// NewWriteHistory loads history writes from a history stream scanner.
// Memory allocation scales linearly with event count, bounded by MaxHistoryEvents.
func NewWriteHistory(scanner LineScanner) (*WriteHistory, error) {
	h := &WriteHistory{
		writesByAddr: make(map[uint32][]HistoryWrite),
		minCycle:     ^uint64(0),
	}

	eventCount := 0
	for scanner.Scan() {
		eventCount++
		if eventCount > MaxHistoryEvents {
			return nil, fmt.Errorf("history event limit exceeded: %d > %d", eventCount, MaxHistoryEvents)
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev RawEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("unmarshal history line: %w", err)
		}
		if ev.Cycle < h.minCycle {
			h.minCycle = ev.Cycle
		}

		if ev.Kind == "bus" && ev.Op == "write" && ev.Space == "wram" {
			addr, err := canonicalWRAMOffset(ev.Addr)
			if err != nil {
				return nil, fmt.Errorf("history event %d: %w", ev.ID, err)
			}
			val := ev.After
			if val == 0 && ev.Value != 0 {
				val = ev.Value
			}
			hw := HistoryWrite{
				Cycle: ev.Cycle,
				ID:    ev.ID,
				Value: val,
			}
			if len(ev.DMA) > 0 && string(ev.DMA) != "null" {
				hw.Actor = "dma"
			} else if ev.CPU != nil && completeHistoryCPU(line) && (len(ev.DMA) == 0 || string(ev.DMA) == "null") {
				hw.Actor = "cpu"
				hw.CPUPBR = ev.CPU.PBR
				hw.CPUPC = ev.CPU.PC
				hw.CPUOpcode = ev.CPU.Opcode
				hw.CPUBytes = append([]byte(nil), ev.CPU.Bytes...)
				hw.CPUS = ev.CPU.S
				hw.CPUA = ev.CPU.A
				hw.CPUX = ev.CPU.X
				hw.CPUY = ev.CPU.Y
				var context struct {
					CPU struct {
						D uint16 `json:"dp"`
					} `json:"cpu"`
				}
				if err := json.Unmarshal(line, &context); err != nil {
					return nil, fmt.Errorf("history CPU context: %w", err)
				}
				hw.CPUD = context.CPU.D
				hw.CPUDB = ev.CPU.DBR
				hw.CPUP = ev.CPU.P
				hw.CPUE = ev.CPU.E
			} else {
				hw.Actor = "unknown"
			}
			h.writesByAddr[addr] = append(h.writesByAddr[addr], hw)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan history: %w", err)
	}

	// Sort each address bucket by cycle, then event ID.
	for _, ws := range h.writesByAddr {
		sort.Slice(ws, func(i, j int) bool {
			if ws[i].Cycle != ws[j].Cycle {
				return ws[i].Cycle < ws[j].Cycle
			}
			return ws[i].ID < ws[j].ID
		})
	}

	return h, nil
}

// lookupWriteBefore returns the most recent write value to canonical address before or at cutoffCycle.
func (h *WriteHistory) lookupWriteBefore(addr uint32, cutoffCycle uint64) (uint8, error) {
	ws := h.writesByAddr[addr]
	idx := sort.Search(len(ws), func(i int) bool {
		return ws[i].Cycle > cutoffCycle
	})
	if idx > 0 {
		return ws[idx-1].Value, nil
	}
	if len(h.pinnedWRAM) > 0 {
		wramOffset := int(addr & 0x1FFFF)
		if wramOffset < len(h.pinnedWRAM) {
			return h.pinnedWRAM[wramOffset], nil
		}
	}
	return 0, fmt.Errorf("no write to address 0x%06X before cycle %d", addr, cutoffCycle)
}

// latestWriteBefore returns the most recent HistoryWrite event to canonical address before or at cutoffCycle.
func (h *WriteHistory) latestWriteBefore(addr uint32, cutoffCycle uint64) (HistoryWrite, bool) {
	ws := h.writesByAddr[addr]
	idx := sort.Search(len(ws), func(i int) bool {
		return ws[i].Cycle > cutoffCycle
	})
	if idx > 0 {
		return ws[idx-1], true
	}
	return HistoryWrite{}, false
}

// ClassifyInitialMemory classifies each initial memory cell against write history before cutoffCycle.
//
// Verification rules:
//  1. Cells with a prior write before cutoffCycle must match the written value exactly
//     ("confirmed_by_prior_write").
//  2. Missing prior writes are refused unless backed by verified pinned checkpoint state
//     matching the read value ("restored_checkpoint_wram").
//  3. Any cell unconfirmed by prior write or verified pinned state causes immediate occurrence refusal
//     ("unconfirmed_initial_memory:addr=...").
func (h *WriteHistory) ClassifyInitialMemory(cells []MemoryCell, cutoffCycle uint64, startBoundary string) ([]MemorySource, error) {
	var sources []MemorySource

	for _, cell := range cells {
		ws := h.writesByAddr[cell.Address]
		// Find last write with cycle <= cutoffCycle.
		idx := sort.Search(len(ws), func(i int) bool {
			return ws[i].Cycle > cutoffCycle
		})

		if idx > 0 {
			lastWrite := ws[idx-1]
			if lastWrite.Value == cell.Value {
				sources = append(sources, MemorySource{
					Address: cell.Address,
					Source:  "confirmed_by_prior_write",
				})
				continue
			}
			return nil, fmt.Errorf("initial memory mismatch at 0x%06X: read %d, history wrote %d at cycle %d",
				cell.Address, cell.Value, lastWrite.Value, lastWrite.Cycle)
		}

		// idx == 0: No prior write observed before cutoffCycle.
		// Refuse missing prior writes unless an actual pinned initial state establishes each address/value.
		if (startBoundary == "restored_state" || startBoundary == "") && len(h.pinnedWRAM) > 0 {
			wramOffset := int(cell.Address & 0x1FFFF)
			if wramOffset < len(h.pinnedWRAM) {
				if h.pinnedWRAM[wramOffset] == cell.Value {
					sources = append(sources, MemorySource{
						Address: cell.Address,
						Source:  "restored_checkpoint_wram",
					})
					continue
				}
				return nil, fmt.Errorf("checkpoint memory mismatch at 0x%06X: read %d, checkpoint had %d",
					cell.Address, cell.Value, h.pinnedWRAM[wramOffset])
			}
		}

		// Cell value is unconfirmed by prior write or pinned state; refuse occurrence.
		return nil, fmt.Errorf("unconfirmed_initial_memory:addr=0x%06X", cell.Address)
	}

	return sources, nil
}

// completeHistoryCPU requires measured byte metadata and explicit CPU fields.
// Missing zero-valued fields are not observations of zero.
func completeHistoryCPU(line []byte) bool {
	var e struct {
		Schema *int                       `json:"schema"`
		Width  *int                       `json:"width"`
		Value  *byte                      `json:"value"`
		After  *byte                      `json:"after"`
		CPU    map[string]json.RawMessage `json:"cpu"`
	}
	if json.Unmarshal(line, &e) != nil || e.Schema == nil || *e.Schema != 2 || e.Width == nil || *e.Width != 1 || (e.Value == nil && e.After == nil) {
		return false
	}
	if e.Value != nil && e.After != nil && *e.Value != *e.After {
		return false
	}
	for _, k := range []string{"a", "x", "y", "s", "dp", "dbr", "pbr", "pc", "p", "e", "opcode", "bytes"} {
		if b, ok := e.CPU[k]; !ok || string(b) == "null" {
			return false
		}
	}
	var dp uint16
	if json.Unmarshal(e.CPU["dp"], &dp) != nil {
		return false
	}
	return true
}
