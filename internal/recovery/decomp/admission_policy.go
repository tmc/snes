package decomp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// AdmissionPolicy contains operator-reviewed authority. Its digest binds the
// selected policy; it does not establish the correctness of the operator's review.
// ROM bytes are supplied separately and are never serialized here.
type AdmissionPolicy struct {
	Corpora  map[string]CorpusTrustRoot `json:"corpora"`
	Blocks   map[string][]uint32        `json:"blocks,omitempty"`
	Routines []RoutineContract          `json:"routines"`
	// NamedRegionBindings pins reviewed authored accessor profiles by routine ID.
	// It does not assert that a name has game-level semantic meaning.
	NamedRegionBindings map[string]string `json:"named_region_bindings,omitempty"`
}

// AddressRange is a half-open range of physical instruction bytes.
type AddressRange struct {
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
}

// RoutineCall identifies a reviewed outer call and its continuation.
type RoutineCall struct {
	Address uint32 `json:"address"`
	Resume  uint32 `json:"resume"`
	Opcode  byte   `json:"opcode"`
}

// RoutineReturn identifies an allowed final return instruction.
type RoutineReturn struct {
	Address uint32 `json:"address"`
	Opcode  byte   `json:"opcode"`
}

// RoutineContract bounds a routine's native-mode LoROM instruction closure.
// Producer bounds and names are never substituted for this reviewed contract.
type RoutineContract struct {
	ID             string             `json:"id"`
	Aliases        []string           `json:"aliases,omitempty"`
	Corpora        []string           `json:"corpora"`
	ROMSHA256      string             `json:"rom_sha256"`
	Entry          uint32             `json:"entry"`
	Calls          []RoutineCall      `json:"calls"`
	Returns        []RoutineReturn    `json:"returns"`
	Ranges         []AddressRange     `json:"ranges"`
	RefusalTargets []uint32           `json:"refusal_targets,omitempty"`
	Kind           string             `json:"kind,omitempty"`
	Dispatch       *DispatchContract  `json:"dispatch,omitempty"`
	Connected      *ConnectedContract `json:"connected,omitempty"`
}

// ConnectedContract bounds the discontiguous code spans and control/stack expectations
// for a connected routine closure.
type ConnectedContract struct {
	CallerPC               uint32              `json:"caller_pc"`
	CallerOpcode           byte                `json:"caller_opcode"`
	ContinuationPC         uint32              `json:"continuation_pc"`
	Spans                  []AddressRange      `json:"spans"`
	OuterJSRPC             uint32              `json:"outer_jsr_pc"`
	OuterJSRResume         uint32              `json:"outer_jsr_resume"`
	DispatcherCallPC       uint32              `json:"dispatcher_call_pc"`
	HelperEntryPC          uint32              `json:"helper_entry_pc"`
	HelperExitPC           uint32              `json:"helper_exit_pc"`
	AllowedIndirectTargets map[uint32][]uint32 `json:"allowed_indirect_targets"`
	HandlerEntryPC         uint32              `json:"handler_entry_pc"`
	HandlerReturnPC        uint32              `json:"handler_return_pc"`
	HandlerReturnPCs       []uint32            `json:"handler_return_pcs,omitempty"`
	TerminalReturnPC       uint32              `json:"terminal_return_pc"`
	ExpectedEntryS         uint16              `json:"expected_entry_s"`
	ExpectedReturnS        uint16              `json:"expected_return_s"`
	StackReturnBytes       []byte              `json:"stack_return_bytes"`
	EarlyReturnBranchPC    uint32              `json:"early_return_branch_pc"`
	Timer64PC              uint32              `json:"timer64_pc"`
	AllowedPathLengths     []int               `json:"allowed_path_lengths"`
}

// DispatchContract bounds the control flow predecessor, JumpTable lookup, and return semantics
// for an indirect or jump-table dispatched routine.
type DispatchContract struct {
	CallerPC         uint32 `json:"caller_pc,omitempty"`
	CallerOpcode     byte   `json:"caller_opcode,omitempty"`
	DispatcherPC     uint32 `json:"dispatcher_pc"`
	DispatcherCallPC uint32 `json:"dispatcher_call_pc,omitempty"`
	HelperEntryPC    uint32 `json:"helper_entry_pc,omitempty"`
	HelperExitPC     uint32 `json:"helper_exit_pc,omitempty"`
	HelperCount      int    `json:"helper_count,omitempty"`
	JumpTablePC      uint32 `json:"jump_table_pc"`
	SelectorAddress  uint32 `json:"selector_address,omitempty"`
	SelectorIndex    uint8  `json:"selector_index"`
	ContinuationPC   uint32 `json:"continuation_pc"`
	ExpectedEntryS   uint16 `json:"expected_entry_s"`
	ExpectedReturnS  uint16 `json:"expected_return_s"`
	StackReturnBytes []byte `json:"stack_return_bytes"`
	InstructionCount int    `json:"instruction_count"`
}

type admissionPolicy struct {
	roots         map[string]CorpusTrustRoot
	blocks        map[string][]uint32
	routines      map[string]RoutineContract
	namedBindings map[string]string
	digest        string
	rom           []byte
	legacy        bool
}

func policyDigest(x any) string {
	b, _ := json.Marshal(x)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func validPolicySHA(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && hex.EncodeToString(b) == s
}

// NewEvidenceVerifierWithPolicy validates and copies explicitly reviewed policy
// and ROM bytes. It never discovers authority from producer cases or ambient ROMs.
// A single bounded LoROM ROM is supported per verifier.
func NewEvidenceVerifierWithPolicy(root string, policy AdmissionPolicy, rom []byte) (*EvidenceVerifier, error) {
	if len(rom) == 0 || len(rom) > 4<<20 {
		return nil, fmt.Errorf("admission policy: ROM must contain 1..4194304 bytes")
	}
	owned := append([]byte(nil), rom...)
	sha := fmt.Sprintf("%x", sha256.Sum256(owned))
	p, err := copyAdmissionPolicy(policy, sha, true)
	if err != nil {
		return nil, err
	}
	p.rom = owned
	for _, c := range p.routines {
		if c.Kind == "dispatch_handler" && c.Dispatch != nil {
			disp := c.Dispatch
			target, err := readJumpTableTarget(owned, disp.JumpTablePC, disp.SelectorIndex)
			if err != nil {
				return nil, fmt.Errorf("admission policy: %w", err)
			}
			if target != c.Entry {
				return nil, fmt.Errorf("admission policy: jump table target $%06X != routine entry $%06X", target, c.Entry)
			}
			if disp.CallerPC != 0 {
				b0, err := romByte(owned, disp.CallerPC)
				if err != nil {
					return nil, fmt.Errorf("admission policy: caller ROM read: %w", err)
				}
				if disp.CallerOpcode != 0 && b0 != disp.CallerOpcode {
					return nil, fmt.Errorf("admission policy: caller opcode $%02X != $%02X", b0, disp.CallerOpcode)
				}
				if b0 == 0x20 {
					lo, _ := romByte(owned, disp.CallerPC+1)
					hi, _ := romByte(owned, disp.CallerPC+2)
					callTarget := uint32(lo) | (uint32(hi) << 8)
					if callTarget != (disp.DispatcherPC & 0xffff) {
						return nil, fmt.Errorf("admission policy: caller JSR target $%04X != dispatcher $%04X", callTarget, disp.DispatcherPC&0xffff)
					}
				}
			}
			if disp.DispatcherCallPC != 0 {
				b0, err := romByte(owned, disp.DispatcherCallPC)
				if err != nil {
					return nil, fmt.Errorf("admission policy: dispatcher call ROM read: %w", err)
				}
				if b0 != 0x22 {
					return nil, fmt.Errorf("admission policy: dispatcher call must be JSL ($22), got $%02X", b0)
				}
				if disp.HelperEntryPC != 0 {
					lo, _ := romByte(owned, disp.DispatcherCallPC+1)
					hi, _ := romByte(owned, disp.DispatcherCallPC+2)
					bank, _ := romByte(owned, disp.DispatcherCallPC+3)
					helperTarget := (uint32(bank) << 16) | (uint32(hi) << 8) | uint32(lo)
					if helperTarget != disp.HelperEntryPC {
						return nil, fmt.Errorf("admission policy: dispatcher JSL target $%06X != helper $%06X", helperTarget, disp.HelperEntryPC)
					}
				}
			}
			if disp.HelperExitPC != 0 {
				b0, err := romByte(owned, disp.HelperExitPC)
				if err != nil {
					return nil, fmt.Errorf("admission policy: helper exit ROM read: %w", err)
				}
				if b0 != 0xDC {
					return nil, fmt.Errorf("admission policy: helper exit must be JML [abs] ($DC), got $%02X", b0)
				}
			}
		}
		if c.Kind == "connected_routine" && c.Connected != nil {
			conn := c.Connected
			if conn.CallerPC != 0 {
				b0, err := romByte(owned, conn.CallerPC)
				if err != nil {
					return nil, fmt.Errorf("admission policy: connected caller ROM read: %w", err)
				}
				if conn.CallerOpcode != 0x20 || b0 != 0x20 {
					return nil, fmt.Errorf("admission policy: connected caller must be JSR ($20), got opcode $%02X, rom $%02X", conn.CallerOpcode, b0)
				}
				lo, err1 := romByte(owned, conn.CallerPC+1)
				hi, err2 := romByte(owned, conn.CallerPC+2)
				if err1 != nil || err2 != nil {
					return nil, fmt.Errorf("admission policy: connected caller JSR operand read: %v, %v", err1, err2)
				}
				callTarget := (conn.CallerPC & 0xff0000) | uint32(lo) | (uint32(hi) << 8)
				if callTarget != c.Entry {
					return nil, fmt.Errorf("admission policy: connected caller JSR target $%06X != entry $%06X", callTarget, c.Entry)
				}
				if conn.ContinuationPC != conn.CallerPC+3 {
					return nil, fmt.Errorf("admission policy: connected continuation PC $%06X != caller PC + 3", conn.ContinuationPC)
				}
			}
			if conn.OuterJSRPC != 0 {
				b0, err := romByte(owned, conn.OuterJSRPC)
				if err != nil {
					return nil, fmt.Errorf("admission policy: connected outer JSR ROM read: %w", err)
				}
				if b0 != 0x20 {
					return nil, fmt.Errorf("admission policy: connected outer JSR must be $20, got $%02X", b0)
				}
				lo, err1 := romByte(owned, conn.OuterJSRPC+1)
				hi, err2 := romByte(owned, conn.OuterJSRPC+2)
				if err1 != nil || err2 != nil {
					return nil, fmt.Errorf("admission policy: connected outer JSR operand read: %v, %v", err1, err2)
				}
				_ = uint16(lo) | (uint16(hi) << 8)
			}
			if conn.DispatcherCallPC != 0 {
				b0, err := romByte(owned, conn.DispatcherCallPC)
				if err != nil {
					return nil, fmt.Errorf("admission policy: connected dispatcher call ROM read: %w", err)
				}
				if b0 != 0x22 {
					return nil, fmt.Errorf("admission policy: connected dispatcher call must be JSL ($22), got $%02X", b0)
				}
				if conn.HelperEntryPC != 0 {
					lo, err1 := romByte(owned, conn.DispatcherCallPC+1)
					hi, err2 := romByte(owned, conn.DispatcherCallPC+2)
					bank, err3 := romByte(owned, conn.DispatcherCallPC+3)
					if err1 != nil || err2 != nil || err3 != nil {
						return nil, fmt.Errorf("admission policy: connected dispatcher JSL operand read: %v, %v, %v", err1, err2, err3)
					}
					helperTarget := (uint32(bank) << 16) | (uint32(hi) << 8) | uint32(lo)
					if helperTarget != conn.HelperEntryPC {
						return nil, fmt.Errorf("admission policy: connected dispatcher JSL target $%06X != helper $%06X", helperTarget, conn.HelperEntryPC)
					}
				}
			}
			if conn.HelperExitPC != 0 {
				b0, err := romByte(owned, conn.HelperExitPC)
				if err != nil {
					return nil, fmt.Errorf("admission policy: connected helper exit ROM read: %w", err)
				}
				if b0 != 0xDC {
					return nil, fmt.Errorf("admission policy: connected helper exit must be JML [abs] ($DC), got $%02X", b0)
				}
				lo, err1 := romByte(owned, conn.HelperExitPC+1)
				hi, err2 := romByte(owned, conn.HelperExitPC+2)
				if err1 != nil || err2 != nil {
					return nil, fmt.Errorf("admission policy: connected helper exit operand read: %v, %v", err1, err2)
				}
				_ = uint16(lo) | (uint16(hi) << 8)
			}
			if conn.TerminalReturnPC != 0 {
				b0, err := romByte(owned, conn.TerminalReturnPC)
				if err != nil {
					return nil, fmt.Errorf("admission policy: connected terminal return ROM read: %w", err)
				}
				if b0 != 0x60 {
					return nil, fmt.Errorf("admission policy: connected terminal return must be RTS ($60), got $%02X", b0)
				}
			}
			for _, pc := range connectedHandlerReturns(conn) {
				b0, err := romByte(owned, pc)
				if err != nil {
					return nil, fmt.Errorf("admission policy: connected handler return ROM read: %w", err)
				}
				if b0 != 0x60 {
					return nil, fmt.Errorf("admission policy: connected handler return must be RTS ($60), got $%02X", b0)
				}
			}
		}
	}
	v := newEvidenceVerifier(root)
	v.policy = p
	return v, nil
}

// PolicySHA256 identifies the immutable reviewed policy owned by the verifier.
func (v *EvidenceVerifier) PolicySHA256() string {
	if v == nil || v.policy == nil {
		return ""
	}
	return v.policy.digest
}

func copyAdmissionPolicy(policy AdmissionPolicy, romSHA string, strict bool) (*admissionPolicy, error) {
	b, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	var owned AdmissionPolicy
	if err = json.Unmarshal(b, &owned); err != nil {
		return nil, err
	}
	p := &admissionPolicy{roots: owned.Corpora, blocks: owned.Blocks, routines: make(map[string]RoutineContract), namedBindings: owned.NamedRegionBindings, digest: policyDigest(owned), legacy: !strict}
	if len(p.roots) > 1024 || len(owned.Routines) > 1024 || len(owned.Blocks) > 1024 || len(p.namedBindings) > 1024 {
		return nil, fmt.Errorf("admission policy: too many corpora or routines")
	}
	if strict && len(p.roots) == 0 {
		return nil, fmt.Errorf("admission policy: no reviewed corpora")
	}
	for name, r := range p.roots {
		if name == "" {
			return nil, fmt.Errorf("admission policy: empty corpus name")
		}
		if strict {
			for _, pin := range []string{r.ROMSHA256, r.FixtureSHA256, r.FixtureReceiptSHA256, r.FixtureSummarySHA256, r.DecompressedSHA, r.CaptureSHA256, r.CaptureReceiptSHA256, r.CaptureSummarySHA256, r.HistorySHA256, r.HistorySummarySHA256} {
				if !validPolicySHA(pin) {
					return nil, fmt.Errorf("admission policy: corpus %s has missing or malformed pin", name)
				}
			}
			if r.ROMSHA256 != romSHA {
				return nil, fmt.Errorf("admission policy: corpus %s ROM mismatch", name)
			}
			if r.Label == "" || r.EngineRevision == "" {
				return nil, fmt.Errorf("admission policy: corpus %s lacks label or engine identity", name)
			}
			for _, pin := range []string{r.FixtureReceiptSHA256, r.FixtureSummarySHA256, r.HistoryReceiptSHA256, r.CheckpointSHA256, r.InputsSHA256} {
				if pin != "" && !validPolicySHA(pin) {
					return nil, fmt.Errorf("admission policy: corpus %s malformed optional pin", name)
				}
			}
		}
	}
	for id, pcs := range owned.Blocks {
		if id == "" || len(pcs) == 0 || len(pcs) > 256 {
			return nil, fmt.Errorf("admission policy: invalid block instruction sequence")
		}
		seen := make(map[uint32]bool)
		for _, pc := range pcs {
			if !mappedPolicyROM(pc) || seen[pc] {
				return nil, fmt.Errorf("admission policy: invalid block %s instruction address", id)
			}
			seen[pc] = true
		}
	}
	for _, c := range owned.Routines {
		if len(c.Aliases) > 16 || len(c.Corpora) > 64 || len(c.Calls) > 64 || len(c.Returns) > 64 || len(c.Ranges) > 64 || len(c.RefusalTargets) > 64 {
			return nil, fmt.Errorf("admission policy: routine contract exceeds bounds")
		}
		if c.ID == "" || !validPolicySHA(c.ROMSHA256) || c.Entry > 0xffffff || len(c.Corpora) == 0 || (c.Kind != "dispatch_handler" && c.Kind != "connected_routine" && len(c.Calls) == 0) || len(c.Returns) == 0 || len(c.Ranges) == 0 {
			return nil, fmt.Errorf("admission policy: incomplete routine %s", c.ID)
		}
		if c.Kind == "dispatch_handler" {
			if c.Dispatch == nil {
				return nil, fmt.Errorf("admission policy: dispatch routine %s missing dispatch contract", c.ID)
			}
			d := c.Dispatch
			if !mappedPolicyROM(d.DispatcherPC) || !mappedPolicyROM(d.JumpTablePC) || !mappedPolicyROM(d.ContinuationPC) {
				return nil, fmt.Errorf("admission policy: dispatch routine %s has unmapped ROM address", c.ID)
			}
			if len(d.StackReturnBytes) < 2 {
				return nil, fmt.Errorf("admission policy: dispatch routine %s insufficient stack return bytes", c.ID)
			}
			if d.InstructionCount <= 0 {
				return nil, fmt.Errorf("admission policy: dispatch routine %s non-positive instruction count", c.ID)
			}
		}
		if strict && c.ROMSHA256 != romSHA {
			return nil, fmt.Errorf("admission policy: routine %s ROM mismatch", c.ID)
		}
		for _, name := range c.Corpora {
			r, ok := p.roots[name]
			if !ok || r.ROMSHA256 != c.ROMSHA256 {
				return nil, fmt.Errorf("admission policy: routine %s corpus mismatch", c.ID)
			}
		}
		for i, r := range c.Ranges {
			if r.Start >= r.End || r.End > 0x1000000 || !mappedPolicyROM(r.Start) || r.Start>>16 != (r.End-1)>>16 {
				return nil, fmt.Errorf("admission policy: routine %s invalid range", c.ID)
			}
			for _, prev := range c.Ranges[:i] {
				if r.Start < prev.End && prev.Start < r.End {
					return nil, fmt.Errorf("admission policy: routine %s overlapping ranges", c.ID)
				}
			}
		}
		if !contractContains(c, c.Entry) {
			return nil, fmt.Errorf("admission policy: entry outside closure")
		}
		if c.Kind == "dispatch_handler" {
			disp := c.Dispatch
			if disp == nil {
				return nil, fmt.Errorf("admission policy: dispatch handler %s missing dispatch contract", c.ID)
			}
			if !mappedPolicyROM(disp.DispatcherPC) || !mappedPolicyROM(disp.JumpTablePC) || !mappedPolicyROM(disp.ContinuationPC) {
				return nil, fmt.Errorf("admission policy: dispatch handler %s has unmapped addresses", c.ID)
			}
			if disp.CallerPC != 0 && !mappedPolicyROM(disp.CallerPC) {
				return nil, fmt.Errorf("admission policy: dispatch handler %s has unmapped caller PC", c.ID)
			}
			if disp.ExpectedReturnS != disp.ExpectedEntryS+2 {
				return nil, fmt.Errorf("admission policy: dispatch handler %s invalid stack delta", c.ID)
			}
			if len(disp.StackReturnBytes) != 2 {
				return nil, fmt.Errorf("admission policy: dispatch handler %s requires 2 stack return bytes", c.ID)
			}
			wantLow := uint8((disp.ContinuationPC - 1) & 0xFF)
			wantHigh := uint8(((disp.ContinuationPC - 1) >> 8) & 0xFF)
			if disp.StackReturnBytes[0] != wantLow || disp.StackReturnBytes[1] != wantHigh {
				return nil, fmt.Errorf("admission policy: dispatch handler %s stack return bytes mismatch", c.ID)
			}
		} else if c.Kind == "connected_routine" {
			if c.Connected == nil {
				return nil, fmt.Errorf("admission policy: connected routine %s missing connected contract", c.ID)
			}
			conn := c.Connected
			if !mappedPolicyROM(conn.CallerPC) || !mappedPolicyROM(conn.ContinuationPC) || !mappedPolicyROM(conn.TerminalReturnPC) {
				return nil, fmt.Errorf("admission policy: connected routine %s has unmapped addresses", c.ID)
			}
			if conn.CallerOpcode != 0x20 {
				return nil, fmt.Errorf("admission policy: connected routine %s requires JSR ($20) caller opcode", c.ID)
			}
			if conn.ContinuationPC != conn.CallerPC+3 {
				return nil, fmt.Errorf("admission policy: connected routine %s continuation must be caller+3", c.ID)
			}
			if conn.ExpectedReturnS != conn.ExpectedEntryS+2 {
				return nil, fmt.Errorf("admission policy: connected routine %s invalid stack delta", c.ID)
			}
			if len(conn.StackReturnBytes) != 2 {
				return nil, fmt.Errorf("admission policy: connected routine %s requires 2 stack return bytes", c.ID)
			}
			wantLow := uint8((conn.ContinuationPC - 1) & 0xFF)
			wantHigh := uint8(((conn.ContinuationPC - 1) >> 8) & 0xFF)
			if conn.StackReturnBytes[0] != wantLow || conn.StackReturnBytes[1] != wantHigh {
				return nil, fmt.Errorf("admission policy: connected routine %s stack return bytes mismatch", c.ID)
			}
			if len(conn.Spans) == 0 {
				return nil, fmt.Errorf("admission policy: connected routine %s missing spans", c.ID)
			}
			seenReturns := make(map[uint32]bool)
			for _, pc := range connectedHandlerReturns(conn) {
				if seenReturns[pc] || !mappedPolicyROM(pc) || !contractContains(c, pc) {
					return nil, fmt.Errorf("admission policy: connected routine %s invalid handler return $%06X", c.ID, pc)
				}
				seenReturns[pc] = true
			}
			totalBytes := 0
			for i, s := range conn.Spans {
				if s.Start >= s.End || s.End > 0x1000000 || !mappedPolicyROM(s.Start) || !mappedPolicyROM(s.End-1) || s.Start>>16 != (s.End-1)>>16 {
					return nil, fmt.Errorf("admission policy: connected routine %s span %d invalid", c.ID, i)
				}
				for _, prev := range conn.Spans[:i] {
					if s.Start < prev.End && prev.Start < s.End {
						return nil, fmt.Errorf("admission policy: connected routine %s overlapping spans", c.ID)
					}
				}
				totalBytes += int(s.End - s.Start)
			}
			if totalBytes > 65536 {
				return nil, fmt.Errorf("admission policy: connected routine %s spans exceed budget", c.ID)
			}
			if len(c.Ranges) > 0 {
				if len(c.Ranges) != len(conn.Spans) {
					return nil, fmt.Errorf("admission policy: connected routine %s ranges mismatch spans count", c.ID)
				}
				for i := range c.Ranges {
					if c.Ranges[i] != conn.Spans[i] {
						return nil, fmt.Errorf("admission policy: connected routine %s ranges mismatch spans at %d", c.ID, i)
					}
				}
			}
		} else {
			if len(c.Calls) == 0 {
				return nil, fmt.Errorf("admission policy: no reviewed outer calls")
			}
		}
		for _, call := range c.Calls {
			n := uint16(3)
			if call.Opcode == 0x22 {
				n = 4
			} else if call.Opcode != 0x20 {
				return nil, fmt.Errorf("admission policy: unsupported outer call")
			}
			if !mappedPolicyROM(call.Address) || call.Resume != call.Address&0xff0000|uint32(uint16(call.Address)+n) {
				return nil, fmt.Errorf("admission policy: invalid call continuation")
			}
		}
		for _, ret := range c.Returns {
			if !contractContains(c, ret.Address) || (ret.Opcode != 0x60 && ret.Opcode != 0x6b) {
				return nil, fmt.Errorf("admission policy: invalid final return")
			}
		}
		for _, id := range append([]string{c.ID}, c.Aliases...) {
			if id == "" {
				return nil, fmt.Errorf("admission policy: empty alias")
			}
			if _, ok := p.routines[id]; ok {
				return nil, fmt.Errorf("admission policy: duplicate routine %s", id)
			}
			p.routines[id] = c
		}
	}
	for id, hash := range p.namedBindings {
		if _, ok := p.routines[id]; !ok || !validPolicySHA(hash) {
			return nil, fmt.Errorf("admission policy: invalid named binding for routine %q", id)
		}
	}
	return p, nil
}

func mappedPolicyROM(a uint32) bool {
	return a <= 0xffffff && a&0xffff >= 0x8000 && a>>16 != 0x7e && a>>16 != 0x7f
}

func romByte(rom []byte, addr uint32) (byte, error) {
	if !mappedPolicyROM(addr) {
		return 0, fmt.Errorf("address $%06X is not mapped LoROM", addr)
	}
	bank := (addr >> 16) & 0x7f
	tableOffset := uint32(addr & 0xffff)
	romOff := int(bank*0x8000 + (tableOffset - 0x8000))
	if romOff < 0 || romOff >= len(rom) {
		return 0, fmt.Errorf("offset %d out of ROM bounds (len %d)", romOff, len(rom))
	}
	return rom[romOff], nil
}

func readJumpTableTarget(rom []byte, tablePC uint32, selector uint8) (uint32, error) {
	if !mappedPolicyROM(tablePC) {
		return 0, fmt.Errorf("jump table address $%06X is not mapped LoROM", tablePC)
	}
	bank := (tablePC >> 16) & 0x7f
	tableOffset := uint32(tablePC & 0xffff)
	targetAddr := tableOffset + uint32(selector)*2
	if targetAddr+1 > 0xffff {
		return 0, fmt.Errorf("jump table entry crosses bank boundary")
	}
	romOff := int(bank*0x8000 + (targetAddr - 0x8000))
	if romOff < 0 || romOff+1 >= len(rom) {
		return 0, fmt.Errorf("jump table offset %d out of ROM bounds (len %d)", romOff, len(rom))
	}
	low := uint32(rom[romOff])
	high := uint32(rom[romOff+1])
	targetWord := low | (high << 8)
	return (tablePC & 0xff0000) | targetWord, nil
}

func contractContains(c RoutineContract, a uint32) bool {
	for _, f := range c.RefusalTargets {
		if a == f {
			return false
		}
	}
	if c.Kind == "connected_routine" && c.Connected != nil {
		for _, s := range c.Connected.Spans {
			if a >= s.Start && a < s.End {
				return true
			}
		}
		return false
	}
	for _, r := range c.Ranges {
		if a >= r.Start && a < r.End {
			return true
		}
	}
	return false
}
func physicalCPU(s cpuStateWithCycles) uint32 { return uint32(s.PB)<<16 | uint32(s.PC) }
func physicalTarget(a targetPC) uint32        { return uint32(a.Bank)<<16 | uint32(a.Addr) }
func (v *EvidenceVerifier) routineContract(c *ReplayCase) (RoutineContract, error) {
	p, ok := v.policy.routines[c.RoutineID]
	if !ok {
		return p, fmt.Errorf("routine ID mismatch: case=%s", c.RoutineID)
	}
	if p.ROMSHA256 != c.ROMSHA256 {
		return p, fmt.Errorf("routine contract ROM mismatch")
	}
	for _, name := range p.Corpora {
		if c.Evidence.Corpus == name {
			return p, nil
		}
	}
	return p, fmt.Errorf("routine contract corpus mismatch")
}

func (v *EvidenceVerifier) evidencePolicyPins(ev *CaseEvidence, root CorpusTrustRoot) error {
	if v.policy.legacy {
		return nil
	}
	type pin struct {
		ref  *EvidenceFileRef
		want string
	}
	if ev.Checkpoint != nil && (root.CheckpointSHA256 == "" || ev.Checkpoint.SHA256 != root.CheckpointSHA256) {
		return fmt.Errorf("evidence contains unpinned checkpoint")
	}
	if ev.Checkpoint == nil && root.CheckpointSHA256 != "" {
		return fmt.Errorf("evidence lacks policy-pinned checkpoint")
	}
	refs := []pin{{ev.Fixture, root.FixtureSHA256}, {ev.Capture, root.CaptureSHA256}, {ev.History, root.HistorySHA256}, {ev.Inputs, root.InputsSHA256}}
	if ev.Fixture != nil {
		refs = append(refs, pin{ev.Fixture.Receipt, root.FixtureReceiptSHA256}, pin{ev.Fixture.Summary, root.FixtureSummarySHA256})
	}
	if ev.Capture != nil {
		refs = append(refs, pin{ev.Capture.Receipt, root.CaptureReceiptSHA256}, pin{ev.Capture.Summary, root.CaptureSummarySHA256})
	}
	if ev.History != nil {
		refs = append(refs, pin{ev.History.Receipt, root.HistoryReceiptSHA256}, pin{ev.History.Summary, root.HistorySummarySHA256})
	}
	for _, p := range refs {
		if p.ref != nil && (p.want == "" || p.ref.SHA256 != p.want) {
			return fmt.Errorf("evidence contains unpinned or conflicting metadata")
		}
		if p.ref == nil && p.want != "" {
			return fmt.Errorf("evidence lacks policy-pinned metadata")
		}
	}
	return nil
}

// connectedHandlerReturns includes the original singleton contract and explicit
// additional return sites. Each site is validated against the owned ROM.
func connectedHandlerReturns(c *ConnectedContract) []uint32 {
	pcs := append([]uint32(nil), c.HandlerReturnPCs...)
	if c.HandlerReturnPC != 0 {
		pcs = append(pcs, c.HandlerReturnPC)
	}
	return pcs
}

func connectedHandlerReturn(c *ConnectedContract, pc uint32) bool {
	if c.HandlerReturnPC == pc {
		return true
	}
	for _, p := range c.HandlerReturnPCs {
		if p == pc {
			return true
		}
	}
	return false
}
