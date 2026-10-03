package scheduler

// Thread represents a schedulable component (CPU, SMP, PPU, DSP).
type Thread interface {
	Run()              // Executes for a small slice of time
	GetCycles() uint64 // Returns accumulated cycles in the thread's own clock domain
	ResetCycles()
	Frequency() uint64
}

// Scheduler coordinates the timing of all components.
type Scheduler struct {
	clock uint64 // Master Clock Counter

	cpu Thread
	apu Thread
	ppu Thread

	// frequencies
	cpuFreq uint64
	apuFreq uint64
	ppuFreq uint64

	// Interfaces of the registered threads, resolved once at registration.
	cpuYield YieldingThread
	apuYield YieldingThread
	ppuYield YieldingThread
	ppuBeam  beamSource
	ppuQuiet quietSource
	ppuRun   untilRunner

	// quietThrough is the CPU clock through which SyncPPULazy may skip
	// syncing the PPU, or 0 if unknown. Any PPU sync clears it.
	quietThrough uint64

	// Interrupt State
	nmiEnabled   bool
	nmiTriggered bool
	irqMode      uint8
	irqTriggered bool
	irqLine      uint64
	irqFlag      bool
	irqH         uint16
	irqV         uint16

	pal            bool
	cyclesPerFrame uint64
	vblankStart    uint64
	frameEvent     uint64

	beamFrameStart  uint64
	beamVBlankStart uint64
	displayEvent    uint64

	afterCPU     func(masterCycles uint64)
	runningCPU   bool
	deliveredCPU uint64
}

func NewScheduler() *Scheduler {
	s := &Scheduler{}
	s.SetPAL(false)
	return s
}

func (s *Scheduler) RegisterCPU(cpu Thread, freq uint64) {
	s.cpu = cpu
	s.cpuYield, _ = cpu.(YieldingThread)
	s.cpuFreq = freq
	if s.cpuFreq == 0 && cpu != nil {
		s.cpuFreq = cpu.Frequency()
	}
}

func (s *Scheduler) RegisterAPU(apu Thread, freq uint64) {
	s.apu = apu
	s.apuYield, _ = apu.(YieldingThread)
	s.apuFreq = freq
	if s.apuFreq == 0 && apu != nil {
		s.apuFreq = apu.Frequency()
	}
}

func (s *Scheduler) RegisterPPU(ppu Thread, freq uint64) {
	s.ppu = ppu
	s.ppuYield, _ = ppu.(YieldingThread)
	s.ppuBeam, _ = ppu.(beamSource)
	s.ppuQuiet, _ = ppu.(quietSource)
	s.ppuRun, _ = ppu.(untilRunner)
	s.quietThrough = 0
	s.ppuFreq = freq
	if s.ppuFreq == 0 && ppu != nil {
		s.ppuFreq = ppu.Frequency()
	}
}

// SetAfterCPU installs a callback run whenever CPU master cycles advance.
func (s *Scheduler) SetAfterCPU(fn func(masterCycles uint64)) {
	s.afterCPU = fn
}

func (s *Scheduler) stepAfterCPU(masterCycles uint64) {
	if s.runningCPU {
		s.deliveredCPU += masterCycles
	}
	if masterCycles != 0 && s.afterCPU != nil {
		s.afterCPU(masterCycles)
	}
}

// resolve returns the clock frequency of target and its YieldingThread
// implementation, if any.
func (s *Scheduler) resolve(target Thread) (uint64, YieldingThread) {
	switch target {
	case s.cpu:
		return s.cpuFreq, s.cpuYield
	case s.apu:
		return s.apuFreq, s.apuYield
	case s.ppu:
		return s.ppuFreq, s.ppuYield
	}
	y, _ := target.(YieldingThread)
	return target.Frequency(), y
}

func (s *Scheduler) syncToMaster(target Thread, masterCycles uint64, mode SyncMode) SyncResult {
	if s.cpu == nil || target == nil || s.cpuFreq == 0 {
		if target != nil && target == s.ppu {
			s.observeBeamEvents()
		}
		return SyncResult{}
	}

	targetFreq, yielding := s.resolve(target)
	var result SyncResult
	switch {
	case targetFreq == 0:
	case yielding != nil:
		targetCycles := targetCyclesAtOrAfter(masterCycles, s.cpuFreq, targetFreq)
		result = yielding.RunUntilTarget(targetCycles, mode)
	case target == s.ppu && s.ppuRun != nil && targetFreq == s.cpuFreq:
		s.ppuRun.RunUntil(masterCycles)
	default:
		for clockBefore(target.GetCycles(), s.cpuFreq, masterCycles, targetFreq) {
			target.Run()
		}
	}
	if target == s.ppu {
		s.observeBeamEvents()
	}
	return result
}

func (s *Scheduler) syncBeforeMaster(target Thread, masterCycles uint64) {
	if s.cpu == nil || target == nil || s.cpuFreq == 0 || masterCycles == 0 {
		return
	}

	targetFreq, yielding := s.resolve(target)
	if targetFreq == 0 {
		return
	}

	if yielding != nil {
		targetCycles := targetCyclesBefore(masterCycles, s.cpuFreq, targetFreq)
		yielding.RunUntilTarget(targetCycles, SyncBeforeCPU)
		return
	}

	for target.GetCycles() != ^uint64(0) && clockBefore(target.GetCycles()+1, s.cpuFreq, masterCycles, targetFreq) {
		target.Run()
	}
}

func (s *Scheduler) syncAll(masterCycles uint64) SyncResult {
	var result SyncResult
	if s.apu != nil {
		result = s.syncToMaster(s.apu, masterCycles, SyncPostCPU)
	}
	if s.ppu != nil {
		s.syncToMaster(s.ppu, masterCycles, SyncSafety)
	}
	return result
}

func (s *Scheduler) syncAllSafety(masterCycles uint64) {
	if s.apu != nil {
		s.syncToMaster(s.apu, masterCycles, SyncSafety)
	}
	if s.ppu != nil {
		s.syncToMaster(s.ppu, masterCycles, SyncSafety)
	}
}

// Sync runs the target thread until it catches up to the main thread (CPU).
// This is called when the CPU accesses a device synced with the target.
func (s *Scheduler) Sync(target Thread) {
	if s.cpu == nil || target == nil {
		return
	}

	s.syncToMaster(target, s.cpu.GetCycles(), SyncSafety)
}

// SyncPortRead runs the target thread for a direct CPU port read.
func (s *Scheduler) SyncPortRead(target Thread) SyncResult {
	if s.cpu == nil || target == nil {
		return SyncResult{}
	}
	return s.syncToMaster(target, s.cpu.GetCycles(), SyncPortRead)
}

// SyncPortWrite runs the target thread after a direct CPU port write.
func (s *Scheduler) SyncPortWrite(target Thread) SyncResult {
	if s.cpu == nil || target == nil {
		return SyncResult{}
	}
	return s.syncToMaster(target, s.cpu.GetCycles(), SyncPortWrite)
}

// SyncTo runs the target thread until it catches up to masterCycles.
func (s *Scheduler) SyncTo(target Thread, masterCycles uint64) {
	s.syncToMaster(target, masterCycles, SyncSafety)
}

// SyncBefore runs the target thread until the next target cycle would reach or
// pass the main thread's current timestamp.
func (s *Scheduler) SyncBefore(target Thread) {
	if s.cpu == nil || target == nil {
		return
	}
	s.syncBeforeMaster(target, s.cpu.GetCycles())
}

// AddCycles advances the CPU by master cycles and synchronizes the other domains.
func (s *Scheduler) AddCycles(cycles uint64) {
	if cycles == 0 || s.cpu == nil {
		return
	}

	type cycler interface {
		AddCycles(uint64)
	}

	cpuObj, ok := s.cpu.(cycler)
	if !ok {
		return
	}
	cpuObj.AddCycles(cycles)
	s.stepAfterCPU(cycles)
	s.syncAll(s.cpu.GetCycles())
}

// Enter is called by the main loop to run the CPU for a duration (e.g. 1 frame).
// It acts as the "Main Thread".
func (s *Scheduler) RunFrame() {
	if s.cpu == nil {
		return
	}

	s.nmiTriggered = false
	s.irqTriggered = false
	s.irqLine = ^uint64(0)

	frameStart := s.cpu.GetCycles()
	frameEnd := frameStart + s.cyclesPerFrame

	for s.cpu.GetCycles() < frameEnd {
		currentCycles := s.cpu.GetCycles()
		frameCycles := currentCycles - frameStart

		// NMI Trigger Logic
		if s.ppuBeam == nil && s.nmiEnabled && !s.nmiTriggered && frameCycles >= s.vblankStart {
			if nmiTarget, ok := s.cpu.(interface{ TriggerNMI() }); ok {
				nmiTarget.TriggerNMI()
			}
			s.nmiTriggered = true
		}

		s.triggerIRQBetween(frameCycles, frameCycles)

		startCycles := s.cpu.GetCycles()
		s.runCPU()
		endCycles := s.cpu.GetCycles()

		if startCycles == endCycles {
			// CPU Stopped or Deadlocked
			// Advance cycles effectively to prevent infinite loop
			// Assuming Stopped state essentially halts the system or just idles.
			// Ideally we should check s.cpu.Stopped() but interface doesn't have it.
			// Just force advance.
			// fmt.Println("DEBUG: Scheduler Cycle Stagnation (STP?). Advancing.")

			// We need a way to force cycle update on the CPU object, but interface is restrictive.
			// Using type assertion to fix.
			if _, ok := s.cpu.(interface{ AddCycles(uint64) }); ok {
				s.AddCycles(2)
			} else {
				// Fallback: If we can't advance cycles, we Must break or we loop forever.
				// But breaking implies frame end?
				// The outer loop condition is s.cpu.GetCycles() < CyclesPerFrame
				// If we break, we exit RunFrame(), but next frame we start again.
				// Ideally we simulate time passing.
				// Since we can't write to cpu.Cycles, we might be stuck.
				// But let's assume specific implementation has AddCycles for now.
				break
			}
			continue
		}
		endFrameCycles := endCycles - frameStart
		s.triggerIRQBetween(frameCycles, endFrameCycles)
		s.syncAll(endCycles)
	}

	s.syncAllSafety(s.cpu.GetCycles())
	s.clock = s.cpu.GetCycles()
}

// RunDisplayFrame runs until the next visible-frame boundary.
//
// The reference libretro cores return from retro_run at the start of vblank,
// after the visible frame has been produced. Subsequent calls are one full
// video frame apart.
func (s *Scheduler) RunDisplayFrame() {
	if beam := s.ppuBeam; beam != nil {
		s.runBeamDisplayFrame(beam)
		return
	}
	if s.cpu == nil {
		return
	}
	if s.frameEvent == 0 {
		s.frameEvent = s.vblankStart
	}
	for s.frameEvent <= s.cpu.GetCycles() {
		s.frameEvent += s.cyclesPerFrame
	}

	target := s.frameEvent
	s.irqTriggered = false
	s.irqLine = ^uint64(0)

	for s.cpu.GetCycles() < target {
		frameBase := target - s.vblankStart
		frameCycles := s.cpu.GetCycles() - frameBase
		s.triggerIRQBetween(frameCycles, frameCycles)

		startCycles := s.cpu.GetCycles()
		s.runCPU()
		endCycles := s.cpu.GetCycles()

		if startCycles == endCycles {
			if _, ok := s.cpu.(interface{ AddCycles(uint64) }); ok {
				s.AddCycles(2)
			} else {
				break
			}
			continue
		}
		s.triggerIRQBetween(frameCycles, endCycles-frameBase)
		s.syncAll(endCycles)
	}

	s.nmiTriggered = false
	if s.nmiEnabled {
		if nmiTarget, ok := s.cpu.(interface{ TriggerNMI() }); ok {
			nmiTarget.TriggerNMI()
		}
		s.nmiTriggered = true
	}
	s.frameEvent += s.cyclesPerFrame
	s.syncAllSafety(s.cpu.GetCycles())
	s.clock = s.cpu.GetCycles()
}

func (s *Scheduler) triggerIRQBetween(startFrameCycles, endFrameCycles uint64) {
	if s.irqMode == 0 {
		return
	}
	if s.cyclesPerFrame > 0 && endFrameCycles >= s.cyclesPerFrame {
		endFrameCycles = s.cyclesPerFrame - 1
	}
	if startFrameCycles > endFrameCycles {
		return
	}
	switch s.irqMode & 0x03 {
	case 1:
		startLine := startFrameCycles / 1364
		endLine := endFrameCycles / 1364
		for line := startLine; line <= endLine; line++ {
			target := line*1364 + uint64(s.irqH)*4
			if target < startFrameCycles || target > endFrameCycles || s.irqLine == line {
				continue
			}
			s.triggerIRQ(line)
			return
		}
	case 2:
		target := uint64(s.irqV) * 1364
		if !s.irqTriggered && target >= startFrameCycles && target <= endFrameCycles {
			s.triggerIRQ(uint64(s.irqV))
		}
	case 3:
		target := uint64(s.irqV)*1364 + uint64(s.irqH)*4
		if !s.irqTriggered && target >= startFrameCycles && target <= endFrameCycles {
			s.triggerIRQ(uint64(s.irqV))
		}
	}
}

func (s *Scheduler) triggerIRQ(line uint64) {
	if irqTarget, ok := s.cpu.(interface{ TriggerIRQ() }); ok {
		irqTarget.TriggerIRQ()
	}
	s.irqTriggered = true
	s.irqLine = line
	s.irqFlag = true
}

// SetPAL configures PAL (50Hz) or NTSC (60Hz) frame timing.
func (s *Scheduler) SetPAL(enabled bool) {
	s.quietThrough = 0
	modeChanged := s.pal != enabled
	s.pal = enabled
	if enabled {
		s.cyclesPerFrame = 425568
		s.vblankStart = 240 * 1364
		if modeChanged || s.frameEvent == 0 {
			s.frameEvent = s.vblankStart
		}
		return
	}
	s.cyclesPerFrame = 357366
	s.vblankStart = 225 * 1364
	if modeChanged || s.frameEvent == 0 {
		s.frameEvent = s.vblankStart
	}
}

// SetNMI updates NMITIMEN.7. It returns true when the bit transitions
// 0->1; bsnes nmiPoll delivers a held NMI immediately on that edge if the
// PPU NMI line is still latched, so the caller is expected to consult the
// PPU flag and re-trigger the CPU on a rising edge.
func (s *Scheduler) SetNMI(enabled bool) (rising bool) {
	rising = enabled && !s.nmiEnabled
	s.nmiEnabled = enabled
	return rising
}

// NMIEnabled reports the current NMITIMEN.7 state.
func (s *Scheduler) NMIEnabled() bool { return s.nmiEnabled }

// NMITriggered reports whether NMI has already been delivered for the current
// video frame.
func (s *Scheduler) NMITriggered() bool { return s.nmiTriggered }

func (s *Scheduler) SetIRQ(enabled bool) {
	if enabled {
		s.irqMode = 2
	} else {
		s.irqMode = 0
	}
}

func (s *Scheduler) SetIRQMode(mode uint8) {
	s.irqMode = mode & 0x03
	if s.irqMode == 0 {
		s.irqFlag = false
		if irqTarget, ok := s.cpu.(interface{ ClearIRQ() }); ok {
			irqTarget.ClearIRQ()
		}
	}
}

func (s *Scheduler) SetIRQTimer(h, v uint16) {
	s.irqH = h
	s.irqV = v
}

func (s *Scheduler) ReadTIMEUP() uint8 {
	if !s.irqFlag {
		return 0
	}
	s.irqFlag = false
	if irqTarget, ok := s.cpu.(interface{ ClearIRQ() }); ok {
		irqTarget.ClearIRQ()
	}
	return 0x80
}

func (s *Scheduler) SetHTimerLow(val uint8) {
	s.irqH = (s.irqH & 0xFF00) | uint16(val)
}

func (s *Scheduler) SetHTimerHigh(val uint8) {
	s.irqH = (s.irqH & 0x00FF) | (uint16(val) << 8)
}

func (s *Scheduler) SetVTimerLow(val uint8) {
	s.irqV = (s.irqV & 0xFF00) | uint16(val)
}

func (s *Scheduler) SetVTimerHigh(val uint8) {
	s.irqV = (s.irqV & 0x00FF) | (uint16(val) << 8)
}

// beamSource supplies authoritative field-start and vblank-edge timestamps in
// master clocks. The scheduler separately records observed and returned edges.
type beamSource interface {
	BeamEvents() (frameStart, vblankStart uint64)
}

// quietSource reports how far the PPU can lag the CPU without any Run having
// an effect outside the PPU (see PPU.QuietUntil).
type quietSource interface {
	QuietUntil() uint64
}

// untilRunner runs a thread clocked like the CPU until its cycle count
// reaches a target, equivalent to calling Run while GetCycles is below it.
type untilRunner interface {
	RunUntil(cycles uint64)
}

// SyncPPULazy is Sync(ppu) for callers that only need the PPU's beam events
// delivered on time: it does nothing while the PPU is quiet and every event
// has been observed. Callers that read or write PPU state must use Sync.
func (s *Scheduler) SyncPPULazy() {
	if s.cpu == nil {
		return
	}
	now := s.cpu.GetCycles()
	if now <= s.quietThrough {
		return
	}
	if s.ppuQuiet != nil && s.ppuFreq == s.cpuFreq {
		if until := s.ppuQuiet.QuietUntil(); now <= until {
			frame, vblank := s.ppuBeam.BeamEvents()
			if frame <= s.beamFrameStart && vblank <= s.beamVBlankStart {
				s.quietThrough = until
				return
			}
		}
	}
	s.Sync(s.ppu)
}

func (s *Scheduler) observeBeamEvents() {
	s.quietThrough = 0
	if s.ppuBeam == nil || s.cpu == nil {
		return
	}
	frame, vblank := s.ppuBeam.BeamEvents()
	now := s.cpu.GetCycles()
	if frame > s.beamFrameStart && frame <= now {
		s.beamFrameStart = frame
		s.nmiTriggered = false
		s.irqTriggered = false
		s.irqLine = ^uint64(0)
	}
	if vblank > s.beamVBlankStart && vblank <= now {
		s.beamVBlankStart = vblank
		s.nmiTriggered = false
		if s.nmiEnabled {
			if target, ok := s.cpu.(interface{ TriggerNMI() }); ok {
				target.TriggerNMI()
			}
			s.nmiTriggered = true
		}
	}
}

func (s *Scheduler) runBeamDisplayFrame(beam beamSource) {
	if s.cpu == nil {
		return
	}
	// A display call requests the next boundary after entry, even when a
	// full-frame call or explicit synchronization has already crossed an edge.
	s.syncToMaster(s.ppu, s.cpu.GetCycles(), SyncSafety)
	_, entered := beam.BeamEvents()
	if entered <= s.cpu.GetCycles() && entered > s.displayEvent {
		s.displayEvent = entered
	}
	for {
		s.observeBeamEvents()
		_, event := beam.BeamEvents()
		if event > s.displayEvent && event <= s.cpu.GetCycles() {
			s.displayEvent = event
			s.syncAllSafety(s.cpu.GetCycles())
			s.clock = s.cpu.GetCycles()
			return
		}
		start := s.cpu.GetCycles()
		frame, _ := beam.BeamEvents()
		s.runCPU()
		end := s.cpu.GetCycles()
		if end == start {
			if _, ok := s.cpu.(interface{ AddCycles(uint64) }); !ok {
				return
			}
			s.AddCycles(2)
			continue
		}
		if frame <= start {
			s.triggerIRQBetween(start-frame, end-frame)
		}
		s.syncAll(end)
	}
}

// runCPU accounts for waits delivered to the cartridge during suspended bus
// cycles, so instruction retirement delivers only the remaining CPU clocks.
func (s *Scheduler) runCPU() {
	start := s.cpu.GetCycles()
	s.runningCPU, s.deliveredCPU = true, 0
	s.cpu.Run()
	s.runningCPU = false
	elapsed := s.cpu.GetCycles() - start
	s.stepAfterCPU(elapsed - s.deliveredCPU)
	s.deliveredCPU = 0
}

// AddDMACycles advances the suspended CPU clock without another CPU bus edge.
// It must be called outside a target thread's Run method.
func (s *Scheduler) AddDMACycles(clocks uint64) {
	c, ok := s.cpu.(interface{ AdvanceDMA(uint64) })
	if !ok || clocks == 0 {
		return
	}
	before := s.cpu.GetCycles()
	c.AdvanceDMA(clocks)
	s.stepAfterCPU(s.cpu.GetCycles() - before)
	s.syncAll(s.cpu.GetCycles())
}
