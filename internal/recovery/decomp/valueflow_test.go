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
