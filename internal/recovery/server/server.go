package server

import (
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tmc/snes/internal/framecap"
	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/asmexport"
	"github.com/tmc/snes/internal/recovery/coverage"
	"github.com/tmc/snes/internal/recovery/decomp"
	"github.com/tmc/snes/internal/recovery/structure"
	"github.com/tmc/snes/internal/recovery/visualmap"
	"github.com/tmc/snes/internal/recovery/watches"
)

//go:embed ui.html
var uiHTML []byte

// Server provides read-only HTTP inspection endpoints and UI for a recovery project.
type Server struct {
	ProjectDir   string
	Document     *recovery.Document
	Coverage     *coverage.Index
	Watches      *watches.File
	Snapshots    []*watches.Snapshot
	FrameCapture *framecap.Capture
	Blocks       []*structure.BasicBlock
	Routines     []*structure.Routine
	References   []structure.MemoryReference
	ProvenanceEngine *visualmap.Engine
	Revision     string
	mux          *http.ServeMux

	routineViews []routineView
	byAddress    []int               // instruction indices sorted by address
	byOffset     []int               // instruction indices sorted by ROM offset
	instRoutines map[string][]int    // instruction ID -> indices into Routines
	instAddress  map[string]uint32   // instruction ID -> address
	entryNames   map[uint32]string   // routine entry address -> name
	jumpTargets  map[string][]uint32 // instruction ID -> observed jump/call destinations
}

// routineView is the JSON form of a routine served by /api/routines.
// Addresses lists the distinct instruction addresses in the routine so
// clients can aggregate coverage per routine.
type routineView struct {
	*structure.Routine
	Addresses []uint32 `json:"addresses"`
	Bytes     int      `json:"bytes"`
}

// NewServer initializes a Server from an on-disk recovery project directory.
func NewServer(projectDir string) (*Server, error) {
	docPath := filepath.Join(projectDir, "recovery.json")
	docFile, err := os.Open(docPath)
	if err != nil {
		return nil, fmt.Errorf("server: open recovery.json: %w", err)
	}
	defer docFile.Close()

	doc, err := recovery.Decode(docFile)
	if err != nil {
		return nil, fmt.Errorf("server: decode recovery.json: %w", err)
	}

	var covIdx *coverage.Index
	covPath := filepath.Join(projectDir, "coverage.json")
	if cf, err := os.Open(covPath); err == nil {
		covIdx, _ = coverage.Decode(cf)
		cf.Close()
	}

	blocks := structure.ExtractBasicBlocks(doc)
	routines := structure.ExtractRoutines(doc)
	refs := structure.ExtractMemoryReferences(doc)

	var watchFile *watches.File
	watchPath := filepath.Join(projectDir, "watches.json")
	if wf, err := watches.LoadWatches(watchPath); err == nil {
		watchFile = wf
	}

	var snaps []*watches.Snapshot
	if snapDir := filepath.Join(projectDir, "snapshots"); fileExists(snapDir) {
		snaps, _ = watches.LoadSnapshots(snapDir)
	} else if snapFile := filepath.Join(projectDir, "snapshots.json"); fileExists(snapFile) {
		snaps, _ = watches.LoadSnapshots(snapFile)
	} else if snapLineFile := filepath.Join(projectDir, "snapshots.jsonl"); fileExists(snapLineFile) {
		snaps, _ = watches.LoadSnapshots(snapLineFile)
	}

	var frameCap *framecap.Capture
	framesDir := filepath.Join(projectDir, "frames")
	if fileExists(filepath.Join(framesDir, framecap.ManifestName)) {
		if fc, err := framecap.Open(framesDir); err == nil {
			frameCap = fc
		}
	} else if fileExists(filepath.Join(projectDir, framecap.ManifestName)) {
		if fc, err := framecap.Open(projectDir); err == nil {
			frameCap = fc
		}
	}

	rev := computeProjectRevision(projectDir, doc)

	s := &Server{
		ProjectDir:   projectDir,
		Document:     doc,
		Coverage:     covIdx,
		Watches:      watchFile,
		Snapshots:    snaps,
		FrameCapture: frameCap,
		Blocks:       blocks,
		Routines:     routines,
		References:   refs,
		Revision:     rev,
	}
	s.buildIndexes()

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/project", s.handleProject)
	mux.HandleFunc("/api/routines", s.handleRoutines)
	mux.HandleFunc("/api/disasm", s.handleDisasm)
	mux.HandleFunc("/api/refs", s.handleRefs)
	mux.HandleFunc("/api/graph", s.handleGraph)
	mux.HandleFunc("/api/coverage", s.handleCoverage)
	mux.HandleFunc("/api/frames", s.handleFrames)
	mux.HandleFunc("/api/frame", s.handleFrame)
	mux.HandleFunc("/api/evidence", s.handleEvidence)
	mux.HandleFunc("/api/watches", s.handleWatches)
	mux.HandleFunc("/api/watch", s.handleWatch)
	mux.HandleFunc("/api/snapshots", s.handleSnapshots)
	mux.HandleFunc("/api/locate", s.handleLocate)
	mux.HandleFunc("/api/pseudoc", s.handlePseudoc)
	mux.HandleFunc("/api/pseudoc/validate", s.handlePseudocValidate)
	mux.HandleFunc("/api/pseudoc/replay", s.handlePseudocReplay)
	mux.HandleFunc("/api/provenance", s.handleProvenance)
	s.mux = mux

	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(uiHTML)
}

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	totalHits := "0"
	hasCoverage := false
	if s.Coverage != nil {
		hasCoverage = true
		res, _ := s.Coverage.Query(coverage.Filter{})
		if res != nil {
			totalHits = res.TotalHits
		}
	}

	// Count distinct decoded instruction offsets per 32 KiB ROM bank.
	bankInsns := make([]int, (s.Document.ROM.NormalizedSize+0x7FFF)/0x8000)
	seen := make(map[uint32]bool)
	for _, inst := range s.Document.Instructions {
		b := int(inst.Offset / 0x8000)
		if seen[inst.Offset] || b >= len(bankInsns) {
			continue
		}
		seen[inst.Offset] = true
		bankInsns[b]++
	}

	resp := map[string]any{
		"project_dir":        s.ProjectDir,
		"project_rom_hash":   s.Document.ROM.NormalizedSHA256,
		"revision":           s.Revision,
		"rom_size":           s.Document.ROM.NormalizedSize,
		"mapper":             s.Document.ROM.Mapper,
		"total_instructions": len(s.Document.Instructions),
		"total_edges":        len(s.Document.Edges),
		"total_routines":     len(s.Routines),
		"total_hits":         totalHits,
		"has_coverage":       hasCoverage,
		"bank_instructions":  bankInsns,
	}
	writeJSON(w, resp)
}

func (s *Server) handleRoutines(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.routineViews)
}

// DisasmItem represents an instruction formatted for code views with operands and provenance.
//
// Target is the destination of a direct jump, call, or branch, resolved to a
// 24-bit address using the instruction's program bank. ObservedTargets lists
// destinations recorded in the recovery document for indirect jumps and calls.
type DisasmItem struct {
	recovery.Instruction
	Assembly        string   `json:"assembly"`
	Provenance      string   `json:"provenance"`
	Length          int      `json:"length"`
	Target          *uint32  `json:"target,omitempty"`
	TargetKind      string   `json:"target_kind,omitempty"`
	TargetLabel     string   `json:"target_label,omitempty"`
	ObservedTargets []uint32 `json:"observed_targets,omitempty"`
}

func (s *Server) handleDisasm(w http.ResponseWriter, r *http.Request) {
	var targetAddr uint32
	if addrStr := r.URL.Query().Get("addr"); addrStr != "" {
		a, err := parseAddress(addrStr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		targetAddr = a
	}

	// Find instructions for the routine or starting at address
	var matching []recovery.Instruction
	if targetAddr != 0 {
		var targetRoutine *structure.Routine
		for _, rtn := range s.Routines {
			if rtn.EntryAddress == targetAddr {
				targetRoutine = rtn
				break
			}
		}

		if targetRoutine != nil {
			blockSet := make(map[string]bool)
			for _, bID := range targetRoutine.BlockIDs {
				blockSet[bID] = true
			}
			for _, b := range s.Blocks {
				if blockSet[b.ID] {
					matching = append(matching, b.Instructions...)
				}
			}
		} else {
			// Find individual instruction or slice
			for _, inst := range s.Document.Instructions {
				if inst.Address >= targetAddr && len(matching) < 100 {
					matching = append(matching, inst)
				}
			}
		}
	} else {
		// First 200 instructions by default
		maxCount := 200
		if len(s.Document.Instructions) < maxCount {
			maxCount = len(s.Document.Instructions)
		}
		matching = s.Document.Instructions[:maxCount]
	}

	items := make([]DisasmItem, len(matching))
	for i, inst := range matching {
		items[i] = s.disasmItem(inst)
	}

	writeJSON(w, map[string]any{
		"instructions": items,
		"total":        len(items),
	})
}

// handleEvidence reports project provenance. With ?addr= or ?instruction=
// it also reports, for each matching instruction, its evidence records,
// control-flow edges, and decode issues.
func (s *Server) handleEvidence(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"total_evidence": len(s.Document.Evidence),
		"total_issues":   len(s.Document.Issues),
		"rom":            s.Document.ROM,
		"producer":       s.Document.Producer,
	}
	q := r.URL.Query()
	var match func(recovery.Instruction) bool
	switch {
	case q.Get("instruction") != "":
		id := q.Get("instruction")
		match = func(inst recovery.Instruction) bool { return inst.ID == id }
	case q.Get("addr") != "":
		a, err := parseAddress(q.Get("addr"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		match = func(inst recovery.Instruction) bool { return inst.Address == a }
	default:
		writeJSON(w, resp)
		return
	}

	var found []recovery.Instruction
	for _, inst := range s.Document.Instructions {
		if match(inst) {
			found = append(found, inst)
		}
	}
	if len(found) == 0 {
		http.Error(w, "no instruction matches query", http.StatusNotFound)
		return
	}
	resp["instructions"] = s.instructionEvidence(found)
	writeJSON(w, resp)
}

func (s *Server) handleRefs(w http.ResponseWriter, r *http.Request) {
	addrStr := r.URL.Query().Get("addr")
	if addrStr == "" {
		writeJSON(w, s.References)
		return
	}

	targetAddr, err := parseAddress(addrStr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var filtered []structure.MemoryReference
	// If routine address, match all references within routine blocks
	var targetRoutine *structure.Routine
	for _, rtn := range s.Routines {
		if rtn.EntryAddress == targetAddr {
			targetRoutine = rtn
			break
		}
	}

	if targetRoutine != nil {
		blockAddrs := make(map[uint32]bool)
		for _, b := range s.Blocks {
			for _, bID := range targetRoutine.BlockIDs {
				if b.ID == bID {
					for _, inst := range b.Instructions {
						blockAddrs[inst.Address] = true
					}
				}
			}
		}
		for _, ref := range s.References {
			if blockAddrs[ref.InstructionAddress] {
				filtered = append(filtered, ref)
			}
		}
	} else {
		for _, ref := range s.References {
			if ref.InstructionAddress == targetAddr || ref.EncodedAddress == targetAddr {
				filtered = append(filtered, ref)
			}
		}
	}

	writeJSON(w, filtered)
}

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	var targetAddr uint32
	if addrStr := r.URL.Query().Get("addr"); addrStr != "" {
		a, err := parseAddress(addrStr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		targetAddr = a
	}

	cfg := structure.BuildCFG(s.Document, targetAddr)
	format := r.URL.Query().Get("format")
	if format == "dot" {
		w.Header().Set("Content-Type", "text/vnd.graphviz; charset=utf-8")
		w.Write([]byte(cfg.ToDOT()))
		return
	}
	if format == "svg" {
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		w.Write([]byte(cfg.ToSVG()))
		return
	}

	writeJSON(w, cfg)
}

func (s *Server) handleCoverage(w http.ResponseWriter, r *http.Request) {
	if s.Coverage == nil {
		writeJSON(w, coverage.CoverageResult{
			ProjectROMHash: s.Document.ROM.NormalizedSHA256,
			Quality:        coverage.QualityUnavailable,
			TotalHits:      "0",
			Limitations:    []string{"coverage index not available for project"},
		})
		return
	}

	filter := coverage.Filter{}
	if framesStr := r.URL.Query().Get("frames"); framesStr != "" {
		a, b, err := parseFrameRange(framesStr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		filter.FrameStart, filter.FrameEnd = &a, &b
	}

	res, err := s.Coverage.Query(filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 32 bins for LoROM 32KB banks
	binSize := uint32(32 * 1024)
	bins, _ := s.Coverage.QueryBins(filter, binSize, uint32(s.Document.ROM.NormalizedSize))
	res.Bins = bins

	writeJSON(w, res)
}

func (s *Server) handleFrames(w http.ResponseWriter, r *http.Request) {
	if s.FrameCapture != nil {
		type frameItem struct {
			Index      int     `json:"index"`
			Number     int     `json:"number"`
			TraceFrame *int    `json:"trace_frame,omitempty"`
			Start      uint64  `json:"start"`
			VBlank     uint64  `json:"vblank"`
			End        *uint64 `json:"end,omitempty"`
			SeqStart   *uint64 `json:"seq_start,omitempty"`
			SeqVBlank  *uint64 `json:"seq_vblank,omitempty"`
			SeqEnd     *uint64 `json:"seq_end,omitempty"`
			Stored     bool    `json:"stored"`
			Width      int     `json:"width,omitempty"`
			Height     int     `json:"height,omitempty"`
			HasPNG     bool    `json:"has_png"`
			PNG        string  `json:"png,omitempty"`
		}
		frames := make([]frameItem, len(s.FrameCapture.Records))
		for i, rec := range s.FrameCapture.Records {
			hasPNG := rec.PNG != "" || fileExists(filepath.Join(s.FrameCapture.Dir, "png", fmt.Sprintf("%06d.png", rec.Number)))
			frames[i] = frameItem{
				Index:      rec.Index,
				Number:     rec.Number,
				TraceFrame: rec.TraceFrame,
				Start:      rec.Start,
				VBlank:     rec.VBlank,
				End:        rec.End,
				SeqStart:   rec.SeqStart,
				SeqVBlank:  rec.SeqVBlank,
				SeqEnd:     rec.SeqEnd,
				Stored:     rec.Stored,
				Width:      rec.Width,
				Height:     rec.Height,
				HasPNG:     hasPNG,
				PNG:        rec.PNG,
			}
		}
		writeJSON(w, map[string]any{
			"header":  s.FrameCapture.Header,
			"receipt": s.FrameCapture.Receipt,
			"frames":  frames,
		})
		return
	}

	framesPath := filepath.Join(s.ProjectDir, "frames.json")
	if data, err := os.ReadFile(framesPath); err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
		return
	}
	writeJSON(w, map[string]any{"frames": []any{}})
}

func (s *Server) handleFrame(w http.ResponseWriter, r *http.Request) {
	if s.FrameCapture == nil {
		http.Error(w, "no frame capture available", http.StatusNotFound)
		return
	}
	numStr := r.URL.Query().Get("number")
	if numStr == "" {
		numStr = r.URL.Query().Get("index")
	}
	if numStr == "" {
		http.Error(w, "missing number or index parameter", http.StatusBadRequest)
		return
	}
	targetNum, err := strconv.Atoi(numStr)
	if err != nil {
		http.Error(w, "invalid frame number", http.StatusBadRequest)
		return
	}

	var targetRec *framecap.Record
	for i := range s.FrameCapture.Records {
		rec := &s.FrameCapture.Records[i]
		if rec.Number == targetNum || rec.Index == targetNum {
			targetRec = rec
			break
		}
	}
	if targetRec == nil {
		http.Error(w, "frame not found", http.StatusNotFound)
		return
	}

	pngCandidates := []string{
		filepath.Join(s.FrameCapture.Dir, "png", fmt.Sprintf("%06d.png", targetRec.Number)),
		filepath.Join(s.FrameCapture.Dir, filepath.FromSlash(targetRec.PNG)),
	}
	for _, p := range pngCandidates {
		if p != "" && fileExists(p) {
			data, err := os.ReadFile(p)
			if err == nil {
				w.Header().Set("Content-Type", "image/png")
				w.Header().Set("Cache-Control", "public, max-age=3600")
				w.Write(data)
				return
			}
		}
	}

	if targetRec.Stored {
		px, err := s.FrameCapture.Pixels(targetRec)
		if err != nil {
			http.Error(w, fmt.Sprintf("read pixels: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		if err := framecap.EncodePNG(w, targetRec.Width, targetRec.Height, px); err != nil {
			http.Error(w, fmt.Sprintf("encode png: %v", err), http.StatusInternalServerError)
			return
		}
		return
	}

	http.Error(w, "frame pixels not stored", http.StatusNotFound)
}

func computeProjectRevision(projectDir string, doc *recovery.Document) string {
	return recovery.ComputeProjectRevision(projectDir, doc)
}

func (s *Server) handleWatches(w http.ResponseWriter, r *http.Request) {
	if s.Watches == nil {
		writeJSON(w, watches.File{
			Format:        watches.FileFormatWatches,
			SchemaVersion: watches.CurrentSchema,
			Watches:       []watches.WatchDefinition{},
		})
		return
	}
	writeJSON(w, s.Watches)
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	type snapMeta struct {
		RunID         string `json:"run_id"`
		Sequence      uint64 `json:"sequence"`
		Frame         uint64 `json:"frame"`
		Length        uint32 `json:"length"`
		Boundary      string `json:"boundary,omitempty"`
		ContentSHA256 string `json:"content_sha256"`
	}
	var metas []snapMeta
	for _, snap := range s.Snapshots {
		metas = append(metas, snapMeta{
			RunID:         snap.RunID,
			Sequence:      snap.Sequence,
			Frame:         snap.Frame,
			Length:        snap.Length,
			Boundary:      snap.Boundary,
			ContentSHA256: snap.ContentSHA256,
		})
	}
	if metas == nil {
		metas = []snapMeta{}
	}
	writeJSON(w, metas)
}

func (s *Server) handleWatch(w http.ResponseWriter, r *http.Request) {
	if s.Watches == nil {
		http.Error(w, "watches not defined for project", http.StatusNotFound)
		return
	}

	watchID := r.URL.Query().Get("id")
	if watchID == "" {
		http.Error(w, "missing required ?id= parameter", http.StatusBadRequest)
		return
	}

	var targetWatch *watches.WatchDefinition
	for i := range s.Watches.Watches {
		if s.Watches.Watches[i].ID == watchID {
			targetWatch = &s.Watches.Watches[i]
			break
		}
	}
	if targetWatch == nil {
		http.Error(w, "watch not found", http.StatusNotFound)
		return
	}

	frameStr := r.URL.Query().Get("frame")
	if frameStr != "" {
		frameNum, err := strconv.ParseUint(frameStr, 10, 64)
		if err != nil {
			http.Error(w, "invalid frame", http.StatusBadRequest)
			return
		}
		var chosenSnap *watches.Snapshot
		for _, snap := range s.Snapshots {
			if snap.Frame == frameNum {
				chosenSnap = snap
				break
			}
		}
		if chosenSnap == nil && len(s.Snapshots) > 0 {
			for _, snap := range s.Snapshots {
				if snap.Frame <= frameNum {
					if chosenSnap == nil || snap.Frame > chosenSnap.Frame {
						chosenSnap = snap
					}
				}
			}
			if chosenSnap == nil {
				chosenSnap = s.Snapshots[0]
			}
		}

		if chosenSnap == nil {
			writeJSON(w, watches.Evaluation{
				WatchID:        targetWatch.ID,
				Name:           targetWatch.Name,
				Validity:       watches.ValidityMissing,
				ValidityReason: "no snapshots available",
			})
			return
		}

		allEvals, _ := watches.EvaluateAll(s.Watches, chosenSnap)
		eval, ok := allEvals[targetWatch.ID]
		if !ok {
			eval = watches.Evaluate(targetWatch, chosenSnap, nil)
		}
		writeJSON(w, eval)
		return
	}

	var filteredSnaps []*watches.Snapshot
	var fStart, fEnd *uint64
	if framesFilter := r.URL.Query().Get("frames"); framesFilter != "" {
		a, b, err := parseFrameRange(framesFilter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fStart, fEnd = &a, &b
	}

	runID := r.URL.Query().Get("run")
	for _, snap := range s.Snapshots {
		if runID != "" && snap.RunID != runID {
			continue
		}
		if fStart != nil && snap.Frame < *fStart {
			continue
		}
		if fEnd != nil && snap.Frame >= *fEnd {
			continue
		}
		filteredSnaps = append(filteredSnaps, snap)
	}

	history, err := watches.BuildHistory(targetWatch, filteredSnaps, s.Watches)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.URL.Query().Get("changes") == "true" || r.URL.Query().Get("changes") == "1" {
		writeJSON(w, watches.ExtractChanges(history))
		return
	}

	writeJSON(w, history)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(data)
}

// buildIndexes precomputes the lookup tables used by navigation endpoints.
func (s *Server) buildIndexes() {
	insts := s.Document.Instructions
	s.byAddress = make([]int, len(insts))
	s.byOffset = make([]int, len(insts))
	s.instAddress = make(map[string]uint32, len(insts))
	for i, inst := range insts {
		s.byAddress[i] = i
		s.byOffset[i] = i
		s.instAddress[inst.ID] = inst.Address
	}
	sort.SliceStable(s.byAddress, func(i, j int) bool {
		return insts[s.byAddress[i]].Address < insts[s.byAddress[j]].Address
	})
	sort.SliceStable(s.byOffset, func(i, j int) bool {
		return insts[s.byOffset[i]].Offset < insts[s.byOffset[j]].Offset
	})

	blocks := make(map[string]*structure.BasicBlock, len(s.Blocks))
	for _, b := range s.Blocks {
		blocks[b.ID] = b
	}
	s.instRoutines = make(map[string][]int)
	s.entryNames = make(map[uint32]string, len(s.Routines))
	s.routineViews = make([]routineView, len(s.Routines))
	for ri, rtn := range s.Routines {
		s.entryNames[rtn.EntryAddress] = rtn.Name
		view := routineView{Routine: rtn, Addresses: []uint32{}}
		seen := make(map[uint32]bool)
		for _, id := range rtn.BlockIDs {
			b := blocks[id]
			if b == nil {
				continue
			}
			for _, inst := range b.Instructions {
				s.instRoutines[inst.ID] = append(s.instRoutines[inst.ID], ri)
				if !seen[inst.Address] {
					seen[inst.Address] = true
					view.Addresses = append(view.Addresses, inst.Address)
					view.Bytes += len(inst.Bytes) / 2
				}
			}
		}
		s.routineViews[ri] = view
	}

	s.jumpTargets = make(map[string][]uint32)
	for _, e := range s.Document.Edges {
		if e.Kind == "jump" || e.Kind == "call" {
			s.jumpTargets[e.Source] = append(s.jumpTargets[e.Source], e.Destination)
		}
	}
}

func (s *Server) disasmItem(inst recovery.Instruction) DisasmItem {
	assembly, err := asmexport.FormatInstructionASM(inst)
	if err != nil || assembly == "" {
		assembly = inst.Mnemonic
	}
	item := DisasmItem{
		Instruction: inst,
		Assembly:    assembly,
		Provenance:  provenance(inst),
		Length:      len(inst.Bytes) / 2,
	}
	if target, kind, ok := controlTarget(inst); ok {
		item.Target = &target
		item.TargetKind = kind
		item.TargetLabel = s.entryNames[target]
	} else if isIndirectJump(inst.Opcode) {
		item.ObservedTargets = s.jumpTargets[inst.ID]
	}
	return item
}

func provenance(inst recovery.Instruction) string {
	for _, ev := range inst.Evidence {
		if ev != "derived" && ev != "" {
			return "observed"
		}
	}
	return "static"
}

// controlTarget returns the destination encoded in a direct branch, jump,
// or call. Relative branches and 16-bit jumps stay in the instruction's
// program bank, wrapping within it.
func controlTarget(inst recovery.Instruction) (target uint32, kind string, ok bool) {
	raw, err := hex.DecodeString(inst.Bytes)
	if err != nil || len(raw) == 0 {
		return 0, "", false
	}
	bank := inst.Address & 0xFF0000
	pc := inst.Address & 0xFFFF
	switch raw[0] {
	case 0x10, 0x30, 0x50, 0x70, 0x80, 0x90, 0xB0, 0xD0, 0xF0: // Bxx, BRA
		if len(raw) < 2 {
			return 0, "", false
		}
		return bank | (pc+2+uint32(int8(raw[1])))&0xFFFF, "branch", true
	case 0x82: // BRL
		if len(raw) < 3 {
			return 0, "", false
		}
		rel := int16(binary.LittleEndian.Uint16(raw[1:3]))
		return bank | (pc+3+uint32(rel))&0xFFFF, "branch", true
	case 0x4C, 0x20: // JMP abs, JSR abs
		if len(raw) < 3 {
			return 0, "", false
		}
		kind = "jump"
		if raw[0] == 0x20 {
			kind = "call"
		}
		return bank | uint32(binary.LittleEndian.Uint16(raw[1:3])), kind, true
	case 0x5C, 0x22: // JML long, JSL long
		if len(raw) < 4 {
			return 0, "", false
		}
		kind = "jump"
		if raw[0] == 0x22 {
			kind = "call"
		}
		return uint32(raw[1]) | uint32(raw[2])<<8 | uint32(raw[3])<<16, kind, true
	}
	return 0, "", false
}

func isIndirectJump(opcode byte) bool {
	switch opcode {
	case 0x6C, 0x7C, 0xDC, 0xFC: // JMP (abs), JMP (abs,X), JML [abs], JSR (abs,X)
		return true
	}
	return false
}

// routineRef identifies a routine in navigation responses.
type routineRef struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	EntryAddress uint32 `json:"entry_address"`
}

// routinesFor returns the routines containing inst, preferring one whose
// entry is inst itself.
func (s *Server) routinesFor(inst recovery.Instruction) []routineRef {
	idxs := s.instRoutines[inst.ID]
	refs := make([]routineRef, 0, len(idxs))
	for _, i := range idxs {
		rtn := s.Routines[i]
		ref := routineRef{ID: rtn.ID, Name: rtn.Name, EntryAddress: rtn.EntryAddress}
		if rtn.EntryAddress == inst.Address {
			refs = append([]routineRef{ref}, refs...)
		} else {
			refs = append(refs, ref)
		}
	}
	return refs
}

// edgeRef is a control-flow edge with its source resolved to an address.
type edgeRef struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	Source      string  `json:"source"`
	From        *uint32 `json:"from,omitempty"`
	Destination uint32  `json:"destination"`
}

// instructionEvidence is the per-instruction report served by /api/evidence.
type instructionEvidence struct {
	DisasmItem
	Records    []recovery.Evidence `json:"records"`
	Unresolved []string            `json:"unresolved_evidence,omitempty"`
	EdgesOut   []edgeRef           `json:"edges_out"`
	EdgesIn    []edgeRef           `json:"edges_in"`
	Issues     []recovery.Issue    `json:"issues"`
	Routines   []routineRef        `json:"routines"`
}

func (s *Server) instructionEvidence(insts []recovery.Instruction) []instructionEvidence {
	wanted := make(map[string]bool)
	for _, inst := range insts {
		for _, id := range inst.Evidence {
			wanted[id] = true
		}
	}
	records := make(map[string]recovery.Evidence)
	for _, ev := range s.Document.Evidence {
		if wanted[ev.ID] {
			records[ev.ID] = ev
		}
	}

	out := make([]instructionEvidence, len(insts))
	for i, inst := range insts {
		ie := instructionEvidence{
			DisasmItem: s.disasmItem(inst),
			Records:    []recovery.Evidence{},
			EdgesOut:   []edgeRef{},
			EdgesIn:    []edgeRef{},
			Issues:     []recovery.Issue{},
			Routines:   s.routinesFor(inst),
		}
		for _, id := range inst.Evidence {
			if ev, ok := records[id]; ok {
				ie.Records = append(ie.Records, ev)
			} else {
				ie.Unresolved = append(ie.Unresolved, id)
			}
		}
		for _, e := range s.Document.Edges {
			if e.Source != inst.ID && e.Destination != inst.Address {
				continue
			}
			ref := edgeRef{ID: e.ID, Kind: e.Kind, Source: e.Source, Destination: e.Destination}
			if a, ok := s.instAddress[e.Source]; ok {
				ref.From = &a
			}
			if e.Source == inst.ID {
				ie.EdgesOut = append(ie.EdgesOut, ref)
			} else {
				ie.EdgesIn = append(ie.EdgesIn, ref)
			}
		}
		for _, iss := range s.Document.Issues {
			if iss.Offset == inst.Offset || iss.Address == inst.Address {
				ie.Issues = append(ie.Issues, iss)
			}
		}
		out[i] = ie
	}
	return out
}

// handleLocate resolves ?addr= (CPU address) or ?offset= (ROM offset) to
// the instruction containing it and that instruction's routines.
func (s *Server) handleLocate(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	insts := s.Document.Instructions
	var (
		order []int
		key   func(recovery.Instruction) uint32
		query string
	)
	switch {
	case q.Get("addr") != "":
		order, key, query = s.byAddress, func(i recovery.Instruction) uint32 { return i.Address }, q.Get("addr")
	case q.Get("offset") != "":
		order, key, query = s.byOffset, func(i recovery.Instruction) uint32 { return i.Offset }, q.Get("offset")
	default:
		http.Error(w, "missing addr or offset parameter", http.StatusBadRequest)
		return
	}
	v, err := parseAddress(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Find the last instruction starting at or before v, then back up to
	// the first of any instructions sharing that start.
	n := sort.Search(len(order), func(i int) bool { return key(insts[order[i]]) > v }) - 1
	if n < 0 {
		http.Error(w, "no instruction contains location", http.StatusNotFound)
		return
	}
	start := key(insts[order[n]])
	for n > 0 && key(insts[order[n-1]]) == start {
		n--
	}
	inst := insts[order[n]]
	if v >= start+uint32(len(inst.Bytes)/2) {
		http.Error(w, "no instruction contains location", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{
		"instruction": s.disasmItem(inst),
		"exact":       v == start,
		"routines":    s.routinesFor(inst),
	})
}

// parseAddress parses a hexadecimal address or offset such as "$80B5",
// "0x0080B5", "0080B5", or "00:80B5".
func parseAddress(s string) (uint32, error) {
	t := strings.TrimSpace(s)
	t = strings.TrimPrefix(t, "$")
	if len(t) > 2 && (t[:2] == "0x" || t[:2] == "0X") {
		t = t[2:]
	}
	if bank, addr, ok := strings.Cut(t, ":"); ok {
		t = bank + fmt.Sprintf("%04s", addr)
	}
	v, err := strconv.ParseUint(t, 16, 32)
	if err != nil || v > 0xFFFFFF {
		return 0, fmt.Errorf("invalid address %q", s)
	}
	return uint32(v), nil
}

// parseFrameRange parses a half-open frame interval "A:B" with A <= B.
func parseFrameRange(s string) (start, end uint64, err error) {
	a, b, ok := strings.Cut(s, ":")
	if !ok {
		return 0, 0, fmt.Errorf("invalid frame range %q: want A:B", s)
	}
	start, errA := strconv.ParseUint(strings.TrimSpace(a), 10, 64)
	end, errB := strconv.ParseUint(strings.TrimSpace(b), 10, 64)
	if errA != nil || errB != nil {
		return 0, 0, fmt.Errorf("invalid frame range %q: want A:B", s)
	}
	if start > end {
		return 0, 0, fmt.Errorf("invalid frame range %q: start after end", s)
	}
	return start, end, nil
}

func (s *Server) handlePseudoc(w http.ResponseWriter, r *http.Request) {
	var targetAddr uint32
	if addrStr := r.URL.Query().Get("addr"); addrStr != "" {
		a, err := parseAddress(addrStr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		targetAddr = a
	}

	var targetBlock *structure.BasicBlock
	if targetAddr != 0 {
		for _, b := range s.Blocks {
			if b.StartAddress == targetAddr || (targetAddr >= b.StartAddress && targetAddr < b.EndAddress) {
				targetBlock = b
				break
			}
		}
		if targetBlock == nil {
			http.Error(w, fmt.Sprintf("no block found covering address $%06X", targetAddr), http.StatusNotFound)
			return
		}
	} else {
		for _, b := range s.Blocks {
			if b.StartAddress == 0x0086DF {
				targetBlock = b
				break
			}
		}
		if targetBlock == nil && len(s.Blocks) > 0 {
			targetBlock = s.Blocks[0]
		}
	}

	if targetBlock == nil {
		http.Error(w, "no basic blocks available", http.StatusNotFound)
		return
	}

	entryCtx := s.Document.Instructions[0].Context
	if len(targetBlock.Instructions) > 0 && targetBlock.Instructions[0].Context.M != "" {
		entryCtx = targetBlock.Instructions[0].Context
	}

	ir, err := decomp.LiftBlock(targetBlock, entryCtx)
	if err != nil {
		http.Error(w, fmt.Sprintf("lift block: %v", err), http.StatusInternalServerError)
		return
	}

	pseudoCText, sourceMap := decomp.GeneratePseudoC(ir)
	compilableCText, _ := decomp.GenerateCompilableC(ir)

	out := map[string]any{
		"block_id":      targetBlock.ID,
		"start_address": targetBlock.StartAddress,
		"end_address":   targetBlock.EndAddress,
		"entry_context": entryCtx,
		"pseudoc":       pseudoCText,
		"compilable_c":  compilableCText,
		"source_map":    sourceMap,
		"lowering_coverage": map[string]int{
			"lowered":     ir.LoweredCount,
			"total":       ir.TotalCount,
			"unsupported": ir.UnsupportedCount,
		},
		"assumptions": ir.Assumptions,
	}

	if r.URL.Query().Get("validate") == "true" {
		http.Error(w, "GET /api/pseudoc is read-only; use POST /api/pseudoc/validate or CLI 'snesdasm pseudoc -validate' to run verification", http.StatusBadRequest)
		return
	}

	saved, err := decomp.LoadReceipt(decomp.ReceiptPath(s.ProjectDir, targetBlock.ID))
	if err == nil {
		decomp.ValidateReceiptFreshness(&saved, ir, compilableCText, nil, s.Document.ROM.NormalizedSHA256, s.Revision)
		out["saved_receipt"] = saved
		if r.URL.Query().Get("receipt") == "true" {
			out["validation_receipt"] = saved
		}
	}

	if r.URL.Query().Get("cases") == "true" {
		cs, _ := decomp.ListCases(s.ProjectDir, targetBlock.ID)
		if cs == nil {
			cs = []decomp.ReplayCase{}
		}
		out["replay_cases"] = cs
	}

	savedReplay, err := decomp.LoadReplayReceipt(decomp.ReplayReceiptPath(s.ProjectDir, targetBlock.ID))
	if err == nil {
		cs, _ := decomp.ListCases(s.ProjectDir, targetBlock.ID)
		var matchedCase *decomp.ReplayCase
		for i := range cs {
			if cs[i].CaseID == savedReplay.CaseID {
				matchedCase = &cs[i]
				break
			}
		}
		if matchedCase == nil && len(cs) > 0 {
			matchedCase = &cs[0]
		}
		decomp.ValidateReplayReceiptFreshness(&savedReplay, matchedCase, ir, compilableCText, s.Document.ROM.NormalizedSHA256, s.Revision)
		out["saved_replay_receipt"] = savedReplay
		if r.URL.Query().Get("replay_receipt") == "true" {
			out["replay_receipt"] = savedReplay
		}
	}

	writeJSON(w, out)
}

func (s *Server) handlePseudocValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed; use POST to validate", http.StatusMethodNotAllowed)
		return
	}

	var targetAddr uint32
	if addrStr := r.URL.Query().Get("addr"); addrStr != "" {
		a, err := parseAddress(addrStr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		targetAddr = a
	}

	var targetBlock *structure.BasicBlock
	if targetAddr != 0 {
		for _, b := range s.Blocks {
			if b.StartAddress == targetAddr || (targetAddr >= b.StartAddress && targetAddr < b.EndAddress) {
				targetBlock = b
				break
			}
		}
		if targetBlock == nil {
			http.Error(w, fmt.Sprintf("no block found covering address $%06X", targetAddr), http.StatusNotFound)
			return
		}
	} else {
		for _, b := range s.Blocks {
			if b.StartAddress == 0x0086DF {
				targetBlock = b
				break
			}
		}
		if targetBlock == nil && len(s.Blocks) > 0 {
			targetBlock = s.Blocks[0]
		}
	}

	if targetBlock == nil {
		http.Error(w, "no basic blocks available", http.StatusNotFound)
		return
	}

	entryCtx := s.Document.Instructions[0].Context
	if len(targetBlock.Instructions) > 0 && targetBlock.Instructions[0].Context.M != "" {
		entryCtx = targetBlock.Instructions[0].Context
	}

	ir, err := decomp.LiftBlock(targetBlock, entryCtx)
	if err != nil {
		http.Error(w, fmt.Sprintf("lift block: %v", err), http.StatusInternalServerError)
		return
	}

	compilableCText, compErr := decomp.GenerateCompilableC(ir)
	if compErr != nil {
		http.Error(w, fmt.Sprintf("generate compilable C: %v", compErr), http.StatusInternalServerError)
		return
	}

	pb := uint8(targetBlock.StartAddress >> 16)
	pc := uint16(targetBlock.StartAddress & 0xFFFF)
	initState := decomp.CPUState{
		A:  0x0000,
		X:  0x0000,
		Y:  0x0000,
		S:  0x01FF,
		D:  0x0000,
		DB: pb,
		PB: pb,
		PC: pc,
		P:  0x00,
		E:  false,
	}
	if entryCtx.M == "set" || entryCtx.M == "1" {
		initState.P |= 0x20
	}
	if entryCtx.X == "set" || entryCtx.X == "1" {
		initState.P |= 0x10
	}
	if entryCtx.E == "set" || entryCtx.E == "1" {
		initState.E = true
	}

	cfg := decomp.DefaultVerifyConfig()
	cfg.ProjectRevision = s.Revision
	cfg.ROMSHA256 = s.Document.ROM.NormalizedSHA256

	mem := make(map[uint32]uint8)
	receipt := decomp.Compare(r.Context(), ir, "default_verification", initState, mem, cfg)

	// Save receipt atomically
	receiptPath := decomp.ReceiptPath(s.ProjectDir, targetBlock.ID)
	if err := decomp.SaveReceipt(receiptPath, receipt); err != nil {
		http.Error(w, fmt.Sprintf("save receipt: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"block_id":           targetBlock.ID,
		"validation_receipt": receipt,
		"compilable_c":       compilableCText,
	})
}

func (s *Server) handlePseudocReplay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed; use POST to run replay", http.StatusMethodNotAllowed)
		return
	}

	var targetAddr uint32
	if addrStr := r.URL.Query().Get("addr"); addrStr != "" {
		a, err := parseAddress(addrStr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		targetAddr = a
	}

	var targetBlock *structure.BasicBlock
	if targetAddr != 0 {
		for _, b := range s.Blocks {
			if b.StartAddress <= targetAddr && targetAddr < b.EndAddress {
				targetBlock = b
				break
			}
		}
		if targetBlock == nil {
			http.Error(w, fmt.Sprintf("no block found covering address $%06X", targetAddr), http.StatusNotFound)
			return
		}
	} else {
		for _, b := range s.Blocks {
			if b.StartAddress == 0x0086DF {
				targetBlock = b
				break
			}
		}
		if targetBlock == nil && len(s.Blocks) > 0 {
			targetBlock = s.Blocks[0]
		}
	}

	if targetBlock == nil {
		http.Error(w, "no basic blocks available", http.StatusNotFound)
		return
	}

	entryCtx := s.Document.Instructions[0].Context
	if len(targetBlock.Instructions) > 0 && targetBlock.Instructions[0].Context.M != "" {
		entryCtx = targetBlock.Instructions[0].Context
	}

	ir, err := decomp.LiftBlock(targetBlock, entryCtx)
	if err != nil {
		http.Error(w, fmt.Sprintf("lift block: %v", err), http.StatusInternalServerError)
		return
	}

	cs, _ := decomp.ListCases(s.ProjectDir, targetBlock.ID)
	if len(cs) == 0 {
		http.Error(w, fmt.Sprintf("no captured replay cases found for block %s", targetBlock.ID), http.StatusNotFound)
		return
	}
	targetCase := cs[0]

	runner, err := decomp.NewCompiledRunner(r.Context(), ir)
	if err != nil {
		http.Error(w, fmt.Sprintf("create runner: %v", err), http.StatusInternalServerError)
		return
	}
	defer runner.Close()

	cfg := decomp.DefaultVerifyConfig()
	cfg.ROMSHA256 = s.Document.ROM.NormalizedSHA256
	cfg.ProjectRevision = s.Revision

	receipt := decomp.ExecuteThreeWayReplay(r.Context(), ir, targetCase, runner, cfg)

	receiptPath := decomp.ReplayReceiptPath(s.ProjectDir, targetBlock.ID)
	if err := decomp.SaveReplayReceipt(receiptPath, receipt); err != nil {
		http.Error(w, fmt.Sprintf("save replay receipt: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"block_id":       targetBlock.ID,
		"case_id":        targetCase.CaseID,
		"replay_receipt": receipt,
	})
}

func (s *Server) handleProvenance(w http.ResponseWriter, r *http.Request) {
	frameStr := r.URL.Query().Get("frame")
	xStr := r.URL.Query().Get("x")
	yStr := r.URL.Query().Get("y")

	if frameStr == "" || xStr == "" || yStr == "" {
		http.Error(w, "missing required parameters: frame, x, y", http.StatusBadRequest)
		return
	}

	frame, errF := strconv.Atoi(frameStr)
	x, errX := strconv.Atoi(xStr)
	y, errY := strconv.Atoi(yStr)
	if errF != nil || errX != nil || errY != nil || x < 0 || x > 255 || y < 0 || y > 239 {
		http.Error(w, "invalid frame, x, or y coordinates", http.StatusBadRequest)
		return
	}

	if s.ProvenanceEngine == nil {
		s.ProvenanceEngine = visualmap.NewEngine(s.Document, s.Blocks)
	}

	prov, err := s.ProvenanceEngine.Query(r.Context(), frame, x, y)
	if err != nil {
		http.Error(w, fmt.Sprintf("provenance query: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, prov)
}

