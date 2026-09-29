package structure

import (
	"fmt"
	"strings"

	"github.com/tmc/snes/internal/recovery"
)

// BuildCFG constructs a control-flow graph for the entire document or a specific entry routine.
func BuildCFG(doc *recovery.Document, entryAddr uint32) *CFG {
	blocks := ExtractBasicBlocks(doc)
	if len(blocks) == 0 {
		return &CFG{}
	}

	blockByAddr := make(map[uint32]*BasicBlock)
	for _, b := range blocks {
		blockByAddr[b.StartAddress] = b
	}

	// Filter blocks if entryAddr is specified
	var activeBlocks []*BasicBlock
	if entryAddr != 0 {
		routines := ExtractRoutines(doc)
		var targetRoutine *Routine
		for _, r := range routines {
			if r.EntryAddress == entryAddr {
				targetRoutine = r
				break
			}
		}

		if targetRoutine != nil {
			activeSet := make(map[string]bool)
			for _, bID := range targetRoutine.BlockIDs {
				activeSet[bID] = true
			}
			for _, b := range blocks {
				if activeSet[b.ID] {
					activeBlocks = append(activeBlocks, b)
				}
			}
		} else if b, ok := blockByAddr[entryAddr]; ok {
			activeBlocks = []*BasicBlock{b}
		} else {
			activeBlocks = blocks
		}
	} else {
		activeBlocks = blocks
	}

	cfg := &CFG{
		Nodes: []CFGNode{},
		Edges: []CFGEdge{},
	}
	if entryAddr != 0 {
		cfg.RoutineID = fmt.Sprintf("rtn-%06x", entryAddr)
	}

	activeBlockSet := make(map[uint32]string)
	addedNodes := make(map[string]bool)
	for _, b := range activeBlocks {
		activeBlockSet[b.StartAddress] = b.ID
		if addedNodes[b.ID] {
			continue
		}
		addedNodes[b.ID] = true
		label := formatBlockLabel(b)
		cfg.Nodes = append(cfg.Nodes, CFGNode{
			ID:           b.ID,
			Label:        label,
			StartAddress: b.StartAddress,
			EndAddress:   b.EndAddress,
			IsUnresolved: false,
		})
	}

	// Build edges and identify unresolved targets
	unresolvedNodes := make(map[uint32]bool)
	edgeSeen := make(map[string]bool)

	for _, b := range activeBlocks {
		if len(b.Instructions) == 0 {
			continue
		}
		lastInst := b.Instructions[len(b.Instructions)-1]

		for _, edge := range doc.Edges {
			if edge.Source != lastInst.ID || edge.Destination == 0 {
				continue
			}

			destAddr := edge.Destination
			destNodeID, exists := activeBlockSet[destAddr]
			if !exists {
				destNodeID = fmt.Sprintf("unresolved-%06x", destAddr)
				if !unresolvedNodes[destAddr] {
					unresolvedNodes[destAddr] = true
					cfg.Nodes = append(cfg.Nodes, CFGNode{
						ID:           destNodeID,
						Label:        fmt.Sprintf("Unresolved $%06X", destAddr),
						StartAddress: destAddr,
						IsUnresolved: true,
					})
				}
			}

			edgeKey := fmt.Sprintf("%s->%s:%s", b.ID, destNodeID, edge.Kind)
			if !edgeSeen[edgeKey] {
				edgeSeen[edgeKey] = true
				prov := "static"
				if len(edge.Evidence) > 0 {
					prov = "observed"
				}
				cfg.Edges = append(cfg.Edges, CFGEdge{
					From:       b.ID,
					To:         destNodeID,
					Kind:       edge.Kind,
					Provenance: prov,
				})
			}
		}
	}

	return cfg
}

func formatBlockLabel(b *BasicBlock) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("$%06X - $%06X (%d insns)", b.StartAddress, b.EndAddress, len(b.Instructions)))
	maxLines := 6
	for i, inst := range b.Instructions {
		if i >= maxLines {
			lines = append(lines, fmt.Sprintf("... (%d more)", len(b.Instructions)-maxLines))
			break
		}
		lines = append(lines, fmt.Sprintf("  $%06X: %s", inst.Address, inst.Mnemonic))
	}
	return strings.Join(lines, "\n")
}

// ToDOT generates a Graphviz DOT representation of the control-flow graph.
func (cfg *CFG) ToDOT() string {
	var sb strings.Builder
	sb.WriteString("digraph CFG {\n")
	sb.WriteString("  node [shape=box, fontname=\"Courier\"];\n")
	sb.WriteString("  edge [fontname=\"Courier\"];\n\n")

	for _, n := range cfg.Nodes {
		escapedLabel := strings.ReplaceAll(n.Label, "\"", "\\\"")
		escapedLabel = strings.ReplaceAll(escapedLabel, "\n", "\\n")
		if n.IsUnresolved {
			sb.WriteString(fmt.Sprintf("  \"%s\" [label=\"%s\", style=dashed, color=red];\n", n.ID, escapedLabel))
		} else {
			sb.WriteString(fmt.Sprintf("  \"%s\" [label=\"%s\"];\n", n.ID, escapedLabel))
		}
	}

	sb.WriteString("\n")
	for _, e := range cfg.Edges {
		style := "solid"
		color := "black"
		if e.Provenance == "static" {
			style = "dashed"
		}
		switch e.Kind {
		case "call":
			color = "blue"
		case "return":
			color = "purple"
		case "branch":
			color = "darkgreen"
		case "jump":
			color = "darkorange"
		case "interrupt":
			color = "crimson"
		}
		sb.WriteString(fmt.Sprintf("  \"%s\" -> \"%s\" [label=\"%s\", style=%s, color=%s];\n",
			e.From, e.To, e.Kind, style, color))
	}

	sb.WriteString("}\n")
	return sb.String()
}
