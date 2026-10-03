package decomp

import (
	"testing"
)

func TestReplayFreshnessClearsCapturedProof(t *testing.T) {
	for _, name := range []string{"nil case", "tampered case hash", "different case", "different identity", "standard freshness"} {
		t.Run(name, func(t *testing.T) {
			c := ReplayCase{CaseID: "current"}
			PopulateHashes(&c)
			r := ReplayReceipt{Eligible: true, CapturedProofEligible: true, Matched: true, CaseHash: c.CaseHash, CaseIdentity: c.Identity()}
			current := &c
			switch name {
			case "nil case":
				current = nil
			case "tampered case hash":
				c.CaseHash = "invalid"
			case "different case":
				r.CaseHash = "invalid"
			case "different identity":
				r.CaseIdentity.CaseID = "invalid"
			}
			ValidateReplayReceiptFreshness(&r, current, nil, "", "rom", "revision")
			if r.Eligible || r.CapturedProofEligible || !r.Metadata.IsStale {
				t.Fatalf("stale proof flags: %+v", r)
			}
		})
	}
}

func TestRoutineRegionCloneRejectsInvalid(t *testing.T) {
	for _, name := range []string{"nil block", "nil expression", "cycle"} {
		t.Run(name, func(t *testing.T) {
			region := &RegionIR{Blocks: []*BlockIR{{Statements: []Statement{{}}}}}
			switch name {
			case "nil block":
				region.Blocks[0] = nil
			case "nil expression":
				var expr *ConstExpr
				region.Blocks[0].Statements[0].Expr = expr
			case "cycle":
				expr := &UnaryExpr{}
				expr.Expr = expr
				region.Blocks[0].Statements[0].Expr = expr
			}
			if _, err := cloneRoutineRegion(region); err == nil {
				t.Fatal("invalid region accepted")
			}
		})
	}
}
