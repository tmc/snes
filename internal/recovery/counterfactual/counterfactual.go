package counterfactual

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tmc/snes/internal/recovery/computation"
	"github.com/tmc/snes/internal/recovery/divergence"
)

// Status constants for CaseResult.
const (
	StatusBaselineMatch       = "baseline_match"
	StatusPredictedDivergence = "predicted_divergence"
	StatusExecutionError      = "execution_error"
)

// Perturbation defines a named modification to computation inputs.
type Perturbation struct {
	Name        string            `json:"name"`
	Overrides   map[string]uint64 `json:"overrides"`
	Description string            `json:"description"`
}

// CaseResult records the outcome of a single perturbation run.
type CaseResult struct {
	Perturbation     Perturbation       `json:"perturbation"`
	Inputs           map[string]uint64  `json:"inputs"`
	OutputValue      uint64             `json:"output_value"`
	DivergenceReport *divergence.Report `json:"divergence_report,omitempty"`
	OutputDiffers    bool               `json:"output_differs"`
	Status           string             `json:"status"` // "baseline_match", "predicted_divergence", "execution_error"
	Error            error              `json:"error,omitempty"`
}

// InputSensitivity reports whether variations to an input variable affect the output.
type InputSensitivity struct {
	Variable     string   `json:"variable"`
	TestedValues []uint64 `json:"tested_values"`
	OutputValues []uint64 `json:"output_values"`
	Sensitive    bool     `json:"sensitive"`
}

// WorkbenchReport summarizes the results of running the counterfactual workbench.
type WorkbenchReport struct {
	Target         computation.OutputEffect `json:"target"`
	BaselineInputs map[string]uint64        `json:"baseline_inputs"`
	BaselineOutput uint64                   `json:"baseline_output"`
	Cases          []CaseResult             `json:"cases"`
	Sensitivities  []InputSensitivity       `json:"sensitivities"`
	Summary        string                   `json:"summary"`
}

// Workbench coordinates counterfactual execution experiments over an extracted computation.
type Workbench struct {
	Computation    *computation.Computation `json:"computation"`
	BaselineInputs map[string]uint64        `json:"baseline_inputs"`
	Perturbations  []Perturbation           `json:"perturbations"`
}

// NewWorkbench initializes a counterfactual workbench for the given computation.
// It verifies that baseline execution succeeds and captures the baseline input state.
func NewWorkbench(comp *computation.Computation) (*Workbench, error) {
	if comp == nil {
		return nil, fmt.Errorf("counterfactual: nil computation")
	}

	baselineInputs := make(map[string]uint64, len(comp.Inputs))
	for _, inp := range comp.Inputs {
		baselineInputs[inp.Name] = inp.InitialValue
	}

	// Validate baseline execution.
	if _, err := computation.Execute(comp, baselineInputs); err != nil {
		return nil, fmt.Errorf("counterfactual: baseline execution: %w", err)
	}

	return &Workbench{
		Computation:    comp,
		BaselineInputs: baselineInputs,
	}, nil
}

// AddPerturbation registers a new perturbation with the specified input overrides.
// It returns an error if any overridden variable does not exist in the computation inputs.
func (w *Workbench) AddPerturbation(name string, overrides map[string]uint64, desc string) error {
	if w == nil || w.Computation == nil {
		return fmt.Errorf("counterfactual: uninitialized workbench")
	}

	for k := range overrides {
		if !w.hasInput(k) {
			return fmt.Errorf("counterfactual: unknown variable %q", k)
		}
	}

	cp := make(map[string]uint64, len(overrides))
	for k, v := range overrides {
		cp[k] = v
	}

	w.Perturbations = append(w.Perturbations, Perturbation{
		Name:        name,
		Overrides:   cp,
		Description: desc,
	})
	return nil
}

// AddStandardNeighborhood generates perturbation cases around the baseline value of varName
// using the provided delta offsets (defaulting to -1 and +1).
func (w *Workbench) AddStandardNeighborhood(varName string, deltas ...int64) error {
	if w == nil || w.Computation == nil {
		return fmt.Errorf("counterfactual: uninitialized workbench")
	}
	if !w.hasInput(varName) {
		return fmt.Errorf("counterfactual: unknown variable %q", varName)
	}

	if len(deltas) == 0 {
		deltas = []int64{-1, 1}
	}

	baseVal := w.BaselineInputs[varName]
	mask := uint64(0xFF)
	inp := w.findInput(varName)
	if inp.Type == "uint16" || w.Computation.Target.Width == 16 {
		mask = 0xFFFF
	}

	for _, d := range deltas {
		newVal := uint64(int64(baseVal)+d) & mask
		name := fmt.Sprintf("%s%+d", varName, d)
		desc := fmt.Sprintf("%s with delta %+d (value %d)", varName, d, newVal)
		if err := w.AddPerturbation(name, map[string]uint64{varName: newVal}, desc); err != nil {
			return err
		}
	}
	return nil
}

// AddBoundaries adds perturbation cases for boundary values (0 and maximum width bound)
// for the specified variable.
func (w *Workbench) AddBoundaries(varName string) error {
	if w == nil || w.Computation == nil {
		return fmt.Errorf("counterfactual: uninitialized workbench")
	}
	if !w.hasInput(varName) {
		return fmt.Errorf("counterfactual: unknown variable %q", varName)
	}

	maxVal := uint64(0xFF)
	inp := w.findInput(varName)
	if inp.Type == "uint16" || w.Computation.Target.Width == 16 {
		maxVal = 0xFFFF
	}

	if err := w.AddPerturbation(fmt.Sprintf("%s_min", varName), map[string]uint64{varName: 0}, fmt.Sprintf("%s boundary min (0)", varName)); err != nil {
		return err
	}
	if err := w.AddPerturbation(fmt.Sprintf("%s_max", varName), map[string]uint64{varName: maxVal}, fmt.Sprintf("%s boundary max (%d)", varName, maxVal)); err != nil {
		return err
	}
	return nil
}

// Run executes baseline and all perturbation cases, compares traces with divergence analysis,
// performs input sensitivity analysis, and returns a WorkbenchReport.
func (w *Workbench) Run() (*WorkbenchReport, error) {
	if w == nil || w.Computation == nil {
		return nil, fmt.Errorf("counterfactual: uninitialized workbench")
	}

	// 1. Run baseline simulation.
	baseSteps, baseOut, err := w.simulate(w.BaselineInputs)
	if err != nil {
		return nil, fmt.Errorf("counterfactual: baseline run: %w", err)
	}

	// 2. Run each perturbation case.
	cases := make([]CaseResult, 0, len(w.Perturbations))
	for _, p := range w.Perturbations {
		caseInputs := make(map[string]uint64, len(w.BaselineInputs))
		for k, v := range w.BaselineInputs {
			caseInputs[k] = v
		}
		for k, v := range p.Overrides {
			caseInputs[k] = v
		}

		altSteps, altOut, simErr := w.simulate(caseInputs)
		if simErr != nil {
			cases = append(cases, CaseResult{
				Perturbation: p,
				Inputs:       caseInputs,
				Status:       StatusExecutionError,
				Error:        simErr,
			})
			continue
		}

		report, cmpErr := divergence.Compare(baseSteps, altSteps, divergence.Options{})
		if cmpErr != nil {
			cases = append(cases, CaseResult{
				Perturbation: p,
				Inputs:       caseInputs,
				OutputValue:  altOut,
				Status:       StatusExecutionError,
				Error:        cmpErr,
			})
			continue
		}

		outputDiffers := (altOut != baseOut)
		status := StatusBaselineMatch
		if outputDiffers || (report.Decision != nil && report.Decision.Category != divergence.DivergenceNone) {
			status = StatusPredictedDivergence
		}

		cases = append(cases, CaseResult{
			Perturbation:     p,
			Inputs:           caseInputs,
			OutputValue:      altOut,
			DivergenceReport: report,
			OutputDiffers:    outputDiffers,
			Status:           status,
		})
	}

	// 3. Compute sensitivity analysis.
	sensitivities := w.computeSensitivities(baseOut, cases)

	// 4. Build summary.
	summary := buildWorkbenchSummary(w.Computation.Target, baseOut, w.BaselineInputs, cases, sensitivities)

	return &WorkbenchReport{
		Target:         w.Computation.Target,
		BaselineInputs: w.BaselineInputs,
		BaselineOutput: baseOut,
		Cases:          cases,
		Sensitivities:  sensitivities,
		Summary:        summary,
	}, nil
}

func (w *Workbench) hasInput(name string) bool {
	for _, inp := range w.Computation.Inputs {
		if inp.Name == name {
			return true
		}
	}
	return false
}

func (w *Workbench) findInput(name string) computation.InputVariable {
	for _, inp := range w.Computation.Inputs {
		if inp.Name == name {
			return inp
		}
	}
	return computation.InputVariable{}
}

func (w *Workbench) computeSensitivities(baseOut uint64, cases []CaseResult) []InputSensitivity {
	sensitivities := make([]InputSensitivity, 0, len(w.Computation.Inputs))

	for _, inp := range w.Computation.Inputs {
		valMap := make(map[uint64]uint64)
		baseVal := w.BaselineInputs[inp.Name]
		valMap[baseVal] = baseOut

		for _, c := range cases {
			if c.Error != nil {
				continue
			}
			if _, overridden := c.Perturbation.Overrides[inp.Name]; overridden {
				if v, ok := c.Inputs[inp.Name]; ok {
					valMap[v] = c.OutputValue
				}
			}
		}

		testedValues := make([]uint64, 0, len(valMap))
		for val := range valMap {
			testedValues = append(testedValues, val)
		}
		sort.Slice(testedValues, func(i, j int) bool {
			return testedValues[i] < testedValues[j]
		})

		outputValues := make([]uint64, len(testedValues))
		sensitive := false
		for i, v := range testedValues {
			out := valMap[v]
			outputValues[i] = out
			if i > 0 && out != outputValues[0] {
				sensitive = true
			}
		}

		sensitivities = append(sensitivities, InputSensitivity{
			Variable:     inp.Name,
			TestedValues: testedValues,
			OutputValues: outputValues,
			Sensitive:    sensitive,
		})
	}

	return sensitivities
}

func buildWorkbenchSummary(target computation.OutputEffect, baseOut uint64, baseInputs map[string]uint64, cases []CaseResult, sensitivities []InputSensitivity) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Counterfactual Workbench: %s\n", target.Description))
	sb.WriteString(fmt.Sprintf("Baseline Output: %d", baseOut))

	if len(baseInputs) > 0 {
		var inputPairs []string
		for k, v := range baseInputs {
			inputPairs = append(inputPairs, fmt.Sprintf("%s=%d", k, v))
		}
		sort.Strings(inputPairs)
		sb.WriteString(fmt.Sprintf(" (inputs: %s)", strings.Join(inputPairs, ", ")))
	}
	sb.WriteString("\n")

	matches := 0
	divergences := 0
	errors := 0
	for _, c := range cases {
		switch c.Status {
		case StatusBaselineMatch:
			matches++
		case StatusPredictedDivergence:
			divergences++
		case StatusExecutionError:
			errors++
		}
	}
	sb.WriteString(fmt.Sprintf("Cases: %d total (%d baseline match, %d predicted divergence, %d error)\n",
		len(cases), matches, divergences, errors))

	if len(sensitivities) > 0 {
		sb.WriteString("Sensitivity Analysis:\n")
		for _, s := range sensitivities {
			status := "insensitive"
			if s.Sensitive {
				status = "sensitive"
			}
			sb.WriteString(fmt.Sprintf("  - %s: %s (tested %v -> outputs %v)\n",
				s.Variable, status, s.TestedValues, s.OutputValues))
		}
	}

	if len(cases) > 0 {
		sb.WriteString("Perturbation Outcomes:\n")
		for _, c := range cases {
			if c.Error != nil {
				sb.WriteString(fmt.Sprintf("  - %s: error: %v\n", c.Perturbation.Name, c.Error))
				continue
			}
			diffStr := fmt.Sprintf("output %d (matches baseline)", c.OutputValue)
			if c.OutputDiffers {
				diffStr = fmt.Sprintf("output %d (differs)", c.OutputValue)
			} else if c.Status != StatusBaselineMatch {
				diffStr = fmt.Sprintf("output %d", c.OutputValue)
			}
			divDesc := ""
			if c.DivergenceReport != nil && c.DivergenceReport.Decision != nil && c.DivergenceReport.Decision.Category != divergence.DivergenceNone {
				divDesc = fmt.Sprintf("; divergence: %s", c.DivergenceReport.Decision.Description)
				if c.DivergenceReport.FirstEffect != nil {
					divDesc += fmt.Sprintf("; effect: %s", c.DivergenceReport.FirstEffect.Description)
				}
			}
			sb.WriteString(fmt.Sprintf("  - %s: %s%s\n", c.Perturbation.Name, diffStr, divDesc))
		}
	}

	return strings.TrimSpace(sb.String())
}

func (w *Workbench) simulate(inputs map[string]uint64) ([]divergence.Step, uint64, error) {
	comp := w.Computation
	if len(comp.Instructions) == 0 {
		return nil, 0, fmt.Errorf("counterfactual: empty instructions in computation")
	}

	regA := uint64(0)
	regX := uint64(0)
	regY := uint64(0)
	regC := uint64(0)
	regZ := true
	regN := false
	mem := make(map[uint32]uint64)

	// Apply inputs or default to initial values.
	for _, inp := range comp.Inputs {
		val := inp.InitialValue
		if v, ok := inputs[inp.Name]; ok {
			val = v
		}
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
	signBit := uint64(0x80)
	widthBits := 8
	if comp.Target.Width == 16 {
		mask = 0xFFFF
		signBit = 0x8000
		widthBits = 16
	}

	findInputBySource := func(source string) (computation.InputVariable, bool) {
		for _, inp := range comp.Inputs {
			if inp.Source == source {
				return inp, true
			}
		}
		return computation.InputVariable{}, false
	}

	getInputValue := func(inp computation.InputVariable) uint64 {
		if v, ok := inputs[inp.Name]; ok {
			return v
		}
		return inp.InitialValue
	}

	getDataDepValue := func(insn computation.SliceInstruction) uint64 {
		if len(insn.DataDeps) > 0 {
			dd := insn.DataDeps[0]
			if inp, ok := findInputBySource(dd.Source); ok {
				return getInputValue(inp)
			}
			return dd.Value
		}
		return 0
	}

	addrToIdx := make(map[uint32]int, len(comp.Instructions))
	for i, insn := range comp.Instructions {
		addrToIdx[insn.Address] = i
	}

	var steps []divergence.Step
	var lastStoreVal uint64
	hasStore := false

	ip := 0
	const maxInstructions = 10000
	instructionsRun := 0

	for ip >= 0 && ip < len(comp.Instructions) && instructionsRun < maxInstructions {
		instructionsRun++
		insn := comp.Instructions[ip]
		m := insn.Mnemonic

		var reads []divergence.MemoryAccess
		var writes []divergence.MemoryAccess

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
					idxReg := strings.TrimSpace(parts[1])
					if idxReg == "Y" {
						effAddr = baseAddr + uint32(regY)
					} else if idxReg == "X" {
						effAddr = baseAddr + uint32(regX)
					}
				}
			}
		}

		nextIp := ip + 1
		isBranchInsn := false
		branchTaken := false
		var branchTarget uint32

		switch m {
		case "LDY":
			val := uint64(0)
			if strings.HasPrefix(insn.Operands, "#") {
				var imm uint64
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &imm)
				val = imm
			} else {
				val = getDataDepValue(insn)
				if len(insn.DataDeps) > 0 && strings.HasPrefix(insn.DataDeps[0].Source, "Mem:") {
					var rAddr uint32
					fmt.Sscanf(strings.TrimPrefix(insn.DataDeps[0].Source, "Mem:$"), "%X", &rAddr)
					reads = append(reads, divergence.MemoryAccess{Address: rAddr, Value: uint16(val), Width: widthBits})
				}
			}
			regY = val & mask
			regZ = (regY == 0)
			regN = (regY & signBit) != 0

		case "LDX":
			val := uint64(0)
			if strings.HasPrefix(insn.Operands, "#") {
				var imm uint64
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &imm)
				val = imm
			} else {
				val = getDataDepValue(insn)
				if len(insn.DataDeps) > 0 && strings.HasPrefix(insn.DataDeps[0].Source, "Mem:") {
					var rAddr uint32
					fmt.Sscanf(strings.TrimPrefix(insn.DataDeps[0].Source, "Mem:$"), "%X", &rAddr)
					reads = append(reads, divergence.MemoryAccess{Address: rAddr, Value: uint16(val), Width: widthBits})
				}
			}
			regX = val & mask
			regZ = (regX == 0)
			regN = (regX & signBit) != 0

		case "LDA":
			if strings.HasPrefix(insn.Operands, "#") {
				var imm uint64
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &imm)
				regA = imm & mask
			} else if hasBase {
				readVal := uint64(0)
				if v, ok := mem[effAddr]; ok {
					readVal = v & mask
				} else if comp.ROMData != nil {
					if v, ok := comp.ROMData[effAddr]; ok {
						readVal = uint64(v) & mask
					} else if len(insn.DataDeps) > 0 {
						readVal = insn.DataDeps[0].Value & mask
					}
				} else if len(insn.DataDeps) > 0 {
					readVal = insn.DataDeps[0].Value & mask
				}
				regA = readVal & mask
				reads = append(reads, divergence.MemoryAccess{Address: effAddr, Value: uint16(regA), Width: widthBits})
			} else {
				val := getDataDepValue(insn)
				if len(insn.DataDeps) > 0 && strings.HasPrefix(insn.DataDeps[0].Source, "Mem:") {
					var rAddr uint32
					fmt.Sscanf(strings.TrimPrefix(insn.DataDeps[0].Source, "Mem:$"), "%X", &rAddr)
					reads = append(reads, divergence.MemoryAccess{Address: rAddr, Value: uint16(val), Width: widthBits})
				}
				regA = val & mask
			}
			regZ = (regA == 0)
			regN = (regA & signBit) != 0

		case "STA":
			dest := comp.Target.Address
			if hasBase {
				dest = effAddr
			}
			mem[dest] = regA & mask
			lastStoreVal = regA & mask
			hasStore = true
			writes = append(writes, divergence.MemoryAccess{Address: dest, Value: uint16(regA & mask), Width: widthBits})

		case "STX":
			dest := comp.Target.Address
			if hasBase {
				dest = effAddr
			}
			mem[dest] = regX & mask
			lastStoreVal = regX & mask
			hasStore = true
			writes = append(writes, divergence.MemoryAccess{Address: dest, Value: uint16(regX & mask), Width: widthBits})

		case "STY":
			dest := comp.Target.Address
			if hasBase {
				dest = effAddr
			}
			mem[dest] = regY & mask
			lastStoreVal = regY & mask
			hasStore = true
			writes = append(writes, divergence.MemoryAccess{Address: dest, Value: uint16(regY & mask), Width: widthBits})

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
						if inp, ok := findInputBySource(dd.Source); ok {
							opVal = getInputValue(inp)
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
			regZ = (regA == 0)
			regN = (regA & signBit) != 0

		case "SBC":
			opVal := uint64(0)
			if strings.HasPrefix(insn.Operands, "#") {
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &opVal)
			} else {
				for _, dd := range insn.DataDeps {
					if strings.HasPrefix(dd.Source, "Mem:") {
						if inp, ok := findInputBySource(dd.Source); ok {
							opVal = getInputValue(inp)
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
			regZ = (regA == 0)
			regN = (regA & signBit) != 0

		case "AND":
			opVal := uint64(0)
			if strings.HasPrefix(insn.Operands, "#") {
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &opVal)
			}
			regA = (regA & opVal) & mask
			regZ = (regA == 0)
			regN = (regA & signBit) != 0

		case "ORA":
			opVal := uint64(0)
			if strings.HasPrefix(insn.Operands, "#") {
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &opVal)
			}
			regA = (regA | opVal) & mask
			regZ = (regA == 0)
			regN = (regA & signBit) != 0

		case "EOR":
			opVal := uint64(0)
			if strings.HasPrefix(insn.Operands, "#") {
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &opVal)
			}
			regA = (regA ^ opVal) & mask
			regZ = (regA == 0)
			regN = (regA & signBit) != 0

		case "TAX":
			regX = regA
			regZ = (regX == 0)
			regN = (regX & signBit) != 0

		case "TXA":
			regA = regX
			regZ = (regA == 0)
			regN = (regA & signBit) != 0

		case "TAY":
			regY = regA
			regZ = (regY == 0)
			regN = (regY & signBit) != 0

		case "TYA":
			regA = regY
			regZ = (regA == 0)
			regN = (regA & signBit) != 0

		case "INX":
			regX = (regX + 1) & mask
			regZ = (regX == 0)
			regN = (regX & signBit) != 0

		case "DEX":
			regX = (regX - 1) & mask
			regZ = (regX == 0)
			regN = (regX & signBit) != 0

		case "INY":
			regY = (regY + 1) & mask
			regZ = (regY == 0)
			regN = (regY & signBit) != 0

		case "DEY":
			regY = (regY - 1) & mask
			regZ = (regY == 0)
			regN = (regY & signBit) != 0

		case "CMP":
			opVal := uint64(0)
			if strings.HasPrefix(insn.Operands, "#") {
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &opVal)
			} else {
				opVal = getDataDepValue(insn)
			}
			if regA >= opVal {
				regC = 1
			} else {
				regC = 0
			}
			regZ = (regA == opVal)
			regN = ((regA - opVal) & signBit) != 0

		case "CPX":
			opVal := uint64(0)
			if strings.HasPrefix(insn.Operands, "#") {
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &opVal)
			}
			if regX >= opVal {
				regC = 1
			} else {
				regC = 0
			}
			regZ = (regX == opVal)
			regN = ((regX - opVal) & signBit) != 0

		case "CPY":
			opVal := uint64(0)
			if strings.HasPrefix(insn.Operands, "#") {
				fmt.Sscanf(strings.TrimPrefix(insn.Operands, "#$"), "%X", &opVal)
			}
			if regY >= opVal {
				regC = 1
			} else {
				regC = 0
			}
			regZ = (regY == opVal)
			regN = ((regY - opVal) & signBit) != 0

		case "BEQ", "BNE", "BCS", "BCC", "BMI", "BPL", "BVS", "BVC", "BRA", "JMP":
			isBranchInsn = true
			var tgt uint32
			hexStr := strings.TrimPrefix(strings.TrimSpace(insn.Operands), "$")
			fmt.Sscanf(hexStr, "%X", &tgt)
			branchTarget = tgt

			if m == "BRA" || m == "JMP" {
				branchTaken = true
			} else if len(insn.DataDeps) > 0 {
				val := getDataDepValue(insn)
				branchTaken = (val != 0)
			} else {
				switch m {
				case "BEQ":
					branchTaken = regZ
				case "BNE":
					branchTaken = !regZ
				case "BCS":
					branchTaken = (regC == 1)
				case "BCC":
					branchTaken = (regC == 0)
				case "BMI":
					branchTaken = regN
				case "BPL":
					branchTaken = !regN
				}
			}
		}

		pFlags := uint8(0)
		if widthBits == 8 {
			pFlags |= 0x30
		}
		if regC != 0 {
			pFlags |= 0x01
		}

		regState := divergence.RegisterState{
			A: uint16(regA),
			X: uint16(regX),
			Y: uint16(regY),
			P: pFlags,
		}

		steps = append(steps, divergence.Step{
			Address:   insn.Address,
			Opcode:    insn.Opcode,
			Mnemonic:  insn.Mnemonic,
			Registers: regState,
			Reads:     reads,
			Writes:    writes,
			Sequence:  len(steps),
		})

		if isBranchInsn {
			if branchTaken {
				if targetIdx, ok := addrToIdx[branchTarget]; ok {
					nextIp = targetIdx
				} else {
					steps = append(steps, divergence.Step{
						Address:   branchTarget,
						Opcode:    0xEA,
						Mnemonic:  "NOP",
						Registers: regState,
						Sequence:  len(steps),
					})
					break
				}
			} else {
				nextIp = ip + 1
			}
		}

		ip = nextIp
	}

	var outputVal uint64
	if hasStore || comp.Target.Address != 0 {
		outputVal = lastStoreVal
	} else if strings.Contains(comp.Target.Description, "RegX") {
		outputVal = regX
	} else if strings.Contains(comp.Target.Description, "RegY") {
		outputVal = regY
	} else {
		outputVal = regA
	}

	return steps, outputVal, nil
}
