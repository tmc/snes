package gsu

const (
	stepSlicePhaseFMULTWait    = 1
	stepSlicePhaseIWTFetchLow  = 2
	stepSlicePhaseIWTFetchHigh = 3
	stepSlicePhaseSTWWaitHigh  = 4
	stepSlicePhaseSTBWaitWrite = 5
	stepSlicePhaseGETBWaitRead = 6
	stepSlicePhaseGETCWaitRead = 7
	stepSlicePhaseROMBWaitSet  = 8
	stepSlicePhaseRAMBWaitSet  = 9
	stepSlicePhaseLDBWaitRead  = 10
	stepSlicePhaseLDWWaitRead  = 11
	stepSlicePhaseLMWaitRead   = 12
	stepSlicePhaseSBKWaitHigh  = 13
	stepSlicePhaseLMSFetchAddr = 14
	stepSlicePhaseLMSWaitRead  = 15
	stepSlicePhaseSMSFetchAddr = 16
	stepSlicePhaseSMSWaitHigh  = 17
	stepSlicePhaseSMWaitHigh   = 18
	stepSlicePhaseSTWWaitLow   = 19
	stepSlicePhaseSMSWaitLow   = 20
	stepSlicePhaseSMWaitLow    = 21
	stepSlicePhaseSBKWaitLow   = 22
)

// StepSliceResult reports the work completed by StepSlice.
type StepSliceResult struct {
	Cycles         uint64
	RetiredOpcodes int
	Running        bool
	Partial        bool
}

// StepSlice advances a bounded slice of a currently running GSU opcode.
//
// The current implementation slices plain FMULT's deterministic multiply wait,
// plain IWT's two operand-byte fetches, plain STW's inter-byte RAM write wait,
// plain STB's pending RAM-buffer sync before staging its byte, GETB/GETC's
// pending ROM-buffer sync before returning the data byte, ROMB's pending
// ROM-buffer sync before changing ROMBR, RAMB's pending RAM-buffer sync before
// changing RAMBR, LDB's pending RAM-buffer sync before returning the data
// byte, LDW's pending RAM-buffer sync before returning the word, bounded
// STW's pending RAM-buffer sync before staging its low byte, ALT1/ALT3 LM's
// immediate operand progress plus pending RAM-buffer sync before returning the
// word, ALT1/ALT3 LMS's immediate operand progress plus pending RAM-buffer sync
// before returning the word, plain SBK's pending-buffer and inter-byte waits, and plain
// SMS/SM's immediate operand progress plus pending RAM-buffer sync before the
// low byte and inter-byte RAM write wait.
// Unsupported opcodes and unsupported boundaries make no progress.
func (d *Device) StepSlice(masterCycles uint64) StepSliceResult {
	result := d.stepSliceResult(0, 0)
	if masterCycles == 0 || !d.Running() {
		return result
	}

	start := d.cycles
	if d.stepSlice.Active {
		retired := d.advanceStepSliceFrame(masterCycles)
		return d.stepSliceResult(d.cycles-start, retired)
	}

	if !d.canStartFMULTStepSlice() &&
		!d.canStartIWTStepSlice() &&
		!d.canStartSTWStepSlice() &&
		!d.canStartSTBStepSlice() &&
		!d.canStartLDWStepSlice() &&
		!d.canStartLDBStepSlice() &&
		!d.canStartLMStepSlice() &&
		!d.canStartLMSStepSlice() &&
		!d.canStartSMSStepSlice() &&
		!d.canStartSMStepSlice() &&
		!d.canStartGETBStepSlice() &&
		!d.canStartGETCStepSlice() &&
		!d.canStartROMBStepSlice() &&
		!d.canStartRAMBStepSlice() &&
		!d.canStartSBKStepSlice() {
		return result
	}
	dispatchCycles := d.nextOpcodeFetchCycles()
	if masterCycles < dispatchCycles {
		return result
	}
	if !d.startStepSliceFrame() {
		return result
	}
	spent := d.cycles - start
	if spent >= masterCycles {
		return d.stepSliceResult(spent, 0)
	}
	retired := d.advanceStepSliceFrame(masterCycles - spent)
	return d.stepSliceResult(d.cycles-start, retired)
}

func (d *Device) stepSliceResult(cycles uint64, retired int) StepSliceResult {
	return StepSliceResult{
		Cycles:         cycles,
		RetiredOpcodes: retired,
		Running:        d.Running(),
		Partial:        d.stepSlice.Active,
	}
}

func (d *Device) canStartFMULTStepSlice() bool {
	return d.Pipeline == 0x9f &&
		d.alt() == AltNone &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix
}

func (d *Device) canStartIWTStepSlice() bool {
	return d.Pipeline >= 0xf0 &&
		d.Pipeline <= 0xff &&
		d.alt() == AltNone &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix
}

func (d *Device) canStartLMStepSlice() bool {
	mode := d.alt()
	return d.Pipeline >= 0xf0 &&
		d.Pipeline <= 0xff &&
		(mode == Alt1 || mode == Alt3) &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix
}

func (d *Device) canStartLMSStepSlice() bool {
	mode := d.alt()
	return d.Pipeline >= 0xa0 &&
		d.Pipeline <= 0xaf &&
		(mode == Alt1 || mode == Alt3) &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix &&
		d.ramPending &&
		d.ramDelay > d.nextOpcodeFetchCycles()
}

func (d *Device) canStartSMSStepSlice() bool {
	return d.Pipeline >= 0xa0 &&
		d.Pipeline <= 0xaf &&
		d.alt() == Alt2 &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix
}

func (d *Device) canStartSMStepSlice() bool {
	return d.Pipeline >= 0xf0 &&
		d.Pipeline <= 0xff &&
		d.alt() == Alt2 &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix
}

func (d *Device) canStartSTWStepSlice() bool {
	return d.Pipeline >= 0x30 &&
		d.Pipeline <= 0x3b &&
		d.alt() == AltNone &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix
}

func (d *Device) canStartSTBStepSlice() bool {
	mode := d.alt()
	return d.Pipeline >= 0x30 &&
		d.Pipeline <= 0x3b &&
		(mode == Alt1 || mode == Alt3) &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix &&
		d.ramPending &&
		d.ramDelay > d.nextOpcodeFetchCycles()
}

func (d *Device) canStartLDBStepSlice() bool {
	mode := d.alt()
	return d.Pipeline >= 0x40 &&
		d.Pipeline <= 0x4b &&
		(mode == Alt1 || mode == Alt3) &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix &&
		d.ramPending &&
		d.ramDelay > d.nextOpcodeFetchCycles()
}

func (d *Device) canStartLDWStepSlice() bool {
	return d.Pipeline >= 0x40 &&
		d.Pipeline <= 0x4b &&
		d.alt() == AltNone &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix &&
		d.ramPending &&
		d.ramDelay > d.nextOpcodeFetchCycles()
}

func (d *Device) canStartGETBStepSlice() bool {
	return d.Pipeline == 0xef &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix &&
		d.romPending &&
		d.romDelay > d.nextOpcodeFetchCycles()
}

func (d *Device) canStartGETCStepSlice() bool {
	mode := d.alt()
	return d.Pipeline == 0xdf &&
		(mode == AltNone || mode == Alt1) &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix &&
		d.romPending &&
		d.romDelay > d.nextOpcodeFetchCycles()
}

func (d *Device) canStartROMBStepSlice() bool {
	return d.Pipeline == 0xdf &&
		d.alt() == Alt3 &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix &&
		d.romPending &&
		d.romDelay > d.nextOpcodeFetchCycles()
}

func (d *Device) canStartRAMBStepSlice() bool {
	return d.Pipeline == 0xdf &&
		d.alt() == Alt2 &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix &&
		d.ramPending &&
		d.ramDelay > d.nextOpcodeFetchCycles()
}

func (d *Device) canStartSBKStepSlice() bool {
	return d.Pipeline == 0x90 &&
		d.alt() == AltNone &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix
}

func (d *Device) startStepSliceFrame() bool {
	switch {
	case d.canStartFMULTStepSlice():
		return d.startFMULTStepSlice()
	case d.canStartIWTStepSlice():
		return d.startIWTStepSlice()
	case d.canStartLMStepSlice():
		return d.startLMStepSlice()
	case d.canStartLMSStepSlice():
		return d.startLMSStepSlice()
	case d.canStartSMSStepSlice():
		return d.startSMSStepSlice()
	case d.canStartSMStepSlice():
		return d.startSMStepSlice()
	case d.canStartSTWStepSlice():
		return d.startSTWStepSlice()
	case d.canStartSTBStepSlice():
		return d.startSTBStepSlice()
	case d.canStartLDWStepSlice():
		return d.startLDWStepSlice()
	case d.canStartLDBStepSlice():
		return d.startLDBStepSlice()
	case d.canStartGETBStepSlice():
		return d.startGETBStepSlice()
	case d.canStartGETCStepSlice():
		return d.startGETCStepSlice()
	case d.canStartROMBStepSlice():
		return d.startROMBStepSlice()
	case d.canStartRAMBStepSlice():
		return d.startRAMBStepSlice()
	case d.canStartSBKStepSlice():
		return d.startSBKStepSlice()
	default:
		return false
	}
}

func (d *Device) startStepSliceDispatch() (uint8, uint8, uint16) {
	pbr, pc := d.PBR, d.R[15]
	if d.TraceHookEx != nil {
		d.TraceHookEx(TracePhasePrePeek, pbr, pc, d.Pipeline, d.cycles)
	}
	op := d.peekpipe()
	if d.TraceHook != nil {
		d.TraceHook(pbr, pc, op)
	}
	if d.TraceHookEx != nil {
		d.TraceHookEx(TracePhasePostPeek, pbr, pc, op, d.cycles)
	}
	return op, pbr, pc
}

func (d *Device) startFMULTStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	if op != 0x9f {
		return false
	}
	src, dst := d.srcReg(), d.dstReg()
	d.executeMultResult(AltNone)
	d.stepSlice = stepSliceFrame{
		Active:          true,
		Op:              op,
		PBR:             pbr,
		PC:              pc,
		Phase:           stepSlicePhaseFMULTWait,
		Mode:            AltNone,
		SrcReg:          src,
		DstReg:          dst,
		RemainingCycles: d.multWaitCycles(),
		PostPending:     true,
		PrefixPending:   true,
	}
	return true
}

func (d *Device) startIWTStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	if op < 0xf0 || op > 0xff {
		return false
	}
	d.stepSlice = stepSliceFrame{
		Active:        true,
		Op:            op,
		PBR:           pbr,
		PC:            pc,
		Phase:         stepSlicePhaseIWTFetchLow,
		Mode:          AltNone,
		DstReg:        op & 0x0f,
		Nibble:        op & 0x0f,
		PostPending:   true,
		PrefixPending: true,
	}
	return true
}

func (d *Device) startLMStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	mode := d.alt()
	if op < 0xf0 || op > 0xff || (mode != Alt1 && mode != Alt3) {
		return false
	}
	d.stepSlice = stepSliceFrame{
		Active:        true,
		Op:            op,
		PBR:           pbr,
		PC:            pc,
		Phase:         stepSlicePhaseIWTFetchLow,
		Mode:          mode,
		DstReg:        op & 0x0f,
		Nibble:        op & 0x0f,
		Bank:          d.RAMBR,
		PostPending:   true,
		PrefixPending: true,
	}
	return true
}

func (d *Device) startLMSStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	mode := d.alt()
	if op < 0xa0 || op > 0xaf || (mode != Alt1 && mode != Alt3) || !d.ramPending {
		return false
	}
	d.stepSlice = stepSliceFrame{
		Active:        true,
		Op:            op,
		PBR:           pbr,
		PC:            pc,
		Phase:         stepSlicePhaseLMSFetchAddr,
		Mode:          mode,
		DstReg:        op & 0x0f,
		Nibble:        op & 0x0f,
		Bank:          d.RAMBR,
		PostPending:   true,
		PrefixPending: true,
	}
	return true
}

func (d *Device) startSMSStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	if op < 0xa0 || op > 0xaf || d.alt() != Alt2 {
		return false
	}
	d.stepSlice = stepSliceFrame{
		Active:        true,
		Op:            op,
		PBR:           pbr,
		PC:            pc,
		Phase:         stepSlicePhaseSMSFetchAddr,
		Mode:          Alt2,
		SrcReg:        op & 0x0f,
		Nibble:        op & 0x0f,
		Bank:          d.RAMBR,
		PostPending:   true,
		PrefixPending: true,
	}
	return true
}

func (d *Device) startSMStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	if op < 0xf0 || op > 0xff || d.alt() != Alt2 {
		return false
	}
	d.stepSlice = stepSliceFrame{
		Active:        true,
		Op:            op,
		PBR:           pbr,
		PC:            pc,
		Phase:         stepSlicePhaseIWTFetchLow,
		Mode:          Alt2,
		SrcReg:        op & 0x0f,
		Nibble:        op & 0x0f,
		Bank:          d.RAMBR,
		PostPending:   true,
		PrefixPending: true,
	}
	return true
}

func (d *Device) startSTWStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	if op < 0x30 || op > 0x3b {
		return false
	}
	n := op & 0x0f
	addr := d.R[n]
	v := d.R[d.srcReg()]
	bank := d.RAMBR
	d.RAMAddr = addr
	if d.ramPending {
		d.stepSlice = stepSliceFrame{
			Active:          true,
			Op:              op,
			PBR:             pbr,
			PC:              pc,
			Phase:           stepSlicePhaseSTWWaitLow,
			Mode:            AltNone,
			SrcReg:          d.srcReg(),
			Nibble:          n,
			OperandLow:      uint8(v),
			OperandHigh:     uint8(v >> 8),
			Bank:            bank,
			Address:         addr,
			RemainingCycles: d.ramDelay,
			PostPending:     true,
			PrefixPending:   true,
		}
		return true
	}
	d.writeRAMBufferBank(bank, addr, uint8(v))
	d.stepSlice = stepSliceFrame{
		Active:          true,
		Op:              op,
		PBR:             pbr,
		PC:              pc,
		Phase:           stepSlicePhaseSTWWaitHigh,
		Mode:            AltNone,
		SrcReg:          d.srcReg(),
		Nibble:          n,
		OperandHigh:     uint8(v >> 8),
		Bank:            bank,
		Address:         addr ^ 1,
		RemainingCycles: d.ramDelay,
		PostPending:     true,
		PrefixPending:   true,
	}
	return true
}

func (d *Device) startSTBStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	if op < 0x30 || op > 0x3b {
		return false
	}
	mode := d.alt()
	if mode != Alt1 && mode != Alt3 || !d.ramPending {
		return false
	}
	n := op & 0x0f
	addr := d.R[n]
	v := d.R[d.srcReg()]
	bank := d.RAMBR
	d.RAMAddr = addr
	d.stepSlice = stepSliceFrame{
		Active:          true,
		Op:              op,
		PBR:             pbr,
		PC:              pc,
		Phase:           stepSlicePhaseSTBWaitWrite,
		Mode:            mode,
		SrcReg:          d.srcReg(),
		Nibble:          n,
		OperandLow:      uint8(v),
		Bank:            bank,
		Address:         addr,
		RemainingCycles: d.ramDelay,
		PostPending:     true,
		PrefixPending:   true,
	}
	return true
}

func (d *Device) startLDBStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	mode := d.alt()
	if op < 0x40 || op > 0x4b || (mode != Alt1 && mode != Alt3) || !d.ramPending {
		return false
	}
	n := op & 0x0f
	addr := d.R[n]
	d.RAMAddr = addr
	d.stepSlice = stepSliceFrame{
		Active:          true,
		Op:              op,
		PBR:             pbr,
		PC:              pc,
		Phase:           stepSlicePhaseLDBWaitRead,
		Mode:            mode,
		DstReg:          d.dstReg(),
		Nibble:          n,
		Bank:            d.RAMBR,
		Address:         addr,
		RemainingCycles: d.ramDelay,
		PostPending:     true,
		PrefixPending:   true,
	}
	return true
}

func (d *Device) startLDWStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	if op < 0x40 || op > 0x4b || d.alt() != AltNone || !d.ramPending {
		return false
	}
	n := op & 0x0f
	addr := d.R[n]
	d.RAMAddr = addr
	d.stepSlice = stepSliceFrame{
		Active:          true,
		Op:              op,
		PBR:             pbr,
		PC:              pc,
		Phase:           stepSlicePhaseLDWWaitRead,
		Mode:            AltNone,
		DstReg:          d.dstReg(),
		Nibble:          n,
		Bank:            d.RAMBR,
		Address:         addr,
		RemainingCycles: d.ramDelay,
		PostPending:     true,
		PrefixPending:   true,
	}
	return true
}

func (d *Device) startGETBStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	if op != 0xef || !d.romPending {
		return false
	}
	d.stepSlice = stepSliceFrame{
		Active:          true,
		Op:              op,
		PBR:             pbr,
		PC:              pc,
		Phase:           stepSlicePhaseGETBWaitRead,
		Mode:            d.alt(),
		SrcReg:          d.srcReg(),
		DstReg:          d.dstReg(),
		RemainingCycles: d.romDelay,
		PostPending:     true,
		PrefixPending:   true,
	}
	return true
}

func (d *Device) startGETCStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	mode := d.alt()
	if op != 0xdf || (mode != AltNone && mode != Alt1) || !d.romPending {
		return false
	}
	d.stepSlice = stepSliceFrame{
		Active:          true,
		Op:              op,
		PBR:             pbr,
		PC:              pc,
		Phase:           stepSlicePhaseGETCWaitRead,
		Mode:            mode,
		RemainingCycles: d.romDelay,
		PostPending:     true,
		PrefixPending:   true,
	}
	return true
}

func (d *Device) startROMBStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	if op != 0xdf || d.alt() != Alt3 || !d.romPending {
		return false
	}
	d.stepSlice = stepSliceFrame{
		Active:          true,
		Op:              op,
		PBR:             pbr,
		PC:              pc,
		Phase:           stepSlicePhaseROMBWaitSet,
		Mode:            Alt3,
		SrcReg:          d.srcReg(),
		RemainingCycles: d.romDelay,
		PostPending:     true,
		PrefixPending:   true,
	}
	return true
}

func (d *Device) startRAMBStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	if op != 0xdf || d.alt() != Alt2 || !d.ramPending {
		return false
	}
	d.stepSlice = stepSliceFrame{
		Active:          true,
		Op:              op,
		PBR:             pbr,
		PC:              pc,
		Phase:           stepSlicePhaseRAMBWaitSet,
		Mode:            Alt2,
		SrcReg:          d.srcReg(),
		RemainingCycles: d.ramDelay,
		PostPending:     true,
		PrefixPending:   true,
	}
	return true
}

func (d *Device) startSBKStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	if op != 0x90 || d.alt() != AltNone {
		return false
	}
	v := d.R[d.srcReg()]
	bank := d.RAMBR
	addr := d.RAMAddr
	if d.ramPending {
		d.stepSlice = stepSliceFrame{
			Active:          true,
			Op:              op,
			PBR:             pbr,
			PC:              pc,
			Phase:           stepSlicePhaseSBKWaitLow,
			Mode:            AltNone,
			SrcReg:          d.srcReg(),
			OperandLow:      uint8(v),
			OperandHigh:     uint8(v >> 8),
			Bank:            bank,
			Address:         addr,
			RemainingCycles: d.ramDelay,
			PostPending:     true,
			PrefixPending:   true,
		}
		return true
	}
	d.writeRAMBufferBank(bank, addr, uint8(v))
	d.stepSlice = stepSliceFrame{
		Active:          true,
		Op:              op,
		PBR:             pbr,
		PC:              pc,
		Phase:           stepSlicePhaseSBKWaitHigh,
		Mode:            AltNone,
		SrcReg:          d.srcReg(),
		OperandHigh:     uint8(v >> 8),
		Bank:            bank,
		Address:         addr ^ 1,
		RemainingCycles: d.ramDelay,
		PostPending:     true,
		PrefixPending:   true,
	}
	return true
}

func (d *Device) advanceStepSliceFrame(masterCycles uint64) int {
	switch {
	case d.stepSlice.Op == 0x9f && d.stepSlice.Phase == stepSlicePhaseFMULTWait:
		return d.advanceFMULTStepSliceFrame(masterCycles)
	case d.stepSlice.Op >= 0xf0 && d.stepSlice.Op <= 0xff &&
		(d.stepSlice.Mode == Alt1 || d.stepSlice.Mode == Alt3):
		return d.advanceLMStepSliceFrame(masterCycles)
	case d.stepSlice.Op >= 0xa0 && d.stepSlice.Op <= 0xaf &&
		(d.stepSlice.Phase == stepSlicePhaseLMSFetchAddr ||
			d.stepSlice.Phase == stepSlicePhaseLMSWaitRead):
		return d.advanceLMSStepSliceFrame(masterCycles)
	case d.stepSlice.Op >= 0xa0 && d.stepSlice.Op <= 0xaf &&
		(d.stepSlice.Phase == stepSlicePhaseSMSFetchAddr ||
			d.stepSlice.Phase == stepSlicePhaseSMSWaitLow ||
			d.stepSlice.Phase == stepSlicePhaseSMSWaitHigh):
		return d.advanceSMSStepSliceFrame(masterCycles)
	case d.stepSlice.Op >= 0xf0 && d.stepSlice.Op <= 0xff &&
		d.stepSlice.Mode == Alt2:
		return d.advanceSMStepSliceFrame(masterCycles)
	case d.stepSlice.Op >= 0xf0 && d.stepSlice.Op <= 0xff:
		return d.advanceIWTStepSliceFrame(masterCycles)
	case d.stepSlice.Op >= 0x30 && d.stepSlice.Op <= 0x3b &&
		(d.stepSlice.Phase == stepSlicePhaseSTWWaitLow ||
			d.stepSlice.Phase == stepSlicePhaseSTWWaitHigh):
		return d.advanceWordStoreStepSliceFrame(masterCycles)
	case d.stepSlice.Op >= 0x30 && d.stepSlice.Op <= 0x3b &&
		d.stepSlice.Phase == stepSlicePhaseSTBWaitWrite:
		return d.advanceSTBStepSliceFrame(masterCycles)
	case d.stepSlice.Op >= 0x40 && d.stepSlice.Op <= 0x4b &&
		d.stepSlice.Phase == stepSlicePhaseLDBWaitRead:
		return d.advanceLDBStepSliceFrame(masterCycles)
	case d.stepSlice.Op >= 0x40 && d.stepSlice.Op <= 0x4b &&
		d.stepSlice.Phase == stepSlicePhaseLDWWaitRead:
		return d.advanceLDWStepSliceFrame(masterCycles)
	case d.stepSlice.Op == 0xef && d.stepSlice.Phase == stepSlicePhaseGETBWaitRead:
		return d.advanceGETBStepSliceFrame(masterCycles)
	case d.stepSlice.Op == 0xdf && d.stepSlice.Phase == stepSlicePhaseGETCWaitRead:
		return d.advanceGETCStepSliceFrame(masterCycles)
	case d.stepSlice.Op == 0xdf && d.stepSlice.Phase == stepSlicePhaseROMBWaitSet:
		return d.advanceROMBStepSliceFrame(masterCycles)
	case d.stepSlice.Op == 0xdf && d.stepSlice.Phase == stepSlicePhaseRAMBWaitSet:
		return d.advanceRAMBStepSliceFrame(masterCycles)
	case d.stepSlice.Op == 0x90 && (d.stepSlice.Phase == stepSlicePhaseSBKWaitLow || d.stepSlice.Phase == stepSlicePhaseSBKWaitHigh):
		return d.advanceWordStoreStepSliceFrame(masterCycles)
	default:
		return 0
	}
}

func (d *Device) advanceFMULTStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	if d.stepSlice.RemainingCycles != 0 {
		if masterCycles < d.stepSlice.RemainingCycles {
			d.advanceCycles(masterCycles)
			d.stepSlice.RemainingCycles -= masterCycles
			return 0
		}
		d.advanceCycles(d.stepSlice.RemainingCycles)
		d.stepSlice.RemainingCycles = 0
	}
	d.finishStepSliceFrame()
	return 1
}

func (d *Device) advanceIWTStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	for {
		switch d.stepSlice.Phase {
		case stepSlicePhaseIWTFetchLow:
			lo, spent, ok := d.stepSliceFetch8(masterCycles)
			if !ok {
				return 0
			}
			d.stepSlice.OperandLow = lo
			d.stepSlice.Phase = stepSlicePhaseIWTFetchHigh
			masterCycles -= spent
		case stepSlicePhaseIWTFetchHigh:
			hi, _, ok := d.stepSliceFetch8(masterCycles)
			if !ok {
				return 0
			}
			d.setReg(d.stepSlice.DstReg, uint16(d.stepSlice.OperandLow)|uint16(hi)<<8)
			d.finishStepSliceFrame()
			return 1
		default:
			return 0
		}
	}
}

func (d *Device) advanceLMStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	for {
		switch d.stepSlice.Phase {
		case stepSlicePhaseIWTFetchLow:
			lo, spent, ok := d.stepSliceFetch8(masterCycles)
			if !ok {
				return 0
			}
			d.stepSlice.OperandLow = lo
			d.stepSlice.Phase = stepSlicePhaseIWTFetchHigh
			masterCycles -= spent
		case stepSlicePhaseIWTFetchHigh:
			hi, spent, ok := d.stepSliceFetch8(masterCycles)
			if !ok {
				return 0
			}
			d.stepSlice.OperandHigh = hi
			d.stepSlice.Address = uint16(d.stepSlice.OperandLow) | uint16(hi)<<8
			d.RAMAddr = d.stepSlice.Address
			d.stepSlice.Phase = stepSlicePhaseLMWaitRead
			d.stepSlice.RemainingCycles = d.ramDelay
			masterCycles -= spent
		case stepSlicePhaseLMWaitRead:
			if d.stepSlice.RemainingCycles != 0 {
				if masterCycles == 0 {
					return 0
				}
				if masterCycles < d.stepSlice.RemainingCycles {
					d.advanceCycles(masterCycles)
					d.stepSlice.RemainingCycles -= masterCycles
					return 0
				}
				d.advanceCycles(d.stepSlice.RemainingCycles)
				d.stepSlice.RemainingCycles = 0
			}

			addr := uint32(d.stepSlice.Bank)<<16 | uint32(d.stepSlice.Address)
			lo := d.ramRead(addr)
			hi := d.ramRead((uint32(d.stepSlice.Bank) << 16) | uint32(d.stepSlice.Address^1))
			d.setReg(d.stepSlice.DstReg, uint16(lo)|uint16(hi)<<8)
			d.finishStepSliceFrame()
			return 1
		default:
			return 0
		}
	}
}

func (d *Device) advanceLMSStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	for {
		switch d.stepSlice.Phase {
		case stepSlicePhaseLMSFetchAddr:
			imm, spent, ok := d.stepSliceFetch8(masterCycles)
			if !ok {
				return 0
			}
			addr := uint16(imm) << 1
			d.stepSlice.OperandLow = imm
			d.stepSlice.Address = addr
			d.RAMAddr = addr
			d.stepSlice.Phase = stepSlicePhaseLMSWaitRead
			d.stepSlice.RemainingCycles = d.ramDelay
			masterCycles -= spent
		case stepSlicePhaseLMSWaitRead:
			if d.stepSlice.RemainingCycles != 0 {
				if masterCycles == 0 {
					return 0
				}
				if masterCycles < d.stepSlice.RemainingCycles {
					d.advanceCycles(masterCycles)
					d.stepSlice.RemainingCycles -= masterCycles
					return 0
				}
				d.advanceCycles(d.stepSlice.RemainingCycles)
				d.stepSlice.RemainingCycles = 0
			}

			addr := uint32(d.stepSlice.Bank)<<16 | uint32(d.stepSlice.Address)
			lo := d.ramRead(addr)
			hi := d.ramRead(addr + 1)
			d.setReg(d.stepSlice.DstReg, uint16(lo)|uint16(hi)<<8)
			d.finishStepSliceFrame()
			return 1
		default:
			return 0
		}
	}
}

func (d *Device) advanceSMSStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	for {
		switch d.stepSlice.Phase {
		case stepSlicePhaseSMSFetchAddr:
			imm, spent, ok := d.stepSliceFetch8(masterCycles)
			if !ok {
				return 0
			}
			addr := uint16(imm) << 1
			v := d.R[d.stepSlice.SrcReg]
			d.stepSlice.OperandLow = uint8(v)
			d.stepSlice.OperandHigh = uint8(v >> 8)
			d.stepSlice.Address = addr
			d.RAMAddr = addr
			d.stepSlice.Phase = stepSlicePhaseSMSWaitLow
			d.stepSlice.RemainingCycles = d.ramDelay
			masterCycles -= spent
		case stepSlicePhaseSMSWaitLow:
			if d.stepSlice.RemainingCycles != 0 {
				if masterCycles == 0 {
					return 0
				}
				if masterCycles < d.stepSlice.RemainingCycles {
					d.advanceCycles(masterCycles)
					d.stepSlice.RemainingCycles -= masterCycles
					return 0
				}
				wait := d.stepSlice.RemainingCycles
				d.advanceCycles(wait)
				d.stepSlice.RemainingCycles = 0
				masterCycles -= wait
			}

			addr := d.stepSlice.Address
			d.writeRAMBufferBank(d.stepSlice.Bank, addr, d.stepSlice.OperandLow)
			d.stepSlice.Phase = stepSlicePhaseSMSWaitHigh
			d.stepSlice.Address = addr + 1
			d.stepSlice.RemainingCycles = d.ramDelay
		case stepSlicePhaseSMSWaitHigh:
			if d.stepSlice.RemainingCycles != 0 {
				if masterCycles == 0 {
					return 0
				}
				if masterCycles < d.stepSlice.RemainingCycles {
					d.advanceCycles(masterCycles)
					d.stepSlice.RemainingCycles -= masterCycles
					return 0
				}
				d.advanceCycles(d.stepSlice.RemainingCycles)
				d.stepSlice.RemainingCycles = 0
			}

			d.writeRAMBufferBank(d.stepSlice.Bank, d.stepSlice.Address, d.stepSlice.OperandHigh)
			d.finishStepSliceFrame()
			return 1
		default:
			return 0
		}
	}
}

func (d *Device) advanceSMStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	for {
		switch d.stepSlice.Phase {
		case stepSlicePhaseIWTFetchLow:
			lo, spent, ok := d.stepSliceFetch8(masterCycles)
			if !ok {
				return 0
			}
			d.stepSlice.OperandLow = lo
			d.stepSlice.Phase = stepSlicePhaseIWTFetchHigh
			masterCycles -= spent
		case stepSlicePhaseIWTFetchHigh:
			hi, spent, ok := d.stepSliceFetch8(masterCycles)
			if !ok {
				return 0
			}
			addr := uint16(d.stepSlice.OperandLow) | uint16(hi)<<8
			v := d.R[d.stepSlice.SrcReg]
			d.stepSlice.Address = addr
			if d.ramPending {
				d.stepSlice.OperandLow = uint8(v)
				d.stepSlice.OperandHigh = uint8(v >> 8)
				d.RAMAddr = addr
				d.stepSlice.Phase = stepSlicePhaseSMWaitLow
				d.stepSlice.RemainingCycles = d.ramDelay
				masterCycles -= spent
				continue
			}
			d.stepSlice.OperandHigh = uint8(v >> 8)
			d.stepSlice.Address = addr ^ 1
			d.RAMAddr = addr
			d.writeRAMBufferBank(d.stepSlice.Bank, addr, uint8(v))
			d.stepSlice.Phase = stepSlicePhaseSMWaitHigh
			d.stepSlice.RemainingCycles = d.ramDelay
			masterCycles -= spent
		case stepSlicePhaseSMWaitLow:
			if d.stepSlice.RemainingCycles != 0 {
				if masterCycles == 0 {
					return 0
				}
				if masterCycles < d.stepSlice.RemainingCycles {
					d.advanceCycles(masterCycles)
					d.stepSlice.RemainingCycles -= masterCycles
					return 0
				}
				wait := d.stepSlice.RemainingCycles
				d.advanceCycles(wait)
				d.stepSlice.RemainingCycles = 0
				masterCycles -= wait
			}

			addr := d.stepSlice.Address
			d.writeRAMBufferBank(d.stepSlice.Bank, addr, d.stepSlice.OperandLow)
			d.stepSlice.Phase = stepSlicePhaseSMWaitHigh
			d.stepSlice.Address = addr ^ 1
			d.stepSlice.RemainingCycles = d.ramDelay
		case stepSlicePhaseSMWaitHigh:
			if d.stepSlice.RemainingCycles != 0 {
				if masterCycles == 0 {
					return 0
				}
				if masterCycles < d.stepSlice.RemainingCycles {
					d.advanceCycles(masterCycles)
					d.stepSlice.RemainingCycles -= masterCycles
					return 0
				}
				d.advanceCycles(d.stepSlice.RemainingCycles)
				d.stepSlice.RemainingCycles = 0
			}

			d.writeRAMBufferBank(d.stepSlice.Bank, d.stepSlice.Address, d.stepSlice.OperandHigh)
			d.finishStepSliceFrame()
			return 1
		default:
			return 0
		}
	}
}

func (d *Device) advanceWordStoreStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	for {
		switch d.stepSlice.Phase {
		case stepSlicePhaseSTWWaitLow, stepSlicePhaseSBKWaitLow:
			if d.stepSlice.RemainingCycles != 0 {
				if masterCycles < d.stepSlice.RemainingCycles {
					d.advanceCycles(masterCycles)
					d.stepSlice.RemainingCycles -= masterCycles
					return 0
				}
				d.advanceCycles(d.stepSlice.RemainingCycles)
				masterCycles -= d.stepSlice.RemainingCycles
				d.stepSlice.RemainingCycles = 0
			}
			d.writeRAMBufferBank(d.stepSlice.Bank, d.stepSlice.Address, d.stepSlice.OperandLow)
			d.stepSlice.Address ^= 1
			if d.stepSlice.Op == 0x90 {
				d.stepSlice.Phase = stepSlicePhaseSBKWaitHigh
			} else {
				d.stepSlice.Phase = stepSlicePhaseSTWWaitHigh
			}
			d.stepSlice.RemainingCycles = d.ramDelay
			if masterCycles == 0 {
				return 0
			}
		case stepSlicePhaseSTWWaitHigh, stepSlicePhaseSBKWaitHigh:
			if d.stepSlice.RemainingCycles != 0 {
				if masterCycles == 0 {
					return 0
				}
				if masterCycles < d.stepSlice.RemainingCycles {
					d.advanceCycles(masterCycles)
					d.stepSlice.RemainingCycles -= masterCycles
					return 0
				}
				d.advanceCycles(d.stepSlice.RemainingCycles)
				d.stepSlice.RemainingCycles = 0
			}
			d.writeRAMBufferBank(d.stepSlice.Bank, d.stepSlice.Address, d.stepSlice.OperandHigh)
			d.finishStepSliceFrame()
			return 1
		default:
			return 0
		}
	}
}

func (d *Device) advanceSTBStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	if d.stepSlice.RemainingCycles != 0 {
		if masterCycles < d.stepSlice.RemainingCycles {
			d.advanceCycles(masterCycles)
			d.stepSlice.RemainingCycles -= masterCycles
			return 0
		}
		d.advanceCycles(d.stepSlice.RemainingCycles)
		d.stepSlice.RemainingCycles = 0
	}
	d.writeRAMBufferBank(d.stepSlice.Bank, d.stepSlice.Address, d.stepSlice.OperandLow)
	d.finishStepSliceFrame()
	return 1
}

func (d *Device) advanceLDBStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	if d.stepSlice.RemainingCycles != 0 {
		if masterCycles < d.stepSlice.RemainingCycles {
			d.advanceCycles(masterCycles)
			d.stepSlice.RemainingCycles -= masterCycles
			return 0
		}
		d.advanceCycles(d.stepSlice.RemainingCycles)
		d.stepSlice.RemainingCycles = 0
	}

	addr := uint32(d.stepSlice.Bank)<<16 | uint32(d.stepSlice.Address)
	d.setReg(d.stepSlice.DstReg, uint16(d.ramRead(addr)))
	d.finishStepSliceFrame()
	return 1
}

func (d *Device) advanceLDWStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	if d.stepSlice.RemainingCycles != 0 {
		if masterCycles < d.stepSlice.RemainingCycles {
			d.advanceCycles(masterCycles)
			d.stepSlice.RemainingCycles -= masterCycles
			return 0
		}
		d.advanceCycles(d.stepSlice.RemainingCycles)
		d.stepSlice.RemainingCycles = 0
	}

	addr := uint32(d.stepSlice.Bank)<<16 | uint32(d.stepSlice.Address)
	lo := d.ramRead(addr)
	hi := d.ramRead((uint32(d.stepSlice.Bank) << 16) | uint32(d.stepSlice.Address^1))
	d.setReg(d.stepSlice.DstReg, uint16(lo)|uint16(hi)<<8)
	d.finishStepSliceFrame()
	return 1
}

func (d *Device) advanceGETBStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	if d.stepSlice.RemainingCycles != 0 {
		if masterCycles < d.stepSlice.RemainingCycles {
			d.advanceCycles(masterCycles)
			d.stepSlice.RemainingCycles -= masterCycles
			return 0
		}
		d.advanceCycles(d.stepSlice.RemainingCycles)
		d.stepSlice.RemainingCycles = 0
	}

	b := uint16(d.romRead())
	var v uint16
	switch d.stepSlice.Mode {
	case Alt1:
		v = b<<8 | (d.R[d.stepSlice.SrcReg] & 0x00FF)
	case Alt2:
		v = (d.R[d.stepSlice.SrcReg] & 0xFF00) | b
	case Alt3:
		v = uint16(int16(int8(b)))
	default:
		v = b
	}
	d.setReg(d.stepSlice.DstReg, v)
	d.finishStepSliceFrame()
	return 1
}

func (d *Device) advanceGETCStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	if d.stepSlice.RemainingCycles != 0 {
		if masterCycles < d.stepSlice.RemainingCycles {
			d.advanceCycles(masterCycles)
			d.stepSlice.RemainingCycles -= masterCycles
			return 0
		}
		d.advanceCycles(d.stepSlice.RemainingCycles)
		d.stepSlice.RemainingCycles = 0
	}

	d.COLR = d.color(d.romRead())
	d.finishStepSliceFrame()
	return 1
}

func (d *Device) advanceROMBStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	if d.stepSlice.RemainingCycles != 0 {
		if masterCycles < d.stepSlice.RemainingCycles {
			d.advanceCycles(masterCycles)
			d.stepSlice.RemainingCycles -= masterCycles
			return 0
		}
		d.advanceCycles(d.stepSlice.RemainingCycles)
		d.stepSlice.RemainingCycles = 0
	}

	d.ROMBR = uint8(d.R[d.stepSlice.SrcReg] & 0x7f)
	d.finishStepSliceFrame()
	return 1
}

func (d *Device) advanceRAMBStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	if d.stepSlice.RemainingCycles != 0 {
		if masterCycles < d.stepSlice.RemainingCycles {
			d.advanceCycles(masterCycles)
			d.stepSlice.RemainingCycles -= masterCycles
			return 0
		}
		d.advanceCycles(d.stepSlice.RemainingCycles)
		d.stepSlice.RemainingCycles = 0
	}

	d.RAMBR = uint8(d.R[d.stepSlice.SrcReg] & 1)
	d.finishStepSliceFrame()
	return 1
}

func (d *Device) stepSliceFetch8(masterCycles uint64) (uint8, uint64, bool) {
	// fetch8 refills after incrementing R15. Price that address, including a
	// possible cache-line fill, before changing the pipeline or any bus state.
	cycles := d.opcodeFetchCycles(d.R[15] + 1)
	if masterCycles < cycles {
		return 0, 0, false
	}
	start := d.cycles
	v := d.fetch8()
	return v, d.cycles - start, true
}

func (d *Device) finishStepSliceFrame() {
	frame := d.stepSlice
	if frame.PrefixPending {
		d.consumePrefixes()
	}
	if frame.PostPending {
		if d.r15Modified {
			d.r15Modified = false
		} else {
			d.R[15]++
		}
	}
	d.stepSlice = stepSliceFrame{}
}
