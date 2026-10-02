package decomp

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestTimedSemanticIdentityMaterial(t *testing.T) {
	region, rom, plan := timedFixture(t)
	base, err := GenerateTimedRegionC(region, rom, plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	edit, err := GenerateTimedRegionC(region, rom, plan, &TimedImmediateEdit{Address: 0x8004, Expected: 5, Replacement: 6})
	if err != nil {
		t.Fatal(err)
	}
	if timedHash(base.OriginalIRJSON) != base.IRSHA256 || timedHash(edit.EditedIRJSON) != edit.EditedIRSHA256 {
		t.Fatal("IR material digest mismatch")
	}
	if !bytes.Equal(base.OriginalIRJSON, edit.OriginalIRJSON) || bytes.Equal(base.EditedIRJSON, edit.EditedIRJSON) {
		t.Fatal("semantic material does not separate edit")
	}
	var material struct {
		Schema string
		Blocks []timedBlockIdentity
	}
	if err = json.Unmarshal(edit.EditedIRJSON, &material); err != nil {
		t.Fatal(err)
	}
	if material.Schema != "timed-machine-ir-v1" {
		t.Fatal("missing schema")
	}
	found := false
	for _, b := range material.Blocks {
		if b.Metadata.StartAddress != 0x8004 {
			continue
		}
		for _, s := range b.Statements {
			if s.Expression != nil && s.Expression.Kind == "binary" && s.Expression.Left != nil && s.Expression.Left.Right != nil && s.Expression.Left.Right.Kind == "constant" && s.Expression.Left.Right.Value == 6 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("edited immediate not present in typed material")
	}
	other := append([]byte(nil), rom...)
	other[100] ^= 1
	changed, err := GenerateTimedRegionC(region, other, plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changed.ROMSHA256 == base.ROMSHA256 || changed.SourceSHA256 != base.SourceSHA256 || changed.IRSHA256 != base.IRSHA256 {
		t.Fatal("unrelated ROM identity separation failed")
	}
}

func TestTimedCanonicalExpressionFields(t *testing.T) {
	base := &BlockIR{Statements: []Statement{{Kind: "assign_reg", Expr: &ConstExpr{Value: 5, Width: Width8}, ExprString: "stale", Condition: &FlagExpr{Flag: FlagZ}, MemAddress: &RegExpr{Reg: RegD, Width: Width16}}}}
	original, err := timedSemanticIR([]*BlockIR{base})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"expression", "condition", "address", "effect", "cached text"} {
		t.Run(kind, func(t *testing.T) {
			b := *base
			b.Statements = append([]Statement(nil), base.Statements...)
			switch kind {
			case "expression":
				b.Statements[0].Expr = &ConstExpr{Value: 6, Width: Width8}
			case "condition":
				b.Statements[0].Condition = &FlagExpr{Flag: FlagC}
			case "address":
				b.Statements[0].MemAddress = &RegExpr{Reg: RegS, Width: Width16}
			case "effect":
				b.Statements[0].AffectsN = true
			case "cached text":
				b.Statements[0].ExprString = "different stale"
				b.Statements[0].CondString = "stale too"
			}
			changed, e := timedSemanticIR([]*BlockIR{&b})
			if e != nil {
				t.Fatal(e)
			}
			if bytes.Equal(original, changed) != (kind == "cached text") {
				t.Fatal("wrong canonical field identity")
			}
		})
	}
	cycle := &UnaryExpr{}
	cycle.Expr = cycle
	if _, err := timedExpression(cycle, 0); err == nil {
		t.Fatal("cyclic/deep expression accepted")
	}
}
