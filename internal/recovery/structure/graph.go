package structure

import (
	"fmt"
	"html"
	"math"
	"sort"
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

	routines := ExtractRoutines(doc)
	routineByAddr := make(map[uint32]*Routine)
	for _, r := range routines {
		routineByAddr[r.EntryAddress] = r
	}

	// Filter blocks if entryAddr is specified
	var activeBlocks []*BasicBlock
	if entryAddr != 0 {
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
		var insns []string
		for _, inst := range b.Instructions {
			insns = append(insns, fmt.Sprintf("$%06X: %s", inst.Address, inst.Mnemonic))
		}
		label := formatBlockLabel(b)
		cfg.Nodes = append(cfg.Nodes, CFGNode{
			ID:           b.ID,
			Label:        label,
			StartAddress: b.StartAddress,
			EndAddress:   b.EndAddress,
			IsUnresolved: false,
			Instructions: insns,
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
				if rtn, ok := routineByAddr[destAddr]; ok {
					destNodeID = fmt.Sprintf("rtn-%06x", destAddr)
					if !unresolvedNodes[destAddr] {
						unresolvedNodes[destAddr] = true
						lbl := rtn.Name
						if lbl == "" {
							lbl = fmt.Sprintf("sub_%06X", destAddr)
						}
						cfg.Nodes = append(cfg.Nodes, CFGNode{
							ID:           destNodeID,
							Label:        lbl,
							StartAddress: destAddr,
							IsExternal:   true,
						})
					}
				} else if blk, ok := blockByAddr[destAddr]; ok {
					destNodeID = blk.ID
					if !unresolvedNodes[destAddr] {
						unresolvedNodes[destAddr] = true
						cfg.Nodes = append(cfg.Nodes, CFGNode{
							ID:           destNodeID,
							Label:        fmt.Sprintf("block $%06X", destAddr),
							StartAddress: destAddr,
							IsExternal:   true,
						})
					}
				} else {
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
		} else if n.IsExternal {
			sb.WriteString(fmt.Sprintf("  \"%s\" [label=\"%s\", style=dashed, color=skyblue];\n", n.ID, escapedLabel))
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

// ToSVG generates a standalone, interactive SVG representation of the control-flow graph.
func (cfg *CFG) ToSVG() string {
	if len(cfg.Nodes) == 0 {
		return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 200" width="100%" height="100%"><rect width="100%" height="100%" fill="#0f172a"/><text x="200" y="100" fill="#94a3b8" font-family="sans-serif" font-size="13" text-anchor="middle">No CFG available</text></svg>`
	}

	numNodes := len(cfg.Nodes)
	nodeIndex := make(map[string]int, numNodes)
	for i, n := range cfg.Nodes {
		nodeIndex[n.ID] = i
	}

	// Calculate node dimensions
	nodeWidths := make([]float64, numNodes)
	nodeHeights := make([]float64, numNodes)
	const (
		regularWidth    = 240.0
		unresolvedWidth = 190.0
		unresolvedH     = 42.0
		maxVisibleInsns = 8
	)

	for i, n := range cfg.Nodes {
		if n.IsUnresolved || n.IsExternal {
			nodeWidths[i] = unresolvedWidth
			nodeHeights[i] = unresolvedH
		} else {
			nodeWidths[i] = regularWidth
			insnCount := len(n.Instructions)
			visible := insnCount
			if visible > maxVisibleInsns {
				visible = maxVisibleInsns + 1 // +1 for "more" line
			}
			if visible == 0 {
				nodeHeights[i] = 48.0
			} else {
				nodeHeights[i] = 32.0 + float64(visible)*16.0 + 8.0
			}
		}
	}

	// Entry point selection
	entryIdx := 0
	if cfg.RoutineID != "" {
		var addr uint32
		if n, err := fmt.Sscanf(cfg.RoutineID, "rtn-%x", &addr); err == nil && n == 1 {
			for i, node := range cfg.Nodes {
				if node.StartAddress == addr {
					entryIdx = i
					break
				}
			}
		}
	}

	type edgeInternal struct {
		from       int
		to         int
		kind       string
		provenance string
		isBackedge bool
	}

	edges := make([]edgeInternal, 0, len(cfg.Edges))
	adj := make([][]int, numNodes)
	inDegree := make([]int, numNodes)

	for _, e := range cfg.Edges {
		u, ok1 := nodeIndex[e.From]
		v, ok2 := nodeIndex[e.To]
		if !ok1 || !ok2 {
			continue
		}
		adj[u] = append(adj[u], v)
		inDegree[v]++
		edges = append(edges, edgeInternal{
			from:       u,
			to:         v,
			kind:       e.Kind,
			provenance: e.Provenance,
		})
	}

	// Detect backedges with DFS
	state := make([]int, numNodes) // 0: unvisited, 1: visiting, 2: visited
	isBack := make(map[[2]int]bool)
	var dfs func(int)
	dfs = func(u int) {
		state[u] = 1
		for _, v := range adj[u] {
			if state[v] == 1 {
				isBack[[2]int{u, v}] = true
			} else if state[v] == 0 {
				dfs(v)
			}
		}
		state[u] = 2
	}

	dfs(entryIdx)
	for i := 0; i < numNodes; i++ {
		if state[i] == 0 {
			dfs(i)
		}
	}

	for i := range edges {
		if isBack[[2]int{edges[i].from, edges[i].to}] {
			edges[i].isBackedge = true
		}
	}

	// Assign layers / ranks via longest path on DAG
	layer := make([]int, numNodes)
	for iter := 0; iter < numNodes; iter++ {
		changed := false
		for _, e := range edges {
			if e.isBackedge {
				continue
			}
			if layer[e.to] < layer[e.from]+1 {
				layer[e.to] = layer[e.from] + 1
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	maxLayer := 0
	for _, l := range layer {
		if l > maxLayer {
			maxLayer = l
		}
	}

	layers := make([][]int, maxLayer+1)
	for i, l := range layer {
		layers[l] = append(layers[l], i)
	}

	// Crossing reduction via barycenter heuristic
	posInLayer := make([]float64, numNodes)
	for pos, idx := range layers[0] {
		posInLayer[idx] = float64(pos)
	}
	for l := 1; l <= maxLayer; l++ {
		bary := make(map[int]float64)
		for _, v := range layers[l] {
			sum := 0.0
			count := 0
			for _, e := range edges {
				if e.to == v && !e.isBackedge && layer[e.from] < l {
					sum += posInLayer[e.from]
					count++
				}
			}
			if count > 0 {
				bary[v] = sum / float64(count)
			} else {
				bary[v] = float64(v)
			}
		}
		sort.SliceStable(layers[l], func(i, j int) bool {
			return bary[layers[l][i]] < bary[layers[l][j]]
		})
		for pos, idx := range layers[l] {
			posInLayer[idx] = float64(pos)
		}
	}

	// Coordinates assignment
	const (
		gapX    = 40.0
		gapY    = 60.0
		marginX = 90.0
		marginY = 60.0
	)

	layerHeights := make([]float64, maxLayer+1)
	layerWidths := make([]float64, maxLayer+1)
	maxLayerWidth := 0.0

	for l := 0; l <= maxLayer; l++ {
		maxH := 0.0
		totalW := 0.0
		for i, idx := range layers[l] {
			if i > 0 {
				totalW += gapX
			}
			totalW += nodeWidths[idx]
			if nodeHeights[idx] > maxH {
				maxH = nodeHeights[idx]
			}
		}
		layerHeights[l] = maxH
		layerWidths[l] = totalW
		if totalW > maxLayerWidth {
			maxLayerWidth = totalW
		}
	}

	nodeX := make([]float64, numNodes)
	nodeY := make([]float64, numNodes)

	currY := marginY
	for l := 0; l <= maxLayer; l++ {
		startX := marginX + (maxLayerWidth-layerWidths[l])/2.0
		currX := startX
		for _, idx := range layers[l] {
			nodeX[idx] = currX
			nodeY[idx] = currY
			currX += nodeWidths[idx] + gapX
		}
		currY += layerHeights[l] + gapY
	}

	totalWidth := maxLayerWidth + 2*marginX
	totalHeight := currY - gapY + marginY

	// Prepare tracking of canvas bounds (including backedges)
	minCanvasX := 0.0
	maxCanvasX := totalWidth
	minCanvasY := 0.0
	maxCanvasY := totalHeight

	// Outgoing forward edges indexing to spread source points
	outForwardCount := make(map[int]int)
	outForwardIdx := make(map[int]int)
	inForwardCount := make(map[int]int)
	inForwardIdx := make(map[int]int)

	for _, e := range edges {
		if !e.isBackedge && layer[e.to] > layer[e.from] {
			outForwardCount[e.from]++
			inForwardCount[e.to]++
		}
	}

	var sb strings.Builder
	sb.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" id="cfg-svg" width="100%" height="100%" `)
	sb.WriteString(`class="cfg-canvas" `)

	var defs strings.Builder
	defs.WriteString(`<defs>`)
	kinds := []struct {
		id    string
		color string
	}{
		{"arrow-call", "#38bdf8"},
		{"arrow-branch", "#34d399"},
		{"arrow-jump", "#fb923c"},
		{"arrow-return", "#c084fc"},
		{"arrow-fallthrough", "#94a3b8"},
		{"arrow-interrupt", "#f87171"},
		{"arrow-default", "#64748b"},
	}
	for _, k := range kinds {
		defs.WriteString(fmt.Sprintf(`<marker id="%s" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">`, k.id))
		defs.WriteString(fmt.Sprintf(`<path d="M 0 1.5 L 8 5 L 0 8.5 z" fill="%s"/>`, k.color))
		defs.WriteString(`</marker>`)
	}
	defs.WriteString(`</defs>`)

	var styles strings.Builder
	styles.WriteString(`<style>`)
	styles.WriteString(`.cfg-canvas { background: #090d16; user-select: none; }`)
	styles.WriteString(`.cfg-node { cursor: pointer; }`)
	styles.WriteString(`.cfg-node:hover rect.card-bg { stroke: #38bdf8 !important; stroke-width: 2 !important; filter: drop-shadow(0 0 8px rgba(56,189,248,0.5)); }`)
	styles.WriteString(`.cfg-node.selected rect.card-bg { stroke: #38bdf8 !important; stroke-width: 2.5 !important; filter: drop-shadow(0 0 10px rgba(56,189,248,0.7)); }`)
	styles.WriteString(`.cfg-edge-path { transition: stroke-width 0.15s ease; cursor: pointer; }`)
	styles.WriteString(`.cfg-edge:hover .cfg-edge-path { stroke-width: 2.5 !important; }`)
	styles.WriteString(`</style>`)

	var edgeContent strings.Builder
	backedgeIndex := 0

	for _, e := range edges {
		u := e.from
		v := e.to
		color := edgeColor(e.kind)
		markerID := "arrow-" + e.kind
		if e.kind == "" {
			markerID = "arrow-default"
		}

		dashAttr := ""
		if e.provenance == "static" {
			dashAttr = ` stroke-dasharray="4,3"`
		}

		var pathStr string
		var mx, my float64

		if u == v {
			// Self loop
			sx := nodeX[u] + nodeWidths[u]
			sy := nodeY[u] + nodeHeights[u]*0.25
			tx := nodeX[u] + nodeWidths[u]
			ty := nodeY[u] + nodeHeights[u]*0.75
			loopX := sx + 36.0
			pathStr = fmt.Sprintf("M %.1f %.1f C %.1f %.1f, %.1f %.1f, %.1f %.1f", sx, sy, loopX, sy-15, loopX, ty+15, tx, ty)
			mx = loopX + 4
			my = (sy + ty) / 2
			if loopX+30 > maxCanvasX {
				maxCanvasX = loopX + 30
			}
		} else if e.isBackedge || layer[v] <= layer[u] {
			// Backedge / loop
			backedgeIndex++
			goLeft := (nodeX[u]+nodeX[v])/2.0 < totalWidth/2.0
			cornerR := 8.0
			if goLeft {
				sideX := math.Min(nodeX[u], nodeX[v]) - 35.0 - float64(backedgeIndex%5)*14.0
				if sideX < minCanvasX+20 {
					minCanvasX = sideX - 40
				}
				sx := nodeX[u]
				sy := nodeY[u] + nodeHeights[u]*0.7
				tx := nodeX[v]
				ty := nodeY[v] + nodeHeights[v]*0.3
				pathStr = fmt.Sprintf("M %.1f %.1f L %.1f %.1f Q %.1f %.1f %.1f %.1f L %.1f %.1f Q %.1f %.1f %.1f %.1f L %.1f %.1f",
					sx, sy, sideX+cornerR, sy, sideX, sy, sideX, sy-cornerR,
					sideX, ty+cornerR, sideX, ty, sideX+cornerR, ty,
					tx, ty)
				mx = sideX
				my = (sy + ty) / 2
			} else {
				sideX := math.Max(nodeX[u]+nodeWidths[u], nodeX[v]+nodeWidths[v]) + 35.0 + float64(backedgeIndex%5)*14.0
				if sideX > maxCanvasX-20 {
					maxCanvasX = sideX + 40
				}
				sx := nodeX[u] + nodeWidths[u]
				sy := nodeY[u] + nodeHeights[u]*0.7
				tx := nodeX[v] + nodeWidths[v]
				ty := nodeY[v] + nodeHeights[v]*0.3
				pathStr = fmt.Sprintf("M %.1f %.1f L %.1f %.1f Q %.1f %.1f %.1f %.1f L %.1f %.1f Q %.1f %.1f %.1f %.1f L %.1f %.1f",
					sx, sy, sideX-cornerR, sy, sideX, sy, sideX, sy-cornerR,
					sideX, ty+cornerR, sideX, ty, sideX-cornerR, ty,
					tx, ty)
				mx = sideX
				my = (sy + ty) / 2
			}
		} else {
			// Forward edge
			cnt := outForwardCount[u]
			idx := outForwardIdx[u]
			outForwardIdx[u]++
			sxOffset := 0.5
			if cnt > 1 {
				sxOffset = float64(idx+1) / float64(cnt+1)
			}
			sx := nodeX[u] + nodeWidths[u]*sxOffset
			sy := nodeY[u] + nodeHeights[u]

			inCnt := inForwardCount[v]
			inIdx := inForwardIdx[v]
			inForwardIdx[v]++
			txOffset := 0.5
			if inCnt > 1 {
				txOffset = float64(inIdx+1) / float64(inCnt+1)
			}
			tx := nodeX[v] + nodeWidths[v]*txOffset
			ty := nodeY[v]

			dy := ty - sy
			c1x := sx
			c1y := sy + dy*0.5
			c2x := tx
			c2y := ty - dy*0.5
			pathStr = fmt.Sprintf("M %.1f %.1f C %.1f %.1f, %.1f %.1f, %.1f %.1f", sx, sy, c1x, c1y, c2x, c2y, tx, ty)
			mx = (sx + tx) / 2
			my = (sy + ty) / 2
		}

		edgeContent.WriteString(fmt.Sprintf(`<g class="cfg-edge" data-from="%s" data-to="%s" data-kind="%s">`,
			html.EscapeString(cfg.Nodes[u].ID), html.EscapeString(cfg.Nodes[v].ID), html.EscapeString(e.kind)))
		edgeContent.WriteString(fmt.Sprintf(`<path class="cfg-edge-path" d="%s" fill="none" stroke="%s" stroke-width="1.5"%s marker-end="url(#%s)"/>`,
			pathStr, color, dashAttr, markerID))

		// Label pill
		labelLen := len(e.kind)
		labelW := float64(labelLen)*6.5 + 14.0
		if labelW < 36.0 {
			labelW = 36.0
		}
		edgeContent.WriteString(fmt.Sprintf(`<g class="cfg-edge-label">`))
		edgeContent.WriteString(fmt.Sprintf(`<rect x="%.1f" y="%.1f" width="%.1f" height="16" rx="3" fill="#090d16" stroke="%s" stroke-width="1"/>`,
			mx-labelW/2, my-8, labelW, color))
		edgeContent.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" fill="%s" font-family="monospace, sans-serif" font-size="9" font-weight="600" text-anchor="middle">%s</text>`,
			mx, my+3.5, color, html.EscapeString(e.kind)))
		edgeContent.WriteString(`</g></g>`)
	}

	var nodeContent strings.Builder
	for i, n := range cfg.Nodes {
		x := nodeX[i]
		y := nodeY[i]
		w := nodeWidths[i]
		h := nodeHeights[i]

		nodeClass := ""
		if n.IsUnresolved {
			nodeClass = " unresolved"
		} else if n.IsExternal {
			nodeClass = " external"
		}
		nodeContent.WriteString(fmt.Sprintf(`<g class="cfg-node%s" id="node-%s" data-id="%s" data-addr="%d" onclick="onCFGNodeClick('%s', %d)">`,
			nodeClass,
			html.EscapeString(n.ID),
			html.EscapeString(n.ID),
			n.StartAddress,
			html.EscapeString(n.ID),
			n.StartAddress))

		if n.IsUnresolved {
			nodeContent.WriteString(fmt.Sprintf(`<rect class="card-bg" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="6" ry="6" fill="#241419" stroke="#f87171" stroke-width="1.5" stroke-dasharray="5,3"/>`,
				x, y, w, h))
			lbl := n.Label
			if lbl == "" {
				lbl = fmt.Sprintf("Unresolved $%06X", n.StartAddress)
			}
			nodeContent.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" fill="#f87171" font-family="monospace" font-size="11" font-weight="600" text-anchor="middle">%s</text>`,
				x+w/2, y+h/2+4, html.EscapeString(lbl)))
		} else if n.IsExternal {
			nodeContent.WriteString(fmt.Sprintf(`<rect class="card-bg" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="6" ry="6" fill="#141e33" stroke="#38bdf8" stroke-width="1.5" stroke-dasharray="4,3"/>`,
				x, y, w, h))
			lbl := n.Label
			if lbl == "" {
				lbl = fmt.Sprintf("sub_%06X", n.StartAddress)
			}
			nodeContent.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" fill="#38bdf8" font-family="monospace" font-size="11" font-weight="600" text-anchor="middle">%s</text>`,
				x+w/2, y+h/2+4, html.EscapeString(lbl)))
		} else {
			// Card body
			nodeContent.WriteString(fmt.Sprintf(`<rect class="card-bg" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="6" ry="6" fill="#1e293b" stroke="#334155" stroke-width="1.5"/>`,
				x, y, w, h))
			// Header bar
			nodeContent.WriteString(fmt.Sprintf(`<rect x="%.1f" y="%.1f" width="%.1f" height="26" rx="5" ry="5" fill="#141c2c"/>`,
				x, y, w))
			nodeContent.WriteString(fmt.Sprintf(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#334155" stroke-width="1"/>`,
				x, y+26, x+w, y+26))

			addrRange := fmt.Sprintf("$%06X - $%06X", n.StartAddress, n.EndAddress)
			if n.EndAddress == 0 || n.EndAddress < n.StartAddress {
				addrRange = fmt.Sprintf("$%06X", n.StartAddress)
			}
			nodeContent.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" fill="#38bdf8" font-family="monospace" font-size="11" font-weight="600">%s</text>`,
				x+8, y+17, html.EscapeString(addrRange)))

			insnCount := len(n.Instructions)
			nodeContent.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" fill="#94a3b8" font-family="sans-serif" font-size="10" text-anchor="end">%d insns</text>`,
				x+w-8, y+17, insnCount))

			// Instructions
			textY := y + 42.0
			for idx, inst := range n.Instructions {
				if idx >= maxVisibleInsns {
					remaining := insnCount - maxVisibleInsns
					nodeContent.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" fill="#94a3b8" font-family="monospace" font-size="10">... (+%d more)</text>`,
						x+8, textY, remaining))
					break
				}
				parts := strings.SplitN(inst, ": ", 2)
				if len(parts) == 2 {
					nodeContent.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" fill="#f1f5f9" font-family="monospace" font-size="11"><tspan fill="#38bdf8">%s:</tspan> <tspan font-weight="600">%s</tspan></text>`,
						x+8, textY, html.EscapeString(parts[0]), html.EscapeString(parts[1])))
				} else {
					nodeContent.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" fill="#f1f5f9" font-family="monospace" font-size="11">%s</text>`,
						x+8, textY, html.EscapeString(inst)))
				}
				textY += 16.0
			}
		}

		nodeContent.WriteString(`</g>`)
	}

	// Finalize viewBox
	viewBoxW := maxCanvasX - minCanvasX + 20
	viewBoxH := maxCanvasY - minCanvasY + 20
	sb.WriteString(fmt.Sprintf(`viewBox="%.1f %.1f %.1f %.1f">`, minCanvasX-10, minCanvasY-10, viewBoxW, viewBoxH))
	sb.WriteString(defs.String())
	sb.WriteString(styles.String())
	sb.WriteString(edgeContent.String())
	sb.WriteString(nodeContent.String())
	sb.WriteString(`</svg>`)

	return sb.String()
}

func edgeColor(kind string) string {
	switch kind {
	case "call":
		return "#38bdf8"
	case "branch":
		return "#34d399"
	case "jump":
		return "#fb923c"
	case "return":
		return "#c084fc"
	case "fallthrough":
		return "#94a3b8"
	case "interrupt":
		return "#f87171"
	default:
		return "#64748b"
	}
}
