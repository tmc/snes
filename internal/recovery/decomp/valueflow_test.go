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
