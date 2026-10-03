package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/trace"
)

func TestScaledCalculation_Arithmetic(t *testing.T) {
	tests := []struct {
		name                 string
		coeff                int
		mult                 int
		wantProduct          int
		want24Hex            string
		wantLowByte          uint8
		wantMiddleHigh       int
		wantMiddleHighWord   uint16
		wantStep8ExitA       uint16 // after 1st ASL
		wantStep9ExitA       uint16 // after 2nd ASL
		wantScaledWord       uint16
		wantSignedOutput     int
		withoutTruncationVal float64
	}{
		{
			name:                 "Anchor1_52170_Positive64",
			coeff:                20,
			mult:                 64,
			wantProduct:          1280,
			want24Hex:            "000500",
			wantLowByte:          0x00,
			wantMiddleHigh:       5,
			wantMiddleHighWord:   0x0005,
			wantStep8ExitA:       10,
			wantStep9ExitA:       20,
			wantScaledWord:       20,
			wantSignedOutput:     20,
			withoutTruncationVal: 20.0,
		},
		{
			name:                 "Anchor2_52212_NegativeMinus8",
			coeff:                -61,
			mult:                 -8,
			wantProduct:          488,
			want24Hex:            "0001E8",
			wantLowByte:          0xE8,
			wantMiddleHigh:       1,
			wantMiddleHighWord:   0x0001,
			wantStep8ExitA:       2,
			wantStep9ExitA:       4,
			wantScaledWord:       4,
			wantSignedOutput:     4,
			withoutTruncationVal: 7.625, // truncation reduces 7 down to 4
		},
		{
			name:                 "Anchor3_52254_Negative64",
			coeff:                -61,
			mult:                 64,
			wantProduct:          -3904,
			want24Hex:            "FFF0C0",
			wantLowByte:          0xC0,
			wantMiddleHigh:       -16,
			wantMiddleHighWord:   0xFFF0,
			wantStep8ExitA:       65504, // 0xFFE0 (-32)
			wantStep9ExitA:       65472, // 0xFFC0 (-64)
			wantScaledWord:       65472, // 0xFFC0
			wantSignedOutput:     -64,
			withoutTruncationVal: -61.0,
		},
		{
			name:                 "Anchor4_52296_PositiveMinus8",
			coeff:                20,
			mult:                 -8,
			wantProduct:          -160,
			want24Hex:            "FFFF60",
			wantLowByte:          0x60,
			wantMiddleHigh:       -1,
			wantMiddleHighWord:   0xFFFF,
			wantStep8ExitA:       65534, // 0xFFFE (-2)
			wantStep9ExitA:       65532, // 0xFFFC (-4)
			wantScaledWord:       65532, // 0xFFFC
			wantSignedOutput:     -4,
			withoutTruncationVal: -2.5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prod, lowByte, midHigh, scaledWord, signedOutput := CalculateScaledProduct(tc.coeff, tc.mult)

			if prod != tc.wantProduct {
				t.Errorf("product: got %d, want %d", prod, tc.wantProduct)
			}
			if lowByte != tc.wantLowByte {
				t.Errorf("low byte: got $%02X, want $%02X", lowByte, tc.wantLowByte)
			}
			hex24 := fmt.Sprintf("%06X", uint32(prod)&0xFFFFFF)
			if hex24 != tc.want24Hex {
				t.Errorf("24-bit hex: got %s, want %s", hex24, tc.want24Hex)
			}
			if midHigh != tc.wantMiddleHigh {
				t.Errorf("middle/high: got %d, want %d", midHigh, tc.wantMiddleHigh)
			}
			if uint16(int16(midHigh)) != tc.wantMiddleHighWord {
				t.Errorf("middle/high word: got $%04X, want $%04X", uint16(int16(midHigh)), tc.wantMiddleHighWord)
			}

			// Simulate two ASL A steps as executed on SNES hardware
			asl1 := uint16(int16(midHigh)) << 1
			if asl1 != tc.wantStep8ExitA {
				t.Errorf("step 8 ASL 1 exit A: got $%04X (%d), want $%04X (%d)", asl1, asl1, tc.wantStep8ExitA, tc.wantStep8ExitA)
			}
			asl2 := asl1 << 1
			if asl2 != tc.wantStep9ExitA {
				t.Errorf("step 9 ASL 2 exit A: got $%04X (%d), want $%04X (%d)", asl2, asl2, tc.wantStep9ExitA, tc.wantStep9ExitA)
			}

			if scaledWord != tc.wantScaledWord {
				t.Errorf("scaled word: got $%04X (%d), want $%04X (%d)", scaledWord, scaledWord, tc.wantScaledWord, tc.wantScaledWord)
			}
			if signedOutput != tc.wantSignedOutput {
				t.Errorf("signed output: got %d, want %d", signedOutput, tc.wantSignedOutput)
			}

			// Verify low product byte truncation distinction
			rawCalculated := float64(prod) * 4.0 / 256.0
			if rawCalculated != tc.withoutTruncationVal {
				t.Errorf("untruncated float value: got %f, want %f", rawCalculated, tc.withoutTruncationVal)
			}
		})
	}
}

func TestScaledCalculation_JSONSchema(t *testing.T) {
	// 1. Test DefaultScaledOutputPacket
	pkt := DefaultScaledOutputPacket()
	if err := ValidateScaledOutputPacket(pkt); err != nil {
		t.Fatalf("DefaultScaledOutputPacket failed validation: %v", err)
	}

	// 2. Test disk loading from canonical plan artifact
	diskPath := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/scaled-output-plan/scaled-output-packet.json"
	diskPkt, err := LoadScaledOutputPacket(diskPath)
	if err != nil {
		t.Fatalf("LoadScaledOutputPacket(%s) failed: %v", diskPath, err)
	}
	if err := ValidateScaledOutputPacket(diskPkt); err != nil {
		t.Fatalf("disk packet failed validation: %v", err)
	}

	// 3. Verify schema structure across 4 cases
	expectedCases := []struct {
		caseID         string
		physicalOutput string
		anchorRetID    uint64
		anchorSeq      uint64
		anchorAddr     uint32
		anchorAddrStr  string
		anchorBytes    string
		coeffLowAddr   string
		coeffHighAddr  string
		factorAddr     string
	}{
		{
			caseID:         "positive64",
			physicalOutput: "7E:1F58",
			anchorRetID:    52170,
			anchorSeq:      13223,
			anchorAddr:     0x09F8B5,
			anchorAddrStr:  "09:F8B5",
			anchorBytes:    "8558",
			coeffLowAddr:   "7E:1F54",
			coeffHighAddr:  "7E:1F55",
			factorAddr:     "7E:1F50",
		},
		{
			caseID:         "negative_minus8",
			physicalOutput: "7E:1F5E",
			anchorRetID:    52212,
			anchorSeq:      13233,
			anchorAddr:     0x09F8CB,
			anchorAddrStr:  "09:F8CB",
			anchorBytes:    "855e",
			coeffLowAddr:   "7E:1F56",
			coeffHighAddr:  "7E:1F57",
			factorAddr:     "7E:1F52",
		},
		{
			caseID:         "negative64",
			physicalOutput: "7E:1F5A",
			anchorRetID:    52254,
			anchorSeq:      13243,
			anchorAddr:     0x09F8E1,
			anchorAddrStr:  "09:F8E1",
			anchorBytes:    "855a",
			coeffLowAddr:   "7E:1F56",
			coeffHighAddr:  "7E:1F57",
			factorAddr:     "7E:1F50",
		},
		{
			caseID:         "positive_minus8",
			physicalOutput: "7E:1F5C",
			anchorRetID:    52296,
			anchorSeq:      13253,
			anchorAddr:     0x09F8F7,
			anchorAddrStr:  "09:F8F7",
			anchorBytes:    "855c",
			coeffLowAddr:   "7E:1F54",
			coeffHighAddr:  "7E:1F55",
			factorAddr:     "7E:1F52",
		},
	}

	if len(pkt.Cases) != len(expectedCases) {
		t.Fatalf("expected %d cases, got %d", len(expectedCases), len(pkt.Cases))
	}

	for i, want := range expectedCases {
		got := pkt.Cases[i]
		if got.CaseID != want.caseID {
			t.Errorf("case [%d] ID: got %s, want %s", i, got.CaseID, want.caseID)
		}
		if got.PhysicalOutput != want.physicalOutput {
			t.Errorf("case [%d] PhysicalOutput: got %s, want %s", i, got.PhysicalOutput, want.physicalOutput)
		}
		if got.AnchorRetirementID != want.anchorRetID {
			t.Errorf("case [%d] AnchorRetirementID: got %d, want %d", i, got.AnchorRetirementID, want.anchorRetID)
		}
		if got.AnchorSeq != want.anchorSeq {
			t.Errorf("case [%d] AnchorSeq: got %d, want %d", i, got.AnchorSeq, want.anchorSeq)
		}
		if got.AnchorAddress != want.anchorAddr {
			t.Errorf("case [%d] AnchorAddress: got $%06X, want $%06X", i, got.AnchorAddress, want.anchorAddr)
		}
		if got.AnchorAddressStr != want.anchorAddrStr {
			t.Errorf("case [%d] AnchorAddressStr: got %s, want %s", i, got.AnchorAddressStr, want.anchorAddrStr)
		}
		if got.AnchorBytes != want.anchorBytes {
			t.Errorf("case [%d] AnchorBytes: got %s, want %s", i, got.AnchorBytes, want.anchorBytes)
		}
		if got.CoefficientLow.PhysicalAddress != want.coeffLowAddr {
			t.Errorf("case [%d] CoefficientLow: got %s, want %s", i, got.CoefficientLow.PhysicalAddress, want.coeffLowAddr)
		}
		if got.CoefficientHigh.PhysicalAddress != want.coeffHighAddr {
			t.Errorf("case [%d] CoefficientHigh: got %s, want %s", i, got.CoefficientHigh.PhysicalAddress, want.coeffHighAddr)
		}
		if got.Factor.PhysicalAddress != want.factorAddr {
			t.Errorf("case [%d] Factor: got %s, want %s", i, got.Factor.PhysicalAddress, want.factorAddr)
		}

		// Verify 10 walkthrough steps per case
		if len(got.Walkthrough) != 10 {
			t.Fatalf("case [%d] walkthrough length: got %d, want 10", i, len(got.Walkthrough))
		}
		for sIdx, row := range got.Walkthrough {
			if row.Step != sIdx+1 {
				t.Errorf("case [%d] step [%d] step number: got %d, want %d", i, sIdx, row.Step, sIdx+1)
			}
			if row.RetirementID == 0 {
				t.Errorf("case [%d] step [%d] retirement ID is zero", i, sIdx)
			}
			if row.PC == "" || row.Mnemonic == "" || row.Bytes == "" {
				t.Errorf("case [%d] step [%d] incomplete instruction info: %+v", i, sIdx, row)
			}
		}

		// Final step must match the anchor
		lastStep := got.Walkthrough[9]
		if lastStep.RetirementID != want.anchorRetID {
			t.Errorf("case [%d] final step retirement ID: got %d, want %d", i, lastStep.RetirementID, want.anchorRetID)
		}
		if lastStep.PC != want.anchorAddrStr {
			t.Errorf("case [%d] final step PC: got %s, want %s", i, lastStep.PC, want.anchorAddrStr)
		}
	}

	// 4. Test validation error detections
	t.Run("ValidationRejections", func(t *testing.T) {
		// Nil packet
		if err := ValidateScaledOutputPacket(nil); err == nil {
			t.Errorf("expected error for nil packet")
		}

		// Wrong number of cases
		shortPkt := *pkt
		shortPkt.Cases = pkt.Cases[:2]
		if err := ValidateScaledOutputPacket(&shortPkt); err == nil {
			t.Errorf("expected error for 2 cases")
		}

		// Incomplete walkthrough steps
		badStepsPkt := *pkt
		badStepsPkt.Cases = make([]ScaledOutputCase, len(pkt.Cases))
		copy(badStepsPkt.Cases, pkt.Cases)
		badStepsPkt.Cases[0].Walkthrough = badStepsPkt.Cases[0].Walkthrough[:8]
		if err := ValidateScaledOutputPacket(&badStepsPkt); err == nil {
			t.Errorf("expected error for 8 walkthrough steps")
		}

		// Arithmetic mismatch
		badArithPkt := *pkt
		badArithPkt.Cases = make([]ScaledOutputCase, len(pkt.Cases))
		copy(badArithPkt.Cases, pkt.Cases)
		badArithPkt.Cases[0].ScaledWord = 999
		if err := ValidateScaledOutputPacket(&badArithPkt); err == nil {
			t.Errorf("expected error for corrupted scaled word")
		}
	})
}

func newAdmittedOccurrenceIndex(pkt *ScaledOutputPacket) *OccurrenceIndex {
	occ := newOccurrenceIndex(pkt.StreamSHA256)
	for _, c := range pkt.Cases {
		anchorP := byte(0)
		if c.AnchorContext.M == "set" {
			anchorP |= 0x20
		}
		if c.AnchorContext.X == "set" {
			anchorP |= 0x10
		}
		if c.AnchorContext.C == "set" {
			anchorP |= 0x01
		}
		anchorBytes, _ := hex.DecodeString(c.AnchorBytes)
		var anchorFetches []trace.FetchRecord
		for idx, b := range anchorBytes {
			anchorFetches = append(anchorFetches, trace.FetchRecord{
				Addr:  c.AnchorAddress + uint32(idx),
				Value: b,
			})
		}

		anchorPrevID := c.AnchorRetirementID - 2
		occ.retainedEvents[anchorPrevID] = trace.Event{
			ID:   anchorPrevID,
			Kind: "cpu_insn",
			Insn: &trace.Insn{
				Seq:    c.AnchorSeq - 1,
				Status: "retired",
			},
		}

		occ.retainedEvents[c.AnchorRetirementID] = trace.Event{
			ID:   c.AnchorRetirementID,
			Kind: "cpu_insn",
			Insn: &trace.Insn{
				Seq:     c.AnchorSeq,
				Fetches: anchorFetches,
				Entry: trace.Registers{
					PB: byte(c.AnchorAddress >> 16),
					PC: uint16(c.AnchorAddress & 0xFFFF),
					P:  anchorP,
					E:  c.AnchorContext.E == "set",
				},
				Exit: trace.Registers{
					PB: byte(c.AnchorAddress >> 16),
					PC: uint16(c.AnchorAddress & 0xFFFF) + uint16(len(anchorBytes)),
					P:  anchorP,
					E:  c.AnchorContext.E == "set",
				},
				Status: "retired",
			},
		}

		// Owned write for anchor instruction
		outAddr := parsePhysicalAddr(c.PhysicalOutput)
		wramOut := uint32(0)
		if outAddr >= 0x7E0000 {
			wramOut = outAddr - 0x7E0000
		}
		anchorWrite := trace.Event{
			ID:    c.AnchorRetirementID - 1,
			Kind:  "bus",
			Space: "wram",
			Op:    "write",
			Width: 1,
			Addr:  wramOut,
			Value: uint64(c.ScaledWord & 0xFF),
		}
		occ.retainedEvents[c.AnchorRetirementID-1] = anchorWrite
		occ.RecordWRAMAccess(outAddr, anchorWrite)

		for rowIdx, row := range c.Walkthrough {
			parts := strings.Split(row.PC, ":")
			var pb byte
			var pc uint16
			if len(parts) == 2 {
				p0, _ := strconv.ParseUint(parts[0], 16, 8)
				p1, _ := strconv.ParseUint(parts[1], 16, 16)
				pb = byte(p0)
				pc = uint16(p1)
			}
			rBytes, _ := hex.DecodeString(row.Bytes)
			var rFetches []trace.FetchRecord
			for idx, b := range rBytes {
				rFetches = append(rFetches, trace.FetchRecord{
					Addr:  (uint32(pb)<<16 | uint32(pc)) + uint32(idx),
					Value: b,
				})
			}

			// Predecessor retirement for row
			if rowIdx == 0 {
				prevID := row.RetirementID - 2
				occ.retainedEvents[prevID] = trace.Event{
					ID:   prevID,
					Kind: "cpu_insn",
					Insn: &trace.Insn{
						Seq:    row.Seq - 1,
						Status: "retired",
					},
				}
			}

			// Owned bus event for row if needed
			if row.BusAccess != "" && row.BusAccess != "-" {
				busEvID := row.RetirementID - 1
				op := "read"
				busAddr := uint32(0x1F54)
				val := uint64(0)
				if strings.HasPrefix(row.BusAccess, "write") {
					op = "write"
					busAddr = wramOut
					val = uint64(c.ScaledWord & 0xFF)
				}
				busEv := trace.Event{
					ID:    busEvID,
					Kind:  "bus",
					Space: "wram",
					Op:    op,
					Width: 1,
					Addr:  busAddr,
					Value: val,
				}
				occ.retainedEvents[busEvID] = busEv
				if op == "write" {
					occ.RecordWRAMAccess(outAddr, busEv)
				}
			}

			occ.retainedEvents[row.RetirementID] = trace.Event{
				ID:   row.RetirementID,
				Kind: "cpu_insn",
				Insn: &trace.Insn{
					Seq:     row.Seq,
					Fetches: rFetches,
					Entry: trace.Registers{
						PB: pb,
						PC: pc,
						A:  row.EntryA,
						X:  row.EntryX,
						P:  row.EntryP,
					},
					Exit: trace.Registers{
						PB: pb,
						PC: pc,
						A:  row.ExitA,
						X:  row.ExitX,
						P:  row.ExitP,
					},
					Status: "retired",
				},
			}
		}

		for _, w := range []InputWitness{c.CoefficientLow, c.CoefficientHigh, c.Factor} {
			addr := parsePhysicalAddr(w.PhysicalAddress)
			wramAddr := uint32(0)
			if addr >= 0x7E0000 {
				wramAddr = addr - 0x7E0000
			}

			if _, ok := occ.retainedEvents[w.RetirementID]; !ok {
				occ.retainedEvents[w.RetirementID] = trace.Event{
					ID:   w.RetirementID,
					Kind: "cpu_insn",
					Insn: &trace.Insn{
						Seq:    w.Seq,
						Status: "retired",
					},
				}
			}
			retPrevID := w.RetirementID - 2
			if retPrevID >= w.ReadID {
				retPrevID = w.ReadID - 1
			}
			occ.retainedEvents[retPrevID] = trace.Event{
				ID:   retPrevID,
				Kind: "cpu_insn",
				Insn: &trace.Insn{
					Seq:    w.Seq - 1,
					Status: "retired",
				},
			}

			readEv := trace.Event{
				ID:    w.ReadID,
				Kind:  "bus",
				Space: "wram",
				Op:    "read",
				Width: 1,
				Addr:  wramAddr,
				Value: uint64(w.Byte),
			}
			occ.retainedEvents[w.ReadID] = readEv
			occ.RecordWRAMAccess(addr, readEv)

			if w.LatestCapturedWriteID > 0 {
				writeEv := trace.Event{
					ID:    w.LatestCapturedWriteID,
					Kind:  "bus",
					Space: "wram",
					Op:    "write",
					Width: 1,
					Addr:  wramAddr,
					Value: uint64(w.Byte),
					Frame: w.WriterFrame,
				}
				occ.retainedEvents[w.LatestCapturedWriteID] = writeEv
				occ.RecordWRAMAccess(addr, writeEv)
			}
		}
	}
	return occ
}

func TestScaledCalculation_HTTPEndpoint(t *testing.T) {
	pkt := DefaultScaledOutputPacket()
	occ := newAdmittedOccurrenceIndex(pkt)
	doc := &recovery.Document{
		ROM: recovery.ROMIdentity{
			NormalizedSHA256: pkt.ROMSHA256,
		},
	}
	mux := http.NewServeMux()
	RegisterScaledCalculationRoutes(mux, occ, doc, "")

	tests := []struct {
		name           string
		method         string
		url            string
		wantStatusCode int
		wantStatus     string
		wantCaseCount  int
		wantAnchorID   uint64
		wantOutput     string
		wantSigned     int
	}{
		{
			name:           "GetAllCases",
			method:         http.MethodGet,
			url:            "/api/provenance/scaled-calculation",
			wantStatusCode: http.StatusOK,
			wantStatus:     "available",
			wantCaseCount:  4,
		},
		{
			name:           "QueryAnchor1_52170",
			method:         http.MethodGet,
			url:            "/api/provenance/scaled-calculation?anchor=52170",
			wantStatusCode: http.StatusOK,
			wantStatus:     "available",
			wantAnchorID:   52170,
			wantOutput:     "7E:1F58",
			wantSigned:     20,
		},
		{
			name:           "QueryAnchor2_52212",
			method:         http.MethodGet,
			url:            "/api/provenance/scaled-calculation?anchor=52212",
			wantStatusCode: http.StatusOK,
			wantStatus:     "available",
			wantAnchorID:   52212,
			wantOutput:     "7E:1F5E",
			wantSigned:     4,
		},
		{
			name:           "QueryAnchor3_52254",
			method:         http.MethodGet,
			url:            "/api/provenance/scaled-calculation?anchor=52254",
			wantStatusCode: http.StatusOK,
			wantStatus:     "available",
			wantAnchorID:   52254,
			wantOutput:     "7E:1F5A",
			wantSigned:     -64,
		},
		{
			name:           "QueryAnchor4_52296",
			method:         http.MethodGet,
			url:            "/api/provenance/scaled-calculation?anchor=52296",
			wantStatusCode: http.StatusOK,
			wantStatus:     "available",
			wantAnchorID:   52296,
			wantOutput:     "7E:1F5C",
			wantSigned:     -4,
		},
		{
			name:           "QueryRetirementIDAlias",
			method:         http.MethodGet,
			url:            "/api/provenance/scaled-calculation?retirement_id=52170",
			wantStatusCode: http.StatusOK,
			wantStatus:     "available",
			wantAnchorID:   52170,
			wantOutput:     "7E:1F58",
			wantSigned:     20,
		},
		{
			name:           "QueryCaseName",
			method:         http.MethodGet,
			url:            "/api/provenance/scaled-calculation?case=negative64",
			wantStatusCode: http.StatusOK,
			wantStatus:     "available",
			wantAnchorID:   52254,
			wantOutput:     "7E:1F5A",
			wantSigned:     -64,
		},
		{
			name:           "QueryCaseOutputAddr",
			method:         http.MethodGet,
			url:            "/api/provenance/scaled-calculation?case=7E:1F5E",
			wantStatusCode: http.StatusOK,
			wantStatus:     "available",
			wantAnchorID:   52212,
			wantOutput:     "7E:1F5E",
			wantSigned:     4,
		},
		{
			name:           "QueryAddressHex",
			method:         http.MethodGet,
			url:            "/api/provenance/scaled-calculation?addr=09F8B5",
			wantStatusCode: http.StatusOK,
			wantStatus:     "available",
			wantAnchorID:   52170,
			wantOutput:     "7E:1F58",
			wantSigned:     20,
		},
		{
			name:           "QueryAddressColonFormat",
			method:         http.MethodGet,
			url:            "/api/provenance/scaled-calculation?addr=09:F8F7",
			wantStatusCode: http.StatusOK,
			wantStatus:     "available",
			wantAnchorID:   52296,
			wantOutput:     "7E:1F5C",
			wantSigned:     -4,
		},
		{
			name:           "MethodNotAllowed",
			method:         http.MethodPost,
			url:            "/api/provenance/scaled-calculation",
			wantStatusCode: http.StatusMethodNotAllowed,
			wantStatus:     "unavailable",
		},
		{
			name:           "UnknownAnchor",
			method:         http.MethodGet,
			url:            "/api/provenance/scaled-calculation?anchor=99999",
			wantStatusCode: http.StatusNotFound,
			wantStatus:     "unavailable",
		},
		{
			name:           "UnknownCase",
			method:         http.MethodGet,
			url:            "/api/provenance/scaled-calculation?case=nonexistent",
			wantStatusCode: http.StatusNotFound,
			wantStatus:     "unavailable",
		},
		{
			name:           "UnknownAddress",
			method:         http.MethodGet,
			url:            "/api/provenance/scaled-calculation?addr=008000",
			wantStatusCode: http.StatusNotFound,
			wantStatus:     "unavailable",
		},
		{
			name:           "InvalidAnchorNumber",
			method:         http.MethodGet,
			url:            "/api/provenance/scaled-calculation?anchor=invalid",
			wantStatusCode: http.StatusBadRequest,
			wantStatus:     "unavailable",
		},
		{
			name:           "InvalidAddressFormat",
			method:         http.MethodGet,
			url:            "/api/provenance/scaled-calculation?addr=not_an_address",
			wantStatusCode: http.StatusBadRequest,
			wantStatus:     "unavailable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.url, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatusCode {
				t.Fatalf("status code: got %d, want %d (body: %s)", rec.Code, tc.wantStatusCode, rec.Body.String())
			}

			var resp ScaledCalculationResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}

			if resp.Status != tc.wantStatus {
				t.Errorf("status: got %q, want %q", resp.Status, tc.wantStatus)
			}

			if tc.wantCaseCount > 0 && len(resp.Cases) != tc.wantCaseCount {
				t.Errorf("case count: got %d, want %d", len(resp.Cases), tc.wantCaseCount)
			}

			if tc.wantAnchorID > 0 {
				if resp.SelectedCase == nil {
					t.Fatalf("expected selected_case to be populated")
				}
				if resp.SelectedCase.AnchorRetirementID != tc.wantAnchorID {
					t.Errorf("selected anchor ID: got %d, want %d", resp.SelectedCase.AnchorRetirementID, tc.wantAnchorID)
				}
				if resp.SelectedCase.PhysicalOutput != tc.wantOutput {
					t.Errorf("selected physical output: got %s, want %s", resp.SelectedCase.PhysicalOutput, tc.wantOutput)
				}
				if resp.SelectedCase.SignedOutput != tc.wantSigned {
					t.Errorf("selected signed output: got %d, want %d", resp.SelectedCase.SignedOutput, tc.wantSigned)
				}
			}
		})
	}
}

func TestScaledCalculation_CustomFileLoading(t *testing.T) {
	tempDir := t.TempDir()
	pkt := DefaultScaledOutputPacket()

	docBytes := []byte(`{"mock": "recovery"}`)
	h := sha256.Sum256(docBytes)
	pkt.RecoveryDocumentSHA256 = hex.EncodeToString(h[:])
	if err := os.WriteFile(filepath.Join(tempDir, "recovery.json"), docBytes, 0644); err != nil {
		t.Fatalf("write temp recovery.json: %v", err)
	}

	data, err := json.MarshalIndent(pkt, "", "  ")
	if err != nil {
		t.Fatalf("marshal packet: %v", err)
	}

	tempFile := filepath.Join(tempDir, "custom-packet.json")
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		t.Fatalf("write temp packet: %v", err)
	}

	mux := http.NewServeMux()
	occ := newAdmittedOccurrenceIndex(pkt)
	doc := &recovery.Document{
		ROM: recovery.ROMIdentity{
			NormalizedSHA256: pkt.ROMSHA256,
		},
	}
	RegisterScaledCalculationRoutes(mux, occ, doc, tempFile)

	req := httptest.NewRequest(http.MethodGet, "/api/provenance/scaled-calculation?anchor=52254", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code: got %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp ScaledCalculationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Status != "available" || resp.SelectedCase == nil || resp.SelectedCase.AnchorRetirementID != 52254 {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestScaledCalculation_UnreadableFileError(t *testing.T) {
	mux := http.NewServeMux()
	RegisterScaledCalculationRoutes(mux, nil, nil, "/nonexistent/path/packet.json")

	// Missing file should return default packet or unavailable report gracefully
	req := httptest.NewRequest(http.MethodGet, "/api/provenance/scaled-calculation", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Since LoadScaledOutputPacket falls back to DefaultScaledOutputPacket when unconfigured,
	// test with an invalid JSON file to exercise the error path.
	tempDir := t.TempDir()
	invalidFile := filepath.Join(tempDir, "invalid.json")
	if err := os.WriteFile(invalidFile, []byte("{invalid json"), 0644); err != nil {
		t.Fatalf("write invalid json: %v", err)
	}

	mux2 := http.NewServeMux()
	RegisterScaledCalculationRoutes(mux2, nil, nil, invalidFile)

	rec2 := httptest.NewRecorder()
	mux2.ServeHTTP(rec2, req)

	var resp ScaledCalculationResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "unavailable" || !strings.Contains(resp.Reason, "scaled calculation packet unavailable") {
		t.Errorf("expected unavailable report on corrupted file, got %+v", resp)
	}
}

func TestScaledCalculation_RecordedAdmission(t *testing.T) {
	// Case 1: Incomplete or empty occurrence index -> admission failure
	occEmpty := newOccurrenceIndex("stream-123")
	muxRefuse := http.NewServeMux()
	RegisterScaledCalculationRoutes(muxRefuse, occEmpty, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/api/provenance/scaled-calculation", nil)
	rec := httptest.NewRecorder()
	muxRefuse.ServeHTTP(rec, req)

	var respRefuse ScaledCalculationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &respRefuse); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if respRefuse.Status != "unavailable" {
		t.Errorf("expected status 'unavailable' for empty retained records, got %q", respRefuse.Status)
	}
	if !strings.Contains(respRefuse.Reason, "admission failed") {
		t.Errorf("expected admission failure reason, got %q", respRefuse.Reason)
	}

	// Case 2: Mutated read_id (e.g. 999999) must fail admission and not be silently skipped
	pktMutated := DefaultScaledOutputPacket()
	occPopulated := newAdmittedOccurrenceIndex(pktMutated)
	pktMutated.Cases[0].CoefficientLow.ReadID = 999999
	err := ValidateScaledOutputRecords(pktMutated, occPopulated)
	if err == nil || !strings.Contains(err.Error(), "999999") {
		t.Fatalf("expected error mentioning missing read 999999, got %v", err)
	}

	// Case 3: Nil occurrence index must be refused
	if err := ValidateScaledOutputRecords(pktMutated, nil); err == nil || !strings.Contains(err.Error(), "occurrence index required") {
		t.Fatalf("expected nil occurrence index to be refused, got %v", err)
	}
}

func ExampleTruncateAndScale() {
	// Mode 7 multiply with coefficient -61 and multiplier -8 produces product 488 ($0001E8).
	// SNES hardware discards the low product byte ($E8 = 232) before two 16-bit ASLs.
	product := -61 * -8
	middleHigh, scaledWord, signedOutput := TruncateAndScale(product)

	fmt.Printf("product: %d ($%06X)\n", product, uint32(product)&0xFFFFFF)
	fmt.Printf("middle/high readback: %d ($%04X)\n", middleHigh, uint16(int16(middleHigh)))
	fmt.Printf("scaled output: %d ($%04X)\n", signedOutput, scaledWord)
	// Output:
	// product: 488 ($0001E8)
	// middle/high readback: 1 ($0001)
	// scaled output: 4 ($0004)
}

func ExampleCalculateScaledProduct() {
	// Mode 7 multiply with coefficient -61 and multiplier 64 produces product -3904 ($FFF0C0).
	coeff, mult := -61, 64
	product, lowByte, middleHigh, scaledWord, signedOutput := CalculateScaledProduct(coeff, mult)

	fmt.Printf("inputs: %d * %d\n", coeff, mult)
	fmt.Printf("product: %d, discarded low byte: $%02X\n", product, lowByte)
	fmt.Printf("middle/high readback: %d ($%04X)\n", middleHigh, uint16(int16(middleHigh)))
	fmt.Printf("stored output: %d ($%04X)\n", signedOutput, scaledWord)
	// Output:
	// inputs: -61 * 64
	// product: -3904, discarded low byte: $C0
	// middle/high readback: -16 ($FFF0)
	// stored output: -64 ($FFC0)
}
