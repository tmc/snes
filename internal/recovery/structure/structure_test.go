package structure

import (
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

func createTestDocument() *recovery.Document {
	doc := recovery.NewDocument(recovery.ROMIdentity{
		NormalizedSHA256: "test-hash",
		Mapper:           "lorom",
	})

	// Instructions:
	// 0x008000: SEI (78)
	// 0x008001: STZ $2100 (9C 00 21) - writes INIDISP
	// 0x008004: JSR $008020 (20 20 80)
	// 0x008007: STP (DB)
	// Subroutine at 0x008020:
	// 0x008020: LDA $0042 (A5 42) - direct page read
	// 0x008022: RTS (60)
	insts := []recovery.Instruction{
		{
			ID:           "inst-8000",
			Address:      0x008000,
			Offset:       0,
			Bytes:        "78",
			Opcode:       0x78,
			Mnemonic:     "sei",
			Mode:         "implied",
			Context:      recovery.Context{E: "set", M: "set", X: "set", C: "clear"},
			Architecture: "wdc65816",
		},
		{
			ID:           "inst-8001",
			Address:      0x008001,
			Offset:       1,
			Bytes:        "9c0021",
			Opcode:       0x9C,
			Mnemonic:     "stz",
			Mode:         "absolute",
			Context:      recovery.Context{E: "set", M: "set", X: "set", C: "clear"},
			Architecture: "wdc65816",
		},
		{
			ID:           "inst-8004",
			Address:      0x008004,
			Offset:       4,
			Bytes:        "202080",
			Opcode:       0x20,
			Mnemonic:     "jsr",
			Mode:         "absolute",
			Context:      recovery.Context{E: "set", M: "set", X: "set", C: "clear"},
			Architecture: "wdc65816",
		},
		{
			ID:           "inst-8007",
			Address:      0x008007,
			Offset:       7,
			Bytes:        "db",
			Opcode:       0xDB,
			Mnemonic:     "stp",
			Mode:         "implied",
			Context:      recovery.Context{E: "set", M: "set", X: "set", C: "clear"},
			Architecture: "wdc65816",
		},
		{
			ID:           "inst-8020",
			Address:      0x008020,
			Offset:       0x20,
			Bytes:        "a542",
			Opcode:       0xA5,
			Mnemonic:     "lda.b",
			Mode:         "direct_page",
			Context:      recovery.Context{E: "set", M: "set", X: "set", C: "clear"},
			Architecture: "wdc65816",
		},
		{
			ID:           "inst-8022",
			Address:      0x008022,
			Offset:       0x22,
			Bytes:        "60",
			Opcode:       0x60,
			Mnemonic:     "rts",
			Mode:         "implied",
			Context:      recovery.Context{E: "set", M: "set", X: "set", C: "clear"},
			Architecture: "wdc65816",
		},
	}

	edges := []recovery.Edge{
		{
			ID:          "edge-1",
			Kind:        "fallthrough",
			Source:      "inst-8000",
			Destination: 0x008001,
			Evidence:    []string{"ev-1"},
		},
		{
			ID:          "edge-2",
			Kind:        "fallthrough",
			Source:      "inst-8001",
			Destination: 0x008004,
			Evidence:    []string{"ev-2"},
		},
		{
			ID:          "edge-3",
			Kind:        "call",
			Source:      "inst-8004",
			Destination: 0x008020,
			Evidence:    []string{"ev-3"},
		},
		{
			ID:          "edge-4",
			Kind:        "fallthrough",
			Source:      "inst-8020",
			Destination: 0x008022,
			Evidence:    []string{"ev-4"},
		},
		{
			ID:          "edge-5",
			Kind:        "return",
			Source:      "inst-8022",
			Destination: 0x008007,
			Evidence:    []string{"ev-5"},
		},
	}

	doc.Instructions = insts
	doc.Edges = edges
	return doc
}

func TestStructure_BasicBlocks(t *testing.T) {
	doc := createTestDocument()
	blocks := ExtractBasicBlocks(doc)

	if len(blocks) < 2 {
		t.Fatalf("expected at least 2 basic blocks, got %d", len(blocks))
	}

	firstBlock := blocks[0]
	if firstBlock.StartAddress != 0x008000 {
		t.Errorf("expected first block start at 0x008000, got 0x%06X", firstBlock.StartAddress)
	}
}

func TestStructure_Routines(t *testing.T) {
	doc := createTestDocument()
	routines := ExtractRoutines(doc)

	if len(routines) != 2 {
		t.Fatalf("expected 2 routines (reset and sub_8020), got %d", len(routines))
	}

	r0 := routines[0]
	if r0.EntryAddress != 0x008000 {
		t.Errorf("expected routine 0 entry 0x008000, got 0x%06X", r0.EntryAddress)
	}

	r1 := routines[1]
	if r1.EntryAddress != 0x008020 {
		t.Errorf("expected routine 1 entry 0x008020, got 0x%06X", r1.EntryAddress)
	}
	if len(r1.Callers) != 1 || r1.Callers[0] != 0x008004 {
		t.Errorf("expected caller 0x008004, got %v", r1.Callers)
	}
	if len(r1.Exits) != 1 || r1.Exits[0] != 0x008022 {
		t.Errorf("expected exit 0x008022, got %v", r1.Exits)
	}
}

func TestStructure_MemoryReferences(t *testing.T) {
	doc := createTestDocument()
	refs := ExtractMemoryReferences(doc)

	if len(refs) != 3 {
		t.Fatalf("expected 3 memory references, got %d", len(refs))
	}

	// Ref 1: STZ $2100 -> INIDISP (write, hardware_register)
	r1 := refs[0]
	if r1.EncodedAddress != 0x002100 {
		t.Errorf("expected encoded address 0x002100, got 0x%06X", r1.EncodedAddress)
	}
	if r1.HardwareName != "INIDISP" {
		t.Errorf("expected hardware name INIDISP, got %q", r1.HardwareName)
	}
	if r1.AddressSpace != "hardware_register" {
		t.Errorf("expected address space hardware_register, got %q", r1.AddressSpace)
	}
	if r1.Direction != "write" {
		t.Errorf("expected direction write, got %q", r1.Direction)
	}

	// Ref 2: JSR $8020 -> execute, rom
	r2 := refs[1]
	if r2.EncodedAddress != 0x008020 {
		t.Errorf("expected encoded address 0x008020, got 0x%06X", r2.EncodedAddress)
	}
	if r2.Direction != "execute" {
		t.Errorf("expected direction execute, got %q", r2.Direction)
	}

	// Ref 3: LDA $42 -> direct_page read
	r3 := refs[2]
	if r3.EncodedAddress != 0x42 {
		t.Errorf("expected direct page 0x42, got 0x%06X", r3.EncodedAddress)
	}
	if r3.AddressSpace != "direct_page" {
		t.Errorf("expected address space direct_page, got %q", r3.AddressSpace)
	}
	if r3.Direction != "read" {
		t.Errorf("expected direction read, got %q", r3.Direction)
	}
}

func TestStructure_CFGAndDOT(t *testing.T) {
	doc := createTestDocument()
	cfg := BuildCFG(doc, 0)

	if len(cfg.Nodes) == 0 {
		t.Fatalf("expected CFG nodes, got none")
	}
	if len(cfg.Edges) == 0 {
		t.Fatalf("expected CFG edges, got none")
	}

	dot := cfg.ToDOT()
	if !strings.Contains(dot, "digraph CFG") {
		t.Errorf("expected DOT to contain 'digraph CFG', got:\n%s", dot)
	}
	if !strings.Contains(dot, "call") {
		t.Errorf("expected DOT to contain 'call' edge, got:\n%s", dot)
	}
}

func TestStructure_CFGToSVG(t *testing.T) {
	doc := createTestDocument()
	// Add an edge to an unresolved target
	doc.Edges = append(doc.Edges, recovery.Edge{
		ID:          "edge-unresolved",
		Kind:        "branch",
		Source:      "inst-8022",
		Destination: 0x009999, // Unresolved
		Evidence:    []string{"ev-unresolved"},
	})

	cfg := BuildCFG(doc, 0)
	if len(cfg.Nodes) == 0 {
		t.Fatalf("expected CFG nodes, got none")
	}

	svg := cfg.ToSVG()

	// Verify valid SVG structure
	if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
		t.Errorf("expected valid SVG tags, got:\n%s", svg)
	}
	if !strings.Contains(svg, "viewBox=") {
		t.Errorf("expected SVG viewBox attribute")
	}
	if !strings.Contains(svg, "<defs>") || !strings.Contains(svg, "marker id=\"arrow-call\"") {
		t.Errorf("expected arrowhead marker defs")
	}

	// Verify nodes
	if !strings.Contains(svg, "class=\"cfg-node") {
		t.Errorf("expected cfg-node elements in SVG")
	}
	if !strings.Contains(svg, "$008000") {
		t.Errorf("expected address 0x008000 in SVG")
	}
	if !strings.Contains(svg, "sei") {
		t.Errorf("expected mnemonic 'sei' in SVG")
	}

	// Verify unresolved node styling (red, dashed)
	if !strings.Contains(svg, "class=\"cfg-node unresolved\"") {
		t.Errorf("expected unresolved cfg-node class")
	}
	if !strings.Contains(svg, "Unresolved $009999") {
		t.Errorf("expected unresolved label in SVG")
	}
	if !strings.Contains(svg, "stroke=\"#f87171\"") || !strings.Contains(svg, "stroke-dasharray=\"5,3\"") {
		t.Errorf("expected red dashed styling for unresolved node")
	}

	// Verify edges (call, return, branch)
	if !strings.Contains(svg, "data-kind=\"call\"") {
		t.Errorf("expected call edge in SVG")
	}
	if !strings.Contains(svg, "data-kind=\"branch\"") {
		t.Errorf("expected branch edge in SVG")
	}

	// Test empty CFG
	emptyCFG := &CFG{}
	emptySVG := emptyCFG.ToSVG()
	if !strings.Contains(emptySVG, "No CFG available") {
		t.Errorf("expected fallback for empty CFG, got: %s", emptySVG)
	}
}

func TestStructure_CFGExternalRoutineResolution(t *testing.T) {
	doc := createTestDocument()
	// Build CFG for routine 0x008000, which has JSR $008020
	cfg := BuildCFG(doc, 0x008000)
	if len(cfg.Nodes) == 0 {
		t.Fatalf("expected CFG nodes, got none")
	}

	var foundExternal, foundUnresolved bool
	for _, n := range cfg.Nodes {
		if n.StartAddress == 0x008020 {
			if n.IsUnresolved {
				t.Errorf("expected known routine 0x008020 to not be marked unresolved")
			}
			if !n.IsExternal {
				t.Errorf("expected known routine 0x008020 to be marked IsExternal")
			}
			foundExternal = true
		}
		if n.IsUnresolved {
			foundUnresolved = true
		}
	}

	if !foundExternal {
		t.Errorf("expected to find external callee node at 0x008020")
	}
	if foundUnresolved {
		t.Errorf("did not expect any unresolved nodes in this routine CFG")
	}
}
