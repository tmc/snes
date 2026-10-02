package decomp

import (
	"bytes"
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
	if contract.Kind == "connected_routine" {
		conn := contract.Connected
		if conn == nil {
			return nil, fmt.Errorf("connected routine missing connected contract")
		}
		pathAllowed := false
		for _, l := range conn.AllowedPathLengths {
			if len(insns) == l {
				pathAllowed = true
				break
			}
		}
		if !pathAllowed {
			return nil, fmt.Errorf("connected routine invalid instruction count: %d", len(insns))
		}

		// 1. Entry and outer caller boundary verification
		entryInsn := insns[0]
		if physicalCPU(entryInsn.Entry) != contract.Entry {
			return nil, fmt.Errorf("connected routine entry PC mismatch: got $%06X, want $%06X", physicalCPU(entryInsn.Entry), contract.Entry)
		}
		if entryInsn.Entry.S != conn.ExpectedEntryS {
			return nil, fmt.Errorf("connected routine entry stack mismatch: got $%04X, want $%04X", entryInsn.Entry.S, conn.ExpectedEntryS)
		}
		if c.CallSeq != 0 {
			return nil, fmt.Errorf("connected routine CallSeq must be 0 (unobserved), got %d", c.CallSeq)
		}
		if c.CallPC != conn.CallerPC {
			return nil, fmt.Errorf("connected routine CallPC $%06X != contract caller $%06X", c.CallPC, conn.CallerPC)
		}
		hist, err := v.loadHistory(c.Evidence.History.Path)
		if err != nil {
			return nil, fmt.Errorf("connected routine history load: %w", err)
		}
		addrLow := uint32(0x7E0000) | uint32(conn.ExpectedEntryS+1)
		addrHigh := uint32(0x7E0000) | uint32(conn.ExpectedEntryS+2)
		entryCycles := entryInsn.Entry.Cycles
		wLow := latestConnectedWrite(hist[addrLow], entryCycles)
		wHigh := latestConnectedWrite(hist[addrHigh], entryCycles)
		if wLow == nil || wHigh == nil {
			return nil, fmt.Errorf("connected routine missing caller stack writes before entry")
		}
		if wLow.Value != conn.StackReturnBytes[0] || wHigh.Value != conn.StackReturnBytes[1] {
			return nil, fmt.Errorf("connected routine caller stack write values mismatch")
		}
		if !connectedHistoryOrder(*wHigh, *wLow) {
			return nil, fmt.Errorf("connected routine caller stack write order mismatch")
		}
		if wHigh.Actor != "cpu" || wLow.Actor != "cpu" {
			return nil, fmt.Errorf("connected routine caller stack actor not CPU")
		}
		callerBank := byte(conn.CallerPC >> 16)
		callerPC16 := uint16(conn.CallerPC & 0xFFFF)
		if wHigh.CPUPBR != callerBank || wHigh.CPUPC != callerPC16 || wLow.CPUPBR != callerBank || wLow.CPUPC != callerPC16 {
			return nil, fmt.Errorf("connected routine caller PC mismatch in history")
		}
		if wHigh.CPUOpcode != 0x20 || wLow.CPUOpcode != 0x20 {
			return nil, fmt.Errorf("connected routine caller opcode mismatch in history")
		}
		if wHigh.CPUS != conn.ExpectedEntryS+2 || wLow.CPUS != conn.ExpectedEntryS+2 {
			return nil, fmt.Errorf("connected routine caller S mismatch in history")
		}
		callerBytes := make([]byte, 3)
		for i := range callerBytes {
			b, err := romByte(v.policy.rom, conn.CallerPC&0xff0000|uint32(uint16(conn.CallerPC)+uint16(i)))
			if err != nil {
				return nil, fmt.Errorf("connected routine caller ROM: %w", err)
			}
			callerBytes[i] = b
		}
		if !connectedCallerBytes(*wHigh, *wLow, callerBytes) {
			return nil, fmt.Errorf("connected routine caller bytes mismatch in history")
		}
		if !connectedHistoryContextMatches(*wHigh, entryInsn.Entry) || !connectedHistoryContextMatches(*wLow, entryInsn.Entry) {
			return nil, fmt.Errorf("connected routine caller context mismatch with entry instruction")
		}

		// 2. Terminal return verification
		lastInsn := insns[len(insns)-1]
		if err := verifyConnectedReturnMetadata(c, lastInsn, conn.TerminalReturnPC); err != nil {
			return nil, err
		}
		if physicalCPU(lastInsn.Entry) != conn.TerminalReturnPC {
			return nil, fmt.Errorf("connected routine terminal return PC mismatch: got $%06X, want $%06X", physicalCPU(lastInsn.Entry), conn.TerminalReturnPC)
		}
		termBytes, err := v.verifiedInstruction(lastInsn)
		if err != nil {
			return nil, fmt.Errorf("connected routine terminal instruction verification: %w", err)
		}
		if termBytes[0] != 0x60 {
			return nil, fmt.Errorf("connected routine terminal return must be RTS ($60), got $%02X", termBytes[0])
		}
		if lastInsn.Entry.S != conn.ExpectedEntryS || lastInsn.Exit.S != conn.ExpectedReturnS {
			return nil, fmt.Errorf("connected routine terminal return stack mismatch")
		}
		if physicalTarget(lastInsn.SuccessorPC) != conn.ContinuationPC || physicalCPU(lastInsn.Exit) != conn.ContinuationPC {
			return nil, fmt.Errorf("connected routine continuation mismatch")
		}
		if err := verifyStackEvents(lastInsn, cd.bus, "read", conn.StackReturnBytes, lastInsn.Entry.S, true); err != nil {
			return nil, fmt.Errorf("connected routine terminal return stack read: %w", err)
		}

		// 3. Verify all instructions and control flow
		dispatcherResume := conn.DispatcherCallPC&0xff0000 | uint32(uint16(conn.DispatcherCallPC)+4)
		for i, insn := range insns {
			if i > 0 {
				prev := insns[i-1]
				if !connectedContinuity(prev.Exit, insn.Entry) {
					return nil, fmt.Errorf("connected routine CPU continuity break at seq %d -> %d", prev.Seq, insn.Seq)
				}
			}

			a := physicalCPU(insn.Entry)
			b, err := v.verifiedInstruction(insn)
			if err != nil {
				return nil, err
			}
			for j := range b {
				if !contractContains(contract, a&0xff0000|uint32(uint16(a)+uint16(j))) {
					return nil, fmt.Errorf("connected routine instruction out of range: $%06X at seq %d", a, insn.Seq)
				}
			}

			if i == len(insns)-1 {
				// Terminal return already verified
				continue
			}

			if a == conn.OuterJSRPC { // 0x0CC43F JSR $C448
				if b[0] != 0x20 {
					return nil, fmt.Errorf("outer JSR opcode must be $20, got $%02X", b[0])
				}
				target := uint32(insn.Entry.PB)<<16 | uint32(b[1]) | (uint32(b[2]) << 8)
				if physicalTarget(insn.SuccessorPC) != target || physicalCPU(insn.Exit) != target {
					return nil, fmt.Errorf("outer JSR successor mismatch: got $%06X, want $%06X", physicalTarget(insn.SuccessorPC), target)
				}
				if insn.Exit.S != insn.Entry.S-2 {
					return nil, fmt.Errorf("outer JSR stack delta mismatch: got $%04X, want $%04X", insn.Exit.S, insn.Entry.S-2)
				}
				saved := uint16(conn.OuterJSRResume) - 1
				pushed := []byte{byte(saved >> 8), byte(saved)}
				if err := verifyStackEvents(insn, cd.bus, "write", pushed, insn.Entry.S, false); err != nil {
					return nil, fmt.Errorf("outer JSR stack write: %w", err)
				}
			} else if a == conn.DispatcherCallPC { // 0x0CC44B JSL $008781
				if b[0] != 0x22 {
					return nil, fmt.Errorf("dispatcher call opcode must be JSL ($22), got $%02X", b[0])
				}
				target := uint32(b[3])<<16 | uint32(b[1]) | (uint32(b[2]) << 8)
				if target != conn.HelperEntryPC {
					return nil, fmt.Errorf("dispatcher call target mismatch: got $%06X, want $%06X", target, conn.HelperEntryPC)
				}
				if physicalTarget(insn.SuccessorPC) != target || physicalCPU(insn.Exit) != target {
					return nil, fmt.Errorf("dispatcher call successor mismatch: got $%06X, want $%06X", physicalTarget(insn.SuccessorPC), target)
				}
				if insn.Exit.S != insn.Entry.S-3 {
					return nil, fmt.Errorf("dispatcher call stack delta mismatch: got $%04X, want $%04X", insn.Exit.S, insn.Entry.S-3)
				}
				saved := uint16(dispatcherResume) - 1 // 0xC44E
				pushed := []byte{insn.Entry.PB, byte(saved >> 8), byte(saved)}
				if err := verifyStackEvents(insn, cd.bus, "write", pushed, insn.Entry.S, false); err != nil {
					return nil, fmt.Errorf("dispatcher call JSL stack write: %w", err)
				}
				if i+16 <= len(insns) {
					if err := v.verifyHelperBus(insns[i:i+16], cd.helperBus); err != nil {
						return nil, fmt.Errorf("connected helper bus verification: %w", err)
					}
				}
			} else if a == 0x008783 { // PLY (8-bit: pulls return-low)
				if b[0] != 0x7A {
					return nil, fmt.Errorf("helper pull low opcode must be PLY ($7A), got $%02X", b[0])
				}
				next := a&0xff0000 | uint32(uint16(a)+uint16(len(b)))
				if physicalTarget(insn.SuccessorPC) != next || physicalCPU(insn.Exit) != next {
					return nil, fmt.Errorf("helper PLY successor mismatch: got $%06X, want $%06X", physicalTarget(insn.SuccessorPC), next)
				}
				if insn.Exit.S != insn.Entry.S+1 {
					return nil, fmt.Errorf("helper PLY stack delta mismatch: got $%04X, want $%04X", insn.Exit.S, insn.Entry.S+1)
				}
				saved := uint16(dispatcherResume) - 1
				pulls := []byte{byte(saved)}
				if err := verifyStackEvents(insn, cd.bus, "read", pulls, insn.Entry.S, true); err != nil {
					return nil, fmt.Errorf("helper PLY stack read: %w", err)
				}
			} else if a == 0x00878D { // PLA (16-bit: pulls return-high + bank)
				if b[0] != 0x68 {
					return nil, fmt.Errorf("helper pull high opcode must be PLA ($68), got $%02X", b[0])
				}
				next := a&0xff0000 | uint32(uint16(a)+uint16(len(b)))
				if physicalTarget(insn.SuccessorPC) != next || physicalCPU(insn.Exit) != next {
					return nil, fmt.Errorf("helper PLA successor mismatch: got $%06X, want $%06X", physicalTarget(insn.SuccessorPC), next)
				}
				if insn.Exit.S != insn.Entry.S+2 {
					return nil, fmt.Errorf("helper PLA stack delta mismatch: got $%04X, want $%04X", insn.Exit.S, insn.Entry.S+2)
				}
				saved := uint16(dispatcherResume) - 1
				pulls := []byte{byte(saved >> 8), byte(dispatcherResume >> 16)}
				if err := verifyStackEvents(insn, cd.bus, "read", pulls, insn.Entry.S, true); err != nil {
					return nil, fmt.Errorf("helper PLA stack read: %w", err)
				}
			} else if a == conn.HelperExitPC { // 0x008799 JML [$0000]
				if b[0] != 0xDC {
					return nil, fmt.Errorf("helper exit opcode must be JML [abs] ($DC), got $%02X", b[0])
				}
				target := physicalTarget(insn.SuccessorPC)
				if physicalCPU(insn.Exit) != target {
					return nil, fmt.Errorf("helper JML exit mismatch: got $%06X, want $%06X", physicalCPU(insn.Exit), target)
				}
				if insn.Exit.S != insn.Entry.S {
					return nil, fmt.Errorf("helper JML stack delta mismatch: got $%04X, want $%04X", insn.Exit.S, insn.Entry.S)
				}
				allowed := false
				for _, t := range conn.AllowedIndirectTargets[a] {
					if target == t {
						allowed = true
						break
					}
				}
				if !allowed {
					return nil, fmt.Errorf("helper JML target $%06X not in allowlist", target)
				}
			} else if connectedHandlerReturn(conn, a) { // reviewed handler RTS
				if b[0] != 0x60 {
					return nil, fmt.Errorf("handler return opcode must be RTS ($60), got $%02X", b[0])
				}
				if physicalTarget(insn.SuccessorPC) != conn.OuterJSRResume || physicalCPU(insn.Exit) != conn.OuterJSRResume {
					return nil, fmt.Errorf("handler RTS successor mismatch: got $%06X, want $%06X", physicalTarget(insn.SuccessorPC), conn.OuterJSRResume)
				}
				if insn.Exit.S != insn.Entry.S+2 {
					return nil, fmt.Errorf("handler RTS stack delta mismatch: got $%04X, want $%04X", insn.Exit.S, insn.Entry.S+2)
				}
				saved := uint16(conn.OuterJSRResume) - 1
				pulls := []byte{byte(saved), byte(saved >> 8)}
				if err := verifyStackEvents(insn, cd.bus, "read", pulls, insn.Entry.S, true); err != nil {
					return nil, fmt.Errorf("handler RTS stack read: %w", err)
				}
			} else {
				if err := verifySuccessor(insn, b); err != nil {
					return nil, err
				}
				if !contractContains(contract, physicalTarget(insn.SuccessorPC)) {
					return nil, fmt.Errorf("routine successor leaves reviewed closure at $%06X", a)
				}
			}
		}

		// 4. Transitions and gaps check
		lo := insns[0].Entry.Cycles
		hi := lastInsn.Exit.Cycles
		for _, tr := range cd.transitions {
			if tr.Cycle >= lo && tr.Cycle <= hi {
				return nil, fmt.Errorf("cpu_transition %s inside routine window at cycle %d", tr.Kind, tr.Cycle)
			}
		}
		maxSeq := c.ReturnSeq
		if maxSeq == 0 {
			maxSeq = c.ExitSeq
		}
		minSeq := c.CallSeq
		if minSeq == 0 {
			minSeq = c.EntrySeq
		}
		for _, gap := range cd.gaps {
			if gap.FirstSeq <= maxSeq && gap.LastSeq >= minSeq {
				return nil, fmt.Errorf("capture gap %d..%d overlaps routine window", gap.FirstSeq, gap.LastSeq)
			}
		}

		return insns, nil
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

func connectedHistoryContextMatches(w historyWrite, e cpuStateWithCycles) bool {
	return w.CPUA == e.A && w.CPUX == e.X && w.CPUY == e.Y && w.CPUD == e.D && w.CPUDB == e.DB && w.CPUP == e.P && w.CPUE == e.E
}

func connectedHistoryOrder(high, low historyWrite) bool {
	return high.Cycle <= low.Cycle && high.ID < low.ID
}

func connectedCallerBytes(high, low historyWrite, rom []byte) bool {
	return bytes.Equal(high.CPUBytes, rom) && bytes.Equal(low.CPUBytes, rom)
}

func connectedContinuity(exit, entry cpuStateWithCycles) bool {
	return exit.Cycles == entry.Cycles && cpuStateEqualWithCycles(exit, entry)
}

func latestConnectedWrite(writes []historyWrite, cutoff uint64) *historyWrite {
	for i := len(writes) - 1; i >= 0; i-- {
		if writes[i].Cycle <= cutoff {
			return &writes[i]
		}
	}
	return nil
}

func verifyConnectedReturnMetadata(c *ReplayCase, last captureCPUInsn, terminalPC uint32) error {
	if c.ExitSeq != last.Seq {
		return fmt.Errorf("connected routine terminal sequence mismatch: got %d, want %d", c.ExitSeq, last.Seq)
	}
	if c.ReturnInsnPC != terminalPC || c.ReturnInsnPC != physicalCPU(last.Entry) {
		return fmt.Errorf("connected routine declared return PC mismatch: got $%06X, captured $%06X, contract $%06X", c.ReturnInsnPC, physicalCPU(last.Entry), terminalPC)
	}
	// This boundary is the observed terminal RTS exit. The connected capture does
	// not verify a continuation instruction, so no sequence may claim one.
	if c.ReturnSeq != 0 {
		return fmt.Errorf("connected routine continuation sequence is unsupported: got %d, want 0", c.ReturnSeq)
	}
	return nil
}
