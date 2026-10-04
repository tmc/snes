package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestValueChainCard(t *testing.T) {
	projectDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(projectDir); err != nil {
		t.Skipf("natural producer project not found at %s: %v", projectDir, err)
		return
	}

	srv, err := NewServer(projectDir)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/provenance/value-chain", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", w.Code, w.Body.String())
	}

	var card ValueChainCard
	if err := json.Unmarshal(w.Body.Bytes(), &card); err != nil {
		t.Fatalf("unmarshal value chain card failed: %v", err)
	}

	if card.Status != "available" {
		t.Fatalf("expected status=available, got %q (reason: %s)", card.Status, card.Reason)
	}
	if !card.GuardsMatched {
		t.Fatalf("expected guards_matched=true")
	}

	if len(card.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(card.Nodes))
	}
	if card.Nodes[0].RetirementID != 52077 || card.Nodes[0].Seq != 13199 {
		t.Errorf("node 0 mismatch: retirement %d seq %d", card.Nodes[0].RetirementID, card.Nodes[0].Seq)
	}
	if card.Nodes[1].RetirementID != 52082 || card.Nodes[1].Seq != 13200 {
		t.Errorf("node 1 mismatch: retirement %d seq %d", card.Nodes[1].RetirementID, card.Nodes[1].Seq)
	}
	if card.Nodes[2].RetirementID != 52086 || card.Nodes[2].Seq != 13201 {
		t.Errorf("node 2 mismatch: retirement %d seq %d", card.Nodes[2].RetirementID, card.Nodes[2].Seq)
	}

	if len(card.DependencyEdges) != 4 {
		t.Fatalf("expected 4 dependency edges, got %d", len(card.DependencyEdges))
	}

	edges := card.DependencyEdges
	if edges[0].Kind != "data" || edges[0].From != "bus52076/WRAM7E1F05=115" || edges[0].To != "retirement52077/Y=115" {
		t.Errorf("edge 0 mismatch: %+v", edges[0])
	}
	if edges[1].Kind != "address" || edges[1].IndexValue != 115 || edges[1].EffectiveAddress != "$09:FBE0" {
		t.Errorf("edge 1 mismatch: %+v", edges[1])
	}
	if edges[2].Kind != "data" || edges[2].From != "bus52081/ROMoffset04FBE0=20" || edges[2].To != "retirement52082/A.low=20" {
		t.Errorf("edge 2 mismatch: %+v", edges[2])
	}
	if edges[3].Kind != "data" || edges[3].From != "retirement52082/A.low=20" || edges[3].To != "bus52085/WRAM7E1F54=20" {
		t.Errorf("edge 3 mismatch: %+v", edges[3])
	}

	if card.VersionRelation.EarlierDirectWRAMWriterID != 30147 || card.VersionRelation.LaterReplacementWriteID != 139219 {
		t.Errorf("version relation mismatch: %+v", card.VersionRelation)
	}
}
