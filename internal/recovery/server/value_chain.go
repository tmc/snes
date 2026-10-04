package server

import (
	"fmt"
	"net/http"
)

// Canonical instruction IDs for the 3 value-chain nodes.
const (
	ValueChainNode0CanonicalID = "3c34e9a7b3c58e42ba4fd3a3368d195c5e049db0e2fe0748fa2026ff2e6aec21" // 09:F882 LDY dp05
	ValueChainNode1CanonicalID = "4ed4a3d08194e0f9ea098c20152b30a771106ad5ec1f23d64790ab5f0661de97" // 09:F884 LDA FB6D,Y
	ValueChainNode2CanonicalID = "2cf5a980f243cf349f44dd4cadcf2fc0f4df64f5ffb14ca3ab0ef90ea580cfea" // 09:F887 STA dp54
)

// Expected dynamic identities to guard against non-exact occurrence retrieval.
const (
	ExpectedNode0RetirementID = 52077
	ExpectedNode0Seq          = 13199
	ExpectedNode0BusID        = 52076

	ExpectedNode1RetirementID = 52082
	ExpectedNode1Seq          = 13200
	ExpectedNode1BusID        = 52081

	ExpectedNode2RetirementID = 52086
	ExpectedNode2Seq          = 13201
	ExpectedNode2BusID        = 52085
)

// Pinned admitted content hashes for the authentic capture corpus.
const (
	PinnedStreamSHA256 = "68aecfcf95fac6863d657979ff802c27dae5610799168b3321aad9f41046e421"
	PinnedROMSHA256    = "66871d66be19ad2c34c927d6b14cd8eb6fc3181965b6e517cb361f7316009cfb"
	PinnedDocSHA256    = "cb6f4a1af5d6ec5f2d906596ea7e3d61f05d17f6709477133afc551837f51836"
)

// ValueChainEdge defines a typed dependency edge between an instruction and an operand/address.
type ValueChainEdge struct {
	Kind             string `json:"kind"` // "data" or "address"
	From             string `json:"from"`
	To               string `json:"to"`
	Description      string `json:"description"`
	BaseAddress      string `json:"base_address,omitempty"`
	IndexRegister    string `json:"index_register,omitempty"`
	IndexValue       int    `json:"index_value,omitempty"`
	EffectiveAddress string `json:"effective_address,omitempty"`
	ROMOffset        string `json:"rom_offset,omitempty"`
}

// ValueChainNode represents one of the three dynamic instruction occurrences in the chain.
type ValueChainNode struct {
	Instruction   string            `json:"instruction"`
	InstructionID string            `json:"instruction_id"`
	Address       string            `json:"address"`
	RetirementID  uint64            `json:"retirement_id"`
	Seq           uint64            `json:"seq"`
	TraceFrame    int               `json:"trace_frame"`
	Occurrence    *OccurrenceReport `json:"occurrence,omitempty"`
}

// ValueChainVersionRelation captures the recorded physical WRAM version ordering relative to 139220.
type ValueChainVersionRelation struct {
	EarlierDirectWRAMWriterID    uint64 `json:"earlier_direct_wram_writer_id"`
	EarlierWriterRetirementID    uint64 `json:"earlier_writer_retirement_id"`
	EarlierWriterValue           int    `json:"earlier_writer_value"`
	NoDirectWRAMWritesBetween    bool   `json:"no_direct_wram_writes_between_30147_and_139209"`
	LaterReadID                  uint64 `json:"later_read_id"`
	LaterReadRetirementID        uint64 `json:"later_read_retirement_id"`
	LaterReplacementWriteID      uint64 `json:"later_replacement_write_id"`
	LaterReplacementRetirementID uint64 `json:"later_replacement_retirement_id"`
	LaterValue                   int    `json:"later_value"`
	Explanation                  string `json:"explanation"`
	Limitation                   string `json:"limitation"`
}

// ValueChainCard contains the bounded value-chain payload with verified typed dependency edges.
type ValueChainCard struct {
	Status          string                    `json:"status"` // "available" or "unavailable"
	Reason          string                    `json:"reason,omitempty"`
	Scope           string                    `json:"scope"`
	StreamSHA256    string                    `json:"stream_sha256"`
	ROMSHA256       string                    `json:"rom_sha256"`
	DocumentSHA256  string                    `json:"document_sha256"`
	GuardsMatched   bool                      `json:"guards_matched"`
	Nodes           []ValueChainNode          `json:"nodes"`
	DependencyEdges []ValueChainEdge          `json:"dependency_edges"`
	VersionRelation ValueChainVersionRelation `json:"version_relation"`
}

// BuildValueChainCard inspects the server occurrences and constructs the bounded value chain card,
// guarding exact retirement, Seq, and bus identities against admitted dataset content pins.
func (s *Server) BuildValueChainCard() *ValueChainCard {
	card := &ValueChainCard{
		Scope: "verified recorded value/address chain: 115 -> Y -> ROM lookup -> 20",
	}

	if s == nil || s.Occurrences == nil || s.Document == nil {
		card.Status = "unavailable"
		card.Reason = "server, document, or occurrence index unavailable"
		return card
	}
	card.ROMSHA256 = s.Document.ROM.NormalizedSHA256
	card.StreamSHA256 = s.Occurrences.StreamSHA256
	card.DocumentSHA256 = s.DocumentSHA256

	// Content pins: require exact admitted original stream, ROM, and document content
	if card.StreamSHA256 != PinnedStreamSHA256 {
		card.Status = "unavailable"
		card.Reason = fmt.Sprintf("unadmitted stream SHA-256 %q, require pinned %q", card.StreamSHA256, PinnedStreamSHA256)
		return card
	}
	if card.ROMSHA256 != PinnedROMSHA256 {
		card.Status = "unavailable"
		card.Reason = fmt.Sprintf("unadmitted ROM SHA-256 %q, require pinned %q", card.ROMSHA256, PinnedROMSHA256)
		return card
	}
	if card.DocumentSHA256 != PinnedDocSHA256 {
		card.Status = "unavailable"
		card.Reason = fmt.Sprintf("unadmitted document SHA-256 %q, require pinned %q", card.DocumentSHA256, PinnedDocSHA256)
		return card
	}

	// 1. Query Node 0: LDY dp05 (09:F882)
	rep0 := s.Occurrences.Lookup(1, ValueChainNode0CanonicalID, 0x09F882)
	if rep0 == nil || rep0.Status != "available" {
		card.Status = "unavailable"
		card.Reason = fmt.Sprintf("node 0 (LDY dp05) occurrence unavailable: %v", rep0)
		return card
	}

	// 2. Query Node 1: LDA FB6D,Y (09:F884)
	rep1 := s.Occurrences.Lookup(1, ValueChainNode1CanonicalID, 0x09F884)
	if rep1 == nil || rep1.Status != "available" {
		card.Status = "unavailable"
		card.Reason = fmt.Sprintf("node 1 (LDA FB6D,Y) occurrence unavailable: %v", rep1)
		return card
	}

	// 3. Query Node 2: STA dp54 (09:F887)
	rep2 := s.Occurrences.Lookup(1, ValueChainNode2CanonicalID, 0x09F887)
	if rep2 == nil || rep2.Status != "available" {
		card.Status = "unavailable"
		card.Reason = fmt.Sprintf("node 2 (STA dp54) occurrence unavailable: %v", rep2)
		return card
	}

	// 4. Guard exact dynamic identities and recorded values
	node0Guards := rep0.RetirementID == ExpectedNode0RetirementID && rep0.Seq == ExpectedNode0Seq &&
		rep0.OperandBus != nil && rep0.OperandBus.ID == ExpectedNode0BusID &&
		rep0.OperandBus.Value == 115 && rep0.Exit.Y == 115
	node1Guards := rep1.RetirementID == ExpectedNode1RetirementID && rep1.Seq == ExpectedNode1Seq &&
		rep1.OperandBus != nil && rep1.OperandBus.ID == ExpectedNode1BusID &&
		rep1.OperandBus.Value == 20 && uint8(rep1.Exit.A) == 20
	node2Guards := rep2.RetirementID == ExpectedNode2RetirementID && rep2.Seq == ExpectedNode2Seq &&
		rep2.OperandBus != nil && rep2.OperandBus.ID == ExpectedNode2BusID &&
		rep2.OperandBus.Value == 20

	if !node0Guards || !node1Guards || !node2Guards {
		card.Status = "unavailable"
		card.Reason = fmt.Sprintf("dynamic identity guard mismatch: node0(%v,%v,%v) node1(%v,%v,%v) node2(%v,%v,%v)",
			rep0.RetirementID, rep0.Seq, rep0.OperandBus != nil && rep0.OperandBus.ID == ExpectedNode0BusID,
			rep1.RetirementID, rep1.Seq, rep1.OperandBus != nil && rep1.OperandBus.ID == ExpectedNode1BusID,
			rep2.RetirementID, rep2.Seq, rep2.OperandBus != nil && rep2.OperandBus.ID == ExpectedNode2BusID)
		return card
	}

	// 5. Guard physical direct WRAM ordering and verify no intervening direct writes
	noInterveningDirectWrites := true
	if accesses := s.Occurrences.GetWRAMAccesses(0x7E1F05); len(accesses) > 0 {
		found30147 := false
		for _, acc := range accesses {
			if acc.ID == 30147 {
				found30147 = true
				if acc.Op != "write" || acc.Value != 115 {
					card.Status = "unavailable"
					card.Reason = "event 30147 is not expected write of 115"
					return card
				}
				continue
			}
			if found30147 && acc.ID < 139209 {
				if acc.Op == "write" {
					noInterveningDirectWrites = false
				}
			}
			if acc.ID == 139219 {
				if acc.Op != "write" || acc.Value != 120 {
					card.Status = "unavailable"
					card.Reason = "event 139219 is not expected write of 120"
					return card
				}
			}
		}
	}
	if !noInterveningDirectWrites {
		card.Status = "unavailable"
		card.Reason = "detected intervening direct WRAM write to $7E:1F05 between event 30147 and 139209"
		return card
	}

	card.Status = "available"
	card.GuardsMatched = true

	// Clone node reports without ValueChain to avoid recursive nesting
	cloneNodeRep := func(r *OccurrenceReport) *OccurrenceReport {
		if r == nil {
			return nil
		}
		c := *r
		c.ValueChain = nil
		return &c
	}

	card.Nodes = []ValueChainNode{
		{
			Instruction:   "09:F882 LDY $05",
			InstructionID: ValueChainNode0CanonicalID,
			Address:       "09:F882",
			RetirementID:  rep0.RetirementID,
			Seq:           rep0.Seq,
			TraceFrame:    1,
			Occurrence:    cloneNodeRep(rep0),
		},
		{
			Instruction:   "09:F884 LDA $FB6D,Y",
			InstructionID: ValueChainNode1CanonicalID,
			Address:       "09:F884",
			RetirementID:  rep1.RetirementID,
			Seq:           rep1.Seq,
			TraceFrame:    1,
			Occurrence:    cloneNodeRep(rep1),
		},
		{
			Instruction:   "09:F887 STA $54",
			InstructionID: ValueChainNode2CanonicalID,
			Address:       "09:F887",
			RetirementID:  rep2.RetirementID,
			Seq:           rep2.Seq,
			TraceFrame:    1,
			Occurrence:    cloneNodeRep(rep2),
		},
	}

	card.DependencyEdges = []ValueChainEdge{
		{
			Kind:        "data",
			From:        "bus52076/WRAM7E1F05=115",
			To:          "retirement52077/Y=115",
			Description: "WRAM $7E:1F05 read value 115 loaded into register Y",
		},
		{
			Kind:             "address",
			From:             "retirement52077/Y=115",
			To:               "bus52081/ROM09FBE0",
			Description:      "Y register index 115 selects effective ROM address with base $FB6D in DB $09",
			BaseAddress:      "$09:FB6D",
			IndexRegister:    "Y",
			IndexValue:       115,
			EffectiveAddress: "$09:FBE0",
			ROMOffset:        "$04:FBE0 (326624)",
		},
		{
			Kind:        "data",
			From:        "bus52081/ROMoffset04FBE0=20",
			To:          "retirement52082/A.low=20",
			Description: "ROM byte 20 ($14) loaded into A.low (architectural high A=$FF preserved)",
		},
		{
			Kind:        "data",
			From:        "retirement52082/A.low=20",
			To:          "bus52085/WRAM7E1F54=20",
			Description: "Accumulator low byte 20 stored to WRAM direct page $1F00 + $54 = $7E:1F54",
		},
	}

	card.VersionRelation = ValueChainVersionRelation{
		EarlierDirectWRAMWriterID:    30147,
		EarlierWriterRetirementID:    30148,
		EarlierWriterValue:           115,
		NoDirectWRAMWritesBetween:    true,
		LaterReadID:                  139209,
		LaterReadRetirementID:        139210,
		LaterReplacementWriteID:      139219,
		LaterReplacementRetirementID: 139220,
		LaterValue:                   120,
		Explanation:                  "The earlier lookup at event 52076 used WRAM byte 115 written by event 30147 before later replacement 120 by event 139219.",
		Limitation:                   "Recorded direct-WRAM physical transaction ordering; distinct from standalone byte-version window namespace.",
	}

	return card
}

func (s *Server) handleValueChain(w http.ResponseWriter, r *http.Request) {
	card := s.BuildValueChainCard()
	writeJSON(w, card)
}
