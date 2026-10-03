package decomp

import (
	"strings"
	"testing"
)

func TestSemanticManifestControls(t *testing.T) {
	r := semanticTestRegion(t, []byte{0xa5, 0x10, 0x69, 5, 0x85, 0x11})
	s, err := GenerateSemanticRegionC(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateSemanticSource(r, s); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*SemanticSource)
	}{
		{"source", func(s *SemanticSource) {
			s.Source = strings.Replace(s.Source, "+ 0x05 +", "+ 0x06 +", 1)
			s.SourceSHA256 = semanticHash([]byte(s.Source))
		}},
		{"original", func(s *SemanticSource) { s.OriginalSHA256 = strings.Repeat("a", 64) }},
		{"region", func(s *SemanticSource) { s.RegionSHA256 = strings.Repeat("a", 64) }},
		{"provenance", func(s *SemanticSource) { s.Expressions[0].Instructions = []string{"forged"} }},
		{"source map", func(s *SemanticSource) { s.SourceMap[0].Line++ }},
		{"width", func(s *SemanticSource) { s.Expressions[0].Width = Width16 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := s
			copy.Expressions = append([]SemanticExpression{}, s.Expressions...)
			copy.SourceMap = append([]SemanticLine{}, s.SourceMap...)
			tc.mutate(&copy)
			if ValidateSemanticSource(r, copy) == nil {
				t.Fatal("forged manifest accepted")
			}
		})
	}
}

func TestSemanticNoSupportedValues(t *testing.T) {
	r := semanticTestRegion(t, []byte{0xea})
	if _, err := GenerateSemanticRegionC(r); err == nil {
		t.Fatal("unsupported region accepted")
	}
}

func TestSemanticManifestControlsWithIdioms(t *testing.T) {
	// LDA $10; CMP #$80; SBC $10; EOR #$FF; STA $11; ASL A; ASL A; STA $12
	code := []byte{
		0xa5, 0x10,
		0xc9, 0x80, 0xe5, 0x10, 0x49, 0xff, 0x85, 0x11,
		0x0a, 0x0a, 0x85, 0x12,
	}
	r := semanticTestRegion(t, code)
	s, err := GenerateSemanticRegionC(r)
	if err != nil {
		t.Fatal(err)
	}
	if s.TransformedSignExtensions != 1 {
		t.Fatalf("expected 1 sign extension, got %d", s.TransformedSignExtensions)
	}
	if s.TransformedShifts != 1 {
		t.Fatalf("expected 1 shift cascade, got %d", s.TransformedShifts)
	}
	if err = ValidateSemanticSource(r, s); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*SemanticSource)
	}{
		{"sign_extensions_count", func(s *SemanticSource) { s.TransformedSignExtensions++ }},
		{"shifts_count", func(s *SemanticSource) { s.TransformedShifts++ }},
		{"operation_name", func(s *SemanticSource) { s.Expressions[1].Operation = "FORGED" }},
		{"expression_tamper", func(s *SemanticSource) { s.Expressions[1].Expression = "forged" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := s
			copy.Expressions = append([]SemanticExpression{}, s.Expressions...)
			copy.SourceMap = append([]SemanticLine{}, s.SourceMap...)
			tc.mutate(&copy)
			if ValidateSemanticSource(r, copy) == nil {
				t.Fatal("forged manifest accepted")
			}
		})
	}
}

func TestSemantic16BitMode(t *testing.T) {
	tests := []struct {
		name      string
		code      []byte
		wantLoads int
		wantADC   int
		wantSBC   int
		wantWidth Width
		contains  []string
	}{
		{
			name:      "rep_16bit_dp_load_store",
			code:      []byte{0xc2, 0x20, 0xa5, 0x10, 0x85, 0x12}, // REP #$20; LDA $10; STA $12
			wantLoads: 1,
			wantWidth: Width16,
			contains:  []string{"uint16_t value_", "mem_write16_bank0", "read16_bank0("},
		},
		{
			name:      "rep_16bit_imm_load_store",
			code:      []byte{0xc2, 0x20, 0xa9, 0x34, 0x12, 0x85, 0x10}, // REP #$20; LDA #$1234; STA $10
			wantLoads: 1,
			wantWidth: Width16,
			contains:  []string{"uint16_t value_", "0x1234", "mem_write16_bank0"},
		},
		{
			name:      "mode_switch_rep_sep",
			code:      []byte{0xc2, 0x20, 0xa5, 0x10, 0x85, 0x12, 0xe2, 0x20, 0xa5, 0x14, 0x85, 0x16}, // REP #$20; LDA $10; STA $12; SEP #$20; LDA $14; STA $16
			wantLoads: 2,
			contains:  []string{"uint16_t value_", "mem_write16_bank0", "uint8_t value_", "mem_write8"},
		},
		{
			name:      "rep_16bit_adc",
			code:      []byte{0xc2, 0x20, 0x18, 0xa5, 0x10, 0x69, 0x00, 0x01, 0x85, 0x12}, // REP #$20; CLC; LDA $10; ADC #$0100; STA $12
			wantLoads: 1,
			wantADC:   1,
			wantWidth: Width16,
			contains:  []string{"uint16_t value_", "0x0100", "> 0xFFFF"},
		},
		{
			name:      "rep_16bit_sbc",
			code:      []byte{0xc2, 0x20, 0x38, 0xa5, 0x10, 0xe9, 0x50, 0x00, 0x85, 0x12}, // REP #$20; SEC; LDA $10; SBC #$0050; STA $12
			wantLoads: 1,
			wantSBC:   1,
			wantWidth: Width16,
			contains:  []string{"uint16_t value_", "0x0050", "> 0xFFFF"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := semanticTestRegion(t, tt.code)
			s, err := GenerateSemanticRegionC(r)
			if err != nil {
				t.Fatalf("GenerateSemanticRegionC failed: %v", err)
			}
			if s.TransformedLoads != tt.wantLoads {
				t.Errorf("TransformedLoads = %d, want %d", s.TransformedLoads, tt.wantLoads)
			}
			if s.TransformedADC != tt.wantADC {
				t.Errorf("TransformedADC = %d, want %d", s.TransformedADC, tt.wantADC)
			}
			if s.TransformedSBC != tt.wantSBC {
				t.Errorf("TransformedSBC = %d, want %d", s.TransformedSBC, tt.wantSBC)
			}
			if tt.wantWidth != 0 && len(s.Expressions) > 0 && s.Expressions[0].Width != tt.wantWidth {
				t.Errorf("Expression[0].Width = %v, want %v", s.Expressions[0].Width, tt.wantWidth)
			}
			for _, sub := range tt.contains {
				if !strings.Contains(s.Source, sub) {
					t.Errorf("expected source to contain %q:\n%s", sub, s.Source)
				}
			}
			if err := ValidateSemanticSource(r, s); err != nil {
				t.Fatalf("ValidateSemanticSource failed: %v", err)
			}
		})
	}
}

func TestSemanticCarryChains(t *testing.T) {
	tests := []struct {
		name        string
		code        []byte
		wantLoads   int
		wantADC     int
		wantSBC     int
		wantFused   int
		wantOps     []string
		checkSecond bool
	}{
		{
			name:      "adc_2byte_chain",
			code:      []byte{0x18, 0xa5, 0x10, 0x65, 0x20, 0x85, 0x30, 0xa5, 0x11, 0x65, 0x21, 0x85, 0x31}, // CLC; LDA $10; ADC $20; STA $30; LDA $11; ADC $21; STA $31
			wantLoads: 2,
			wantADC:   2,
			wantFused: 1,
			wantOps:   []string{"LDA", "ADC", "LDA", "ADC_CHAIN"},
		},
		{
			name:      "sbc_2byte_chain",
			code:      []byte{0x38, 0xa5, 0x10, 0xe5, 0x20, 0x85, 0x30, 0xa5, 0x11, 0xe5, 0x21, 0x85, 0x31}, // SEC; LDA $10; SBC $20; STA $30; LDA $11; SBC $21; STA $31
			wantLoads: 2,
			wantSBC:   2,
			wantFused: 1,
			wantOps:   []string{"LDA", "SBC", "LDA", "SBC_CHAIN"},
		},
		{
			name:      "adc_3byte_chain",
			code:      []byte{0x18, 0xa5, 0x10, 0x65, 0x20, 0x85, 0x30, 0xa5, 0x11, 0x65, 0x21, 0x85, 0x31, 0xa5, 0x12, 0x65, 0x22, 0x85, 0x32},
			wantLoads: 3,
			wantADC:   3,
			wantFused: 2,
			wantOps:   []string{"LDA", "ADC", "LDA", "ADC_CHAIN", "LDA", "ADC_CHAIN"},
		},
		{
			name:      "adc_broken_chain_by_clc",
			code:      []byte{0x18, 0xa5, 0x10, 0x65, 0x20, 0x85, 0x30, 0x18, 0xa5, 0x11, 0x65, 0x21, 0x85, 0x31}, // second CLC breaks carry chain
			wantLoads: 2,
			wantADC:   2,
			wantFused: 0,
			wantOps:   []string{"LDA", "ADC", "LDA", "ADC"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := semanticTestRegion(t, tt.code)
			s, err := GenerateSemanticRegionC(r)
			if err != nil {
				t.Fatalf("GenerateSemanticRegionC failed: %v", err)
			}
			if s.TransformedLoads != tt.wantLoads {
				t.Errorf("TransformedLoads = %d, want %d", s.TransformedLoads, tt.wantLoads)
			}
			if s.TransformedADC != tt.wantADC {
				t.Errorf("TransformedADC = %d, want %d", s.TransformedADC, tt.wantADC)
			}
			if s.TransformedSBC != tt.wantSBC {
				t.Errorf("TransformedSBC = %d, want %d", s.TransformedSBC, tt.wantSBC)
			}
			if s.FusedChains != tt.wantFused {
				t.Errorf("FusedChains = %d, want %d", s.FusedChains, tt.wantFused)
			}
			if len(s.Expressions) != len(tt.wantOps) {
				t.Fatalf("len(Expressions) = %d, want %d", len(s.Expressions), len(tt.wantOps))
			}
			for i, op := range tt.wantOps {
				if s.Expressions[i].Operation != op {
					t.Errorf("Expression[%d].Operation = %q, want %q", i, s.Expressions[i].Operation, op)
				}
			}
			if err := ValidateSemanticSource(r, s); err != nil {
				t.Fatalf("ValidateSemanticSource failed: %v", err)
			}

			// Verify provenance: chained operation must include the low-byte instruction ID
			if tt.wantFused > 0 {
				lowOpID := s.Expressions[1].Instructions[len(s.Expressions[1].Instructions)-1]
				highOpInstructions := s.Expressions[3].Instructions
				found := false
				for _, id := range highOpInstructions {
					if id == lowOpID {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("chained operation provenance does not contain low byte instruction %q: %v", lowOpID, highOpInstructions)
				}
			}
		})
	}
}

func TestSemanticSequentialBlockPropagation(t *testing.T) {
	tests := []struct {
		name       string
		code       []byte
		wantBlocks int
		wantLoads  int
		wantADC    int
		wantFused  int
		contains   []string
	}{
		{
			name:       "cross_block_accumulator_store",
			code:       []byte{0xa5, 0x10, 0x80, 0x00, 0x85, 0x11}, // Block 1: LDA $10; BRA +0. Block 2: STA $11
			wantBlocks: 2,
			wantLoads:  1,
			contains:   []string{"mem_write8(&res,", "value_"},
		},
		{
			name:       "cross_block_carry_chain",
			code:       []byte{0x18, 0xa5, 0x10, 0x65, 0x20, 0x85, 0x30, 0x80, 0x00, 0xa5, 0x11, 0x65, 0x21, 0x85, 0x31}, // Block 1: CLC; LDA $10; ADC $20; STA $30; BRA +0. Block 2: LDA $11; ADC $21; STA $31
			wantBlocks: 2,
			wantLoads:  2,
			wantADC:    2,
			wantFused:  1,
			contains:   []string{"value_", "ADC_CHAIN"},
		},
		{
			name:       "cross_block_rep_16bit",
			code:       []byte{0xc2, 0x20, 0x80, 0x00, 0xa5, 0x10, 0x85, 0x12}, // Block 1: REP #$20; BRA +0. Block 2: LDA $10; STA $12
			wantBlocks: 2,
			wantLoads:  1,
			contains:   []string{"uint16_t value_", "mem_write16_bank0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := semanticTestRegion(t, tt.code)
			if len(r.Blocks) != tt.wantBlocks {
				t.Fatalf("len(Blocks) = %d, want %d", len(r.Blocks), tt.wantBlocks)
			}
			s, err := GenerateSemanticRegionC(r)
			if err != nil {
				t.Fatalf("GenerateSemanticRegionC failed: %v", err)
			}
			if s.TransformedLoads != tt.wantLoads {
				t.Errorf("TransformedLoads = %d, want %d", s.TransformedLoads, tt.wantLoads)
			}
			if s.TransformedADC != tt.wantADC {
				t.Errorf("TransformedADC = %d, want %d", s.TransformedADC, tt.wantADC)
			}
			if s.FusedChains != tt.wantFused {
				t.Errorf("FusedChains = %d, want %d", s.FusedChains, tt.wantFused)
			}
			for _, sub := range tt.contains {
				if sub == "ADC_CHAIN" {
					found := false
					for _, expr := range s.Expressions {
						if expr.Operation == "ADC_CHAIN" {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected expression with operation ADC_CHAIN")
					}
				} else if !strings.Contains(s.Source, sub) {
					t.Errorf("expected source to contain %q:\n%s", sub, s.Source)
				}
			}
			if err := ValidateSemanticSource(r, s); err != nil {
				t.Fatalf("ValidateSemanticSource failed: %v", err)
			}
		})
	}
}
