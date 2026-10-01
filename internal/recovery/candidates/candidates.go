package candidates

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/coverage"
	"github.com/tmc/snes/internal/recovery/decomp"
	"github.com/tmc/snes/internal/recovery/structure"
)

// Options bounds proposed regions. Zero values use conservative defaults.
type Options struct {
	MaxInstructions int
	MaxBytes        uint32
	MaxCallDepth    int
}

// Source identifies an input artifact by the hash of the bytes read.
type Source struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
	Kind   string `json:"kind"`
}

// Frontier is a boundary needing additional recovery or an explicit refusal contract.
type Frontier struct {
	From   uint32 `json:"from"`
	Target uint32 `json:"target"`
	Reason string `json:"reason"`
}

// Proposal contains review inputs, not a generation or replay admission contract.
type Proposal struct {
	Entry            uint32             `json:"entry"`
	Start            uint32             `json:"start"`
	End              uint32             `json:"end"`
	InstructionIDs   []string           `json:"instruction_ids"`
	Contexts         []recovery.Context `json:"entry_contexts"`
	RefusalFrontiers []Frontier         `json:"refusal_frontiers"`
}

// Candidate describes a bounded leaf or caller/callee proposal.
type Candidate struct {
	ID                            string           `json:"id"`
	Kind                          string           `json:"kind"`
	Status                        string           `json:"status"`
	Entry                         uint32           `json:"entry"`
	Calls                         []uint32         `json:"call_targets,omitempty"`
	Returns                       []uint32         `json:"returns,omitempty"`
	RoutineReturns                []uint32         `json:"routine_returns,omitempty"`
	InstructionCount              int              `json:"instruction_count"`
	SupportedInstructions         int              `json:"liftable_instructions"`
	ByteSpan                      uint32           `json:"byte_span"`
	ObservedEntryHits             string           `json:"observed_entry_hits"`
	EntryHitQuality               coverage.Quality `json:"entry_hit_quality"`
	ReportedCompleteExecutions    uint64           `json:"reported_complete_executions"`
	ReportedInterruptedExecutions uint64           `json:"reported_interrupted_executions"`
	InventorySources              []string         `json:"inventory_sources,omitempty"`
	EvidenceIDs                   []string         `json:"evidence_ids,omitempty"`
	Flags                         []string         `json:"flags,omitempty"`
	Proposal                      Proposal         `json:"proposal"`
}

// Report preserves source identities and the limits of candidate ranking.
type Report struct {
	Schema      string              `json:"schema"`
	ROMSHA256   string              `json:"rom_sha256"`
	Sources     []Source            `json:"sources"`
	Candidates  []Candidate         `json:"candidates"`
	Limitations []string            `json:"limitations"`
	Occurrences []Occurrence        `json:"reported_occurrences,omitempty"`
	Evidence    []recovery.Evidence `json:"evidence"`
}

type graph struct {
	inst     map[uint32]recovery.Instruction
	variants map[uint32][]recovery.Instruction
	edges    map[string][]recovery.Edge
	entries  map[uint32]bool
}

// Mine derives candidates from recovered instruction encodings and edges.
// Coverage hits are not complete executions. Inventory counts remain unverified.
func Mine(doc *recovery.Document, idx *coverage.Index, inventory []Occurrence, sources []Source, opts Options) (*Report, error) {
	if doc == nil {
		return nil, fmt.Errorf("mine candidates: nil document")
	}
	if opts.MaxInstructions < 0 || opts.MaxInstructions > 1<<20 || opts.MaxCallDepth < 0 || opts.MaxCallDepth > 64 {
		return nil, fmt.Errorf("mine candidates: invalid bound")
	}
	if opts.MaxInstructions == 0 {
		opts.MaxInstructions = 4096
	}
	if opts.MaxBytes == 0 {
		opts.MaxBytes = 32768
	}
	if opts.MaxCallDepth == 0 {
		opts.MaxCallDepth = 2
	}
	if idx != nil && idx.ROMHash != doc.ROM.NormalizedSHA256 {
		return nil, fmt.Errorf("mine candidates: coverage ROM identity differs from document")
	}
	g := graph{inst: map[uint32]recovery.Instruction{}, variants: map[uint32][]recovery.Instruction{}, edges: map[string][]recovery.Edge{}, entries: map[uint32]bool{}}
	for _, in := range doc.Instructions {
		g.variants[in.Address] = append(g.variants[in.Address], in)
	}
	for a, vv := range g.variants {
		sort.Slice(vv, func(i, j int) bool { return vv[i].ID < vv[j].ID })
		g.variants[a] = vv
		g.inst[a] = vv[0]
	}
	for _, e := range doc.Edges {
		g.edges[e.Source] = append(g.edges[e.Source], e)
		if e.Kind == "call" || e.Kind == "interrupt" {
			g.entries[e.Destination] = true
		}
	}
	for a, in := range g.inst {
		if t, ok := callTarget(in); ok {
			g.entries[t] = true
		}
		if in.Opcode == 0x40 {
			g.entries[a] = true
		}
	}
	if len(g.inst) > 0 {
		var first uint32 = 0xFFFFFFFF
		for a := range g.inst {
			if a < first {
				first = a
			}
		}
		g.entries[first] = true
	}
	for _, o := range inventory {
		g.entries[o.EntryPC] = true
	}
	r := &Report{Schema: "snes-candidate-proposals-v1", ROMSHA256: doc.ROM.NormalizedSHA256, Sources: append([]Source(nil), sources...), Limitations: []string{"proposals only: no admission, generation, or proof eligibility", "entry hits do not count complete executions", "liftable instruction counts report current IR lowering, not executable C qualification", "inventory completeness is reported, not independently verified", "unrecovered targets are frontiers, not data", "encoded memory operands do not prove effective addresses or absence of device effects", "cycles, interrupts, and runtime return contexts require qualification", "inventory interval bounds do not establish absence of interrupts"}}
	r.Evidence = append([]recovery.Evidence(nil), doc.Evidence...)
	sort.Slice(r.Evidence, func(i, j int) bool { return r.Evidence[i].ID < r.Evidence[j].ID })
	r.Occurrences = append([]Occurrence(nil), inventory...)
	sort.Slice(r.Occurrences, func(i, j int) bool {
		a, b := r.Occurrences[i], r.Occurrences[j]
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		if a.RunID != b.RunID {
			return a.RunID < b.RunID
		}
		return a.EntrySeq < b.EntrySeq
	})
	var entries []uint32
	for a := range g.entries {
		if _, ok := g.inst[a]; ok {
			entries = append(entries, a)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i] < entries[j] })
	for _, a := range entries {
		c := g.walk(a, false, opts)
		g.observations(&c, doc, idx, inventory)
		r.Candidates = append(r.Candidates, c)
		if len(c.Calls) > 0 {
			c = g.walk(a, true, opts)
			g.observations(&c, doc, idx, inventory)
			r.Candidates = append(r.Candidates, c)
		}
	}
	sort.Slice(r.Candidates, func(i, j int) bool {
		a, b := r.Candidates[i], r.Candidates[j]
		if a.ReportedCompleteExecutions != b.ReportedCompleteExecutions {
			return a.ReportedCompleteExecutions > b.ReportedCompleteExecutions
		}
		if len(a.Flags) != len(b.Flags) {
			return len(a.Flags) < len(b.Flags)
		}
		if a.SupportedInstructions != b.SupportedInstructions {
			return a.SupportedInstructions > b.SupportedInstructions
		}
		if a.ByteSpan != b.ByteSpan {
			return a.ByteSpan < b.ByteSpan
		}
		if len(a.Calls) != len(b.Calls) {
			return len(a.Calls) < len(b.Calls)
		}
		return a.ID < b.ID
	})
	sort.Slice(r.Sources, func(i, j int) bool { return r.Sources[i].ID < r.Sources[j].ID })
	return r, nil
}

func callTarget(in recovery.Instruction) (uint32, bool) {
	b, err := hex.DecodeString(in.Bytes)
	if err != nil {
		return 0, false
	}
	switch in.Opcode {
	case 0x20:
		if len(b) == 3 {
			return in.Address&0xFF0000 | uint32(b[1]) | uint32(b[2])<<8, true
		}
	case 0x22:
		if len(b) == 4 {
			return uint32(b[1]) | uint32(b[2])<<8 | uint32(b[3])<<16, true
		}
	}
	return 0, false
}
func branchTarget(in recovery.Instruction) (uint32, bool) {
	b, err := hex.DecodeString(in.Bytes)
	if err != nil {
		return 0, false
	}
	switch in.Opcode {
	case 0x10, 0x30, 0x50, 0x70, 0x90, 0xB0, 0xD0, 0xF0, 0x80:
		if len(b) == 2 {
			return in.Address&0xFF0000 | uint32(uint16(int32(uint16(in.Address))+2+int32(int8(b[1])))), true
		}
	case 0x82:
		if len(b) == 3 {
			return in.Address&0xFF0000 | uint32(uint16(int32(uint16(in.Address))+3+int32(int16(uint16(b[1])|uint16(b[2])<<8)))), true
		}
	}
	return 0, false
}

func (g graph) walk(entry uint32, compose bool, opts Options) Candidate {
	kind := "leaf"
	if compose {
		kind = "caller_callee"
	}
	c := Candidate{ID: fmt.Sprintf("%s-%06x", kind, entry), Kind: kind, Status: "proposed_unqualified", Entry: entry, ObservedEntryHits: "0", Proposal: Proposal{Entry: entry, Start: entry, End: entry}}
	type item struct {
		addr  uint32
		depth int
	}
	q := []item{{entry, 0}}
	seen := map[uint32]bool{}
	flags := map[string]bool{}
	evidence := map[string]bool{}
	calls := map[uint32]bool{}
	returns := map[uint32]bool{}
	rootReturns := map[uint32]bool{}
	adj := map[uint32][]uint32{}
	coveredBytes := uint32(0)
	frontiers := map[Frontier]bool{}
	for len(q) > 0 {
		v := q[0]
		q = q[1:]
		if seen[v.addr] {
			continue
		}
		seen[v.addr] = true
		in, ok := g.inst[v.addr]
		if !ok {
			continue
		}
		if c.InstructionCount >= opts.MaxInstructions {
			flags["instruction_bound"] = true
			frontiers[Frontier{v.addr, v.addr, "instruction_bound"}] = true
			continue
		}
		b, err := hex.DecodeString(in.Bytes)
		if err != nil || len(b) == 0 || b[0] != in.Opcode || in.Address > 0xFFFFFF || uint32(len(b))+in.Address > 0x1000000 {
			flags["invalid_instruction_encoding"] = true
			continue
		}
		start, end := c.Proposal.Start, c.Proposal.End
		if in.Address < start {
			start = in.Address
		}
		if next := in.Address + uint32(len(b)); next > end {
			end = next
		}
		if end-start > opts.MaxBytes {
			flags["byte_bound"] = true
			frontiers[Frontier{in.Address, in.Address, "byte_bound"}] = true
			continue
		}
		c.Proposal.Start, c.Proposal.End = start, end
		coveredBytes += uint32(len(b))
		c.InstructionCount++
		c.Proposal.InstructionIDs = append(c.Proposal.InstructionIDs, in.ID)
		for _, variant := range g.variants[in.Address] {
			for _, id := range variant.Evidence {
				evidence[id] = true
			}
			if variant.Bytes != in.Bytes || widthContext(variant.Context) != widthContext(in.Context) {
				flags["ambiguous_instruction_context"] = true
			}
		}
		if ambiguous(g.variants[in.Address]) {
			frontiers[Frontier{in.Address, in.Address, "ambiguous_instruction_context"}] = true
			continue
		}
		ir, lerr := decomp.LiftBlock(&structure.BasicBlock{ID: in.ID, StartAddress: in.Address, Instructions: []recovery.Instruction{in}}, in.Context)
		if lerr == nil && ir.UnsupportedCount == 0 {
			if in.Opcode != 0xF8 && !(in.Opcode == 0xE2 && len(b) > 1 && b[1]&8 != 0) {
				c.SupportedInstructions++
			}
		} else {
			flags["unsupported_instruction_or_context"] = true
		}
		if in.Opcode == 0xF8 || (in.Opcode == 0xE2 && len(b) > 1 && b[1]&8 != 0) {
			flags["unsupported_decimal_enable"] = true
		}
		if in.Opcode == 0x28 || in.Opcode == 0x40 || in.Opcode == 0xFB {
			flags["dynamic_status_context"] = true
		}
		if in.Mode == "absolute" || in.Mode == "absolute_x" || in.Mode == "absolute_y" {
			if len(b) >= 3 {
				a := uint16(b[1]) | uint16(b[2])<<8
				if a >= 0x2000 && a < 0x6000 {
					flags["potential_mmio"] = true
				}
			}
		}
		if in.Mode == "long" || in.Mode == "long_x" {
			if len(b) == 4 && structure.HardwareRegister(uint32(b[1])|uint32(b[2])<<8|uint32(b[3])<<16) != "" {
				flags["potential_mmio"] = true
			}
		}
		if strings.Contains(in.Mode, "indirect") {
			flags["unresolved_memory_or_control"] = true
		}
		add := func(target uint32, reason string) {
			if _, ok := g.inst[target]; !ok {
				frontiers[Frontier{in.Address, target, reason}] = true
				return
			}
			adj[in.Address] = append(adj[in.Address], target)
			q = append(q, item{target, v.depth})
		}
		for _, e := range g.edges[in.ID] {
			for _, id := range e.Evidence {
				evidence[id] = true
			}
			if e.Kind == "interrupt" {
				flags["interrupt_edge"] = true
			}
			if e.Destination == 0 && e.Kind != "return" {
				frontiers[Frontier{in.Address, 0, "unresolved_edge"}] = true
			}
		}
		switch in.Opcode {
		case 0x60, 0x6B:
			returns[in.Address] = true
			if v.depth == 0 {
				rootReturns[in.Address] = true
			}
			continue
		case 0x40:
			returns[in.Address] = true
			flags["interrupt_return"] = true
			continue
		case 0x00, 0x02, 0xCB, 0xDB:
			flags["device_or_interrupt_control"] = true
			continue
		case 0x4C, 0x5C, 0x6C, 0x7C, 0xDC, 0xFC:
			found := false
			for _, e := range g.edges[in.ID] {
				if e.Kind == "jump" || e.Kind == "branch" {
					add(e.Destination, "unrecovered_jump")
					found = true
				}
			}
			if !found {
				frontiers[Frontier{in.Address, 0, "unknown_control_target"}] = true
			}
			continue
		}
		if target, ok := callTarget(in); ok {
			calls[target] = true
			if compose {
				flags["call_return_context_unverified"] = true
				if target == entry || target == in.Address {
					flags["possible_recursion"] = true
				} else if v.depth >= opts.MaxCallDepth {
					flags["call_depth_bound"] = true
					frontiers[Frontier{in.Address, target, "call_depth_bound"}] = true
				} else if _, ok := g.inst[target]; ok {
					adj[in.Address] = append(adj[in.Address], target)
					q = append(q, item{target, v.depth + 1})
				} else {
					frontiers[Frontier{in.Address, target, "unrecovered_callee"}] = true
				}
			} else {
				flags["external_call"] = true
			}
		}
		if target, ok := branchTarget(in); ok {
			add(target, "unrecovered_branch")
			if in.Opcode == 0x80 || in.Opcode == 0x82 {
				continue
			}
		}
		add(in.Address&0xFF0000|uint32(uint16(in.Address)+uint16(len(b))), "unrecovered_fallthrough")
	}
	if graphCycle(adj) {
		flags["control_cycle_or_recursion"] = true
	}
	if coveredBytes != c.Proposal.End-c.Proposal.Start {
		flags["noncontiguous_recovered_span"] = true
	}
	if len(rootReturns) == 0 {
		flags["no_recovered_routine_return"] = true
	}
	if len(returns) == 0 {
		flags["no_recovered_return"] = true
	}
	if len(frontiers) > 0 {
		flags["open_frontier"] = true
	}
	for x := range flags {
		c.Flags = append(c.Flags, x)
	}
	sort.Strings(c.Flags)
	for x := range evidence {
		c.EvidenceIDs = append(c.EvidenceIDs, x)
	}
	sort.Strings(c.EvidenceIDs)
	for x := range calls {
		c.Calls = append(c.Calls, x)
	}
	sort.Slice(c.Calls, func(i, j int) bool { return c.Calls[i] < c.Calls[j] })
	for x := range rootReturns {
		c.RoutineReturns = append(c.RoutineReturns, x)
	}
	sort.Slice(c.RoutineReturns, func(i, j int) bool { return c.RoutineReturns[i] < c.RoutineReturns[j] })
	for x := range returns {
		c.Returns = append(c.Returns, x)
	}
	sort.Slice(c.Returns, func(i, j int) bool { return c.Returns[i] < c.Returns[j] })
	for f := range frontiers {
		c.Proposal.RefusalFrontiers = append(c.Proposal.RefusalFrontiers, f)
	}
	sort.Slice(c.Proposal.RefusalFrontiers, func(i, j int) bool {
		a, b := c.Proposal.RefusalFrontiers[i], c.Proposal.RefusalFrontiers[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		return a.Reason < b.Reason
	})
	sort.Strings(c.Proposal.InstructionIDs)
	for _, in := range g.variants[entry] {
		c.Proposal.Contexts = append(c.Proposal.Contexts, in.Context)
	}
	if !compose && len(c.Calls) > 0 {
		c.Kind = "routine_with_calls"
		c.ID = fmt.Sprintf("%s-%06x", c.Kind, c.Entry)
	}
	c.ByteSpan = c.Proposal.End - c.Proposal.Start
	return c
}

func (g graph) observations(c *Candidate, doc *recovery.Document, idx *coverage.Index, inventory []Occurrence) {
	hits := new(big.Int)
	c.EntryHitQuality = coverage.QualityUnavailable
	if idx != nil {
		c.EntryHitQuality = coverage.QualityUnknown
		if len(idx.Runs) > 0 {
			c.EntryHitQuality = coverage.QualityComplete
			for _, r := range idx.Runs {
				if !r.IsComplete || len(r.Gaps) > 0 {
					c.EntryHitQuality = coverage.QualityFiltered
					break
				}
			}
		}
		for _, s := range idx.Sites {
			if s.Address == c.Entry {
				hits.Add(hits, new(big.Int).SetUint64(s.Hits))
			}
		}
	}
	c.ObservedEntryHits = hits.String()
	sources := map[string]bool{}
	for _, o := range inventory {
		if o.EntryPC != c.Entry || o.ROMSHA256 != doc.ROM.NormalizedSHA256 {
			continue
		}
		if o.Interrupted {
			c.ReportedInterruptedExecutions++
			sources[o.SourceID] = true
			continue
		}
		if o.Complete {
			if returned := containsAddress(c.RoutineReturns, o.ReturnInsnPC); returned {
				c.ReportedCompleteExecutions++
				sources[o.SourceID] = true
			}
		}
	}
	for s := range sources {
		c.InventorySources = append(c.InventorySources, s)
	}
	sort.Strings(c.InventorySources)
}

func containsAddress(addresses []uint32, address uint32) bool {
	for _, a := range addresses {
		if a == address {
			return true
		}
	}
	return false
}

func graphCycle(adj map[uint32][]uint32) bool {
	state := map[uint32]uint8{}
	var visit func(uint32) bool
	visit = func(a uint32) bool {
		if state[a] == 1 {
			return true
		}
		if state[a] == 2 {
			return false
		}
		state[a] = 1
		for _, b := range adj[a] {
			if visit(b) {
				return true
			}
		}
		state[a] = 2
		return false
	}
	for a := range adj {
		if visit(a) {
			return true
		}
	}
	return false
}

func ambiguous(variants []recovery.Instruction) bool {
	if len(variants) < 2 {
		return false
	}
	first := variants[0]
	for _, in := range variants[1:] {
		if in.Bytes != first.Bytes || widthContext(in.Context) != widthContext(first.Context) {
			return true
		}
	}
	return false
}

func widthContext(c recovery.Context) string {
	norm := func(s string) string {
		switch s {
		case "clear", "0", "false":
			return "0"
		case "set", "1", "true":
			return "1"
		}
		return s
	}
	return norm(c.E) + ":" + norm(c.M) + ":" + norm(c.X)
}
