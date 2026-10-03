package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/tmc/snes/internal/recovery"
)

// ScaledOutputPacket represents the complete 4-output scaled calculation explainer packet.
type ScaledOutputPacket struct {
	Scope                  string             `json:"scope,omitempty"`
	TraceSHA256            string             `json:"trace_sha256,omitempty"`
	StreamSHA256           string             `json:"stream_sha256,omitempty"`
	ROMSHA256              string             `json:"rom_sha256,omitempty"`
	RecoveryDocumentSHA256 string             `json:"recovery_document_sha256,omitempty"`
	HardwarePrecisionModel string             `json:"hardware_precision_model,omitempty"`
	Cases                  []ScaledOutputCase `json:"cases"`
}

// ScaledOutputCase models one of the 4 anchored scaled calculation cases.
type ScaledOutputCase struct {
	CaseID              string                 `json:"case_id"`
	PhysicalOutput      string                 `json:"physical_output"`
	AnchorRetirementID  uint64                 `json:"anchor_retirement_id"`
	AnchorSeq           uint64                 `json:"anchor_seq"`
	TraceFrame          int                    `json:"trace_frame"`
	AnchorInstructionID string                 `json:"anchor_instruction_id"`
	AnchorAddress       uint32                 `json:"anchor_address"`
	AnchorAddressStr    string                 `json:"anchor_address_str"`
	AnchorBytes         string                 `json:"anchor_bytes"`
	AnchorContext       ContextFlags           `json:"anchor_context"`
	CoefficientLow      InputWitness           `json:"coefficient_low"`
	CoefficientHigh     InputWitness           `json:"coefficient_high"`
	Factor              InputWitness           `json:"factor"`
	SignedCoefficient   int                    `json:"signed_coefficient"`
	SignedMultiplier    int                    `json:"signed_multiplier"`
	Product             int                    `json:"product"`
	Product24Hex        string                 `json:"product_24_hex"`
	DiscardedLowByte    uint8                  `json:"discarded_low_byte"`
	MiddleHighWord      uint16                 `json:"middle_high_word"`
	SignedMiddleHigh    int                    `json:"signed_middle_high"`
	ScaledWord          uint16                 `json:"scaled_word"`
	SignedOutput        int                    `json:"signed_output"`
	StoredWordHex       string                 `json:"stored_word_hex"`
	TruncationFormula   string                 `json:"truncation_formula"`
	Walkthrough         []ScaledWalkthroughRow `json:"walkthrough"`
}

// ContextFlags represents CPU status register flags for context comparison.
type ContextFlags struct {
	E string `json:"e"`
	M string `json:"m"`
	X string `json:"x"`
	C string `json:"c"`
}

// InputWitness captures an observed read of coefficient or factor byte from WRAM.
type InputWitness struct {
	Case                  string `json:"case,omitempty"`
	Seq                   uint64 `json:"seq"`
	RetirementID          uint64 `json:"retirement_id"`
	ReadID                uint64 `json:"read_id"`
	PhysicalAddress       string `json:"physical_address"`
	Byte                  uint8  `json:"byte"`
	LatestCapturedWriteID uint64 `json:"latest_captured_write_id,omitempty"`
	WriterFrame           int    `json:"writer_frame,omitempty"`
	Source                string `json:"source,omitempty"`
}

// ScaledWalkthroughRow describes one of the 10 retirement steps in a scaled calculation case.
type ScaledWalkthroughRow struct {
	Step         int    `json:"step"`
	RetirementID uint64 `json:"retirement_id"`
	Seq          uint64 `json:"seq"`
	PC           string `json:"pc"`
	Mnemonic     string `json:"mnemonic"`
	Bytes        string `json:"bytes"`
	BusAccess    string `json:"bus_access"`
	EntryA       uint16 `json:"entry_a"`
	EntryX       uint16 `json:"entry_x"`
	EntryP       uint8  `json:"entry_p"`
	ExitA        uint16 `json:"exit_a"`
	ExitX        uint16 `json:"exit_x"`
	ExitP        uint8  `json:"exit_p"`
	Description  string `json:"description"`
}

// ScaledCalculationResponse is the JSON payload returned by /api/provenance/scaled-calculation.
type ScaledCalculationResponse struct {
	Status                 string             `json:"status"`
	Reason                 string             `json:"reason,omitempty"`
	Scope                  string             `json:"scope,omitempty"`
	TraceSHA256            string             `json:"trace_sha256,omitempty"`
	StreamSHA256           string             `json:"stream_sha256,omitempty"`
	ROMSHA256              string             `json:"rom_sha256,omitempty"`
	RecoveryDocumentSHA256 string             `json:"recovery_document_sha256,omitempty"`
	HardwarePrecisionModel string             `json:"hardware_precision_model,omitempty"`
	CaseCount              int                `json:"case_count,omitempty"`
	Cases                  []ScaledOutputCase `json:"cases,omitempty"`
	SelectedCase           *ScaledOutputCase  `json:"selected_case,omitempty"`
}

// FloorDiv256 computes floor(n / 256), discarding the lowest byte of a signed product.
func FloorDiv256(n int) int {
	return n >> 8
}

// ScaleMiddleHigh performs two 16-bit ASLs on the middle/high readback word,
// returning the resulting 16-bit word and its signed 16-bit value.
func ScaleMiddleHigh(middleHigh int) (uint16, int) {
	shifted := uint16(int16(middleHigh)) << 2
	return shifted, int(int16(shifted))
}

// TruncateAndScale models SNES hardware precision truncation by discarding
// the low product byte before applying two 16-bit arithmetic shifts left:
// word16(4 * floor(product / 256)).
func TruncateAndScale(product int) (middleHigh int, scaledWord uint16, signedOutput int) {
	middleHigh = FloorDiv256(product)
	scaledWord, signedOutput = ScaleMiddleHigh(middleHigh)
	return middleHigh, scaledWord, signedOutput
}

// CalculateScaledProduct computes product, discarded low byte, middle/high word,
// and the final scaled output for the given coefficient and multiplier.
func CalculateScaledProduct(coeff, mult int) (product int, lowByte uint8, middleHigh int, scaledWord uint16, signedOutput int) {
	product = coeff * mult
	lowByte = uint8(product & 0xFF)
	middleHigh, scaledWord, signedOutput = TruncateAndScale(product)
	return product, lowByte, middleHigh, scaledWord, signedOutput
}

// FindAnchor locates a case by its anchor retirement ID.
func (p *ScaledOutputPacket) FindAnchor(retirementID uint64) *ScaledOutputCase {
	if p == nil {
		return nil
	}
	for i := range p.Cases {
		if p.Cases[i].AnchorRetirementID == retirementID {
			return &p.Cases[i]
		}
	}
	return nil
}

// FindCase locates a case by case ID or physical output address.
func (p *ScaledOutputPacket) FindCase(identifier string) *ScaledOutputCase {
	if p == nil {
		return nil
	}
	norm := strings.ToUpper(strings.TrimSpace(identifier))
	normNoColon := strings.ReplaceAll(norm, ":", "")
	for i := range p.Cases {
		c := &p.Cases[i]
		cNorm := strings.ToUpper(c.CaseID)
		pNorm := strings.ToUpper(c.PhysicalOutput)
		pNormNoColon := strings.ReplaceAll(pNorm, ":", "")
		if cNorm == norm || pNorm == norm || pNormNoColon == normNoColon {
			return c
		}
	}
	return nil
}

// FindAddress locates a case by anchor CPU address.
func (p *ScaledOutputPacket) FindAddress(addr uint32) *ScaledOutputCase {
	if p == nil {
		return nil
	}
	for i := range p.Cases {
		if p.Cases[i].AnchorAddress == addr {
			return &p.Cases[i]
		}
	}
	return nil
}

// ValidateScaledOutputPacket validates that all 4 cases are present, each has 10 walkthrough steps,
// and hardware precision arithmetic matches TruncateAndScale.
func ValidateScaledOutputPacket(pkt *ScaledOutputPacket) error {
	if pkt == nil {
		return fmt.Errorf("scaled output packet is nil")
	}
	if len(pkt.Cases) != 4 {
		return fmt.Errorf("scaled output packet: expected 4 cases, got %d", len(pkt.Cases))
	}
	expectedAnchors := map[uint64]uint32{
		52170: 0x09F8B5,
		52212: 0x09F8CB,
		52254: 0x09F8E1,
		52296: 0x09F8F7,
	}
	for i := range pkt.Cases {
		c := &pkt.Cases[i]
		expectedAddr, ok := expectedAnchors[c.AnchorRetirementID]
		if !ok {
			return fmt.Errorf("case %q has unexpected anchor retirement %d", c.CaseID, c.AnchorRetirementID)
		}
		if c.AnchorAddress != expectedAddr {
			return fmt.Errorf("case %q anchor address mismatch: expected $%06X, got $%06X", c.CaseID, expectedAddr, c.AnchorAddress)
		}
		if len(c.Walkthrough) != 10 {
			return fmt.Errorf("case %q: expected 10 walkthrough steps, got %d", c.CaseID, len(c.Walkthrough))
		}
		expectedProd := c.SignedCoefficient * c.SignedMultiplier
		if expectedProd != c.Product {
			return fmt.Errorf("case %q product mismatch: expected %d, got %d", c.CaseID, expectedProd, c.Product)
		}
		expectedMid, expectedScaled, expectedSigned := TruncateAndScale(expectedProd)
		if expectedScaled != c.ScaledWord {
			return fmt.Errorf("case %q scaled word mismatch: expected %d, got %d", c.CaseID, expectedScaled, c.ScaledWord)
		}
		if expectedSigned != c.SignedOutput {
			return fmt.Errorf("case %q signed output mismatch: expected %d, got %d", c.CaseID, expectedSigned, c.SignedOutput)
		}
		if uint16(int16(expectedMid)) != c.MiddleHighWord {
			return fmt.Errorf("case %q middle/high word mismatch: expected %d, got %d", c.CaseID, uint16(int16(expectedMid)), c.MiddleHighWord)
		}
	}
	return nil
}

// ValidateScaledOutputRecords checks that all cases in pkt are backed by authentic retained
// trace records in occIndex.
func ValidateScaledOutputRecords(pkt *ScaledOutputPacket, occIndex *OccurrenceIndex) error {
	if occIndex == nil {
		return nil
	}
	for _, c := range pkt.Cases {
		ev, ok := occIndex.GetRetainedEvent(c.AnchorRetirementID)
		if !ok {
			return fmt.Errorf("case %q: anchor retirement %d not found in retained trace events", c.CaseID, c.AnchorRetirementID)
		}
		if ev.Insn == nil {
			return fmt.Errorf("case %q: anchor event %d has no instruction entry", c.CaseID, c.AnchorRetirementID)
		}
		pc := uint32(ev.Insn.Entry.PB)<<16 | uint32(ev.Insn.Entry.PC)
		if pc != c.AnchorAddress {
			return fmt.Errorf("case %q: anchor retirement %d PC mismatch: got $%06X, want $%06X", c.CaseID, c.AnchorRetirementID, pc, c.AnchorAddress)
		}

		if evRead, ok := occIndex.GetRetainedEvent(c.CoefficientLow.ReadID); ok {
			if uint8(evRead.Value) != c.CoefficientLow.Byte {
				return fmt.Errorf("case %q: coefficient low read byte mismatch: got %d, want %d", c.CaseID, uint8(evRead.Value), c.CoefficientLow.Byte)
			}
		}

		if evRead, ok := occIndex.GetRetainedEvent(c.CoefficientHigh.ReadID); ok {
			if uint8(evRead.Value) != c.CoefficientHigh.Byte {
				return fmt.Errorf("case %q: coefficient high read byte mismatch: got %d, want %d", c.CaseID, uint8(evRead.Value), c.CoefficientHigh.Byte)
			}
		}

		if evRead, ok := occIndex.GetRetainedEvent(c.Factor.ReadID); ok {
			if uint8(evRead.Value) != c.Factor.Byte {
				return fmt.Errorf("case %q: factor read byte mismatch: got %d, want %d", c.CaseID, uint8(evRead.Value), c.Factor.Byte)
			}
		}
	}
	return nil
}

// LoadScaledOutputPacket loads a scaled calculation packet from the specified path,
// falling back to known plan artifacts or the built-in default packet if unconfigured.
func LoadScaledOutputPacket(packetPath string) (*ScaledOutputPacket, error) {
	if packetPath == "" {
		fallback := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/scaled-output-plan/scaled-output-packet.json"
		if _, err := os.Stat(fallback); err == nil {
			packetPath = fallback
		} else {
			return DefaultScaledOutputPacket(), nil
		}
	}

	data, err := os.ReadFile(packetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultScaledOutputPacket(), nil
		}
		return nil, fmt.Errorf("read scaled output packet %s: %w", packetPath, err)
	}

	var pkt ScaledOutputPacket
	if err := json.Unmarshal(data, &pkt); err != nil {
		return nil, fmt.Errorf("decode scaled output packet %s: %w", packetPath, err)
	}

	if err := ValidateScaledOutputPacket(&pkt); err != nil {
		return nil, fmt.Errorf("validate scaled output packet %s: %w", packetPath, err)
	}

	return &pkt, nil
}

// NewScaledCalculationHandler returns an http.HandlerFunc that serves /api/provenance/scaled-calculation.
func NewScaledCalculationHandler(packetPath string) http.HandlerFunc {
	return NewScaledCalculationHandlerWithTrace(nil, nil, packetPath)
}

// NewScaledCalculationHandlerWithTrace returns an http.HandlerFunc that verifies recorded trace admission.
func NewScaledCalculationHandlerWithTrace(occIndex *OccurrenceIndex, doc *recovery.Document, packetPath string) http.HandlerFunc {
	pkt, _ := LoadScaledOutputPacket(packetPath)

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			writeJSON(w, ScaledCalculationResponse{
				Status: "unavailable",
				Reason: "method not allowed",
			})
			return
		}

		curPkt := pkt
		if curPkt == nil {
			loaded, err := LoadScaledOutputPacket(packetPath)
			if err != nil {
				writeJSON(w, ScaledCalculationResponse{
					Status: "unavailable",
					Reason: fmt.Sprintf("scaled calculation packet unavailable: %v", err),
				})
				return
			}
			curPkt = loaded
		}

		// Validate recorded admission against active occurrence index
		if occIndex != nil {
			if err := ValidateScaledOutputRecords(curPkt, occIndex); err != nil {
				writeJSON(w, ScaledCalculationResponse{
					Status: "unavailable",
					Reason: fmt.Sprintf("recorded evidence admission failed: %v", err),
				})
				return
			}
		}

		q := r.URL.Query()
		var found *ScaledOutputCase

		if anchorStr := q.Get("anchor"); anchorStr != "" {
			id, err := strconv.ParseUint(anchorStr, 10, 64)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, ScaledCalculationResponse{
					Status: "unavailable",
					Reason: fmt.Sprintf("invalid anchor retirement ID %q: %v", anchorStr, err),
				})
				return
			}
			found = curPkt.FindAnchor(id)
			if found == nil {
				w.WriteHeader(http.StatusNotFound)
				writeJSON(w, ScaledCalculationResponse{
					Status: "unavailable",
					Reason: fmt.Sprintf("anchor %d not found in scaled calculation packet", id),
				})
				return
			}
		} else if retStr := q.Get("retirement_id"); retStr != "" {
			id, err := strconv.ParseUint(retStr, 10, 64)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, ScaledCalculationResponse{
					Status: "unavailable",
					Reason: fmt.Sprintf("invalid retirement_id %q: %v", retStr, err),
				})
				return
			}
			found = curPkt.FindAnchor(id)
			if found == nil {
				w.WriteHeader(http.StatusNotFound)
				writeJSON(w, ScaledCalculationResponse{
					Status: "unavailable",
					Reason: fmt.Sprintf("retirement_id %d not found in scaled calculation packet", id),
				})
				return
			}
		} else if caseID := q.Get("case"); caseID != "" {
			found = curPkt.FindCase(caseID)
			if found == nil {
				w.WriteHeader(http.StatusNotFound)
				writeJSON(w, ScaledCalculationResponse{
					Status: "unavailable",
					Reason: fmt.Sprintf("case %q not found in scaled calculation packet", caseID),
				})
				return
			}
		} else if addrStr := q.Get("addr"); addrStr != "" {
			addr, err := parseAddress(addrStr)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, ScaledCalculationResponse{
					Status: "unavailable",
					Reason: fmt.Sprintf("invalid address %q: %v", addrStr, err),
				})
				return
			}
			found = curPkt.FindAddress(addr)
			if found == nil {
				w.WriteHeader(http.StatusNotFound)
				writeJSON(w, ScaledCalculationResponse{
					Status: "unavailable",
					Reason: fmt.Sprintf("address $%06X not found in scaled calculation packet", addr),
				})
				return
			}
		}

		romSHA := curPkt.ROMSHA256
		docSHA := curPkt.RecoveryDocumentSHA256
		streamSHA := curPkt.StreamSHA256
		if doc != nil && doc.ROM.NormalizedSHA256 != "" {
			romSHA = doc.ROM.NormalizedSHA256
		}
		if occIndex != nil && occIndex.StreamSHA256 != "" {
			streamSHA = occIndex.StreamSHA256
		}

		resp := ScaledCalculationResponse{
			Status:                 "available",
			Scope:                  curPkt.Scope,
			TraceSHA256:            curPkt.TraceSHA256,
			StreamSHA256:           streamSHA,
			ROMSHA256:              romSHA,
			RecoveryDocumentSHA256: docSHA,
			HardwarePrecisionModel: curPkt.HardwarePrecisionModel,
			CaseCount:              len(curPkt.Cases),
		}

		if found != nil {
			resp.SelectedCase = found
		} else {
			resp.Cases = curPkt.Cases
		}

		writeJSON(w, resp)
	}
}

// RegisterScaledCalculationRoutes registers the /api/provenance/scaled-calculation endpoint on mux.
func RegisterScaledCalculationRoutes(mux *http.ServeMux, occIndex *OccurrenceIndex, doc *recovery.Document, packetPath string) {
	mux.HandleFunc("/api/provenance/scaled-calculation", NewScaledCalculationHandlerWithTrace(occIndex, doc, packetPath))
}

// DefaultScaledOutputPacket returns the canonical 4-output scaled calculation packet.
func DefaultScaledOutputPacket() *ScaledOutputPacket {
	return &ScaledOutputPacket{
		Scope:                  "four-output scaled calculation explainer with hardware precision truncation",
		TraceSHA256:            "68aecfcf95fac6863d657979ff802c27dae5610799168b3321aad9f41046e421",
		StreamSHA256:           "68aecfcf95fac6863d657979ff802c27dae5610799168b3321aad9f41046e421",
		ROMSHA256:              "66871d66be19ad2c34c927d6b14cd8eb6fc3181965b6e517cb361f7316009cfb",
		RecoveryDocumentSHA256: "cb6f4a1af5d6ec5f2d906596ea7e3d61f05d17f6709477133afc551837f51836",
		HardwarePrecisionModel: "word16(4 * floor(product / 256)) discarding low product byte before two 16-bit ASLs",
		Cases: []ScaledOutputCase{
			{
				CaseID:              "positive64",
				PhysicalOutput:      "7E:1F58",
				AnchorRetirementID:  52170,
				AnchorSeq:           13223,
				TraceFrame:          1,
				AnchorInstructionID: "7f828d7931a141ea1ee8eafa1bdcfdfb3b21a958067a7c47d3b841dbb60d0b17",
				AnchorAddress:       653493,
				AnchorAddressStr:    "09:F8B5",
				AnchorBytes:         "8558",
				AnchorContext:       ContextFlags{E: "clear", M: "clear", X: "set", C: "clear"},
				CoefficientLow: InputWitness{
					Case:                  "7E:1F58",
					Seq:                   13214,
					RetirementID:          52132,
					ReadID:                52131,
					PhysicalAddress:       "7E:1F54",
					Byte:                  20,
					LatestCapturedWriteID: 52085,
					WriterFrame:           1,
					Source:                "latest captured physical write before this exact read; absence means unestablished initial state",
				},
				CoefficientHigh: InputWitness{
					Case:                  "7E:1F58",
					Seq:                   13216,
					RetirementID:          52141,
					ReadID:                52140,
					PhysicalAddress:       "7E:1F55",
					Byte:                  0,
					LatestCapturedWriteID: 52099,
					WriterFrame:           1,
					Source:                "latest captured physical write before this exact read; absence means unestablished initial state",
				},
				Factor: InputWitness{
					Case:                  "7E:1F58",
					Seq:                   13218,
					RetirementID:          52150,
					ReadID:                52149,
					PhysicalAddress:       "7E:1F50",
					Byte:                  64,
					LatestCapturedWriteID: 52035,
					WriterFrame:           1,
					Source:                "latest captured physical write before this exact read; absence means unestablished initial state",
				},
				SignedCoefficient: 20,
				SignedMultiplier:  64,
				Product:           1280,
				Product24Hex:      "000500",
				DiscardedLowByte:  0,
				MiddleHighWord:    5,
				SignedMiddleHigh:  5,
				ScaledWord:        20,
				SignedOutput:      20,
				StoredWordHex:     "0014",
				TruncationFormula: "4 * floor(product / 256)",
				Walkthrough: []ScaledWalkthroughRow{
					{
						Step:         1,
						RetirementID: 52132,
						Seq:          13214,
						PC:           "09:F8A1",
						Mnemonic:     "LDX",
						Bytes:        "a654",
						BusAccess:    "read 7E:1F54 = $14",
						EntryA:       65535,
						EntryX:       152,
						EntryP:       149,
						ExitA:        65535,
						ExitX:        20,
						ExitP:        21,
						Description:  "Read coefficient low byte from direct page into X",
					},
					{
						Step:         2,
						RetirementID: 52137,
						Seq:          13215,
						PC:           "09:F8A3",
						Mnemonic:     "STX",
						Bytes:        "8e1b21",
						BusAccess:    "write M7A ($211B) = $14",
						EntryA:       65535,
						EntryX:       20,
						EntryP:       21,
						ExitA:        65535,
						ExitX:        20,
						ExitP:        21,
						Description:  "Write coefficient low byte to Mode 7 matrix parameter M7A ($211B)",
					},
					{
						Step:         3,
						RetirementID: 52141,
						Seq:          13216,
						PC:           "09:F8A6",
						Mnemonic:     "LDX",
						Bytes:        "a655",
						BusAccess:    "read 7E:1F55 = $00",
						EntryA:       65535,
						EntryX:       20,
						EntryP:       21,
						ExitA:        65535,
						ExitX:        0,
						ExitP:        23,
						Description:  "Read coefficient high byte from direct page into X",
					},
					{
						Step:         4,
						RetirementID: 52146,
						Seq:          13217,
						PC:           "09:F8A8",
						Mnemonic:     "STX",
						Bytes:        "8e1b21",
						BusAccess:    "write M7A ($211B) = $00",
						EntryA:       65535,
						EntryX:       0,
						EntryP:       23,
						ExitA:        65535,
						ExitX:        0,
						ExitP:        23,
						Description:  "Write coefficient high byte to Mode 7 matrix parameter M7A ($211B)",
					},
					{
						Step:         5,
						RetirementID: 52150,
						Seq:          13218,
						PC:           "09:F8AB",
						Mnemonic:     "LDX",
						Bytes:        "a650",
						BusAccess:    "read 7E:1F50 = $40",
						EntryA:       65535,
						EntryX:       0,
						EntryP:       23,
						ExitA:        65535,
						ExitX:        64,
						ExitP:        21,
						Description:  "Read signed multiplier byte from direct page into X",
					},
					{
						Step:         6,
						RetirementID: 52155,
						Seq:          13219,
						PC:           "09:F8AD",
						Mnemonic:     "STX",
						Bytes:        "8e1c21",
						BusAccess:    "write M7B ($211C) = $40",
						EntryA:       65535,
						EntryX:       64,
						EntryP:       21,
						ExitA:        65535,
						ExitX:        64,
						ExitP:        21,
						Description:  "Write signed multiplier byte to Mode 7 matrix parameter M7B ($211C)",
					},
					{
						Step:         7,
						RetirementID: 52161,
						Seq:          13220,
						PC:           "09:F8B0",
						Mnemonic:     "LDA",
						Bytes:        "ad3521",
						BusAccess:    "read MPYM ($2135) = $05, read MPYH ($2136) = $00",
						EntryA:       65535,
						EntryX:       64,
						EntryP:       21,
						ExitA:        5,
						ExitX:        64,
						ExitP:        21,
						Description:  "Read 16-bit product middle and high bytes from MPYM/MPYH ($2135/$2136) into A",
					},
					{
						Step:         8,
						RetirementID: 52163,
						Seq:          13221,
						PC:           "09:F8B3",
						Mnemonic:     "ASL",
						Bytes:        "0a",
						BusAccess:    "-",
						EntryA:       5,
						EntryX:       64,
						EntryP:       21,
						ExitA:        10,
						ExitX:        64,
						ExitP:        20,
						Description:  "First 16-bit arithmetic shift left (ASL A)",
					},
					{
						Step:         9,
						RetirementID: 52165,
						Seq:          13222,
						PC:           "09:F8B4",
						Mnemonic:     "ASL",
						Bytes:        "0a",
						BusAccess:    "-",
						EntryA:       10,
						EntryX:       64,
						EntryP:       20,
						ExitA:        20,
						ExitX:        64,
						ExitP:        20,
						Description:  "Second 16-bit arithmetic shift left (ASL A)",
					},
					{
						Step:         10,
						RetirementID: 52170,
						Seq:          13223,
						PC:           "09:F8B5",
						Mnemonic:     "STA",
						Bytes:        "8558",
						BusAccess:    "write 7E:1F58 = $14, write 7E:1F59 = $00",
						EntryA:       20,
						EntryX:       64,
						EntryP:       20,
						ExitA:        20,
						ExitX:        64,
						ExitP:        20,
						Description:  "Store 16-bit scaled output from Accumulator A to direct page (7E:1F58)",
					},
				},
			},
			{
				CaseID:              "negative_minus8",
				PhysicalOutput:      "7E:1F5E",
				AnchorRetirementID:  52212,
				AnchorSeq:           13233,
				TraceFrame:          1,
				AnchorInstructionID: "470ae8810e590a4d31c9ea8f4bab289a9065f50104590440fe1e645342ed89a4",
				AnchorAddress:       653515,
				AnchorAddressStr:    "09:F8CB",
				AnchorBytes:         "855e",
				AnchorContext:       ContextFlags{E: "clear", M: "clear", X: "set", C: "clear"},
				CoefficientLow: InputWitness{
					Case:                  "7E:1F5E",
					Seq:                   13224,
					RetirementID:          52174,
					ReadID:                52173,
					PhysicalAddress:       "7E:1F56",
					Byte:                  195,
					LatestCapturedWriteID: 52108,
					WriterFrame:           1,
					Source:                "latest captured physical write before this exact read; absence means unestablished initial state",
				},
				CoefficientHigh: InputWitness{
					Case:                  "7E:1F5E",
					Seq:                   13226,
					RetirementID:          52183,
					ReadID:                52182,
					PhysicalAddress:       "7E:1F57",
					Byte:                  255,
					LatestCapturedWriteID: 52122,
					WriterFrame:           1,
					Source:                "latest captured physical write before this exact read; absence means unestablished initial state",
				},
				Factor: InputWitness{
					Case:                  "7E:1F5E",
					Seq:                   13228,
					RetirementID:          52192,
					ReadID:                52191,
					PhysicalAddress:       "7E:1F52",
					Byte:                  248,
					LatestCapturedWriteID: 52058,
					WriterFrame:           1,
					Source:                "latest captured physical write before this exact read; absence means unestablished initial state",
				},
				SignedCoefficient: -61,
				SignedMultiplier:  -8,
				Product:           488,
				Product24Hex:      "0001E8",
				DiscardedLowByte:  232,
				MiddleHighWord:    1,
				SignedMiddleHigh:  1,
				ScaledWord:        4,
				SignedOutput:      4,
				StoredWordHex:     "0004",
				TruncationFormula: "4 * floor(product / 256)",
				Walkthrough: []ScaledWalkthroughRow{
					{
						Step:         1,
						RetirementID: 52174,
						Seq:          13224,
						PC:           "09:F8B7",
						Mnemonic:     "LDX",
						Bytes:        "a656",
						BusAccess:    "read 7E:1F56 = $C3",
						EntryA:       20,
						EntryX:       64,
						EntryP:       20,
						ExitA:        20,
						ExitX:        195,
						ExitP:        148,
						Description:  "Read coefficient low byte from direct page into X",
					},
					{
						Step:         2,
						RetirementID: 52179,
						Seq:          13225,
						PC:           "09:F8B9",
						Mnemonic:     "STX",
						Bytes:        "8e1b21",
						BusAccess:    "write M7A ($211B) = $C3",
						EntryA:       20,
						EntryX:       195,
						EntryP:       148,
						ExitA:        20,
						ExitX:        195,
						ExitP:        148,
						Description:  "Write coefficient low byte to Mode 7 matrix parameter M7A ($211B)",
					},
					{
						Step:         3,
						RetirementID: 52183,
						Seq:          13226,
						PC:           "09:F8BC",
						Mnemonic:     "LDX",
						Bytes:        "a657",
						BusAccess:    "read 7E:1F57 = $FF",
						EntryA:       20,
						EntryX:       195,
						EntryP:       148,
						ExitA:        20,
						ExitX:        255,
						ExitP:        148,
						Description:  "Read coefficient high byte from direct page into X",
					},
					{
						Step:         4,
						RetirementID: 52188,
						Seq:          13227,
						PC:           "09:F8BE",
						Mnemonic:     "STX",
						Bytes:        "8e1b21",
						BusAccess:    "write M7A ($211B) = $FF",
						EntryA:       20,
						EntryX:       255,
						EntryP:       148,
						ExitA:        20,
						ExitX:        255,
						ExitP:        148,
						Description:  "Write coefficient high byte to Mode 7 matrix parameter M7A ($211B)",
					},
					{
						Step:         5,
						RetirementID: 52192,
						Seq:          13228,
						PC:           "09:F8C1",
						Mnemonic:     "LDX",
						Bytes:        "a652",
						BusAccess:    "read 7E:1F52 = $F8",
						EntryA:       20,
						EntryX:       255,
						EntryP:       148,
						ExitA:        20,
						ExitX:        248,
						ExitP:        148,
						Description:  "Read signed multiplier byte from direct page into X",
					},
					{
						Step:         6,
						RetirementID: 52197,
						Seq:          13229,
						PC:           "09:F8C3",
						Mnemonic:     "STX",
						Bytes:        "8e1c21",
						BusAccess:    "write M7B ($211C) = $F8",
						EntryA:       20,
						EntryX:       248,
						EntryP:       148,
						ExitA:        20,
						ExitX:        248,
						ExitP:        148,
						Description:  "Write signed multiplier byte to Mode 7 matrix parameter M7B ($211C)",
					},
					{
						Step:         7,
						RetirementID: 52203,
						Seq:          13230,
						PC:           "09:F8C6",
						Mnemonic:     "LDA",
						Bytes:        "ad3521",
						BusAccess:    "read MPYM ($2135) = $01, read MPYH ($2136) = $00",
						EntryA:       20,
						EntryX:       248,
						EntryP:       148,
						ExitA:        1,
						ExitX:        248,
						ExitP:        20,
						Description:  "Read 16-bit product middle and high bytes from MPYM/MPYH ($2135/$2136) into A",
					},
					{
						Step:         8,
						RetirementID: 52205,
						Seq:          13231,
						PC:           "09:F8C9",
						Mnemonic:     "ASL",
						Bytes:        "0a",
						BusAccess:    "-",
						EntryA:       1,
						EntryX:       248,
						EntryP:       20,
						ExitA:        2,
						ExitX:        248,
						ExitP:        20,
						Description:  "First 16-bit arithmetic shift left (ASL A)",
					},
					{
						Step:         9,
						RetirementID: 52207,
						Seq:          13232,
						PC:           "09:F8CA",
						Mnemonic:     "ASL",
						Bytes:        "0a",
						BusAccess:    "-",
						EntryA:       2,
						EntryX:       248,
						EntryP:       20,
						ExitA:        4,
						ExitX:        248,
						ExitP:        20,
						Description:  "Second 16-bit arithmetic shift left (ASL A)",
					},
					{
						Step:         10,
						RetirementID: 52212,
						Seq:          13233,
						PC:           "09:F8CB",
						Mnemonic:     "STA",
						Bytes:        "855e",
						BusAccess:    "write 7E:1F5E = $04, write 7E:1F5F = $00",
						EntryA:       4,
						EntryX:       248,
						EntryP:       20,
						ExitA:        4,
						ExitX:        248,
						ExitP:        20,
						Description:  "Store 16-bit scaled output from Accumulator A to direct page (7E:1F5E)",
					},
				},
			},
			{
				CaseID:              "negative64",
				PhysicalOutput:      "7E:1F5A",
				AnchorRetirementID:  52254,
				AnchorSeq:           13243,
				TraceFrame:          1,
				AnchorInstructionID: "6a843d132e5d49df49f9c8f6746aa81bac8d9ec0b20b77845e56fbf520d4ee5b",
				AnchorAddress:       653537,
				AnchorAddressStr:    "09:F8E1",
				AnchorBytes:         "855a",
				AnchorContext:       ContextFlags{E: "clear", M: "clear", X: "set", C: "set"},
				CoefficientLow: InputWitness{
					Case:                  "7E:1F5A",
					Seq:                   13234,
					RetirementID:          52216,
					ReadID:                52215,
					PhysicalAddress:       "7E:1F56",
					Byte:                  195,
					LatestCapturedWriteID: 52108,
					WriterFrame:           1,
					Source:                "latest captured physical write before this exact read; absence means unestablished initial state",
				},
				CoefficientHigh: InputWitness{
					Case:                  "7E:1F5A",
					Seq:                   13236,
					RetirementID:          52225,
					ReadID:                52224,
					PhysicalAddress:       "7E:1F57",
					Byte:                  255,
					LatestCapturedWriteID: 52122,
					WriterFrame:           1,
					Source:                "latest captured physical write before this exact read; absence means unestablished initial state",
				},
				Factor: InputWitness{
					Case:                  "7E:1F5A",
					Seq:                   13238,
					RetirementID:          52234,
					ReadID:                52233,
					PhysicalAddress:       "7E:1F50",
					Byte:                  64,
					LatestCapturedWriteID: 52035,
					WriterFrame:           1,
					Source:                "latest captured physical write before this exact read; absence means unestablished initial state",
				},
				SignedCoefficient: -61,
				SignedMultiplier:  64,
				Product:           -3904,
				Product24Hex:      "FFF0C0",
				DiscardedLowByte:  192,
				MiddleHighWord:    65520,
				SignedMiddleHigh:  -16,
				ScaledWord:        65472,
				SignedOutput:      -64,
				StoredWordHex:     "FFC0",
				TruncationFormula: "4 * floor(product / 256)",
				Walkthrough: []ScaledWalkthroughRow{
					{
						Step:         1,
						RetirementID: 52216,
						Seq:          13234,
						PC:           "09:F8CD",
						Mnemonic:     "LDX",
						Bytes:        "a656",
						BusAccess:    "read 7E:1F56 = $C3",
						EntryA:       4,
						EntryX:       248,
						EntryP:       20,
						ExitA:        4,
						ExitX:        195,
						ExitP:        148,
						Description:  "Read coefficient low byte from direct page into X",
					},
					{
						Step:         2,
						RetirementID: 52221,
						Seq:          13235,
						PC:           "09:F8CF",
						Mnemonic:     "STX",
						Bytes:        "8e1b21",
						BusAccess:    "write M7A ($211B) = $C3",
						EntryA:       4,
						EntryX:       195,
						EntryP:       148,
						ExitA:        4,
						ExitX:        195,
						ExitP:        148,
						Description:  "Write coefficient low byte to Mode 7 matrix parameter M7A ($211B)",
					},
					{
						Step:         3,
						RetirementID: 52225,
						Seq:          13236,
						PC:           "09:F8D2",
						Mnemonic:     "LDX",
						Bytes:        "a657",
						BusAccess:    "read 7E:1F57 = $FF",
						EntryA:       4,
						EntryX:       195,
						EntryP:       148,
						ExitA:        4,
						ExitX:        255,
						ExitP:        148,
						Description:  "Read coefficient high byte from direct page into X",
					},
					{
						Step:         4,
						RetirementID: 52230,
						Seq:          13237,
						PC:           "09:F8D4",
						Mnemonic:     "STX",
						Bytes:        "8e1b21",
						BusAccess:    "write M7A ($211B) = $FF",
						EntryA:       4,
						EntryX:       255,
						EntryP:       148,
						ExitA:        4,
						ExitX:        255,
						ExitP:        148,
						Description:  "Write coefficient high byte to Mode 7 matrix parameter M7A ($211B)",
					},
					{
						Step:         5,
						RetirementID: 52234,
						Seq:          13238,
						PC:           "09:F8D7",
						Mnemonic:     "LDX",
						Bytes:        "a650",
						BusAccess:    "read 7E:1F50 = $40",
						EntryA:       4,
						EntryX:       255,
						EntryP:       148,
						ExitA:        4,
						ExitX:        64,
						ExitP:        20,
						Description:  "Read signed multiplier byte from direct page into X",
					},
					{
						Step:         6,
						RetirementID: 52239,
						Seq:          13239,
						PC:           "09:F8D9",
						Mnemonic:     "STX",
						Bytes:        "8e1c21",
						BusAccess:    "write M7B ($211C) = $40",
						EntryA:       4,
						EntryX:       64,
						EntryP:       20,
						ExitA:        4,
						ExitX:        64,
						ExitP:        20,
						Description:  "Write signed multiplier byte to Mode 7 matrix parameter M7B ($211C)",
					},
					{
						Step:         7,
						RetirementID: 52245,
						Seq:          13240,
						PC:           "09:F8DC",
						Mnemonic:     "LDA",
						Bytes:        "ad3521",
						BusAccess:    "read MPYM ($2135) = $F0, read MPYH ($2136) = $FF",
						EntryA:       4,
						EntryX:       64,
						EntryP:       20,
						ExitA:        65520,
						ExitX:        64,
						ExitP:        148,
						Description:  "Read 16-bit product middle and high bytes from MPYM/MPYH ($2135/$2136) into A",
					},
					{
						Step:         8,
						RetirementID: 52247,
						Seq:          13241,
						PC:           "09:F8DF",
						Mnemonic:     "ASL",
						Bytes:        "0a",
						BusAccess:    "-",
						EntryA:       65520,
						EntryX:       64,
						EntryP:       148,
						ExitA:        65504,
						ExitX:        64,
						ExitP:        149,
						Description:  "First 16-bit arithmetic shift left (ASL A)",
					},
					{
						Step:         9,
						RetirementID: 52249,
						Seq:          13242,
						PC:           "09:F8E0",
						Mnemonic:     "ASL",
						Bytes:        "0a",
						BusAccess:    "-",
						EntryA:       65504,
						EntryX:       64,
						EntryP:       149,
						ExitA:        65472,
						ExitX:        64,
						ExitP:        149,
						Description:  "Second 16-bit arithmetic shift left (ASL A)",
					},
					{
						Step:         10,
						RetirementID: 52254,
						Seq:          13243,
						PC:           "09:F8E1",
						Mnemonic:     "STA",
						Bytes:        "855a",
						BusAccess:    "write 7E:1F5A = $C0, write 7E:1F5B = $FF",
						EntryA:       65472,
						EntryX:       64,
						EntryP:       149,
						ExitA:        65472,
						ExitX:        64,
						ExitP:        149,
						Description:  "Store 16-bit scaled output from Accumulator A to direct page (7E:1F5A)",
					},
				},
			},
			{
				CaseID:              "positive_minus8",
				PhysicalOutput:      "7E:1F5C",
				AnchorRetirementID:  52296,
				AnchorSeq:           13253,
				TraceFrame:          1,
				AnchorInstructionID: "6746479ffcb96f941afa58f407e00a948de8824732f724a1410c70ae997845a0",
				AnchorAddress:       653559,
				AnchorAddressStr:    "09:F8F7",
				AnchorBytes:         "855c",
				AnchorContext:       ContextFlags{E: "clear", M: "clear", X: "set", C: "set"},
				CoefficientLow: InputWitness{
					Case:                  "7E:1F5C",
					Seq:                   13244,
					RetirementID:          52258,
					ReadID:                52257,
					PhysicalAddress:       "7E:1F54",
					Byte:                  20,
					LatestCapturedWriteID: 52085,
					WriterFrame:           1,
					Source:                "latest captured physical write before this exact read; absence means unestablished initial state",
				},
				CoefficientHigh: InputWitness{
					Case:                  "7E:1F5C",
					Seq:                   13246,
					RetirementID:          52267,
					ReadID:                52266,
					PhysicalAddress:       "7E:1F55",
					Byte:                  0,
					LatestCapturedWriteID: 52099,
					WriterFrame:           1,
					Source:                "latest captured physical write before this exact read; absence means unestablished initial state",
				},
				Factor: InputWitness{
					Case:                  "7E:1F5C",
					Seq:                   13248,
					RetirementID:          52276,
					ReadID:                52275,
					PhysicalAddress:       "7E:1F52",
					Byte:                  248,
					LatestCapturedWriteID: 52058,
					WriterFrame:           1,
					Source:                "latest captured physical write before this exact read; absence means unestablished initial state",
				},
				SignedCoefficient: 20,
				SignedMultiplier:  -8,
				Product:           -160,
				Product24Hex:      "FFFF60",
				DiscardedLowByte:  96,
				MiddleHighWord:    65535,
				SignedMiddleHigh:  -1,
				ScaledWord:        65532,
				SignedOutput:      -4,
				StoredWordHex:     "FFFC",
				TruncationFormula: "4 * floor(product / 256)",
				Walkthrough: []ScaledWalkthroughRow{
					{
						Step:         1,
						RetirementID: 52258,
						Seq:          13244,
						PC:           "09:F8E3",
						Mnemonic:     "LDX",
						Bytes:        "a654",
						BusAccess:    "read 7E:1F54 = $14",
						EntryA:       65472,
						EntryX:       64,
						EntryP:       149,
						ExitA:        65472,
						ExitX:        20,
						ExitP:        21,
						Description:  "Read coefficient low byte from direct page into X",
					},
					{
						Step:         2,
						RetirementID: 52263,
						Seq:          13245,
						PC:           "09:F8E5",
						Mnemonic:     "STX",
						Bytes:        "8e1b21",
						BusAccess:    "write M7A ($211B) = $14",
						EntryA:       65472,
						EntryX:       20,
						EntryP:       21,
						ExitA:        65472,
						ExitX:        20,
						ExitP:        21,
						Description:  "Write coefficient low byte to Mode 7 matrix parameter M7A ($211B)",
					},
					{
						Step:         3,
						RetirementID: 52267,
						Seq:          13246,
						PC:           "09:F8E8",
						Mnemonic:     "LDX",
						Bytes:        "a655",
						BusAccess:    "read 7E:1F55 = $00",
						EntryA:       65472,
						EntryX:       20,
						EntryP:       21,
						ExitA:        65472,
						ExitX:        0,
						ExitP:        23,
						Description:  "Read coefficient high byte from direct page into X",
					},
					{
						Step:         4,
						RetirementID: 52272,
						Seq:          13247,
						PC:           "09:F8EA",
						Mnemonic:     "STX",
						Bytes:        "8e1b21",
						BusAccess:    "write M7A ($211B) = $00",
						EntryA:       65472,
						EntryX:       0,
						EntryP:       23,
						ExitA:        65472,
						ExitX:        0,
						ExitP:        23,
						Description:  "Write coefficient high byte to Mode 7 matrix parameter M7A ($211B)",
					},
					{
						Step:         5,
						RetirementID: 52276,
						Seq:          13248,
						PC:           "09:F8ED",
						Mnemonic:     "LDX",
						Bytes:        "a652",
						BusAccess:    "read 7E:1F52 = $F8",
						EntryA:       65472,
						EntryX:       0,
						EntryP:       23,
						ExitA:        65472,
						ExitX:        248,
						ExitP:        149,
						Description:  "Read signed multiplier byte from direct page into X",
					},
					{
						Step:         6,
						RetirementID: 52281,
						Seq:          13249,
						PC:           "09:F8EF",
						Mnemonic:     "STX",
						Bytes:        "8e1c21",
						BusAccess:    "write M7B ($211C) = $F8",
						EntryA:       65472,
						EntryX:       248,
						EntryP:       149,
						ExitA:        65472,
						ExitX:        248,
						ExitP:        149,
						Description:  "Write signed multiplier byte to Mode 7 matrix parameter M7B ($211C)",
					},
					{
						Step:         7,
						RetirementID: 52287,
						Seq:          13250,
						PC:           "09:F8F2",
						Mnemonic:     "LDA",
						Bytes:        "ad3521",
						BusAccess:    "read MPYM ($2135) = $FF, read MPYH ($2136) = $FF",
						EntryA:       65472,
						EntryX:       248,
						EntryP:       149,
						ExitA:        65535,
						ExitX:        248,
						ExitP:        149,
						Description:  "Read 16-bit product middle and high bytes from MPYM/MPYH ($2135/$2136) into A",
					},
					{
						Step:         8,
						RetirementID: 52289,
						Seq:          13251,
						PC:           "09:F8F5",
						Mnemonic:     "ASL",
						Bytes:        "0a",
						BusAccess:    "-",
						EntryA:       65535,
						EntryX:       248,
						EntryP:       149,
						ExitA:        65534,
						ExitX:        248,
						ExitP:        149,
						Description:  "First 16-bit arithmetic shift left (ASL A)",
					},
					{
						Step:         9,
						RetirementID: 52291,
						Seq:          13252,
						PC:           "09:F8F6",
						Mnemonic:     "ASL",
						Bytes:        "0a",
						BusAccess:    "-",
						EntryA:       65534,
						EntryX:       248,
						EntryP:       149,
						ExitA:        65532,
						ExitX:        248,
						ExitP:        149,
						Description:  "Second 16-bit arithmetic shift left (ASL A)",
					},
					{
						Step:         10,
						RetirementID: 52296,
						Seq:          13253,
						PC:           "09:F8F7",
						Mnemonic:     "STA",
						Bytes:        "855c",
						BusAccess:    "write 7E:1F5C = $FC, write 7E:1F5D = $FF",
						EntryA:       65532,
						EntryX:       248,
						EntryP:       149,
						ExitA:        65532,
						ExitX:        248,
						ExitP:        149,
						Description:  "Store 16-bit scaled output from Accumulator A to direct page (7E:1F5C)",
					},
				},
			},
		},
	}
}
