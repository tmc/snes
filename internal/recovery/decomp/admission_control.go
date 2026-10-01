package decomp

import (
	"fmt"
	"github.com/tmc/snes/internal/cpu"
)

func (v *EvidenceVerifier) verifiedInstruction(insn captureCPUInsn) ([]byte, error) {
	a := physicalCPU(insn.Entry)
	if !mappedPolicyROM(a) {
		return nil, fmt.Errorf("instruction fetch is not mapped LoROM at $%06X", a)
	}
	if insn.Entry.E {
		return nil, fmt.Errorf("routine emulation-mode control flow unsupported")
	}
	if len(insn.Fetches) != insn.Length || insn.Length < 1 || insn.Length > 4 || insn.Status != "retired" {
		return nil, fmt.Errorf("unsupported instruction fetch at $%06X", a)
	}
	b := make([]byte, insn.Length)
	for i, f := range insn.Fetches {
		expected := a&0xff0000 | uint32(uint16(a)+uint16(i))
		role := "operand"
		if i == 0 {
			role = "opcode"
		}
		if f.Addr != expected || f.Role != role {
			return nil, fmt.Errorf("instruction fetch address/role mismatch at $%06X", a)
		}
		b[i] = f.Value
		if len(v.policy.rom) > 0 {
			off := int((expected>>16&0x7f)*0x8000 + (expected & 0x7fff))
			if expected&0xffff < 0x8000 || off >= len(v.policy.rom) || v.policy.rom[off] != f.Value || f.ROMOffset != uint32(off) {
				return nil, fmt.Errorf("instruction fetch differs from pinned ROM at $%06X", expected)
			}
		}
	}
	n, err := calcInstructionSize(b[0], cpu.Opcodes[b[0]], insn.Entry.P&0x20 != 0, insn.Entry.P&0x10 != 0, a)
	if err != nil || n != len(b) {
		return nil, fmt.Errorf("instruction width mismatch at $%06X", a)
	}
	return b, nil
}

type verifiedCall struct {
	resume uint32
	opcode byte
	stack  uint16
}

func (v *EvidenceVerifier) verifyRoutineWindow(c *ReplayCase, contract RoutineContract, insns []captureCPUInsn, cd *captureData) ([]captureCPUInsn, error) {
	if len(insns) < 3 {
		return nil, fmt.Errorf("routine interval lacks call/body/continuation")
	}
	call := insns[0]
	if c.CallPC != physicalCPU(call.Entry) {
		return nil, fmt.Errorf("call PC metadata mismatch")
	}
	if c.ReturnInsnPC != physicalCPU(insns[len(insns)-2].Entry) {
		return nil, fmt.Errorf("return PC metadata mismatch")
	}
	continuation := insns[len(insns)-1]
	body := insns[1 : len(insns)-1]
	callBytes, err := v.verifiedInstruction(call)
	if err != nil {
		return nil, err
	}
	var outer *RoutineCall
	for i := range contract.Calls {
		x := &contract.Calls[i]
		if physicalCPU(call.Entry) == x.Address && callBytes[0] == x.Opcode {
			outer = x
			break
		}
	}
	if outer == nil || physicalTarget(call.SuccessorPC) != contract.Entry {
		return nil, fmt.Errorf("call instruction mismatch: not a reviewed call to $%06X", contract.Entry)
	}
	if physicalCPU(body[0].Entry) != contract.Entry {
		return nil, fmt.Errorf("routine entry PC mismatch")
	}
	if physicalCPU(continuation.Entry) != outer.Resume {
		return nil, fmt.Errorf("return instruction mismatch: continuation not reviewed")
	}
	frame, err := verifyCallInstruction(call, callBytes, cd.bus)
	if err != nil {
		return nil, err
	}
	if frame.resume != outer.Resume {
		return nil, fmt.Errorf("call continuation differs from contract")
	}
	stack := []verifiedCall{frame}
	for i, insn := range body {
		a := physicalCPU(insn.Entry)
		b, err := v.verifiedInstruction(insn)
		if err != nil {
			return nil, err
		}
		for j := range b {
			if !contractContains(contract, a&0xff0000|uint32(uint16(a)+uint16(j))) {
				return nil, fmt.Errorf("routine instruction out of range: $%06X at seq %d", a, insn.Seq)
			}
		}
		switch b[0] {
		case 0x20, 0x22:
			if !contractContains(contract, physicalTarget(insn.SuccessorPC)) {
				return nil, fmt.Errorf("routine call leaves reviewed closure")
			}
			f, err := verifyCallInstruction(insn, b, cd.bus)
			if err != nil {
				return nil, err
			}
			stack = append(stack, f)
		case 0x60, 0x6b:
			if len(stack) == 0 {
				return nil, fmt.Errorf("routine unmatched return")
			}
			f := stack[len(stack)-1]
			if err := verifyReturnInstruction(insn, b[0], f, cd.bus); err != nil {
				return nil, err
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				if i != len(body)-1 {
					return nil, fmt.Errorf("routine returns before final instruction")
				}
				allowed := false
				for _, r := range contract.Returns {
					if r.Address == a && r.Opcode == b[0] {
						allowed = true
					}
				}
				if !allowed {
					return nil, fmt.Errorf("exit instruction mismatch: not reviewed final return")
				}
			}
		default:
			if err := verifySuccessor(insn, b); err != nil {
				return nil, err
			}
			if !contractContains(contract, physicalTarget(insn.SuccessorPC)) {
				return nil, fmt.Errorf("routine successor leaves reviewed closure")
			}
		}
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("routine interval does not return")
	}
	lo, hi := call.Entry.Cycles, continuation.Exit.Cycles
	for _, tr := range cd.transitions {
		if tr.Cycle >= lo && tr.Cycle <= hi {
			return nil, fmt.Errorf("cpu_transition %s inside routine window at cycle %d", tr.Kind, tr.Cycle)
		}
	}
	for _, gap := range cd.gaps {
		if gap.FirstSeq <= c.ReturnSeq && gap.LastSeq >= c.CallSeq {
			return nil, fmt.Errorf("capture gap %d..%d overlaps routine window", gap.FirstSeq, gap.LastSeq)
		}
	}
	return body, nil
}

func verifyCallInstruction(insn captureCPUInsn, b []byte, events []busEvent) (verifiedCall, error) {
	a := physicalCPU(insn.Entry)
	n := len(b)
	f := verifiedCall{resume: a&0xff0000 | uint32(uint16(a)+uint16(n)), opcode: b[0], stack: insn.Entry.S}
	dest := a&0xff0000 | uint32(b[1]) | uint32(b[2])<<8
	saved := uint16(f.resume) - 1
	pushed := []byte{byte(saved >> 8), byte(saved)}
	if b[0] == 0x22 {
		dest = uint32(b[1]) | uint32(b[2])<<8 | uint32(b[3])<<16
		pushed = append([]byte{insn.Entry.PB}, pushed...)
	}
	if physicalTarget(insn.SuccessorPC) != dest || physicalCPU(insn.Exit) != dest || insn.Exit.S != insn.Entry.S-uint16(len(pushed)) {
		return f, fmt.Errorf("call instruction control/stack mismatch")
	}
	if err := verifyStackEvents(insn, events, "write", pushed, insn.Entry.S, false); err != nil {
		return f, err
	}
	return f, nil
}
func verifyReturnInstruction(insn captureCPUInsn, opcode byte, f verifiedCall, events []busEvent) error {
	want := byte(0x60)
	n := uint16(2)
	if f.opcode == 0x22 {
		want = 0x6b
		n = 3
	}
	if opcode != want || insn.Entry.S != f.stack-n || insn.Exit.S != f.stack || physicalTarget(insn.SuccessorPC) != f.resume || physicalCPU(insn.Exit) != f.resume {
		return fmt.Errorf("return instruction control/stack mismatch")
	}
	saved := uint16(f.resume) - 1
	pulls := []byte{byte(saved), byte(saved >> 8)}
	if want == 0x6b {
		pulls = append(pulls, byte(f.resume>>16))
	} else if insn.Entry.PB != byte(f.resume>>16) {
		return fmt.Errorf("RTS preserves program bank")
	}
	return verifyStackEvents(insn, events, "read", pulls, insn.Entry.S, true)
}
func verifyStackEvents(insn captureCPUInsn, events []busEvent, op string, values []byte, s uint16, pull bool) error {
	if len(insn.Fetches) == 0 {
		return fmt.Errorf("call/return missing instruction fetch")
	}
	var got []busEvent
	for _, e := range events {
		if e.Cycle > insn.Entry.Cycles && e.Cycle <= insn.Exit.Cycles && e.Op == op && e.Space == "wram" {
			got = append(got, e)
		}
	}
	if len(got) != len(values) {
		return fmt.Errorf("call/return stack bus coverage mismatch")
	}
	for i, want := range values {
		addr := uint16(s - uint16(i))
		if pull {
			addr = s + uint16(i) + 1
		}
		if addr >= 0x2000 {
			return fmt.Errorf("call/return non-WRAM stack unsupported")
		}
		e := got[i]
		if e.Actor != "cpu" || e.CPUPC != physicalCPU(insn.Entry) || e.CPUOpcode != insn.Fetches[0].Value {
			return fmt.Errorf("call/return stack bus actor mismatch")
		}
		value := e.Value
		if op == "write" && e.After != nil {
			value = e.After
		}
		if e.Addr != uint32(addr) || value == nil || *value != want {
			return fmt.Errorf("call/return stack bus value/address mismatch")
		}
	}
	return nil
}
func verifySuccessor(insn captureCPUInsn, b []byte) error {
	a := physicalCPU(insn.Entry)
	next := a&0xff0000 | uint32(uint16(a)+uint16(len(b)))
	want := next
	p := insn.Entry.P
	taken := false
	switch b[0] {
	case 0x10:
		taken = p&0x80 == 0
	case 0x30:
		taken = p&0x80 != 0
	case 0x50:
		taken = p&0x40 == 0
	case 0x70:
		taken = p&0x40 != 0
	case 0x90:
		taken = p&1 == 0
	case 0xb0:
		taken = p&1 != 0
	case 0xd0:
		taken = p&2 == 0
	case 0xf0:
		taken = p&2 != 0
	case 0x80:
		taken = true
	case 0x82:
		want = a&0xff0000 | uint32(uint16(next)+uint16(int16(uint16(b[1])|uint16(b[2])<<8)))
	case 0x4c:
		want = a&0xff0000 | uint32(b[1]) | uint32(b[2])<<8
	case 0x5c:
		want = uint32(b[1]) | uint32(b[2])<<8 | uint32(b[3])<<16
	case 0x00, 0x02, 0x40, 0x44, 0x54, 0x6c, 0x7c, 0xdc, 0xfc, 0xcb, 0xdb:
		return fmt.Errorf("unsupported routine control opcode $%02X", b[0])
	}
	if taken {
		want = a&0xff0000 | uint32(uint16(next)+uint16(int16(int8(b[1]))))
	}
	if physicalTarget(insn.SuccessorPC) != want || physicalCPU(insn.Exit) != want {
		return fmt.Errorf("instruction successor differs from fetched control flow at $%06X", a)
	}
	return nil
}
