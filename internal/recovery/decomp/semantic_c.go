package decomp

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

// SemanticSource binds newly generated executable C to its original machine IR.
// It is a transformation artifact, not an admission or replay receipt.
type SemanticSource struct {
	Schema                    string               `json:"schema"`
	OriginalSHA256            string               `json:"original_sha256"`
	SourceSHA256              string               `json:"source_sha256"`
	RegionSHA256              string               `json:"region_sha256"`
	Source                    string               `json:"source"`
	Expressions               []SemanticExpression `json:"expressions"`
	SourceMap                 []SemanticLine       `json:"source_map"`
	FusedChains               int                  `json:"fused_chains"`
	TransformedLoads          int                  `json:"transformed_loads"`
	TransformedADC            int                  `json:"transformed_adc"`
	TransformedSBC            int                  `json:"transformed_sbc,omitempty"`
	TransformedSignExtensions int                  `json:"transformed_sign_extensions,omitempty"`
	TransformedShifts         int                  `json:"transformed_shifts,omitempty"`
	Scope                     string               `json:"scope"`
}

// GenerateSemanticRegionC recovers symbolic load, arithmetic, and idiom expressions.
// Other statements retain their original machine-semantic lowering.
// Memory reads, stores and architectural updates are neither removed nor reordered.
func GenerateSemanticRegionC(region *RegionIR) (SemanticSource, error) {
	original, err := GenerateRegionC(region)
	if err != nil {
		return SemanticSource{}, err
	}
	replacements, expressions := recoverBlocks(region.Blocks)
	if len(expressions) == 0 {
		return SemanticSource{}, fmt.Errorf("semantic C: no supported local values")
	}
	source, err := generateRegionC(region, replacements)
	if err != nil {
		return SemanticSource{}, err
	}
	loads, adc, sbc, signExt, shifts, fused := 0, 0, 0, 0, 0, 0
	for _, e := range expressions {
		switch e.Operation {
		case "LDA":
			loads++
		case "ADC":
			adc++
		case "ADC_CHAIN":
			adc++
			fused++
		case "SBC":
			sbc++
		case "SBC_CHAIN":
			sbc++
			fused++
		case "SIGN_EXTEND":
			signExt++
		case "ASL", "ASL_CASCADE":
			shifts++
		}
	}
	encoded, err := json.Marshal(region)
	if err != nil {
		return SemanticSource{}, fmt.Errorf("semantic C region identity: %w", err)
	}
	return SemanticSource{
		Schema:                    "snes-semantic-local-values-v1",
		OriginalSHA256:            semanticHash([]byte(original)),
		SourceSHA256:              semanticHash([]byte(source)),
		RegionSHA256:              semanticHash(encoded),
		Source:                    source,
		Expressions:               expressions,
		SourceMap:                 semanticLines(source, expressions),
		FusedChains:               fused,
		TransformedLoads:          loads,
		TransformedADC:            adc,
		TransformedSBC:            sbc,
		TransformedSignExtensions: signExt,
		TransformedShifts:         shifts,
		Scope:                     "symbolic expressions with full architectural writeback; entry A/carry remain runtime inputs; sequential basic block value propagation",
	}, nil
}

func semanticHash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

// ValidateSemanticSource reconstructs the transformation from typed IR and checks
// its complete manifest. It establishes freshness, not execution qualification.
func ValidateSemanticSource(region *RegionIR, source SemanticSource) error {
	expected, err := GenerateSemanticRegionC(region)
	if err != nil {
		return err
	}
	a, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	b, err := json.Marshal(source)
	if err != nil {
		return err
	}
	if string(a) != string(b) {
		return fmt.Errorf("semantic C: source or transformation manifest differs")
	}
	return nil
}

// SemanticLine maps a generated local declaration to its defining instruction.
// It maps generated values, not a guessed game-level variable or fused handler.
type SemanticLine struct {
	Line         int      `json:"line"`
	Address      uint32   `json:"address"`
	Name         string   `json:"name"`
	Instructions []string `json:"instructions"`
}

func semanticLines(source string, expressions []SemanticExpression) []SemanticLine {
	var result []SemanticLine
	lines := strings.Split(source, "\n")
	for _, e := range expressions {
		for i, line := range lines {
			if strings.Contains(line, "uint8_t "+e.Name+" =") ||
				strings.Contains(line, "uint16_t "+e.Name+" =") ||
				strings.Contains(line, "uint32_t "+e.Name+" =") {
				result = append(result, SemanticLine{i + 1, e.Address, e.Name, append([]string{}, e.Instructions...)})
				break
			}
		}
	}
	return result
}
