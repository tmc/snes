package traceimport

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tmc/snes/internal/recovery"
)

// Merge merges an ImportResult into a Document deterministically and idempotently.
func Merge(doc *recovery.Document, res *ImportResult) (*MergeResult, error) {
	if doc == nil {
		return nil, errors.New("traceimport: document cannot be nil")
	}
	if res == nil {
		return nil, errors.New("traceimport: import result cannot be nil")
	}

	if res.RunMetadata != nil && doc.ROM.NormalizedSHA256 != "" {
		if !strings.EqualFold(doc.ROM.NormalizedSHA256, res.RunMetadata.ROM_SHA256) {
			return nil, fmt.Errorf("traceimport: document ROM hash %q does not match trace ROM hash %q",
				doc.ROM.NormalizedSHA256, res.RunMetadata.ROM_SHA256)
		}
	}

	mr := &MergeResult{}

	// 1. Evidence deduplication
	evMap := make(map[string]recovery.Evidence)
	for _, ev := range doc.Evidence {
		evMap[ev.ID] = ev
	}
	for _, ev := range res.Evidence {
		if _, ok := evMap[ev.ID]; !ok {
			evMap[ev.ID] = ev
			mr.EvidenceAdded++
		}
	}

	// 2. Instructions deduplication and evidence union
	instMap := make(map[string]recovery.Instruction)
	instEvMap := make(map[string]map[string]bool)
	for _, inst := range doc.Instructions {
		instMap[inst.ID] = inst
		m := make(map[string]bool, len(inst.Evidence))
		for _, e := range inst.Evidence {
			m[e] = true
		}
		instEvMap[inst.ID] = m
	}
	for _, inst := range res.Instructions {
		if _, ok := instMap[inst.ID]; ok {
			m := instEvMap[inst.ID]
			for _, e := range inst.Evidence {
				m[e] = true
			}
			mr.InstructionsExisting++
		} else {
			instMap[inst.ID] = inst
			m := make(map[string]bool, len(inst.Evidence))
			for _, e := range inst.Evidence {
				m[e] = true
			}
			instEvMap[inst.ID] = m
			mr.InstructionsAdded++
		}
	}

	// 3. Edges deduplication and evidence union
	edgeMap := make(map[string]recovery.Edge)
	edgeEvMap := make(map[string]map[string]bool)
	for _, edge := range doc.Edges {
		edgeMap[edge.ID] = edge
		m := make(map[string]bool, len(edge.Evidence))
		for _, e := range edge.Evidence {
			m[e] = true
		}
		edgeEvMap[edge.ID] = m
	}
	for _, edge := range res.Edges {
		if _, ok := edgeMap[edge.ID]; ok {
			m := edgeEvMap[edge.ID]
			for _, e := range edge.Evidence {
				m[e] = true
			}
		} else {
			edgeMap[edge.ID] = edge
			m := make(map[string]bool, len(edge.Evidence))
			for _, e := range edge.Evidence {
				m[e] = true
			}
			edgeEvMap[edge.ID] = m
			mr.EdgesAdded++
		}
	}

	// 4. Issues deduplication
	issMap := make(map[string]recovery.Issue)
	for _, iss := range doc.Issues {
		issMap[iss.ID] = iss
	}
	for _, iss := range res.Issues {
		if _, ok := issMap[iss.ID]; !ok {
			issMap[iss.ID] = iss
			mr.IssuesAdded++
		}
	}

	// 5. Build and sort deterministic slices.
	var finalEvidence []recovery.Evidence
	for _, ev := range evMap {
		finalEvidence = append(finalEvidence, ev)
	}
	sort.Slice(finalEvidence, func(i, j int) bool {
		return finalEvidence[i].ID < finalEvidence[j].ID
	})

	var finalInstructions []recovery.Instruction
	for id, inst := range instMap {
		m := instEvMap[id]
		evList := make([]string, 0, len(m))
		for e := range m {
			evList = append(evList, e)
		}
		sort.Strings(evList)
		inst.Evidence = evList
		finalInstructions = append(finalInstructions, inst)
	}
	sort.Slice(finalInstructions, func(i, j int) bool {
		if finalInstructions[i].Offset != finalInstructions[j].Offset {
			return finalInstructions[i].Offset < finalInstructions[j].Offset
		}
		if finalInstructions[i].Address != finalInstructions[j].Address {
			return finalInstructions[i].Address < finalInstructions[j].Address
		}
		if finalInstructions[i].Context.E != finalInstructions[j].Context.E {
			return finalInstructions[i].Context.E < finalInstructions[j].Context.E
		}
		if finalInstructions[i].Context.M != finalInstructions[j].Context.M {
			return finalInstructions[i].Context.M < finalInstructions[j].Context.M
		}
		if finalInstructions[i].Context.X != finalInstructions[j].Context.X {
			return finalInstructions[i].Context.X < finalInstructions[j].Context.X
		}
		if finalInstructions[i].Context.C != finalInstructions[j].Context.C {
			return finalInstructions[i].Context.C < finalInstructions[j].Context.C
		}
		return finalInstructions[i].Bytes < finalInstructions[j].Bytes
	})

	var finalEdges []recovery.Edge
	for id, edge := range edgeMap {
		m := edgeEvMap[id]
		evList := make([]string, 0, len(m))
		for e := range m {
			evList = append(evList, e)
		}
		sort.Strings(evList)
		edge.Evidence = evList
		finalEdges = append(finalEdges, edge)
	}
	sort.Slice(finalEdges, func(i, j int) bool {
		return finalEdges[i].ID < finalEdges[j].ID
	})

	var finalIssues []recovery.Issue
	for _, iss := range issMap {
		finalIssues = append(finalIssues, iss)
	}
	sort.Slice(finalIssues, func(i, j int) bool {
		return finalIssues[i].ID < finalIssues[j].ID
	})

	doc.Evidence = finalEvidence
	doc.Instructions = finalInstructions
	doc.Edges = finalEdges
	doc.Issues = finalIssues

	return mr, nil
}

