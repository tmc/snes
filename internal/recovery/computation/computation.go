package computation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/recovery/divergence"
)

// DependencyKind categorizes whether a value is used for computation or address calculation.
type DependencyKind string

const (
	// DependencyData represents values operated on (e.g. accumulator operands, arithmetic data).
	DependencyData DependencyKind = "data"

	// DependencyAddress represents indexing registers (e.g. X, Y) or base registers used for address calculation.
	DependencyAddress DependencyKind = "address"
)

// Dependency describes a single value dependency between instructions or external inputs.
type Dependency struct {
	Kind      DependencyKind `json:"kind"`
	Source    string         `json:"source"`
	Target    string         `json:"target"`
	Value     uint64         `json:"value"`
	StepIndex int            `json:"step_index"` // -1 if external input
}

// InputVariable describes a free input into the extracted slice.
type InputVariable struct {
	Name         string `json:"name"`
	Type         string `json:"type"` // "uint8" or "uint16"
	Source       string `json:"source"`
	Address      uint32 `json:"address,omitempty"`
	InitialValue uint64 `json:"initial_value"`
}

// SliceInstruction represents an instruction retained in the backward slice.
type SliceInstruction struct {
	StepIndex int          `json:"step_index"`
	Address   uint32       `json:"address"`
	Opcode    byte         `json:"opcode"`
	Mnemonic  string       `json:"mnemonic"`
	Operands  string       `json:"operands"`
	DataDeps  []Dependency `json:"data_deps,omitempty"`
	AddrDeps  []Dependency `json:"addr_deps,omitempty"`
}

// OutputEffect describes the target observable state change produced by the computation.
type OutputEffect struct {
	Address     uint32 `json:"address"`
	Value       uint64 `json:"value"`
	Width       int    `json:"width"`
	StepIndex   int    `json:"step_index"`
	Description string `json:"description"`
}

// Computation is an extracted backward slice with identified inputs, dependencies, and generated C.
type Computation struct {
	Target       OutputEffect       `json:"target"`
	Instructions []SliceInstruction `json:"instructions"`
	Inputs       []InputVariable    `json:"inputs"`
	Dependencies []Dependency       `json:"dependencies"`
	GeneratedC   string             `json:"generated_c"`

	// ROMData stores recorded ROM bytes referenced by the slice for evaluation.
	ROMData map[uint32]uint8 `json:"rom_data,omitempty"`
}

// ExtractOptions configures backward computation extraction.
type ExtractOptions struct {
	// MaxBackwardSteps limits the backward search depth. Zero means unlimited.
	MaxBackwardSteps int `json:"max_backward_steps"`

	// StopAtMemoryBoundary treats RAM reads as external inputs rather than tracing to earlier writes.
	StopAtMemoryBoundary bool `json:"stop_at_memory_boundary"`
}

// IsROMAddress reports whether an address falls in cartridge ROM space.
func IsROMAddress(addr uint32) bool {
	bank := (addr >> 16) & 0xFF
	offset := addr & 0xFFFF
	if (bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && offset >= 0x8000 {
		return true
	}
	if (bank >= 0x40 && bank <= 0x7D) || bank >= 0xC0 {
		return true
	}
	return false
}

type needItem struct {
	kind        DependencyKind
	isMem       bool
	memAddr     uint32
	reg         string
	targetDesc  string
	expectedVal uint64
	consumerIdx int
}

type stepDepRecord struct {
	dep         Dependency
	consumerIdx int
}

// Extract extracts a backward computation slice starting at targetStepIndex.
func Extract(steps []divergence.Step, targetStepIndex int, opts ExtractOptions) (*Computation, error) {
	if len(steps) == 0 {
		return nil, fmt.Errorf("extract computation: empty steps trace")
	}
	if targetStepIndex < 0 || targetStepIndex >= len(steps) {
		return nil, fmt.Errorf("extract computation: target step index %d out of bounds [0, %d)", targetStepIndex, len(steps))
	}

	targetStep := steps[targetStepIndex]
	mnem := stepMnemonic(targetStep)
	hasWrite := len(targetStep.Writes) > 0
	defs := stepDefinedRegisters(targetStep)

	if !hasWrite && len(defs) == 0 {
		return nil, fmt.Errorf("extract computation: target step %d (%s) has no memory write or register definition", targetStepIndex, mnem)
	}

	var target OutputEffect
	if hasWrite {
		w := targetStep.Writes[0]
		width := w.Width
		if width == 0 {
			width = 8
		}
		target = OutputEffect{
			Address:     w.Address,
			Value:       uint64(w.Value),
			Width:       width,
			StepIndex:   targetStepIndex,
			Description: fmt.Sprintf("store 0x%02X to %s", w.Value, divergence.FormatAddress(w.Address)),
		}
	} else {
		regName := defs[0]
		val := stepExitRegister(targetStep, regName)
		width := 8
		if targetStep.Registers.P&0x20 == 0 && (regName == "RegA") {
			width = 16
		} else if targetStep.Registers.P&0x10 == 0 && (regName == "RegX" || regName == "RegY") {
			width = 16
		}
		target = OutputEffect{
			Address:     0,
			Value:       val,
			Width:       width,
			StepIndex:   targetStepIndex,
			Description: fmt.Sprintf("compute %s = 0x%02X", regName, val),
		}
	}

	var needed []needItem
	var stepDeps []stepDepRecord
	romData := make(map[uint32]uint8)
	sliceIndices := make(map[int]bool)
	sliceIndices[targetStepIndex] = true

	// Add input requirements for the target step.
	entryRegsTarget := stepEntryRegisters(steps, targetStepIndex)
	if hasWrite {
		w := targetStep.Writes[0]
		dataReg := storeSourceRegister(mnem)
		if dataReg != "" {
			needed = append(needed, needItem{
				kind:        DependencyData,
				reg:         dataReg,
				targetDesc:  fmt.Sprintf("Mem:%s", divergence.FormatAddress(w.Address)),
				expectedVal: uint64(w.Value),
				consumerIdx: targetStepIndex,
			})
		}
		if idxReg, ok := stepIndexRegister(targetStep); ok {
			needed = append(needed, needItem{
				kind:        DependencyAddress,
				reg:         idxReg,
				targetDesc:  "Addr",
				expectedVal: stepRegisterVal(entryRegsTarget, idxReg),
				consumerIdx: targetStepIndex,
			})
		}
	} else {
		// Target step defines register; add its inputs to needed.
		addStepInputs(targetStep, targetStepIndex, entryRegsTarget, opts.StopAtMemoryBoundary, &needed, &stepDeps, romData)
	}

	minStep := 0
	if opts.MaxBackwardSteps > 0 && targetStepIndex-opts.MaxBackwardSteps > 0 {
		minStep = targetStepIndex - opts.MaxBackwardSteps
	}

	// Backward traversal from targetStepIndex - 1 down to minStep.
	for i := targetStepIndex - 1; i >= minStep && len(needed) > 0; i-- {
		currStep := steps[i]
		currDefs := stepDefinedRegisters(currStep)
		currEntryRegs := stepEntryRegisters(steps, i)

		// Check if currStep defines any needed item.
		var satisfiedIndices []int
		for ni, item := range needed {
			if item.isMem {
				if !opts.StopAtMemoryBoundary {
					for _, w := range currStep.Writes {
						if w.Address == item.memAddr {
							satisfiedIndices = append(satisfiedIndices, ni)
							sliceIndices[i] = true
							stepDeps = append(stepDeps, stepDepRecord{
								dep: Dependency{
									Kind:      item.kind,
									Source:    fmt.Sprintf("Mem:%s", divergence.FormatAddress(w.Address)),
									Target:    item.targetDesc,
									Value:     item.expectedVal,
									StepIndex: i,
								},
								consumerIdx: item.consumerIdx,
							})
							break
						}
					}
				}
			} else {
				for _, d := range currDefs {
					if d == item.reg {
						satisfiedIndices = append(satisfiedIndices, ni)
						sliceIndices[i] = true
						stepDeps = append(stepDeps, stepDepRecord{
							dep: Dependency{
								Kind:      item.kind,
								Source:    item.reg,
								Target:    item.targetDesc,
								Value:     item.expectedVal,
								StepIndex: i,
							},
							consumerIdx: item.consumerIdx,
						})
						break
					}
				}
			}
		}

		if len(satisfiedIndices) > 0 {
			// Remove satisfied needs in reverse index order.
			sort.Sort(sort.Reverse(sort.IntSlice(satisfiedIndices)))
			for _, idx := range satisfiedIndices {
				needed = append(needed[:idx], needed[idx+1:]...)
			}
			// Add currStep's own inputs to needed.
			addStepInputs(currStep, i, currEntryRegs, opts.StopAtMemoryBoundary, &needed, &stepDeps, romData)
		}
	}

	// Any remaining items in needed are free external inputs.
	var inputs []InputVariable
	seenInput := make(map[string]bool)

	for _, item := range needed {
		if item.isMem {
			src := fmt.Sprintf("Mem:%s", divergence.FormatAddress(item.memAddr))
			varName := formatInputName(item.memAddr)
			if !seenInput[src] {
				seenInput[src] = true
				inputs = append(inputs, InputVariable{
					Name:         varName,
					Type:         "uint8",
					Source:       src,
					Address:      item.memAddr,
					InitialValue: item.expectedVal,
				})
			}
			stepDeps = append(stepDeps, stepDepRecord{
				dep: Dependency{
					Kind:      item.kind,
					Source:    src,
					Target:    item.targetDesc,
					Value:     item.expectedVal,
					StepIndex: -1,
				},
				consumerIdx: item.consumerIdx,
			})
		} else {
			src := item.reg
			varName := "entry_" + strings.ToLower(strings.TrimPrefix(item.reg, "Reg"))
			if !seenInput[src] {
				seenInput[src] = true
				inputs = append(inputs, InputVariable{
					Name:         varName,
					Type:         "uint8",
					Source:       src,
					Address:      0,
					InitialValue: item.expectedVal,
				})
			}
			stepDeps = append(stepDeps, stepDepRecord{
				dep: Dependency{
					Kind:      item.kind,
					Source:    src,
					Target:    item.targetDesc,
					Value:     item.expectedVal,
					StepIndex: -1,
				},
				consumerIdx: item.consumerIdx,
			})
		}
	}

	// Collect all dependencies.
	var allDeps []Dependency
	for _, sd := range stepDeps {
		allDeps = append(allDeps, sd.dep)
	}

	// Collect and sort slice instructions.
	var orderedIndices []int
	for idx := range sliceIndices {
		orderedIndices = append(orderedIndices, idx)
	}
	sort.Ints(orderedIndices)

	var instructions []SliceInstruction
	for _, idx := range orderedIndices {
		st := steps[idx]
		m := stepMnemonic(st)
		entryR := stepEntryRegisters(steps, idx)

		// Filter dependencies consumed by this instruction.
		var dataDeps []Dependency
		var addrDeps []Dependency
		for _, sd := range stepDeps {
			if sd.consumerIdx == idx {
				if sd.dep.Kind == DependencyData {
					dataDeps = append(dataDeps, sd.dep)
				} else {
					addrDeps = append(addrDeps, sd.dep)
				}
			}
		}

		operands := formatOperands(st, m, entryR)
		instructions = append(instructions, SliceInstruction{
			StepIndex: idx,
			Address:   st.Address,
			Opcode:    st.Opcode,
			Mnemonic:  m,
			Operands:  operands,
			DataDeps:  dataDeps,
			AddrDeps:  addrDeps,
		})
	}

	// Sort inputs deterministically.
	sort.Slice(inputs, func(i, j int) bool {
		return inputs[i].Name < inputs[j].Name
	})

	comp := &Computation{
		Target:       target,
		Instructions: instructions,
		Inputs:       inputs,
		Dependencies: allDeps,
		ROMData:      romData,
	}

	comp.GeneratedC = generateC(comp)
	return comp, nil
}

func addStepInputs(st divergence.Step, idx int, entryRegs divergence.RegisterState, stopAtMem bool, needed *[]needItem, stepDeps *[]stepDepRecord, romData map[uint32]uint8) {
	m := stepMnemonic(st)

	// Address dependencies (index registers).
	if idxReg, ok := stepIndexRegister(st); ok {
		*needed = append(*needed, needItem{
			kind:        DependencyAddress,
			reg:         idxReg,
			targetDesc:  "Addr",
			expectedVal: stepRegisterVal(entryRegs, idxReg),
			consumerIdx: idx,
		})
	}

	// Memory reads.
	for _, read := range st.Reads {
		destReg := loadDestRegister(m)
		if destReg == "" {
			destReg = "RegA"
		}
		if IsROMAddress(read.Address) {
			romData[read.Address] = uint8(read.Value)
			*stepDeps = append(*stepDeps, stepDepRecord{
				dep: Dependency{
					Kind:      DependencyData,
					Source:    fmt.Sprintf("ROM:%s", divergence.FormatAddress(read.Address)),
					Target:    destReg,
					Value:     uint64(read.Value),
					StepIndex: -1,
				},
				consumerIdx: idx,
			})
		} else {
			*needed = append(*needed, needItem{
				kind:        DependencyData,
				isMem:       true,
				memAddr:     read.Address,
				targetDesc:  destReg,
				expectedVal: uint64(read.Value),
				consumerIdx: idx,
			})
		}
	}

	// Register data dependencies.
	for _, reg := range stepUsedRegisters(st, m) {
		*needed = append(*needed, needItem{
			kind:        DependencyData,
			reg:         reg,
			targetDesc:  reg,
			expectedVal: stepRegisterVal(entryRegs, reg),
			consumerIdx: idx,
		})
	}
}

func stepMnemonic(step divergence.Step) string {
	if step.Mnemonic != "" {
		return strings.ToUpper(step.Mnemonic)
	}
	if int(step.Opcode) < len(cpu.Opcodes) {
		return strings.ToUpper(cpu.Opcodes[step.Opcode].Name)
	}
	return ""
}

func stepDefinedRegisters(step divergence.Step) []string {
	m := stepMnemonic(step)
	switch m {
	case "LDA", "PLA", "TXA", "TYA":
		return []string{"RegA"}
	case "ADC", "SBC":
		return []string{"RegA", "RegC"}
	case "AND", "ORA", "EOR":
		return []string{"RegA"}
	case "CLC", "SEC":
		return []string{"RegC"}
	case "LDX", "TAX", "TSX", "TYX", "INX", "DEX":
		return []string{"RegX"}
	case "LDY", "TAY", "TXY", "INY", "DEY":
		return []string{"RegY"}
	case "INC", "DEC":
		if len(step.Writes) == 0 {
			return []string{"RegA"}
		}
	case "ASL", "LSR", "ROL", "ROR":
		if len(step.Writes) == 0 {
			return []string{"RegA", "RegC"}
		}
		return []string{"RegC"}
	}
	return nil
}

func stepUsedRegisters(step divergence.Step, m string) []string {
	switch m {
	case "STA":
		return []string{"RegA"}
	case "STX":
		return []string{"RegX"}
	case "STY":
		return []string{"RegY"}
	case "TAX", "TAY":
		return []string{"RegA"}
	case "TXA":
		return []string{"RegX"}
	case "TYA":
		return []string{"RegY"}
	case "ADC", "SBC":
		return []string{"RegA", "RegC"}
	case "AND", "ORA", "EOR":
		return []string{"RegA"}
	case "INX", "DEX":
		return []string{"RegX"}
	case "INY", "DEY":
		return []string{"RegY"}
	case "INC", "DEC":
		if len(step.Writes) == 0 {
			return []string{"RegA"}
		}
	case "ASL", "LSR":
		if len(step.Writes) == 0 {
			return []string{"RegA"}
		}
	case "ROL", "ROR":
		if len(step.Writes) == 0 {
			return []string{"RegA", "RegC"}
		}
		return []string{"RegC"}
	}
	return nil
}

func stepIndexRegister(step divergence.Step) (string, bool) {
	if int(step.Opcode) < len(cpu.Opcodes) {
		mode := cpu.Opcodes[step.Opcode].Mode
		switch mode {
		case cpu.AddrAbsX, cpu.AddrDirX, cpu.AddrLongX, cpu.AddrIndX, cpu.AddrAbsIndX:
			return "RegX", true
		case cpu.AddrAbsY, cpu.AddrDirY, cpu.AddrIndY, cpu.AddrSrIndY, cpu.AddrDirIndLIdxY:
			return "RegY", true
		}
	}
	m := strings.ToUpper(step.Mnemonic)
	if strings.Contains(m, ",X") {
		return "RegX", true
	}
	if strings.Contains(m, ",Y") {
		return "RegY", true
	}
	return "", false
}

func storeSourceRegister(m string) string {
	switch m {
	case "STA":
		return "RegA"
	case "STX":
		return "RegX"
	case "STY":
		return "RegY"
	}
	return ""
}

func loadDestRegister(m string) string {
	switch m {
	case "LDA":
		return "RegA"
	case "LDX":
		return "RegX"
	case "LDY":
		return "RegY"
	}
	return ""
}

func stepEntryRegisters(steps []divergence.Step, i int) divergence.RegisterState {
	zero := divergence.RegisterState{}
	if steps[i].EntryRegisters != zero {
		return steps[i].EntryRegisters
	}
	if i > 0 {
		return steps[i-1].Registers
	}
	return steps[0].Registers
}

func stepRegisterVal(regs divergence.RegisterState, reg string) uint64 {
	switch reg {
	case "RegA":
		return uint64(regs.A)
	case "RegX":
		return uint64(regs.X)
	case "RegY":
		return uint64(regs.Y)
	case "RegC":
		return uint64(regs.P & 1)
	}
	return 0
}

func stepExitRegister(step divergence.Step, reg string) uint64 {
	return stepRegisterVal(step.Registers, reg)
}

func formatInputName(addr uint32) string {
	bank := (addr >> 16) & 0xFF
	offset := addr & 0xFFFF
	if bank == 0x00 && offset < 0x0100 {
		return fmt.Sprintf("in_%02x", offset)
	}
	if bank == 0x7E || bank == 0x00 {
		return fmt.Sprintf("in_%x", offset)
	}
	return fmt.Sprintf("in_%02x%04x", bank, offset)
}

func formatOperands(st divergence.Step, m string, entryR divergence.RegisterState) string {
	var mode cpu.AddressingMode = cpu.AddrImpl
	if int(st.Opcode) < len(cpu.Opcodes) {
		mode = cpu.Opcodes[st.Opcode].Mode
	}

	switch mode {
	case cpu.AddrImpl:
		return ""
	case cpu.AddrAcc:
		return "A"
	case cpu.AddrImm:
		var imm uint64
		if len(st.Reads) > 0 {
			imm = uint64(st.Reads[0].Value)
		} else {
			switch m {
			case "ADC":
				imm = (uint64(st.Registers.A) - uint64(entryR.A) - uint64(entryR.P&1)) & 0xFF
			case "SBC":
				imm = (uint64(entryR.A) - uint64(st.Registers.A) - uint64(1-(entryR.P&1))) & 0xFF
			case "EOR":
				imm = (uint64(st.Registers.A) ^ uint64(entryR.A)) & 0xFF
			case "ORA":
				imm = (uint64(st.Registers.A) ^ uint64(entryR.A)) & 0xFF
			case "LDA":
				imm = uint64(st.Registers.A)
			case "LDX":
				imm = uint64(st.Registers.X)
			case "LDY":
				imm = uint64(st.Registers.Y)
			default:
				imm = uint64(st.Registers.A)
			}
		}
		return fmt.Sprintf("#$%02X", imm)
	}

	if len(st.Reads) > 0 {
		readAddr := st.Reads[0].Address
		if idxReg, ok := stepIndexRegister(st); ok {
			var base uint32
			if idxReg == "RegX" {
				base = readAddr - uint32(entryR.X)
			} else {
				base = readAddr - uint32(entryR.Y)
			}
			return fmt.Sprintf("$%06X,%s", base, strings.TrimPrefix(idxReg, "Reg"))
		}
		if (readAddr >> 16) == 0 && readAddr < 0x100 {
			return fmt.Sprintf("$%02X", readAddr)
		}
		if (readAddr >> 16) == 0 {
			return fmt.Sprintf("$%04X", readAddr)
		}
		return fmt.Sprintf("$%06X", readAddr)
	}

	if len(st.Writes) > 0 {
		writeAddr := st.Writes[0].Address
		if idxReg, ok := stepIndexRegister(st); ok {
			var base uint32
			if idxReg == "RegX" {
				base = writeAddr - uint32(entryR.X)
			} else {
				base = writeAddr - uint32(entryR.Y)
			}
			return fmt.Sprintf("$%06X,%s", base, strings.TrimPrefix(idxReg, "Reg"))
		}
		if (writeAddr >> 16) == 0 && writeAddr < 0x100 {
			return fmt.Sprintf("$%02X", writeAddr)
		}
		if (writeAddr >> 16) == 0 {
			return fmt.Sprintf("$%04X", writeAddr)
		}
		return fmt.Sprintf("$%06X", writeAddr)
	}

	return ""
}

func getInputValue(inp InputVariable, inputs map[string]uint64) uint64 {
	if inputs != nil {
		if v, ok := inputs[inp.Name]; ok {
			return v
		}
	}
	return inp.InitialValue
}

func findInputBySource(comp *Computation, source string) (InputVariable, bool) {
	for _, inp := range comp.Inputs {
		if inp.Source == source {
			return inp, true
		}
	}
	return InputVariable{}, false
}

// Execute simulates the extracted computation on given inputs, verifying output correctness.
func Execute(comp *Computation, inputs map[string]uint64) (uint64, error) {
	if comp == nil {
		return 0, fmt.Errorf("execute computation: nil computation")
	}

	regA := uint64(0)
	regX := uint64(0)
	regY := uint64(0)
	regC := uint64(0)
	mem := make(map[uint32]uint64)

	// Apply inputs or default to initial values.
	for _, inp := range comp.Inputs {
		val := getInputValue(inp, inputs)
		if inp.Source == "RegA" {
			regA = val
		} else if inp.Source == "RegX" {
			regX = val
		} else if inp.Source == "RegY" {
			regY = val
		} else if inp.Source == "RegC" {
			regC = val & 1
		} else if inp.Address != 0 {
			mem[inp.Address] = val
		}
	}

	mask := uint64(0xFF)
	if comp.Target.Width == 16 {
		mask = 0xFFFF
	}

	var lastStoreVal uint64
	for _, insn := range comp.Instructions {
		m := insn.Mnemonic

		// Determine effective address for indexed loads/stores.
		var effAddr uint32
		var baseAddr uint32
		var hasBase bool

		if strings.Contains(insn.Operands, ",") {
			parts := strings.Split(insn.Operands, ",")
			if len(parts) == 2 {
				hexStr := strings.TrimPrefix(strings.TrimSpace(parts[0]), "$")
				var b uint32
				if n, _ := fmt.Sscanf(hexStr, "%X", &b); n > 0 {
					baseAddr = b
					hasBase = true
					if parts[1] == "Y" {
						effAddr = baseAddr + uint32(regY)
					} else if parts[1] == "X" {
						effAddr = baseAddr + uint32(regX)
					}
				}
			}
		}

		switch m {
		case "LDY":
			val := uint64(0)
			if len(insn.DataDeps) > 0 {
				dd := insn.DataDeps[0]
				if inp, ok := findInputBySource(comp, dd.Source); ok {
					val = getInputValue(inp, inputs)
				} else {
					val = dd.Value
				}
			}
			regY = val & mask

		case "LDX":
			val := uint64(0)
			if len(insn.DataDeps) > 0 {
				dd := insn.DataDeps[0]
				if inp, ok := findInputBySource(comp, dd.Source); ok {
					val = getInputValue(inp, inputs)
				} else {
					val = dd.Value
				}
			}
			regX = val & mask

		case "LDA":
			if strings.HasPrefix(insn.Operands, "#") {
				var imm uint64
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &imm)
				regA = imm & mask
			} else if hasBase {
				// Table lookup.
				if v, ok := mem[effAddr]; ok {
					regA = v & mask
				} else if comp.ROMData != nil {
					if v, ok := comp.ROMData[effAddr]; ok {
						regA = uint64(v) & mask
					} else if len(insn.DataDeps) > 0 {
						regA = insn.DataDeps[0].Value & mask
					}
				} else if len(insn.DataDeps) > 0 {
					regA = insn.DataDeps[0].Value & mask
				}
			} else {
				val := uint64(0)
				if len(insn.DataDeps) > 0 {
					dd := insn.DataDeps[0]
					if inp, ok := findInputBySource(comp, dd.Source); ok {
						val = getInputValue(inp, inputs)
					} else {
						val = dd.Value
					}
				}
				regA = val & mask
			}

		case "STA":
			dest := comp.Target.Address
			if hasBase {
				dest = effAddr
			}
			mem[dest] = regA & mask
			lastStoreVal = regA & mask

		case "STX":
			dest := comp.Target.Address
			if hasBase {
				dest = effAddr
			}
			mem[dest] = regX & mask
			lastStoreVal = regX & mask

		case "STY":
			dest := comp.Target.Address
			if hasBase {
				dest = effAddr
			}
			mem[dest] = regY & mask
			lastStoreVal = regY & mask

		case "CLC":
			regC = 0

		case "SEC":
			regC = 1

		case "ADC":
			opVal := uint64(0)
			if strings.HasPrefix(insn.Operands, "#") {
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &opVal)
			} else {
				for _, dd := range insn.DataDeps {
					if strings.HasPrefix(dd.Source, "Mem:") {
						if inp, ok := findInputBySource(comp, dd.Source); ok {
							opVal = getInputValue(inp, inputs)
						} else {
							opVal = dd.Value
						}
						break
					}
				}
			}
			sum := regA + opVal + (regC & 1)
			if sum > mask {
				regC = 1
			} else {
				regC = 0
			}
			regA = sum & mask

		case "SBC":
			opVal := uint64(0)
			if strings.HasPrefix(insn.Operands, "#") {
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &opVal)
			} else {
				for _, dd := range insn.DataDeps {
					if strings.HasPrefix(dd.Source, "Mem:") {
						if inp, ok := findInputBySource(comp, dd.Source); ok {
							opVal = getInputValue(inp, inputs)
						} else {
							opVal = dd.Value
						}
						break
					}
				}
			}
			diff := regA - opVal - (1 - (regC & 1))
			if regA >= opVal+(1-(regC&1)) {
				regC = 1
			} else {
				regC = 0
			}
			regA = diff & mask

		case "AND":
			opVal := uint64(0)
			if strings.HasPrefix(insn.Operands, "#") {
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &opVal)
			}
			regA = (regA & opVal) & mask

		case "ORA":
			opVal := uint64(0)
			if strings.HasPrefix(insn.Operands, "#") {
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &opVal)
			}
			regA = (regA | opVal) & mask

		case "EOR":
			opVal := uint64(0)
			if strings.HasPrefix(insn.Operands, "#") {
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &opVal)
			}
			regA = (regA ^ opVal) & mask

		case "TAX":
			regX = regA

		case "TXA":
			regA = regX

		case "TAY":
			regY = regA

		case "TYA":
			regA = regY

		case "INX":
			regX = (regX + 1) & mask

		case "DEX":
			regX = (regX - 1) & mask

		case "INY":
			regY = (regY + 1) & mask

		case "DEY":
			regY = (regY - 1) & mask
		}
	}

	if comp.Target.Address != 0 {
		return lastStoreVal, nil
	}
	return regA, nil
}

func generateC(comp *Computation) string {
	var sb strings.Builder
	sb.WriteString("#include <stdint.h>\n\n")

	cType := "uint8_t"
	if comp.Target.Width == 16 {
		cType = "uint16_t"
	}

	// Function signature.
	var paramParts []string
	for _, inp := range comp.Inputs {
		t := "uint8_t"
		if inp.Type == "uint16" {
			t = "uint16_t"
		}
		paramParts = append(paramParts, fmt.Sprintf("%s %s", t, inp.Name))
	}
	params := "void"
	if len(paramParts) > 0 {
		params = strings.Join(paramParts, ", ")
	}

	sb.WriteString(fmt.Sprintf("/* Target: %s */\n", comp.Target.Description))
	sb.WriteString(fmt.Sprintf("%s compute(%s) {\n", cType, params))

	declared := make(map[string]bool)
	decl := func(reg string) string {
		if !declared[reg] {
			declared[reg] = true
			return fmt.Sprintf("%s %s = ", cType, reg)
		}
		return fmt.Sprintf("%s = ", reg)
	}

	// Emit table definition if ROM reads exist.
	var romBase uint32
	hasRomTable := false
	for _, insn := range comp.Instructions {
		if strings.Contains(insn.Operands, ",") {
			parts := strings.Split(insn.Operands, ",")
			if len(parts) == 2 {
				hexStr := strings.TrimPrefix(strings.TrimSpace(parts[0]), "$")
				var b uint32
				if n, _ := fmt.Sscanf(hexStr, "%X", &b); n > 0 && IsROMAddress(b) {
					romBase = b
					hasRomTable = true
					break
				}
			}
		}
	}

	if hasRomTable && len(comp.ROMData) > 0 {
		sb.WriteString(fmt.Sprintf("    static const uint8_t rom_%06x[256] = {\n", romBase))
		var keys []uint32
		for k := range comp.ROMData {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		for _, k := range keys {
			offset := k - romBase
			sb.WriteString(fmt.Sprintf("        [%d] = 0x%02X,\n", offset, comp.ROMData[k]))
		}
		sb.WriteString("    };\n")
	}

	for _, insn := range comp.Instructions {
		m := insn.Mnemonic
		switch m {
		case "LDY":
			src := "0"
			for _, inp := range comp.Inputs {
				if len(insn.DataDeps) > 0 && inp.Source == insn.DataDeps[0].Source {
					src = inp.Name
					break
				}
			}
			sb.WriteString(fmt.Sprintf("    %s%s;\n", decl("y"), src))

		case "LDX":
			src := "0"
			for _, inp := range comp.Inputs {
				if len(insn.DataDeps) > 0 && inp.Source == insn.DataDeps[0].Source {
					src = inp.Name
					break
				}
			}
			sb.WriteString(fmt.Sprintf("    %s%s;\n", decl("x"), src))

		case "LDA":
			if strings.HasPrefix(insn.Operands, "#") {
				sb.WriteString(fmt.Sprintf("    %s%s;\n", decl("a"), strings.TrimPrefix(insn.Operands, "#$")))
			} else if hasRomTable && strings.Contains(insn.Operands, ",Y") {
				sb.WriteString(fmt.Sprintf("    %srom_%06x[y];\n", decl("a"), romBase))
			} else if hasRomTable && strings.Contains(insn.Operands, ",X") {
				sb.WriteString(fmt.Sprintf("    %srom_%06x[x];\n", decl("a"), romBase))
			} else {
				src := "0"
				for _, inp := range comp.Inputs {
					if len(insn.DataDeps) > 0 && inp.Source == insn.DataDeps[0].Source {
						src = inp.Name
						break
					}
				}
				sb.WriteString(fmt.Sprintf("    %s%s;\n", decl("a"), src))
			}

		case "CLC":
			sb.WriteString(fmt.Sprintf("    %s0;\n", decl("c")))

		case "SEC":
			sb.WriteString(fmt.Sprintf("    %s1;\n", decl("c")))

		case "ADC":
			op := "0"
			if strings.HasPrefix(insn.Operands, "#") {
				var imm uint64
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &imm)
				op = fmt.Sprintf("%d", imm)
			} else {
				for _, dd := range insn.DataDeps {
					for _, inp := range comp.Inputs {
						if inp.Source == dd.Source {
							op = inp.Name
							break
						}
					}
				}
			}
			cTerm := ""
			if declared["c"] {
				cTerm = " + c"
			}
			maskHex := "0xFF"
			if comp.Target.Width == 16 {
				maskHex = "0xFFFF"
			}
			sb.WriteString(fmt.Sprintf("    a = (a + %s%s) & %s;\n", op, cTerm, maskHex))

		case "SBC":
			op := "0"
			if strings.HasPrefix(insn.Operands, "#") {
				var imm uint64
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &imm)
				op = fmt.Sprintf("%d", imm)
			} else {
				for _, dd := range insn.DataDeps {
					for _, inp := range comp.Inputs {
						if inp.Source == dd.Source {
							op = inp.Name
							break
						}
					}
				}
			}
			cTerm := ""
			if declared["c"] {
				cTerm = " - (1 - c)"
			}
			maskHex := "0xFF"
			if comp.Target.Width == 16 {
				maskHex = "0xFFFF"
			}
			sb.WriteString(fmt.Sprintf("    a = (a - %s%s) & %s;\n", op, cTerm, maskHex))

		case "TAX":
			sb.WriteString(fmt.Sprintf("    %sa;\n", decl("x")))

		case "TXA":
			sb.WriteString(fmt.Sprintf("    %sx;\n", decl("a")))

		case "TAY":
			sb.WriteString(fmt.Sprintf("    %sa;\n", decl("y")))

		case "TYA":
			sb.WriteString(fmt.Sprintf("    %sy;\n", decl("a")))

		case "STA", "STX", "STY":
			// Handled by final return.
		}
	}

	retVar := "a"
	if strings.Contains(comp.Target.Description, "RegX") || comp.Instructions[len(comp.Instructions)-1].Mnemonic == "STX" {
		retVar = "x"
	} else if strings.Contains(comp.Target.Description, "RegY") || comp.Instructions[len(comp.Instructions)-1].Mnemonic == "STY" {
		retVar = "y"
	}
	sb.WriteString(fmt.Sprintf("    return %s;\n", retVar))
	sb.WriteString("}\n")

	return sb.String()
}
