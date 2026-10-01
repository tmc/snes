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
	if contract.Kind == "dispatch_handler" {
		disp := contract.Dispatch
		if disp == nil {
			return nil, fmt.Errorf("dispatch handler missing dispatch contract")
		}
		predCount := 1 + 2 + disp.HelperCount
		if len(insns) < predCount+disp.InstructionCount {
			return nil, fmt.Errorf("dispatch handler lacks required predecessor caller and helper instructions: got %d, want at least %d", len(insns), predCount+disp.InstructionCount)
		}
		if len(insns) != predCount+disp.InstructionCount && len(insns) != predCount+disp.InstructionCount+1 {
			return nil, fmt.Errorf("dispatch instruction count mismatch: got %d, want %d or %d", len(insns), predCount+disp.InstructionCount, predCount+disp.InstructionCount+1)
		}

		// 1. Caller JSR instruction:
		caller := insns[0]
		if physicalCPU(caller.Entry) != disp.CallerPC {
			return nil, fmt.Errorf("dispatch caller PC mismatch: got $%06X, want $%06X", physicalCPU(caller.Entry), disp.CallerPC)
		}
		if c.CallPC != disp.CallerPC {
			return nil, fmt.Errorf("dispatch call PC metadata mismatch: got $%06X, want $%06X", c.CallPC, disp.CallerPC)
		}
		callerBytes, err := v.verifiedInstruction(caller)
		if err != nil {
			return nil, fmt.Errorf("dispatch caller instruction verification: %w", err)
		}
		if disp.CallerOpcode != 0 && callerBytes[0] != disp.CallerOpcode {
			return nil, fmt.Errorf("dispatch caller opcode mismatch: got $%02X, want $%02X", callerBytes[0], disp.CallerOpcode)
		}
		if physicalTarget(caller.SuccessorPC) != disp.DispatcherPC {
			return nil, fmt.Errorf("dispatch caller successor mismatch: got $%06X, want $%06X", physicalTarget(caller.SuccessorPC), disp.DispatcherPC)
		}
		if caller.Exit.S != disp.ExpectedEntryS {
			return nil, fmt.Errorf("dispatch caller exit stack mismatch: got $%04X, want $%04X", caller.Exit.S, disp.ExpectedEntryS)
		}
		saved := uint16(disp.ContinuationPC) - 1
		pushed := []byte{byte(saved >> 8), byte(saved)}
		if err := verifyStackEvents(caller, cd.bus, "write", pushed, caller.Entry.S, false); err != nil {
			return nil, fmt.Errorf("dispatch caller stack write: %w", err)
		}

		// 2. Dispatcher LDA instruction:
		dispatcher := insns[1]
		if physicalCPU(dispatcher.Entry) != disp.DispatcherPC {
			return nil, fmt.Errorf("dispatch dispatcher PC mismatch: got $%06X, want $%06X", physicalCPU(dispatcher.Entry), disp.DispatcherPC)
		}
		if _, err := v.verifiedInstruction(dispatcher); err != nil {
			return nil, fmt.Errorf("dispatch dispatcher verification: %w", err)
		}
		if disp.DispatcherCallPC != 0 && physicalTarget(dispatcher.SuccessorPC) != disp.DispatcherCallPC {
			return nil, fmt.Errorf("dispatch dispatcher successor mismatch: got $%06X, want $%06X", physicalTarget(dispatcher.SuccessorPC), disp.DispatcherCallPC)
		}
		if disp.SelectorAddress != 0 {
			selReadFound := false
			selAddr := disp.SelectorAddress & 0xffff
			for _, e := range cd.bus {
				if e.Cycle > dispatcher.Entry.Cycles && e.Cycle <= dispatcher.Exit.Cycles && e.Space == "wram" && e.Addr == selAddr && e.Op == "read" {
					val := uint8(0)
					if e.Value != nil {
						val = *e.Value
					} else if e.After != nil {
						val = *e.After
					}
					if val != disp.SelectorIndex {
						return nil, fmt.Errorf("dispatch selector value mismatch: read %d, want %d", val, disp.SelectorIndex)
					}
					selReadFound = true
					break
				}
			}
			if !selReadFound {
				return nil, fmt.Errorf("dispatch selector bus read missing in dispatcher window")
			}
		}

		// 3. Dispatcher Call JSL instruction:
		dispCall := insns[2]
		if disp.DispatcherCallPC != 0 {
			if physicalCPU(dispCall.Entry) != disp.DispatcherCallPC {
				return nil, fmt.Errorf("dispatch dispatcher_call PC mismatch: got $%06X, want $%06X", physicalCPU(dispCall.Entry), disp.DispatcherCallPC)
			}
			dispCallBytes, err := v.verifiedInstruction(dispCall)
			if err != nil {
				return nil, fmt.Errorf("dispatch dispatcher_call verification: %w", err)
			}
			if dispCallBytes[0] != 0x22 {
				return nil, fmt.Errorf("dispatch dispatcher_call opcode must be JSL ($22), got $%02X", dispCallBytes[0])
			}
			if disp.HelperEntryPC != 0 && physicalTarget(dispCall.SuccessorPC) != disp.HelperEntryPC {
				return nil, fmt.Errorf("dispatch helper entry mismatch: got $%06X, want $%06X", physicalTarget(dispCall.SuccessorPC), disp.HelperEntryPC)
			}
		}

		// 4. Helper instructions:
		if disp.HelperCount > 0 {
			helper := insns[3 : 3+disp.HelperCount]
			if disp.HelperEntryPC != 0 && physicalCPU(helper[0].Entry) != disp.HelperEntryPC {
				return nil, fmt.Errorf("dispatch helper entry PC mismatch: got $%06X, want $%06X", physicalCPU(helper[0].Entry), disp.HelperEntryPC)
			}
			for i, h := range helper {
				if _, err := v.verifiedInstruction(h); err != nil {
					return nil, fmt.Errorf("dispatch helper instruction %d at seq %d verification: %w", i, h.Seq, err)
				}
			}
			for i := 0; i < len(helper)-1; i++ {
				if !cpuStateEqualWithCycles(helper[i].Exit, helper[i+1].Entry) {
					return nil, fmt.Errorf("dispatch helper CPU continuity break at seq %d", helper[i].Seq)
				}
			}
			if err := v.verifyHelperBus(insns[2:3+disp.HelperCount], cd.helperBus); err != nil {
				return nil, err
			}
			termHelper := helper[len(helper)-1]
			if disp.HelperExitPC != 0 && physicalCPU(termHelper.Entry) != disp.HelperExitPC {
				return nil, fmt.Errorf("dispatch helper exit PC mismatch: got $%06X, want $%06X", physicalCPU(termHelper.Entry), disp.HelperExitPC)
			}
			termHelperBytes, err := v.verifiedInstruction(termHelper)
			if err != nil {
				return nil, fmt.Errorf("dispatch helper terminal instruction verification: %w", err)
			}
			if termHelperBytes[0] != 0xDC {
				return nil, fmt.Errorf("dispatch helper terminal opcode must be JML [abs] ($DC), got $%02X", termHelperBytes[0])
			}
			if physicalTarget(termHelper.SuccessorPC) != contract.Entry {
				return nil, fmt.Errorf("dispatch helper JML target mismatch: got $%06X, want $%06X", physicalTarget(termHelper.SuccessorPC), contract.Entry)
			}
		}

		// 5. Handler body instructions:
		body := insns[predCount : predCount+disp.InstructionCount]
		var continuation *captureCPUInsn
		if len(insns) == predCount+disp.InstructionCount+1 {
			cont := insns[len(insns)-1]
			continuation = &cont
			if physicalCPU(continuation.Entry) != disp.ContinuationPC {
				return nil, fmt.Errorf("dispatch continuation PC mismatch: got $%06X, want $%06X", physicalCPU(continuation.Entry), disp.ContinuationPC)
			}
			if continuation.Entry.S != disp.ExpectedReturnS {
				return nil, fmt.Errorf("dispatch return stack mismatch: got $%04X, want $%04X", continuation.Entry.S, disp.ExpectedReturnS)
			}
		}

		lastInsn := body[len(body)-1]
		if c.ReturnInsnPC != physicalCPU(lastInsn.Entry) {
			return nil, fmt.Errorf("return PC metadata mismatch")
		}
		if physicalCPU(body[0].Entry) != contract.Entry {
			return nil, fmt.Errorf("routine entry PC mismatch")
		}
		if body[0].Entry.S != disp.ExpectedEntryS {
			return nil, fmt.Errorf("dispatch entry stack mismatch: got $%04X, want $%04X", body[0].Entry.S, disp.ExpectedEntryS)
		}
		if physicalTarget(lastInsn.SuccessorPC) != disp.ContinuationPC || physicalCPU(lastInsn.Exit) != disp.ContinuationPC {
			return nil, fmt.Errorf("dispatch continuation PC mismatch: got $%06X, want $%06X", physicalCPU(lastInsn.Exit), disp.ContinuationPC)
		}
		if lastInsn.Exit.S != disp.ExpectedReturnS {
			return nil, fmt.Errorf("dispatch return stack mismatch: got $%04X, want $%04X", lastInsn.Exit.S, disp.ExpectedReturnS)
		}
		if len(v.policy.rom) > 0 {
			target, err := readJumpTableTarget(v.policy.rom, disp.JumpTablePC, disp.SelectorIndex)
			if err != nil {
				return nil, fmt.Errorf("dispatch jump table read: %w", err)
			}
			if target != contract.Entry {
				return nil, fmt.Errorf("dispatch jump table target mismatch: got $%06X, want $%06X", target, contract.Entry)
			}
		}
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
			if i == len(body)-1 {
				if b[0] != 0x60 {
					return nil, fmt.Errorf("dispatch terminal return must be RTS ($60), got $%02X", b[0])
				}
				if insn.Exit.S != disp.ExpectedReturnS || physicalTarget(insn.SuccessorPC) != disp.ContinuationPC || physicalCPU(insn.Exit) != disp.ContinuationPC {
					return nil, fmt.Errorf("dispatch return control/stack mismatch")
				}
				if err := verifyStackEvents(insn, cd.bus, "read", disp.StackReturnBytes, insn.Entry.S, true); err != nil {
					return nil, fmt.Errorf("dispatch return stack read: %w", err)
				}
			} else {
				switch b[0] {
				case 0x20, 0x22:
					return nil, fmt.Errorf("subroutine calls within dispatch handler not supported")
				case 0x60, 0x6b:
					return nil, fmt.Errorf("premature return in dispatch handler at seq %d", insn.Seq)
				default:
					if err := verifySuccessor(insn, b); err != nil {
						return nil, err
					}
					if !contractContains(contract, physicalTarget(insn.SuccessorPC)) {
						return nil, fmt.Errorf("routine successor leaves reviewed closure")
					}
				}
			}
		}
		lo := insns[0].Entry.Cycles
		hi := body[len(body)-1].Exit.Cycles
		if continuation != nil {
			hi = continuation.Exit.Cycles
		}
		for _, tr := range cd.transitions {
			if tr.Cycle >= lo && tr.Cycle <= hi {
				return nil, fmt.Errorf("cpu_transition %s inside routine window at cycle %d", tr.Kind, tr.Cycle)
			}
		}
		maxSeq := c.ReturnSeq
		if maxSeq == 0 {
			maxSeq = c.ExitSeq
		}
		for _, gap := range cd.gaps {
			if gap.FirstSeq <= maxSeq && gap.LastSeq >= c.CallSeq {
				return nil, fmt.Errorf("capture gap %d..%d overlaps routine window", gap.FirstSeq, gap.LastSeq)
			}
		}
		return body, nil
	}
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
