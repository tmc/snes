package decomp

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mrand "math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
)

const (
	MaxBatchCases   = 1000
	MaxBatchBytes   = 4 * 1024 * 1024 // 4 MiB
	MaxCellsPerCase = 256
)

// DerivedMemoryCell marks an operand memory input derived from instruction semantics
// or observed post-instruction register states, rather than independently observed bus reads.
type DerivedMemoryCell struct {
	Address          uint32 `json:"address"`
	Value            uint8  `json:"value"`
	DerivationReason string `json:"derivation_reason"`
}

// CaseIdentity contains immutable identifiers binding an execution occurrence to its source trace.
type CaseIdentity struct {
	CaseID                   string `json:"case_id"`
	BlockID                  string `json:"block_id"`
	RunID                    string `json:"run_id"`
	StreamSHA256             string `json:"stream_sha256"`
	DecompressedStreamSHA256 string `json:"decompressed_stream_sha256,omitempty"`
	ROMSHA256                string `json:"rom_sha256"`
	Frame                    int    `json:"frame"`
	EntrySeq                 uint64 `json:"entry_seq"`
	ExitSeq                  uint64 `json:"exit_seq"`
	StartEventID             uint64 `json:"start_event_id,omitempty"`
	EndEventID               uint64 `json:"end_event_id,omitempty"`
	StartCycle               uint64 `json:"start_cycle,omitempty"`
	EndCycle                 uint64 `json:"end_cycle,omitempty"`
	ObservedNextPC           uint32 `json:"observed_next_pc"`
	ObservedEffectsState     string `json:"observed_effects_state"` // e.g. "not_captured_in_trace"
}

// ReplayCase represents an observed execution occurrence from an emulator trace run.
type ReplayCase struct {
	SchemaVersion            string              `json:"schema_version"`
	CaseID                   string              `json:"case_id"`
	BlockID                  string              `json:"block_id,omitempty"`
	RoutineID                string              `json:"routine_id,omitempty"`
	RunID                    string              `json:"run_id"`
	StreamSHA256             string              `json:"stream_sha256,omitempty"`
	DecompressedStreamSHA256 string              `json:"decompressed_stream_sha256,omitempty"`
	ROMSHA256                string              `json:"rom_sha256"`
	EngineRevision           string              `json:"engine_revision,omitempty"`
	Frame                    int                 `json:"frame"`
	CallSeq                  uint64              `json:"call_seq,omitempty"`
	CallPC                   uint32              `json:"call_pc,omitempty"`
	EntrySeq                 uint64              `json:"entry_seq"`
	ExitSeq                  uint64              `json:"exit_seq"`
	ReturnSeq                uint64              `json:"return_seq,omitempty"`
	ReturnInsnPC             uint32              `json:"return_insn_pc,omitempty"`
	InstructionCount         int                 `json:"instruction_count,omitempty"`
	StartEventID             uint64              `json:"start_event_id,omitempty"`
	EndEventID               uint64              `json:"end_event_id,omitempty"`
	StartCycle               uint64              `json:"start_cycle,omitempty"`
	EndCycle                 uint64              `json:"end_cycle,omitempty"`
	InitialState             CPUState            `json:"initial_state"`
	DerivedMemory            []DerivedMemoryCell `json:"derived_memory,omitempty"`
	InitialMemory            []MemoryCell        `json:"initial_memory"`
	ObservedExit             CPUState            `json:"observed_exit_state"`
	ObservedNextPC           uint32              `json:"observed_next_pc"`
	ObservedBranch           string              `json:"observed_branch"` // "taken" | "fallthrough"
	ObservedEffectsCapture   bool                `json:"observed_effects_captured"`
	ObservedWrites           []MemoryWrite       `json:"observed_writes,omitempty"`
	InitialCPUHash           string              `json:"initial_cpu_hash,omitempty"`
	InitialMemHash           string              `json:"initial_mem_hash,omitempty"`
	CaseHash                 string              `json:"case_hash,omitempty"`
	AdmissionDigest          string              `json:"admission_digest,omitempty"`
	Provenance               *CaseProvenance     `json:"provenance,omitempty"`
	Evidence                 *CaseEvidence       `json:"evidence,omitempty"`
}

// CaseEvidence models cryptographic evidence and capture-run provenance attached to a v2 replay case.
type CaseEvidence struct {
	Label               string                     `json:"label,omitempty"`
	Corpus              string                     `json:"corpus,omitempty"`
	OccurrenceInFrame   int                        `json:"occurrence_in_frame,omitempty"`
	Fixture             *EvidenceFileRef           `json:"fixture,omitempty"`
	Capture             *EvidenceFileRef           `json:"capture,omitempty"`
	History             *EvidenceFileRef           `json:"history,omitempty"`
	Inputs              *EvidenceFileRef           `json:"inputs,omitempty"`
	Checkpoint          *EvidenceCheckpointRef     `json:"checkpoint,omitempty"`
	StartBoundary       string                     `json:"start_boundary,omitempty"`
	InitialMemorySource []InitialMemorySourceClaim `json:"initial_memory_source,omitempty"`
	Accesses            []CaseAccess               `json:"accesses,omitempty"`
	Records             []RetainedTraceEvent       `json:"records,omitempty"`
}

// EvidenceCheckpointRef models a referenced checkpoint state snapshot.
type EvidenceCheckpointRef struct {
	AbsoluteFrame         int              `json:"absolute_frame,omitempty"`
	Path                  string           `json:"path"`
	SHA256                string           `json:"sha256"`
	PowerOnHistorySummary *EvidenceFileRef `json:"power_on_history_summary,omitempty"`
}

// EvidenceFileRef models a referenced file with its cryptographic digest and optional summary/receipt.
type EvidenceFileRef struct {
	Path               string           `json:"path"`
	SHA256             string           `json:"sha256"`
	DecompressedSHA256 string           `json:"decompressed_sha256,omitempty"`
	EngineRevision     string           `json:"engine_revision,omitempty"`
	Receipt            *EvidenceFileRef `json:"receipt,omitempty"`
	Summary            *EvidenceFileRef `json:"summary,omitempty"`
}

// InitialMemorySourceClaim represents an explicit classification claim for an initial memory cell.
type InitialMemorySourceClaim struct {
	Address uint32 `json:"address"`
	Source  string `json:"source"` // "confirmed_by_prior_write" | "power_on_wram"
}

// CaseProvenance models cryptographic and capture-run provenance attached to a replay case.
type CaseProvenance struct {
	CaptureEngineDirty    bool           `json:"capture_engine_dirty"`
	CaptureEngineRevision string         `json:"capture_engine_revision"`
	CaptureStreamSHA256   string         `json:"capture_stream_sha256"`
	FixtureEngineRevision string         `json:"fixture_engine_revision"`
	FixtureStreamSHA256   string         `json:"fixture_stream_sha256"`
	MemoryCapture         string         `json:"memory_capture"`
	MemoryComplete        bool           `json:"memory_complete"`
	OccurrenceInFrame     int            `json:"occurrence_in_frame"`
	Start                 string         `json:"start"`
	Accesses              []CaseAccess   `json:"accesses,omitempty"`
	HistoryChecks         []HistoryCheck `json:"history_checks,omitempty"`
}

// CaseAccess records a single bus-level memory access captured during an execution occurrence.
type CaseAccess struct {
	Address    uint32 `json:"address"`
	BusAddress uint32 `json:"bus_address"`
	Cycle      uint64 `json:"cycle"`
	Op         string `json:"op"`
	Seq        uint64 `json:"seq"`
	Value      uint8  `json:"value"`
	Before     *uint8 `json:"before,omitempty"`
}

// HistoryCheck records independent verification of initial memory against prior write events.
type HistoryCheck struct {
	Address        uint32  `json:"address"`
	LastWriteCycle *uint64 `json:"last_write_cycle"`
	Match          *bool   `json:"match"`
	Value          *uint8  `json:"value"`
	PowerOnValue   *uint8  `json:"power_on_value"`
}

// NormalizeProducerCase normalizes a captured replay case from producer tools,
// ensuring stream identities are disambiguated from engine revision,
// preserving power-on state dependencies, and populating canonical hashes.
// Observed effects capture is NOT set here; it is established only via AdmitVerifiedReplayCase.
func NormalizeProducerCase(c *ReplayCase) {
	if c.Provenance != nil {
		if c.Provenance.FixtureStreamSHA256 != "" {
			c.StreamSHA256 = c.Provenance.FixtureStreamSHA256
		} else if c.StreamSHA256 == "" && c.RunID != "" {
			c.StreamSHA256 = c.RunID
		}
		if c.Provenance.FixtureEngineRevision != "" && c.EngineRevision == "" {
			c.EngineRevision = c.Provenance.FixtureEngineRevision
		}
	}
	if c.Evidence != nil {
		if c.Evidence.Fixture != nil {
			if c.StreamSHA256 == "" {
				c.StreamSHA256 = c.Evidence.Fixture.SHA256
			}
			if c.EngineRevision == "" {
				c.EngineRevision = c.Evidence.Fixture.EngineRevision
			}
		}
		if c.RunID == "" && c.StreamSHA256 != "" {
			c.RunID = c.StreamSHA256
		}
	}
	if c.InitialCPUHash == "" && c.InitialState.PC != 0 {
		c.InitialCPUHash = ComputeCPUStateHash(c.InitialState)
	}
	if c.InitialMemHash == "" && len(c.InitialMemory) > 0 {
		memMap := make(map[uint32]uint8, len(c.InitialMemory))
		for _, cell := range c.InitialMemory {
			memMap[cell.Address] = cell.Value
		}
		c.InitialMemHash, _ = ComputeInitialMemory(memMap)
	}
}

// CorpusTrustRoot pins the authoritative identity of an approved trace corpus.
type CorpusTrustRoot struct {
	Label                string
	ROMSHA256            string
	FixtureSHA256        string
	FixtureReceiptSHA256 string
	FixtureSummarySHA256 string
	CaptureSHA256        string
	CaptureReceiptSHA256 string
	CaptureSummarySHA256 string
	HistorySHA256        string
	HistoryReceiptSHA256 string
	HistorySummarySHA256 string
	CheckpointSHA256     string
	InputsSHA256         string
	DecompressedSHA      string
	EngineRevision       string
	AllowDirtyEngine     bool
}

// ComputeAdmissionDigest computes a cryptographic digest binding the case identity,
// block identity, canonical case hash, and source evidence stream hashes.
func ComputeAdmissionDigest(caseID, blockID, caseHash, fixSHA, capSHA, histSHA string) string {
	h := sha256.New()
	fmt.Fprintf(h, "case:%s|block:%s|case_hash:%s|fix:%s|cap:%s|hist:%s",
		caseID, blockID, caseHash, fixSHA, capSHA, histSHA)
	return hex.EncodeToString(h.Sum(nil))
}

// AdmissionRecord records the verified admission outcome of a replay case.
type AdmissionRecord struct {
	CaseID                 string   `json:"case_id"`
	BlockID                string   `json:"block_id,omitempty"`
	RoutineID              string   `json:"routine_id,omitempty"`
	Admitted               bool     `json:"admitted"`
	Reason                 string   `json:"reason,omitempty"`
	ObservedEffectsCapture bool     `json:"observed_effects_captured"`
	InitialMemorySources   []string `json:"initial_memory_sources,omitempty"`
	Classification         []string `json:"classification,omitempty"`
	AdmissionDigest        string   `json:"admission_digest,omitempty"`
	PolicySHA256           string   `json:"policy_sha256,omitempty"`
	ROMSHA256              string   `json:"rom_sha256,omitempty"`
}

type captureFetch struct {
	Addr      uint32 `json:"addr"`
	Value     uint8  `json:"value"`
	Role      string `json:"role"`
	ROMOffset uint32 `json:"rom_offset"`
}

type captureCPUInsn struct {
	Seq         uint64             `json:"seq"`
	Entry       cpuStateWithCycles `json:"entry"`
	Exit        cpuStateWithCycles `json:"exit"`
	Fetches     []captureFetch     `json:"fetches,omitempty"`
	Length      int                `json:"length"`
	SuccessorPC targetPC           `json:"successor_pc"`
	Status      string             `json:"status"`
}

func cpuInsnEqual(a, b captureCPUInsn) bool {
	if a.Seq != b.Seq || a.Length != b.Length || a.Status != b.Status {
		return false
	}
	if a.SuccessorPC != b.SuccessorPC {
		return false
	}
	if !cpuStateEqualWithCycles(a.Entry, b.Entry) || a.Entry.Cycles != b.Entry.Cycles {
		return false
	}
	if !cpuStateEqualWithCycles(a.Exit, b.Exit) || a.Exit.Cycles != b.Exit.Cycles {
		return false
	}
	if len(a.Fetches) != len(b.Fetches) {
		return false
	}
	for i := range a.Fetches {
		if a.Fetches[i] != b.Fetches[i] {
			return false
		}
	}
	return true
}

type cpuStateWithCycles struct {
	A      uint16 `json:"a"`
	X      uint16 `json:"x"`
	Y      uint16 `json:"y"`
	S      uint16 `json:"s"`
	D      uint16 `json:"d"`
	DB     uint8  `json:"db"`
	PB     uint8  `json:"pb"`
	PC     uint16 `json:"pc"`
	P      uint8  `json:"p"`
	E      bool   `json:"e"`
	Cycles uint64 `json:"cycles"`
}

type targetPC struct {
	Bank uint8  `json:"bank"`
	Addr uint16 `json:"addr"`
}

type busEvent struct {
	Actor         string
	CPUPC         uint32
	CPUOpcode     byte
	CPUBytes      []byte
	ROM           bool
	ROMOffset     uint32
	Schema, Width int
	ValueKnown    bool
	ID            uint64 `json:"id"`
	Cycle         uint64 `json:"cycle"`
	Kind          string `json:"kind"`
	Space         string `json:"space"`
	Addr          uint32 `json:"addr"`
	Op            string `json:"op"`
	Value         *uint8 `json:"value"`
	After         *uint8 `json:"after"`
}

type captureTransition struct {
	Seq   uint64
	Cycle uint64
	Kind  string
}

type captureGap struct {
	FirstSeq uint64
	LastSeq  uint64
}

type captureRunHeader struct {
	ROMSHA256          string  `json:"rom_sha256"`
	Start              string  `json:"start"`
	InitialStateSHA256 string  `json:"initial_state_sha256"`
	EngineRevision     string  `json:"engine_revision"`
	EngineDirty        bool    `json:"engine_dirty"`
	ReplayInputSHA256  *string `json:"replay_input_sha256"`
}

type captureData struct {
	header      captureRunHeader
	insns       map[uint64]captureCPUInsn
	bus         []busEvent
	helperBus   []busEvent
	transitions []captureTransition
	gaps        []captureGap
}

type fixtureData struct {
	header captureRunHeader
	recs   map[uint64]captureCPUInsn
}

type historyWrite struct {
	ID        uint64
	Cycle     uint64
	Value     uint8
	Actor     string
	CPUPBR    byte
	CPUPC     uint16
	CPUOpcode byte
	CPUBytes  []byte
	CPUS      uint16
	CPUA      uint16
	CPUX      uint16
	CPUY      uint16
	CPUD      uint16
	CPUDB     byte
	CPUP      byte
	CPUE      bool
}

type runSummary struct {
	ROMHash       string              `json:"rom_hash"`
	InputHash     *string             `json:"input_hash"`
	TraceHash     string              `json:"trace_hash"`
	Op            string              `json:"op"`
	FrameSummary  []frameSummaryEntry `json:"frame_summary"`
	AddressRanges []addressRange      `json:"address_ranges"`
}

type frameSummaryEntry struct {
	Frame           int               `json:"frame"`
	StateHash       string            `json:"state_hash"`
	FramebufferHash string            `json:"framebuffer_hash"`
	ComponentHashes map[string]string `json:"component_hashes"`
}

type addressRange struct {
	Space string `json:"space"`
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
}

type streamReceipt struct {
	Schema       int    `json:"schema"`
	Outcome      string `json:"outcome"`
	StreamSHA256 string `json:"stream_sha256"`
}

// EvidenceVerifier coordinates verified admission of captured execution cases.
type EvidenceVerifier struct {
	corpusRoot    string
	policy        *admissionPolicy
	mu            sync.Mutex
	receipts      map[string]*streamReceipt
	summaries     map[string]*runSummary
	captures      map[string]*captureData
	fixtures      map[string]*fixtureData
	verified      map[fixtureSnapshotKey]*verifiedFixture
	histories     map[string]map[uint32][]historyWrite
	admittedCases map[string]AdmissionRecord
}

// NewEvidenceVerifier creates a verifier without admission authority. Use
// NewEvidenceVerifierWithPolicy with explicit operator-reviewed roots and ROM
// bytes to admit captured cases. The zero value is not usable.
func NewEvidenceVerifier(corpusRoot string) *EvidenceVerifier {
	v := newEvidenceVerifier(corpusRoot)
	v.policy, _ = copyAdmissionPolicy(AdmissionPolicy{}, "", false)
	return v
}

func newEvidenceVerifier(corpusRoot string) *EvidenceVerifier {
	if corpusRoot == "" {
		corpusRoot = "."
	}
	return &EvidenceVerifier{
		corpusRoot:    corpusRoot,
		receipts:      make(map[string]*streamReceipt),
		summaries:     make(map[string]*runSummary),
		captures:      make(map[string]*captureData),
		fixtures:      make(map[string]*fixtureData),
		verified:      make(map[fixtureSnapshotKey]*verifiedFixture),
		histories:     make(map[string]map[uint32][]historyWrite),
		admittedCases: make(map[string]AdmissionRecord),
	}
}

// IsAdmitted checks whether a case identity and admission digest were granted by verified admission.
func (v *EvidenceVerifier) IsAdmitted(caseHash, digest string) bool {
	if v == nil || v.policy == nil || caseHash == "" || digest == "" {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	rec, ok := v.admittedCases[caseHash+":"+digest]
	return ok && rec.Admitted
}

// recordAdmission stores a successful admission outcome for a case.
func (v *EvidenceVerifier) recordAdmission(caseHash, digest string, rec AdmissionRecord) {
	if v == nil || v.policy == nil || caseHash == "" || digest == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.admittedCases[caseHash+":"+digest] = rec
}

// GetAdmissionRecord retrieves a stored admission record for a case hash and admission digest.
func (v *EvidenceVerifier) GetAdmissionRecord(caseHash, digest string) (AdmissionRecord, bool) {
	if v == nil || v.policy == nil || caseHash == "" || digest == "" {
		return AdmissionRecord{}, false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	rec, ok := v.admittedCases[caseHash+":"+digest]
	return rec, ok
}

var defaultEvidenceVerifier = NewEvidenceVerifier("")

func (v *EvidenceVerifier) resolvePath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if v.corpusRoot != "" {
		cand := filepath.Join(v.corpusRoot, p)
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	home, _ := os.UserHomeDir()
	cand := filepath.Join(home, "tmp/agent-collab/snes", p)
	if _, err := os.Stat(cand); err == nil {
		return cand
	}
	return p
}

func (v *EvidenceVerifier) sha(relPath string, decompress bool) (string, error) {
	absPath := v.resolvePath(relPath)
	f, err := os.Open(absPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var r io.Reader = f
	if decompress && strings.HasSuffix(absPath, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return "", err
		}
		defer gz.Close()
		r = gz
	}

	h := sha256.New()
	buf := make([]byte, 1024*1024)
	if _, err := io.CopyBuffer(h, r, buf); err != nil {
		return "", err
	}
	digest := hex.EncodeToString(h.Sum(nil))

	return digest, nil
}

func (v *EvidenceVerifier) loadReceipt(relPath string) (*streamReceipt, error) {
	absPath := v.resolvePath(relPath)
	v.mu.Lock()
	if r, ok := v.receipts[absPath]; ok {
		v.mu.Unlock()
		return r, nil
	}
	v.mu.Unlock()

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	var rc streamReceipt
	if err := json.Unmarshal(data, &rc); err != nil {
		return nil, err
	}
	v.mu.Lock()
	v.receipts[absPath] = &rc
	v.mu.Unlock()
	return &rc, nil
}

func (v *EvidenceVerifier) loadSummary(relPath string) (*runSummary, error) {
	absPath := v.resolvePath(relPath)
	v.mu.Lock()
	if s, ok := v.summaries[absPath]; ok {
		v.mu.Unlock()
		return s, nil
	}
	v.mu.Unlock()

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	var s runSummary
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	v.mu.Lock()
	v.summaries[absPath] = &s
	v.mu.Unlock()
	return &s, nil
}

func (v *EvidenceVerifier) loadCapture(relPath string) (*captureData, error) {
	absPath := v.resolvePath(relPath)
	v.mu.Lock()
	if c, ok := v.captures[absPath]; ok {
		v.mu.Unlock()
		return c, nil
	}
	v.mu.Unlock()

	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cd := &captureData{
		insns: make(map[uint64]captureCPUInsn),
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var evt struct {
			CPU *struct {
				PBR    *byte   `json:"pbr"`
				PC     *uint16 `json:"pc"`
				Opcode *byte   `json:"opcode"`
				Bytes  []byte  `json:"bytes"`
			} `json:"cpu"`
			Source *struct {
				Space string `json:"space"`
				Start uint32 `json:"start"`
			} `json:"source"`
			DMA        json.RawMessage   `json:"dma"`
			Kind       string            `json:"kind"`
			Run        *captureRunHeader `json:"run"`
			Insn       *captureCPUInsn   `json:"insn"`
			ID         uint64            `json:"id"`
			Schema     int               `json:"schema"`
			Width      int               `json:"width"`
			Cycle      uint64            `json:"cycle"`
			Space      string            `json:"space"`
			Addr       uint32            `json:"addr"`
			Op         string            `json:"op"`
			Value      *uint8            `json:"value"`
			After      *uint8            `json:"after"`
			Transition *struct {
				Seq   uint64 `json:"seq"`
				Kind  string `json:"kind"`
				Cycle uint64 `json:"cycle"`
			} `json:"transition"`
			Gap *struct {
				FirstSeq uint64 `json:"first_seq"`
				LastSeq  uint64 `json:"last_seq"`
			} `json:"gap"`
		}
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		if evt.Kind == "run" && evt.Run != nil {
			cd.header = *evt.Run
		} else if evt.Kind == "cpu_insn" && evt.Insn != nil {
			cd.insns[evt.Insn.Seq] = *evt.Insn
		} else if evt.Kind == "bus" {
			actor := "unknown"
			var pc uint32
			var opcode byte
			if evt.CPU != nil && evt.CPU.PBR != nil && evt.CPU.PC != nil && evt.CPU.Opcode != nil && (len(evt.DMA) == 0 || string(evt.DMA) == "null") {
				actor = "cpu"
				pc = uint32(*evt.CPU.PBR)<<16 | uint32(*evt.CPU.PC)
				opcode = *evt.CPU.Opcode
			} else if len(evt.DMA) > 0 && string(evt.DMA) != "null" {
				actor = "dma"
			}
			var cpuBytes []byte
			if evt.CPU != nil {
				cpuBytes = append([]byte(nil), evt.CPU.Bytes...)
			}
			isROM := evt.Source != nil && evt.Source.Space == "rom"
			var romOffset uint32
			if isROM {
				romOffset = evt.Source.Start
			}
			var fields map[string]json.RawMessage
			json.Unmarshal(line, &fields)
			_, hasValue := fields["value"]
			known := evt.Value != nil || (!hasValue && evt.Schema == 2 && evt.Op == "read")
			if evt.Op == "write" {
				known = evt.After != nil || evt.Value != nil
			}
			if (hasValue && evt.Value == nil) || (fields["after"] != nil && evt.After == nil) || (evt.Value != nil && evt.After != nil && *evt.Value != *evt.After) {
				known = false
			}
			e := busEvent{Actor: actor, CPUPC: pc, CPUOpcode: opcode, CPUBytes: cpuBytes, ROM: isROM, ROMOffset: romOffset,
				Schema: evt.Schema, Width: evt.Width, ValueKnown: known,
				ID:    evt.ID,
				Cycle: evt.Cycle,
				Kind:  evt.Kind,
				Space: evt.Space,
				Addr:  evt.Addr,
				Op:    evt.Op,
				Value: evt.Value,
				After: evt.After,
			}
			cd.helperBus = append(cd.helperBus, e)
			if evt.Space == "wram" {
				cd.bus = append(cd.bus, e)
			}
		} else if evt.Kind == "cpu_transition" && evt.Transition != nil {
			cd.transitions = append(cd.transitions, captureTransition{
				Seq:   evt.Transition.Seq,
				Cycle: evt.Cycle,
				Kind:  evt.Transition.Kind,
			})
		} else if evt.Kind == "gap" && evt.Gap != nil {
			cd.gaps = append(cd.gaps, captureGap{
				FirstSeq: evt.Gap.FirstSeq,
				LastSeq:  evt.Gap.LastSeq,
			})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	sort.Slice(cd.bus, func(i, j int) bool {
		if cd.bus[i].Cycle != cd.bus[j].Cycle {
			return cd.bus[i].Cycle < cd.bus[j].Cycle
		}
		return cd.bus[i].ID < cd.bus[j].ID
	})

	v.mu.Lock()
	v.captures[absPath] = cd
	v.mu.Unlock()
	return cd, nil
}

func (v *EvidenceVerifier) loadHistory(relPath string) (map[uint32][]historyWrite, error) {
	absPath := v.resolvePath(relPath)
	v.mu.Lock()
	if h, ok := v.histories[absPath]; ok {
		v.mu.Unlock()
		return h, nil
	}
	v.mu.Unlock()

	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	hist := make(map[uint32][]historyWrite)
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.Contains(line, []byte(`"write"`)) {
			continue
		}
		var evt struct {
			Schema *int            `json:"schema"`
			Width  *int            `json:"width"`
			Kind   string          `json:"kind"`
			Space  string          `json:"space"`
			Op     string          `json:"op"`
			Addr   uint32          `json:"addr"`
			ID     uint64          `json:"id"`
			Cycle  uint64          `json:"cycle"`
			After  *uint8          `json:"after"`
			Value  *uint8          `json:"value"`
			DMA    json.RawMessage `json:"dma"`
			CPU    *struct {
				PBR    *byte   `json:"pbr"`
				PC     *uint16 `json:"pc"`
				Opcode *byte   `json:"opcode"`
				Bytes  []byte  `json:"bytes"`
				A      *uint16 `json:"a"`
				X      *uint16 `json:"x"`
				Y      *uint16 `json:"y"`
				S      *uint16 `json:"s"`
				D      *uint16 `json:"dp"`
				DBR    *byte   `json:"dbr"`
				P      *byte   `json:"p"`
				E      *bool   `json:"e"`
			} `json:"cpu"`
		}
		if err := json.Unmarshal(line, &evt); err != nil {
			return nil, fmt.Errorf("decode history write: %w", err)
		}
		if evt.Kind == "bus" && evt.Op == "write" && evt.Space == "wram" {
			val := uint8(0)
			if evt.After != nil {
				val = *evt.After
			} else if evt.Value != nil {
				val = *evt.Value
			}
			hw := historyWrite{
				ID:    evt.ID,
				Cycle: evt.Cycle,
				Value: val,
			}
			if len(evt.DMA) > 0 && string(evt.DMA) != "null" {
				hw.Actor = "dma"
			} else if evt.CPU != nil && evt.Schema != nil && *evt.Schema == 2 && evt.Width != nil && *evt.Width == 1 &&
				(evt.Value != nil || evt.After != nil) && (evt.Value == nil || evt.After == nil || *evt.Value == *evt.After) &&
				evt.CPU.PBR != nil && evt.CPU.PC != nil && evt.CPU.Opcode != nil && evt.CPU.Bytes != nil &&
				evt.CPU.A != nil && evt.CPU.X != nil && evt.CPU.Y != nil && evt.CPU.S != nil && evt.CPU.D != nil &&
				evt.CPU.DBR != nil && evt.CPU.P != nil && evt.CPU.E != nil && (len(evt.DMA) == 0 || string(evt.DMA) == "null") {
				hw.Actor = "cpu"
				if evt.CPU.PBR != nil {
					hw.CPUPBR = *evt.CPU.PBR
				}
				if evt.CPU.PC != nil {
					hw.CPUPC = *evt.CPU.PC
				}
				if evt.CPU.Opcode != nil {
					hw.CPUOpcode = *evt.CPU.Opcode
				}
				hw.CPUBytes = append([]byte(nil), evt.CPU.Bytes...)
				if evt.CPU.S != nil {
					hw.CPUS = *evt.CPU.S
				}
				if evt.CPU.A != nil {
					hw.CPUA = *evt.CPU.A
				}
				if evt.CPU.X != nil {
					hw.CPUX = *evt.CPU.X
				}
				if evt.CPU.Y != nil {
					hw.CPUY = *evt.CPU.Y
				}
				if evt.CPU.D != nil {
					hw.CPUD = *evt.CPU.D
				}
				if evt.CPU.DBR != nil {
					hw.CPUDB = *evt.CPU.DBR
				}
				if evt.CPU.P != nil {
					hw.CPUP = *evt.CPU.P
				}
				if evt.CPU.E != nil {
					hw.CPUE = *evt.CPU.E
				}
			} else {
				hw.Actor = "unknown"
			}
			addr := 0x7E0000 + evt.Addr
			hist[addr] = append(hist[addr], hw)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	v.mu.Lock()
	v.histories[absPath] = hist
	v.mu.Unlock()
	return hist, nil
}

func (v *EvidenceVerifier) loadFixture(relPath string, wantSeqs map[uint64]bool) (*fixtureData, error) {
	absPath := v.resolvePath(relPath)
	v.mu.Lock()
	fd, exists := v.fixtures[absPath]
	if !exists {
		fd = &fixtureData{
			recs: make(map[uint64]captureCPUInsn),
		}
		v.fixtures[absPath] = fd
	}
	allFound := true
	for s := range wantSeqs {
		if _, ok := fd.recs[s]; !ok {
			allFound = false
			break
		}
	}
	if (allFound || len(wantSeqs) == 0) && fd.header.ROMSHA256 != "" {
		v.mu.Unlock()
		return fd, nil
	}
	v.mu.Unlock()

	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(absPath, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	}

	maxWant := uint64(0)
	remaining := 0
	for s := range wantSeqs {
		if s > maxWant {
			maxWant = s
		}
		v.mu.Lock()
		_, ok := fd.recs[s]
		v.mu.Unlock()
		if !ok {
			remaining++
		}
	}
	if remaining == 0 && fd.header.ROMSHA256 != "" {
		return fd, nil
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 8*1024*1024)
	isFirst := true
	for sc.Scan() {
		line := sc.Bytes()
		if isFirst {
			isFirst = false
			var runEvt struct {
				Run *captureRunHeader `json:"run"`
			}
			if err := json.Unmarshal(line, &runEvt); err == nil && runEvt.Run != nil {
				v.mu.Lock()
				fd.header = *runEvt.Run
				v.mu.Unlock()
			}
			if len(wantSeqs) == 0 {
				break
			}
			continue
		}
		if !bytes.Contains(line, []byte(`"cpu_insn"`)) {
			continue
		}
		idx := bytes.Index(line, []byte(`"seq":`))
		if idx == -1 {
			continue
		}
		p := idx + 6
		for p < len(line) && (line[p] == ' ' || line[p] == '\t') {
			p++
		}
		var s uint64
		for p < len(line) && line[p] >= '0' && line[p] <= '9' {
			s = s*10 + uint64(line[p]-'0')
			p++
		}
		if !wantSeqs[s] {
			if maxWant > 0 && s > maxWant {
				break
			}
			continue
		}

		var insnEvt struct {
			Kind string          `json:"kind"`
			Insn *captureCPUInsn `json:"insn"`
		}
		if err := json.Unmarshal(line, &insnEvt); err != nil {
			continue
		}
		if insnEvt.Kind == "cpu_insn" && insnEvt.Insn != nil {
			v.mu.Lock()
			if _, ok := fd.recs[s]; !ok {
				remaining--
			}
			fd.recs[s] = *insnEvt.Insn
			v.mu.Unlock()
			if remaining <= 0 {
				break
			}
		}
	}
	return fd, sc.Err()
}

func (v *EvidenceVerifier) checkFiles(c *ReplayCase, root CorpusTrustRoot) (*runSummary, error) {
	ev := c.Evidence
	fx := ev.Fixture
	cap := ev.Capture
	hi := ev.History

	refs := []*EvidenceFileRef{
		fx, fx.Receipt, fx.Summary,
		cap, cap.Receipt, cap.Summary,
		hi, hi.Summary,
	}
	if hi.Receipt != nil {
		refs = append(refs, hi.Receipt)
	}
	if ev.Inputs != nil {
		refs = append(refs, ev.Inputs)
	}

	for _, r := range refs {
		if r == nil || r.Path == "" {
			return nil, errors.New("missing referenced evidence file")
		}
		computed, err := v.sha(r.Path, false)
		if err != nil {
			return nil, fmt.Errorf("failed to hash %s: %w", r.Path, err)
		}
		if computed != r.SHA256 {
			return nil, fmt.Errorf("sha256 mismatch: %s", r.Path)
		}
	}

	if ev.Checkpoint != nil {
		if ev.Checkpoint.Path == "" || ev.Checkpoint.SHA256 == "" {
			return nil, errors.New("missing checkpoint evidence")
		}
		computed, err := v.sha(ev.Checkpoint.Path, false)
		if err != nil {
			return nil, fmt.Errorf("failed to hash checkpoint %s: %w", ev.Checkpoint.Path, err)
		}
		if computed != ev.Checkpoint.SHA256 {
			return nil, fmt.Errorf("checkpoint sha256 mismatch: %s", ev.Checkpoint.Path)
		}
		if root.CheckpointSHA256 != "" && ev.Checkpoint.SHA256 != root.CheckpointSHA256 {
			return nil, fmt.Errorf("checkpoint sha256 does not match root: %s", ev.Checkpoint.Path)
		}
		if ev.Checkpoint.PowerOnHistorySummary != nil {
			if ev.Checkpoint.PowerOnHistorySummary.Path == "" || ev.Checkpoint.PowerOnHistorySummary.SHA256 == "" {
				return nil, errors.New("missing power_on_history_summary evidence")
			}
			computedSummary, err := v.sha(ev.Checkpoint.PowerOnHistorySummary.Path, false)
			if err != nil {
				return nil, fmt.Errorf("failed to hash power_on_history_summary %s: %w", ev.Checkpoint.PowerOnHistorySummary.Path, err)
			}
			if computedSummary != ev.Checkpoint.PowerOnHistorySummary.SHA256 {
				return nil, fmt.Errorf("power_on_history_summary sha256 mismatch: %s", ev.Checkpoint.PowerOnHistorySummary.Path)
			}
			if root.HistorySummarySHA256 != "" && ev.Checkpoint.PowerOnHistorySummary.SHA256 != root.HistorySummarySHA256 {
				return nil, errors.New("power_on_history_summary sha256 does not match root history summary")
			}
		}
	}

	if c.RunID != fx.SHA256 || c.StreamSHA256 != fx.SHA256 {
		return nil, errors.New("run_id/stream_sha256 is not the fixture stream")
	}

	// Every raw artifact was freshly hashed above. A prefetched fixture's
	// decoded digest was measured over the same bytes that produced its immutable
	// records, so it can be reused only under the identical full trust root and
	// with complete requested-sequence coverage.
	want := make(map[uint64]bool)
	for _, seq := range caseFixtureSeqs(c) {
		want[seq] = true
	}
	var decomp string
	if vf, ok := v.verifiedFixtureSnapshot(fx.Path, root, want); ok && len(want) != 0 && fx.SHA256 == vf.sha256 {
		decomp = vf.decompSHA256
	} else {
		var err error
		decomp, err = v.sha(fx.Path, strings.HasSuffix(fx.Path, ".gz"))
		if err != nil {
			return nil, fmt.Errorf("failed to compute decompressed sha for %s: %w", fx.Path, err)
		}
	}
	if root.DecompressedSHA == "" || decomp != root.DecompressedSHA {
		return nil, errors.New("decompressed sha256 does not match corpus trust root")
	}
	if fx.DecompressedSHA256 != "" && fx.DecompressedSHA256 != decomp {
		return nil, errors.New("fixture embedded decompressed sha256 mismatch")
	}
	if c.DecompressedStreamSHA256 != "" && c.DecompressedStreamSHA256 != decomp {
		return nil, errors.New("decompressed sha mismatch")
	}
	if c.DecompressedStreamSHA256 == "" {
		c.DecompressedStreamSHA256 = decomp
	}

	// Receipts
	receiptPairs := []struct {
		target *EvidenceFileRef
		rcpt   *EvidenceFileRef
	}{
		{fx, fx.Receipt},
		{cap, cap.Receipt},
	}
	if hi.Receipt != nil {
		receiptPairs = append(receiptPairs, struct {
			target *EvidenceFileRef
			rcpt   *EvidenceFileRef
		}{hi, hi.Receipt})
	}
	for _, pair := range receiptPairs {
		rc, err := v.loadReceipt(pair.rcpt.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to load receipt %s: %w", pair.rcpt.Path, err)
		}
		if rc.Outcome != "complete" || rc.StreamSHA256 != pair.target.SHA256 {
			return nil, fmt.Errorf("receipt does not bind %s", pair.target.Path)
		}
	}

	// Summaries
	fs, err := v.loadSummary(fx.Summary.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to load fixture summary: %w", err)
	}
	cs, err := v.loadSummary(cap.Summary.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to load capture summary: %w", err)
	}
	hs, err := v.loadSummary(hi.Summary.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to load history summary: %w", err)
	}

	if hs.TraceHash != hi.SHA256 || hs.Op != "write" {
		return nil, errors.New("history summary does not bind history")
	}
	if cs.TraceHash != cap.SHA256 {
		return nil, errors.New("capture summary does not bind capture")
	}

	var inSHA *string
	if ev.Inputs != nil {
		inSHA = &ev.Inputs.SHA256
	}

	for _, s := range []*runSummary{fs, cs, hs} {
		if s.ROMHash != c.ROMSHA256 {
			return nil, errors.New("summary rom mismatch")
		}
	}
	if inSHA == nil {
		if hs.InputHash != nil && *hs.InputHash != "" {
			return nil, errors.New("summary input mismatch")
		}
	} else {
		if hs.InputHash == nil || *hs.InputHash != *inSHA {
			return nil, errors.New("summary input mismatch")
		}
	}

	// Component and framebuffer parity is a projected frame comparison across runs.
	if ev.Checkpoint != nil {
		if len(fs.FrameSummary) != len(cs.FrameSummary) {
			return nil, errors.New("per-frame component/framebuffer hashes differ across fixture/capture")
		}
		for i := range fs.FrameSummary {
			ff := &fs.FrameSummary[i]
			cf := &cs.FrameSummary[i]
			if ff.FramebufferHash != cf.FramebufferHash {
				return nil, errors.New("per-frame component/framebuffer hashes differ across fixture/capture")
			}
			for k, v1 := range ff.ComponentHashes {
				if cf.ComponentHashes[k] != v1 {
					return nil, errors.New("per-frame component/framebuffer hashes differ across fixture/capture")
				}
			}
		}
		baseFrame := ev.Checkpoint.AbsoluteFrame
		if baseFrame < 0 || baseFrame >= len(hs.FrameSummary) {
			return nil, fmt.Errorf("checkpoint absolute frame %d out of range of history summary (%d frames)", baseFrame, len(hs.FrameSummary))
		}
		if hs.FrameSummary[baseFrame].StateHash != ev.Checkpoint.SHA256 {
			return nil, fmt.Errorf("history frame %d state hash does not match checkpoint sha256", baseFrame)
		}
		for i := range fs.FrameSummary {
			hIdx := baseFrame + 1 + i
			if hIdx < len(hs.FrameSummary) {
				ff := &fs.FrameSummary[i]
				hf := &hs.FrameSummary[hIdx]
				if ff.FramebufferHash != hf.FramebufferHash {
					return nil, errors.New("per-frame component/framebuffer hashes differ across fixture/capture and history")
				}
				for k, v1 := range ff.ComponentHashes {
					if hf.ComponentHashes[k] != v1 {
						return nil, errors.New("per-frame component/framebuffer hashes differ across fixture/capture and history")
					}
				}
			}
		}
	} else {
		if len(fs.FrameSummary) != len(cs.FrameSummary) || len(fs.FrameSummary) != len(hs.FrameSummary) {
			return nil, errors.New("per-frame component/framebuffer hashes differ across fixture/capture/history")
		}
		for i := range fs.FrameSummary {
			ff := &fs.FrameSummary[i]
			cf := &cs.FrameSummary[i]
			hf := &hs.FrameSummary[i]
			if ff.FramebufferHash != cf.FramebufferHash || ff.FramebufferHash != hf.FramebufferHash {
				return nil, errors.New("per-frame component/framebuffer hashes differ across fixture/capture/history")
			}
			for k, v1 := range ff.ComponentHashes {
				if cf.ComponentHashes[k] != v1 || hf.ComponentHashes[k] != v1 {
					return nil, errors.New("per-frame component/framebuffer hashes differ across fixture/capture/history")
				}
			}
		}
	}

	// Run headers
	fd, err := v.admissionFixture(fx.Path, root, want)
	if err != nil {
		return nil, fmt.Errorf("failed to load fixture: %w", err)
	}
	cd, err := v.loadCapture(cap.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to load capture: %w", err)
	}

	for _, h := range []*captureRunHeader{&fd.header, &cd.header} {
		if h.ROMSHA256 != c.ROMSHA256 || (h.EngineDirty && !root.AllowDirtyEngine) {
			return nil, errors.New("run header mismatch")
		}
		if ev.Checkpoint != nil {
			if h.Start != "checkpoint" || h.InitialStateSHA256 != ev.Checkpoint.SHA256 {
				return nil, errors.New("run header checkpoint mismatch")
			}
		} else {
			if h.Start != "power_on" {
				return nil, errors.New("run header mismatch")
			}
			if inSHA == nil {
				if h.ReplayInputSHA256 != nil && *h.ReplayInputSHA256 != "" {
					return nil, errors.New("run header input mismatch")
				}
			} else {
				if h.ReplayInputSHA256 == nil || *h.ReplayInputSHA256 != *inSHA {
					return nil, errors.New("run header input mismatch")
				}
			}
		}
	}

	if fd.header.EngineRevision != c.EngineRevision {
		return nil, errors.New("engine_revision is not the fixture engine")
	}

	return hs, nil
}

func cpuStateEqualWithCycles(s1, s2 cpuStateWithCycles) bool {
	return s1.A == s2.A &&
		s1.X == s2.X &&
		s1.Y == s2.Y &&
		s1.S == s2.S &&
		s1.D == s2.D &&
		s1.DB == s2.DB &&
		s1.PB == s2.PB &&
		s1.PC == s2.PC &&
		s1.P == s2.P &&
		s1.E == s2.E
}

func cpuStateMatchesCPUStateWithCycles(c CPUState, s cpuStateWithCycles) bool {
	return c.A == s.A &&
		c.X == s.X &&
		c.Y == s.Y &&
		c.S == s.S &&
		c.D == s.D &&
		c.DB == s.DB &&
		c.PB == s.PB &&
		c.PC == s.PC &&
		c.P == s.P &&
		c.E == s.E
}

// Admit verifies a replay case against its cryptographic evidence and underlying bus capture.
func (v *EvidenceVerifier) Admit(c *ReplayCase, block *structure.BasicBlock, ir *BlockIR) (AdmissionRecord, error) {
	if v == nil || v.policy == nil {
		return AdmissionRecord{}, errors.New("uninitialized evidence verifier")
	}
	if c == nil {
		return AdmissionRecord{Admitted: false, Reason: "nil replay case"}, errors.New("nil replay case")
	}
	rec := AdmissionRecord{
		CaseID:  c.CaseID,
		BlockID: c.BlockID,
	}

	if c.SchemaVersion != "snes-replay-case-v2" && c.SchemaVersion != "snes-routine-case-v1" {
		rec.Reason = "schema"
		return rec, errors.New(rec.Reason)
	}
	if c.Evidence == nil {
		rec.Reason = "legacy case without cryptographic evidence is ineligible for verified admission"
		return rec, errors.New(rec.Reason)
	}
	ev := c.Evidence
	root, ok := v.policy.roots[ev.Corpus]
	if !ok {
		rec.Reason = "unknown corpus"
		return rec, errors.New(rec.Reason)
	}

	if !v.policy.legacy && c.EngineRevision != root.EngineRevision {
		rec.Reason = "fixture engine identity differs from reviewed policy"
		return rec, errors.New(rec.Reason)
	}
	if err := v.evidencePolicyPins(ev, root); err != nil {
		rec.Reason = err.Error()
		return rec, err
	}
	if ev.Label != root.Label || c.ROMSHA256 != root.ROMSHA256 || ev.Fixture == nil || ev.Fixture.SHA256 != root.FixtureSHA256 {
		rec.Reason = fmt.Sprintf("label/ROM/fixture do not match pinned corpus %s", ev.Corpus)
		return rec, errors.New(rec.Reason)
	}

	if c.SchemaVersion == "snes-replay-case-v2" {
		if block != nil && c.BlockID != block.ID {
			rec.Reason = fmt.Sprintf("block ID mismatch: case=%s, block=%s", c.BlockID, block.ID)
			return rec, errors.New(rec.Reason)
		}
	} else if c.SchemaVersion == "snes-routine-case-v1" {
		if _, err := v.routineContract(c); err != nil {
			rec.Reason = err.Error()
			return rec, err
		}
		rec.RoutineID = c.RoutineID
	}

	hs, err := v.checkFiles(c, root)
	if err != nil {
		rec.Reason = err.Error()
		return rec, err
	}

	if root.CaptureSHA256 != "" {
		if ev.Capture == nil || ev.Capture.SHA256 != root.CaptureSHA256 {
			rec.Reason = fmt.Sprintf("capture artifact does not match pinned corpus %s", ev.Corpus)
			return rec, errors.New(rec.Reason)
		}
		if root.CaptureReceiptSHA256 != "" && (ev.Capture.Receipt == nil || ev.Capture.Receipt.SHA256 != root.CaptureReceiptSHA256) {
			rec.Reason = fmt.Sprintf("capture receipt does not match pinned corpus %s", ev.Corpus)
			return rec, errors.New(rec.Reason)
		}
		if root.CaptureSummarySHA256 != "" && (ev.Capture.Summary == nil || ev.Capture.Summary.SHA256 != root.CaptureSummarySHA256) {
			rec.Reason = fmt.Sprintf("capture summary does not match pinned corpus %s", ev.Corpus)
			return rec, errors.New(rec.Reason)
		}
	}
	if root.HistorySHA256 != "" {
		if ev.History == nil || ev.History.SHA256 != root.HistorySHA256 {
			rec.Reason = fmt.Sprintf("history artifact does not match pinned corpus %s", ev.Corpus)
			return rec, errors.New(rec.Reason)
		}
		if root.HistorySummarySHA256 != "" && (ev.History.Summary == nil || ev.History.Summary.SHA256 != root.HistorySummarySHA256) {
			rec.Reason = fmt.Sprintf("history summary does not match pinned corpus %s", ev.Corpus)
			return rec, errors.New(rec.Reason)
		}
	}
	if root.HistoryReceiptSHA256 != "" && (ev.History.Receipt == nil || ev.History.Receipt.SHA256 != root.HistoryReceiptSHA256) {
		rec.Reason = fmt.Sprintf("history receipt does not match pinned corpus %s", ev.Corpus)
		return rec, errors.New(rec.Reason)
	}
	if root.CheckpointSHA256 != "" && (ev.Checkpoint == nil || ev.Checkpoint.SHA256 != root.CheckpointSHA256) {
		rec.Reason = fmt.Sprintf("checkpoint does not match pinned corpus %s", ev.Corpus)
		return rec, errors.New(rec.Reason)
	}
	if root.InputsSHA256 != "" && (ev.Inputs == nil || ev.Inputs.SHA256 != root.InputsSHA256) {
		rec.Reason = fmt.Sprintf("inputs do not match pinned corpus %s", ev.Corpus)
		return rec, errors.New(rec.Reason)
	}
	if root.FixtureReceiptSHA256 != "" && (ev.Fixture.Receipt == nil || ev.Fixture.Receipt.SHA256 != root.FixtureReceiptSHA256) {
		rec.Reason = fmt.Sprintf("fixture receipt does not match pinned corpus %s", ev.Corpus)
		return rec, errors.New(rec.Reason)
	}
	if root.FixtureSummarySHA256 != "" && (ev.Fixture.Summary == nil || ev.Fixture.Summary.SHA256 != root.FixtureSummarySHA256) {
		rec.Reason = fmt.Sprintf("fixture summary does not match pinned corpus %s", ev.Corpus)
		return rec, errors.New(rec.Reason)
	}

	cd, err := v.loadCapture(ev.Capture.Path)
	if err != nil {
		rec.Reason = fmt.Sprintf("failed to load capture: %v", err)
		return rec, errors.New(rec.Reason)
	}

	var seqs []uint64
	var pcs []uint32
	var intervalSeqs []uint64

	if c.SchemaVersion == "snes-replay-case-v2" {
		var ok bool
		pcs, ok = v.policy.blocks[c.BlockID]
		if !ok {
			rec.Reason = fmt.Sprintf("unknown block ID for instruction PCs: %s", c.BlockID)
			return rec, errors.New(rec.Reason)
		}
		expectedLen := uint64(len(pcs))
		if c.ExitSeq < c.EntrySeq || c.ExitSeq-c.EntrySeq+1 != expectedLen {
			rec.Reason = "occurrence length"
			return rec, errors.New(rec.Reason)
		}
		seqs = make([]uint64, expectedLen)
		for i := uint64(0); i < expectedLen; i++ {
			seqs[i] = c.EntrySeq + i
		}
		intervalSeqs = seqs
	} else { // snes-routine-case-v1
		const maxRoutineLen = 5000
		if c.InstructionCount <= 0 || c.InstructionCount > maxRoutineLen {
			rec.Reason = fmt.Sprintf("invalid instruction count: %d", c.InstructionCount)
			return rec, errors.New(rec.Reason)
		}
		if c.ExitSeq < c.EntrySeq || c.ExitSeq == ^uint64(0) {
			rec.Reason = "occurrence length"
			return rec, errors.New(rec.Reason)
		}
		seqLen := c.ExitSeq - c.EntrySeq + 1
		if seqLen > maxRoutineLen {
			rec.Reason = fmt.Sprintf("routine sequence length %d exceeds maximum %d", seqLen, maxRoutineLen)
			return rec, errors.New(rec.Reason)
		}
		if uint64(c.InstructionCount) != seqLen {
			rec.Reason = fmt.Sprintf("instruction count mismatch: %d != %d", c.InstructionCount, seqLen)
			return rec, errors.New(rec.Reason)
		}
		contract, err := v.routineContract(c)
		if err != nil {
			rec.Reason = err.Error()
			return rec, err
		}
		if contract.Kind == "dispatch_handler" {
			if c.CallSeq == 0 || c.CallSeq >= c.EntrySeq {
				rec.Reason = "call_seq does not precede entry_seq"
				return rec, errors.New(rec.Reason)
			}
			if c.ReturnSeq != 0 && c.ReturnSeq != c.ExitSeq+1 {
				rec.Reason = "return_seq does not follow exit_seq"
				return rec, errors.New(rec.Reason)
			}
			seqs = make([]uint64, seqLen)
			for i := uint64(0); i < seqLen; i++ {
				seqs[i] = c.EntrySeq + i
			}
			endSeq := c.ExitSeq
			if c.ReturnSeq != 0 {
				endSeq = c.ReturnSeq
			}
			intervalCount := endSeq - c.CallSeq + 1
			intervalSeqs = make([]uint64, intervalCount)
			for i := uint64(0); i < intervalCount; i++ {
				intervalSeqs[i] = c.CallSeq + i
			}
		} else if contract.Kind == "connected_routine" {
			seqs = make([]uint64, seqLen)
			for i := uint64(0); i < seqLen; i++ {
				seqs[i] = c.EntrySeq + i
			}
			intervalSeqs = seqs
		} else {
			if c.CallSeq == 0 || c.CallSeq+1 != c.EntrySeq {
				rec.Reason = "call_seq does not precede entry_seq"
				return rec, errors.New(rec.Reason)
			}
			if c.ReturnSeq == 0 || c.ReturnSeq != c.ExitSeq+1 {
				rec.Reason = "return_seq does not follow exit_seq"
				return rec, errors.New(rec.Reason)
			}

			seqs = make([]uint64, seqLen)
			for i := uint64(0); i < seqLen; i++ {
				seqs[i] = c.EntrySeq + i
			}

			totalInterval := seqLen + 2
			intervalSeqs = make([]uint64, totalInterval)
			intervalSeqs[0] = c.CallSeq
			for i := uint64(0); i < seqLen; i++ {
				intervalSeqs[i+1] = c.EntrySeq + i
			}
			intervalSeqs[totalInterval-1] = c.ReturnSeq
		}
	}

	wantSeqs := make(map[uint64]bool, len(intervalSeqs))
	for _, s := range intervalSeqs {
		wantSeqs[s] = true
	}
	fd, err := v.admissionFixture(ev.Fixture.Path, root, wantSeqs)
	if err != nil {
		rec.Reason = fmt.Sprintf("failed to load fixture: %v", err)
		return rec, err
	}

	var intervalInsns []captureCPUInsn
	for _, s := range intervalSeqs {
		insn, exists := cd.insns[s]
		if !exists {
			rec.Reason = fmt.Sprintf("capture lacks seq %d", s)
			return rec, errors.New(rec.Reason)
		}
		frec, fexists := fd.recs[s]
		if !fexists || !cpuInsnEqual(frec, insn) {
			rec.Reason = fmt.Sprintf("capture record seq %d differs from fixture", s)
			return rec, errors.New(rec.Reason)
		}
		intervalInsns = append(intervalInsns, insn)
	}

	var blk []captureCPUInsn
	if c.SchemaVersion == "snes-replay-case-v2" {
		for i, insn := range intervalInsns {
			if physicalCPU(insn.Entry) != pcs[i] {
				rec.Reason = fmt.Sprintf("seq %d is not the block", insn.Seq)
				return rec, errors.New(rec.Reason)
			}
		}
		blk = intervalInsns
	} else {
		contract, err := v.routineContract(c)
		if err != nil {
			rec.Reason = err.Error()
			return rec, err
		}
		blk, err = v.verifyRoutineWindow(c, contract, intervalInsns, cd)
		if err != nil {
			rec.Reason = err.Error()
			return rec, err
		}
	}

	// CPU continuity across the interval
	for i := 0; i < len(intervalInsns)-1; i++ {
		a := intervalInsns[i]
		b := intervalInsns[i+1]
		if a.Seq+1 != b.Seq || !cpuStateEqualWithCycles(a.Exit, b.Entry) || a.Exit.Cycles != b.Entry.Cycles {
			rec.Reason = "CPU continuity"
			return rec, errors.New(rec.Reason)
		}
	}

	if !cpuStateMatchesCPUStateWithCycles(c.InitialState, blk[0].Entry) {
		rec.Reason = "initial_state"
		return rec, errors.New(rec.Reason)
	}
	if !cpuStateMatchesCPUStateWithCycles(c.ObservedExit, blk[len(blk)-1].Exit) {
		rec.Reason = "observed_exit_state"
		return rec, errors.New(rec.Reason)
	}

	sp := blk[len(blk)-1].SuccessorPC
	expectedNextPC := (uint32(sp.Bank) << 16) | uint32(sp.Addr)
	if expectedNextPC != c.ObservedNextPC {
		rec.Reason = "observed_next_pc"
		return rec, errors.New(rec.Reason)
	}

	lo := blk[0].Entry.Cycles
	hi := blk[len(blk)-1].Exit.Cycles
	if lo == 0 || hi == 0 || lo >= hi {
		rec.Reason = "cycle window"
		return rec, errors.New(rec.Reason)
	}
	if c.StartCycle != 0 && c.StartCycle != lo {
		rec.Reason = "cycle window"
		return rec, errors.New(rec.Reason)
	}
	if c.EndCycle != 0 && c.EndCycle != hi {
		rec.Reason = "cycle window"
		return rec, errors.New(rec.Reason)
	}
	if c.StartCycle == 0 && c.InitialState.Cycles != lo {
		rec.Reason = "cycle window"
		return rec, errors.New(rec.Reason)
	}
	if c.EndCycle == 0 && c.ObservedExit.Cycles != hi {
		rec.Reason = "cycle window"
		return rec, errors.New(rec.Reason)
	}

	// Reject transitions / interrupts / gaps in the window
	for _, trans := range cd.transitions {
		if trans.Cycle > lo && trans.Cycle <= hi {
			rec.Reason = fmt.Sprintf("interrupt or transition in execution window: %s at cycle %d", trans.Kind, trans.Cycle)
			return rec, errors.New(rec.Reason)
		}
	}
	startGapSeq := c.EntrySeq
	if c.CallSeq != 0 && c.CallSeq < startGapSeq {
		startGapSeq = c.CallSeq
	}
	endGapSeq := c.ExitSeq
	if c.ReturnSeq != 0 && c.ReturnSeq > endGapSeq {
		endGapSeq = c.ReturnSeq
	}
	for _, gap := range cd.gaps {
		if gap.FirstSeq <= endGapSeq && gap.LastSeq >= startGapSeq {
			rec.Reason = fmt.Sprintf("execution gap across sequence [%d, %d]", gap.FirstSeq, gap.LastSeq)
			return rec, errors.New(rec.Reason)
		}
	}

	// Cycle join of WRAM events strictly in (lo, hi]
	mem := make(map[uint32]*uint8)
	cellCurrent := make(map[uint32]uint8)
	var writes []MemoryWrite

	// Check monotonic cycle order of candidate events
	var windowEvents []busEvent
	for _, e := range cd.bus {
		if e.Cycle <= lo || e.Cycle > hi {
			continue
		}
		windowEvents = append(windowEvents, e)
	}
	for i := 1; i < len(windowEvents); i++ {
		prev := windowEvents[i-1]
		curr := windowEvents[i]
		if curr.Cycle < prev.Cycle || (curr.Cycle == prev.Cycle && curr.ID <= prev.ID) {
			rec.Reason = "bus events not monotonically ordered"
			return rec, errors.New(rec.Reason)
		}
	}

	for _, e := range windowEvents {
		hits := 0
		for _, n := range blk {
			if n.Entry.Cycles < e.Cycle && e.Cycle <= n.Exit.Cycles {
				hits++
			}
		}
		if hits != 1 {
			rec.Reason = fmt.Sprintf("bus event %d joins %d instructions", e.ID, hits)
			return rec, errors.New(rec.Reason)
		}
		if e.Op != "read" && e.Op != "write" {
			rec.Reason = fmt.Sprintf("unknown operation: %s", e.Op)
			return rec, errors.New(rec.Reason)
		}

		a := 0x7E0000 + e.Addr
		v := uint8(0)
		if e.Value != nil {
			v = *e.Value
		} else if e.After != nil {
			v = *e.After
		}

		if e.Op == "read" {
			if _, exists := mem[a]; !exists {
				valCopy := v
				mem[a] = &valCopy
				cellCurrent[a] = v
			} else {
				if curr, ok := cellCurrent[a]; ok && curr != v {
					rec.Reason = fmt.Sprintf("read-after-write mismatch at $%06X: expected %d, got %d", a, curr, v)
					return rec, errors.New(rec.Reason)
				}
			}
		} else if e.Op == "write" {
			if _, exists := mem[a]; !exists {
				mem[a] = nil
			}
			cellCurrent[a] = v
			writes = append(writes, MemoryWrite{Address: a, Value: v})
		}
	}

	var initCells []MemoryCell
	for a, vPtr := range mem {
		if vPtr != nil {
			initCells = append(initCells, MemoryCell{Address: a, Value: *vPtr})
		}
	}
	sort.Slice(initCells, func(i, j int) bool { return initCells[i].Address < initCells[j].Address })

	caseInit := make([]MemoryCell, len(c.InitialMemory))
	copy(caseInit, c.InitialMemory)
	sort.Slice(caseInit, func(i, j int) bool { return caseInit[i].Address < caseInit[j].Address })

	initMismatch := len(initCells) != len(caseInit)
	if !initMismatch {
		for i := range initCells {
			if initCells[i].Address != caseInit[i].Address || initCells[i].Value != caseInit[i].Value {
				initMismatch = true
				break
			}
		}
	}
	if initMismatch {
		rec.Reason = fmt.Sprintf("initial_memory differs from derived %v", initCells)
		return rec, errors.New(rec.Reason)
	}

	writesMismatch := len(writes) != len(c.ObservedWrites)
	if !writesMismatch {
		for i := range writes {
			if writes[i].Address != c.ObservedWrites[i].Address || writes[i].Value != c.ObservedWrites[i].Value {
				writesMismatch = true
				break
			}
		}
	}
	if writesMismatch {
		rec.Reason = fmt.Sprintf("observed_writes differ from derived (ordered) %v", writes)
		return rec, errors.New(rec.Reason)
	}

	// Classification from history
	hist, err := v.loadHistory(ev.History.Path)
	if err != nil {
		rec.Reason = fmt.Sprintf("failed to load history: %v", err)
		return rec, errors.New(rec.Reason)
	}

	ranges := hs.AddressRanges
	claimed := make(map[uint32]string)
	for _, m := range ev.InitialMemorySource {
		claimed[m.Address] = m.Source
	}
	if len(claimed) != len(initCells) {
		rec.Reason = "initial_memory_source cells"
		return rec, errors.New(rec.Reason)
	}
	for _, m := range initCells {
		if _, ok := claimed[m.Address]; !ok {
			rec.Reason = "initial_memory_source cells"
			return rec, errors.New(rec.Reason)
		}
	}

	cls := make(map[uint32]string)
	for _, m := range initCells {
		a := m.Address
		offset := a - 0x7E0000
		covered := len(ranges) == 0
		for _, r := range ranges {
			if r.Space == "wram" && r.Start <= offset && offset <= r.End {
				covered = true
				break
			}
		}
		if !covered {
			rec.Reason = fmt.Sprintf("history does not cover $%06X", a)
			return rec, errors.New(rec.Reason)
		}

		var prior []historyWrite
		for _, hw := range hist[a] {
			if c.SchemaVersion == "snes-routine-case-v1" {
				if hw.Cycle <= lo {
					prior = append(prior, hw)
				}
			} else {
				if hw.Cycle < lo {
					prior = append(prior, hw)
				}
			}
		}
		if len(prior) > 0 {
			last := prior[len(prior)-1]
			if last.Value != m.Value {
				rec.Reason = fmt.Sprintf("$%06X: last prior write %d != read %d", a, last.Value, m.Value)
				return rec, errors.New(rec.Reason)
			}
			cls[a] = "confirmed_by_prior_write"
		} else {
			cls[a] = "power_on_wram"
		}

		if claimed[a] != cls[a] {
			rec.Reason = fmt.Sprintf("$%06X classified %s, case claims %s", a, cls[a], claimed[a])
			return rec, errors.New(rec.Reason)
		}
	}

	// Admission success!
	c.ObservedEffectsCapture = true
	rec.Admitted = true
	rec.ObservedEffectsCapture = true

	classSet := make(map[string]bool)
	for _, v := range cls {
		classSet[v] = true
	}
	var classList []string
	for k := range classSet {
		classList = append(classList, k)
	}
	sort.Strings(classList)
	if len(classList) == 0 {
		classList = []string{"no_memory_inputs"}
	}
	rec.Classification = classList

	for _, m := range initCells {
		rec.InitialMemorySources = append(rec.InitialMemorySources, cls[m.Address])
	}

	NormalizeProducerCase(c)
	PopulateHashes(c)

	blockOrRoutine := c.BlockID
	if blockOrRoutine == "" {
		blockOrRoutine = c.RoutineID
	}
	rec.PolicySHA256 = v.PolicySHA256()
	rec.ROMSHA256 = c.ROMSHA256
	rec.AdmissionDigest = ComputeAdmissionDigest(c.CaseID, blockOrRoutine, c.CaseHash,
		ev.Fixture.SHA256, ev.Capture.SHA256, ev.History.SHA256)
	rec.AdmissionDigest = policyDigest([]string{rec.AdmissionDigest, rec.PolicySHA256})
	c.AdmissionDigest = rec.AdmissionDigest

	v.recordAdmission(c.CaseHash, rec.AdmissionDigest, rec)

	return rec, nil
}

// AdmitVerifiedReplayCase admits a captured execution occurrence by re-deriving
// pre-block memory (reads-before-writes) and ordered writes directly from verified
// cycle-joined bus capture events, verifying execution bounds, validating continuity,
// and classifying power-on vs confirmed-by-prior-write dependencies.
func AdmitVerifiedReplayCase(c *ReplayCase, block *structure.BasicBlock, ir *BlockIR) (AdmissionRecord, error) {
	return defaultEvidenceVerifier.Admit(c, block, ir)
}

// Identity returns the CaseIdentity for this ReplayCase.
func (c *ReplayCase) Identity() CaseIdentity {
	effState := "captured"
	if !c.ObservedEffectsCapture {
		effState = "not_captured_in_trace"
	}
	return CaseIdentity{
		CaseID:                   c.CaseID,
		BlockID:                  c.BlockID,
		RunID:                    c.RunID,
		StreamSHA256:             c.StreamSHA256,
		DecompressedStreamSHA256: c.DecompressedStreamSHA256,
		ROMSHA256:                c.ROMSHA256,
		Frame:                    c.Frame,
		EntrySeq:                 c.EntrySeq,
		ExitSeq:                  c.ExitSeq,
		StartEventID:             c.StartEventID,
		EndEventID:               c.EndEventID,
		StartCycle:               c.StartCycle,
		EndCycle:                 c.EndCycle,
		ObservedNextPC:           c.ObservedNextPC,
		ObservedEffectsState:     effState,
	}
}

type canonicalCasePayload struct {
	SchemaVersion            string              `json:"schema_version"`
	CaseID                   string              `json:"case_id"`
	BlockID                  string              `json:"block_id,omitempty"`
	RoutineID                string              `json:"routine_id,omitempty"`
	RunID                    string              `json:"run_id"`
	StreamSHA256             string              `json:"stream_sha256"`
	DecompressedStreamSHA256 string              `json:"decompressed_stream_sha256,omitempty"`
	ROMSHA256                string              `json:"rom_sha256"`
	Frame                    int                 `json:"frame"`
	CallSeq                  uint64              `json:"call_seq,omitempty"`
	CallPC                   uint32              `json:"call_pc,omitempty"`
	EntrySeq                 uint64              `json:"entry_seq"`
	ExitSeq                  uint64              `json:"exit_seq"`
	ReturnSeq                uint64              `json:"return_seq,omitempty"`
	ReturnInsnPC             uint32              `json:"return_insn_pc,omitempty"`
	InstructionCount         int                 `json:"instruction_count,omitempty"`
	StartEventID             uint64              `json:"start_event_id,omitempty"`
	EndEventID               uint64              `json:"end_event_id,omitempty"`
	StartCycle               uint64              `json:"start_cycle,omitempty"`
	EndCycle                 uint64              `json:"end_cycle,omitempty"`
	InitialState             CPUState            `json:"initial_state"`
	DerivedMemory            []DerivedMemoryCell `json:"derived_memory,omitempty"`
	InitialMemory            []MemoryCell        `json:"initial_memory"`
	ObservedExit             CPUState            `json:"observed_exit_state"`
	ObservedNextPC           uint32              `json:"observed_next_pc"`
	ObservedBranch           string              `json:"observed_branch,omitempty"`
	ObservedEffectsCapture   bool                `json:"observed_effects_captured"`
	ObservedWrites           []MemoryWrite       `json:"observed_writes,omitempty"`
}

// ComputeCaseHash computes an immutable, self-contained SHA-256 hash over the canonical content of the case.
// It directly serializes all initial states, derived memory provenance, actual memory cells, exit states,
// observed writes, execution cycles, and trace provenance without relying on cached child hashes.
func ComputeCaseHash(c ReplayCase) string {
	payload := canonicalCasePayload{
		SchemaVersion:            c.SchemaVersion,
		CaseID:                   c.CaseID,
		BlockID:                  c.BlockID,
		RoutineID:                c.RoutineID,
		RunID:                    c.RunID,
		StreamSHA256:             c.StreamSHA256,
		DecompressedStreamSHA256: c.DecompressedStreamSHA256,
		ROMSHA256:                c.ROMSHA256,
		Frame:                    c.Frame,
		CallSeq:                  c.CallSeq,
		CallPC:                   c.CallPC,
		EntrySeq:                 c.EntrySeq,
		ExitSeq:                  c.ExitSeq,
		ReturnSeq:                c.ReturnSeq,
		ReturnInsnPC:             c.ReturnInsnPC,
		InstructionCount:         c.InstructionCount,
		StartEventID:             c.StartEventID,
		EndEventID:               c.EndEventID,
		StartCycle:               c.StartCycle,
		EndCycle:                 c.EndCycle,
		InitialState:             c.InitialState,
		ObservedExit:             c.ObservedExit,
		ObservedNextPC:           c.ObservedNextPC,
		ObservedBranch:           c.ObservedBranch,
		ObservedEffectsCapture:   c.ObservedEffectsCapture,
	}

	// Canonical sort of DerivedMemory by Address
	if len(c.DerivedMemory) > 0 {
		dm := make([]DerivedMemoryCell, len(c.DerivedMemory))
		copy(dm, c.DerivedMemory)
		sort.Slice(dm, func(i, j int) bool { return dm[i].Address < dm[j].Address })
		payload.DerivedMemory = dm
	}

	// Canonical sort of InitialMemory by Address
	if len(c.InitialMemory) > 0 {
		im := make([]MemoryCell, len(c.InitialMemory))
		copy(im, c.InitialMemory)
		sort.Slice(im, func(i, j int) bool { return im[i].Address < im[j].Address })
		payload.InitialMemory = im
	} else {
		payload.InitialMemory = []MemoryCell{}
	}

	// Preserve exact execution order of ObservedWrites (do not sort, write order is semantically significant)
	if len(c.ObservedWrites) > 0 {
		ow := make([]MemoryWrite, len(c.ObservedWrites))
		copy(ow, c.ObservedWrites)
		payload.ObservedWrites = ow
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("marshal canonical case payload: %v", err))
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ReplayReceipt records the outcome of a three-way comparison (Observed Trace vs Reference Emulator vs Generated C).
type ReplayReceipt struct {
	CaseID                string          `json:"case_id"`
	BlockID               string          `json:"block_id"`
	CaseHash              string          `json:"case_hash"`
	CaseIdentity          CaseIdentity    `json:"case_identity"`
	Matched               bool            `json:"matched"`
	Eligible              bool            `json:"eligible"`
	CapturedProofEligible bool            `json:"captured_proof_eligible"`
	AdmissionDigest       string          `json:"admission_digest,omitempty"`
	Discrepancy           string          `json:"discrepancy,omitempty"`
	CPUTransitionMatch    bool            `json:"cpu_transition_match"`
	EffectsMatch          bool            `json:"effects_match"`
	EffectsStatus         string          `json:"effects_status"` // "effects_matched" | "effects_not_captured_in_trace" | "effects_mismatched"
	ObservedMatch         bool            `json:"observed_match"`
	EmulatorMatch         bool            `json:"emulator_match"`
	CMatch                bool            `json:"c_match"`
	TraceObserved         ExecResult      `json:"trace_observed"`
	ReferenceEmu          ExecResult      `json:"reference_emu"`
	CompiledC             ExecResult      `json:"compiled_c"`
	Metadata              ReceiptMetadata `json:"metadata"`
}

// ReplayCaseInput describes a single case passed to the compiled multi-case batch runner.
type ReplayCaseInput struct {
	CaseID  string       `json:"case_id"`
	Initial CPUState     `json:"initial"`
	Memory  []MemoryCell `json:"memory"`
}

// CompiledRunner manages an isolated, reusable compiled C runner for a specific block.
type CompiledRunner struct {
	BlockID        string
	StartAddress   uint32
	GeneratedCHash string
	Context        recovery.Context
	Compiler       string
	CompilerFlags  string
	RunDir         string
	BinPath        string
	Closed         bool
	mu             sync.Mutex
}

// NewCompiledRunner compiles the block once into a reusable multi-case runner executable.
func NewCompiledRunner(ctx context.Context, ir *BlockIR) (*CompiledRunner, error) {
	if ir == nil {
		return nil, fmt.Errorf("nil BlockIR")
	}

	cCode, err := GenerateCompilableC(ir)
	if err != nil {
		return nil, fmt.Errorf("generate compilable C: %w", err)
	}
	cHash := ComputeCHash(cCode)

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("user home dir: %w", err)
	}
	baseTmp := filepath.Join(home, "tmp", "snes", time.Now().UTC().Format("20060102")+"-compiled-runners")
	if err := os.MkdirAll(baseTmp, 0755); err != nil {
		return nil, fmt.Errorf("create base tmp: %w", err)
	}

	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("rand nonce: %w", err)
	}
	runDir := filepath.Join(baseTmp, fmt.Sprintf("snes-runner-%06x-%s", ir.StartAddress, hex.EncodeToString(nonce[:])))
	if err := os.MkdirAll(runDir, 0700); err != nil {
		return nil, fmt.Errorf("create runner dir: %w", err)
	}

	srcPath := filepath.Join(runDir, "runner.c")
	binPath := filepath.Join(runDir, "runner")

	runnerSrc := GenerateMultiCaseRunnerC(cCode, ir.StartAddress)
	if err := os.WriteFile(srcPath, []byte(runnerSrc), 0644); err != nil {
		os.RemoveAll(runDir)
		return nil, fmt.Errorf("write runner src: %w", err)
	}

	compileCtx, cancelCompile := context.WithTimeout(ctx, 30*time.Second)
	defer cancelCompile()

	compilerFlags := "cc -O0 -Wall -Werror -Wno-unused-function -Wno-unused-label"
	cmdCompile := exec.CommandContext(compileCtx, "cc", "-O0", "-Wall", "-Werror", "-Wno-unused-function", "-Wno-unused-label", srcPath, "-o", binPath)
	if out, err := cmdCompile.CombinedOutput(); err != nil {
		os.RemoveAll(runDir)
		return nil, fmt.Errorf("compile multi-case runner: %w (output: %s)", err, string(out))
	}

	return &CompiledRunner{
		BlockID:        ir.BlockID,
		StartAddress:   ir.StartAddress,
		GeneratedCHash: cHash,
		Context:        ir.EntryContext,
		Compiler:       getObservedCompiler(),
		CompilerFlags:  compilerFlags,
		RunDir:         runDir,
		BinPath:        binPath,
	}, nil
}

// GenerateMultiCaseRunnerC produces the complete C source for the multi-case batch runner.
// It uses an unambiguous, line-delimited protocol to prevent buffer truncation and substring scanning bugs.
func GenerateMultiCaseRunnerC(cCode string, startAddr uint32) string {
	return fmt.Sprintf(`/* SNES Multi-Case Compiled Batch Runner */
#include <stdio.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <stdlib.h>
#include <unistd.h>
#include <ctype.h>

#define MAX_WRITES 256
#define MAX_CELLS 256

%s

typedef struct {
    uint32_t addr;
    uint8_t val;
} runner_cell_t;

typedef struct {
    runner_cell_t cells[MAX_CELLS];
    int count;
} runner_mem_t;

static uint8_t test_read_cb(void *user_data, uint32_t addr, bool *missing) {
    runner_mem_t *m = (runner_mem_t *)user_data;
    uint32_t c_addr = bus_canonical_addr(addr);
    for (int i = 0; i < m->count; i++) {
        if (m->cells[i].addr == c_addr) {
            if (missing) *missing = false;
            return m->cells[i].val;
        }
    }
    if (missing) *missing = true;
    return 0;
}

int main(void) {
    /* 10-second watchdog timer to bound batch execution */
    alarm(10);

    printf("[\n");
    bool first_out = true;
    char line[4096];

    while (fgets(line, sizeof(line), stdin)) {
        /* Line format: CASE <idx> A=<uint> X=<uint> Y=<uint> S=<uint> PC=<uint> D=<uint> DB=<uint> PB=<uint> P=<uint> E=<uint> CELLS=<uint> */
        if (strncmp(line, "CASE", 4) != 0) {
            continue;
        }

        int case_idx = 0;
        unsigned int a=0, x=0, y=0, s=0, pc=0, d=0, db=0, pb=0, p=0, e=0, cell_count=0;
        int matched = sscanf(line, "CASE %%d A=%%u X=%%u Y=%%u S=%%u PC=%%u D=%%u DB=%%u PB=%%u P=%%u E=%%u CELLS=%%u",
                             &case_idx, &a, &x, &y, &s, &pc, &d, &db, &pb, &p, &e, &cell_count);
        if (matched != 12) {
            fprintf(stderr, "protocol error: malformed CASE header: %%s\n", line);
            return 2;
        }
        if (cell_count > MAX_CELLS) {
            fprintf(stderr, "protocol error: cell count %%u exceeds MAX_CELLS %%d\n", cell_count, MAX_CELLS);
            return 3;
        }

        cpu_state_t state = {0};
        state.a = (uint16_t)a;
        state.x = (uint16_t)x;
        state.y = (uint16_t)y;
        state.s = (uint16_t)s;
        state.pc = (uint16_t)pc;
        state.d = (uint16_t)d;
        state.db = (uint8_t)db;
        state.pb = (uint8_t)pb;
        state.p = (uint8_t)p;
        state.e = (e != 0);

        runner_mem_t mem;
        memset(&mem, 0, sizeof(mem));

        /* Read cell_count address-value pairs */
        for (unsigned int i = 0; i < cell_count; i++) {
            if (!fgets(line, sizeof(line), stdin)) {
                fprintf(stderr, "protocol error: unexpected EOF reading memory cell %%u/%%u\n", i, cell_count);
                return 4;
            }
            uint32_t caddr = 0;
            unsigned int cval = 0;
            if (sscanf(line, "%%x %%x", &caddr, &cval) != 2) {
                fprintf(stderr, "protocol error: invalid memory cell line: %%s\n", line);
                return 5;
            }
            uint32_t can_addr = bus_canonical_addr(caddr);
            for (int k = 0; k < mem.count; k++) {
                if (mem.cells[k].addr == can_addr) {
                    if (mem.cells[k].val != (uint8_t)cval) {
                        fprintf(stderr, "protocol error: conflicting memory cell alias for $%%06X: $%%02X vs $%%02X\n",
                                can_addr, mem.cells[k].val, (uint8_t)cval);
                        return 6;
                    }
                    goto skip_dup;
                }
            }
            mem.cells[mem.count].addr = can_addr;
            mem.cells[mem.count].val = (uint8_t)cval;
            mem.count++;
        skip_dup:;
        }

        /* Execute block isolated with clean memory */
        exec_result_t res = execute_block_%06x(state, test_read_cb, &mem);

        if (!first_out) printf(",\n");
        first_out = false;
        printf("{\"state\":{\"a\":%%u,\"x\":%%u,\"y\":%%u,\"s\":%%u,\"pc\":%%u,\"d\":%%u,\"db\":%%u,\"pb\":%%u,\"p\":%%u,\"e\":%%s},"
               "\"next_pc\":%%u,\"total_writes\":%%u,\"write_overflow\":%%s,\"missing_read\":%%s,\"missing_addr\":%%u,\"mmio_access\":%%s,\"mmio_addr\":%%u,\"writes\":[",
               res.state.a, res.state.x, res.state.y, res.state.s, res.state.pc, res.state.d,
               res.state.db, res.state.pb, res.state.p, res.state.e ? "true" : "false",
               res.next_pc, res.total_writes, res.write_overflow ? "true" : "false",
               res.uninitialized_read ? "true" : "false", res.uninitialized_addr,
               res.mmio_access ? "true" : "false", res.mmio_addr);

        for (int i = 0; i < res.num_writes; i++) {
            if (i > 0) printf(",");
            printf("{\"address\":%%u,\"value\":%%u}", res.writes[i].address, res.writes[i].value);
        }
        printf("]}");
    }

    printf("\n]\n");
    return 0;
}
`, cCode, startAddr)
}

// Close removes runner artifacts.
func (r *CompiledRunner) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Closed {
		return nil
	}
	r.Closed = true
	return os.RemoveAll(r.RunDir)
}

// RunBatch executes a slice of ReplayCaseInput in a single process invocation.
// It rigorously validates bounds, contracts, and conflicting aliases before launching.
func (r *CompiledRunner) RunBatch(ctx context.Context, cases []ReplayCaseInput) ([]ExecResult, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Closed {
		return nil, fmt.Errorf("runner is closed")
	}
	if len(cases) == 0 {
		return nil, nil
	}
	if len(cases) > MaxBatchCases {
		return nil, fmt.Errorf("batch cases %d exceeds maximum limit of %d", len(cases), MaxBatchCases)
	}

	// Format input buffer using line-delimited protocol and validate each case
	var buf bytes.Buffer
	for idx, c := range cases {
		// 1. Validate entry PC
		expectedPC := uint16(r.StartAddress & 0xFFFF)
		if c.Initial.PC != expectedPC {
			return nil, fmt.Errorf("case %d (%s) entry PC mismatch: got $%04X, want $%04X", idx, c.CaseID, c.Initial.PC, expectedPC)
		}

		// 2. Validate entry contract against runner Context
		if err := enforceContextContract(r.Context, c.Initial); err != nil {
			return nil, fmt.Errorf("case %d (%s) entry contract violation: %w", idx, c.CaseID, err)
		}

		// 3. Validate cell count
		if len(c.Memory) > MaxCellsPerCase {
			return nil, fmt.Errorf("case %d (%s) memory cell count %d exceeds limit of %d", idx, c.CaseID, len(c.Memory), MaxCellsPerCase)
		}

		// 4. Validate conflicting bus aliases in memory cells
		seenCan := make(map[uint32]uint8, len(c.Memory))
		for _, cell := range c.Memory {
			canAddr := CanonicalBusAddress(cell.Address)
			if existVal, exists := seenCan[canAddr]; exists {
				if existVal != cell.Value {
					return nil, fmt.Errorf("case %d (%s) conflicting memory alias for canonical address $%06X: $%02X vs $%02X",
						idx, c.CaseID, canAddr, existVal, cell.Value)
				}
			} else {
				seenCan[canAddr] = cell.Value
			}
		}

		// 5. Serialize case header and cells
		eInt := 0
		if c.Initial.E {
			eInt = 1
		}
		fmt.Fprintf(&buf, "CASE %d A=%d X=%d Y=%d S=%d PC=%d D=%d DB=%d PB=%d P=%d E=%d CELLS=%d\n",
			idx, c.Initial.A, c.Initial.X, c.Initial.Y, c.Initial.S, c.Initial.PC,
			c.Initial.D, c.Initial.DB, c.Initial.PB, c.Initial.P, eInt, len(c.Memory))

		for _, cell := range c.Memory {
			fmt.Fprintf(&buf, "%06X %02X\n", cell.Address, cell.Value)
		}
	}

	if buf.Len() > MaxBatchBytes {
		return nil, fmt.Errorf("batch payload size %d bytes exceeds maximum limit of %d bytes", buf.Len(), MaxBatchBytes)
	}

	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, r.BinPath)
	cmd.Stdin = &buf
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run compiled runner: %w (stderr: %s)", err, stderr.String())
	}

	var results []ExecResult
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		return nil, fmt.Errorf("unmarshal batch results: %w (raw: %s)", err, stdout.String())
	}

	if len(results) != len(cases) {
		return nil, fmt.Errorf("batch results count mismatch: got %d, want %d", len(results), len(cases))
	}

	return results, nil
}

// RunCase executes a single replay case using the reusable compiled runner.
func (r *CompiledRunner) RunCase(ctx context.Context, c ReplayCase) (ExecResult, error) {
	input := ReplayCaseInput{
		CaseID:  c.CaseID,
		Initial: c.InitialState,
		Memory:  c.InitialMemory,
	}
	results, err := r.RunBatch(ctx, []ReplayCaseInput{input})
	if err != nil {
		return ExecResult{}, err
	}
	if len(results) != 1 {
		return ExecResult{}, fmt.Errorf("expected 1 result, got %d", len(results))
	}
	return results[0], nil
}

func enforceContextContract(ctx recovery.Context, state CPUState) error {
	// Decimal mode constraint: 65816 decimal mode (P flag bit 3, D) is not tracked as a static variant in
	// recovery.Context. Compiled runner batch execution enforces D=0 (decimal clear) as an entry invariant.
	if (state.P & 0x08) != 0 {
		return fmt.Errorf("entry contract violation: decimal mode (D=1) is unsupported as entry invariant; expected D=0 (got P=0x%02X)", state.P)
	}
	if ctx.E == "set" && !state.E {
		return fmt.Errorf("expected emulation mode (E=1), got native (E=0)")
	}
	if ctx.E == "clear" && state.E {
		return fmt.Errorf("expected native mode (E=0), got emulation (E=1)")
	}
	if state.E {
		// Emulation mode forces 8-bit registers (M=1, X=1)
		return nil
	}
	if ctx.M == "set" && (state.P&0x20) == 0 {
		return fmt.Errorf("expected M=1 (8-bit accumulator), got M=0")
	}
	if ctx.M == "clear" && (state.P&0x20) != 0 {
		return fmt.Errorf("expected M=0 (16-bit accumulator), got M=1")
	}
	if ctx.X == "set" && (state.P&0x10) == 0 {
		return fmt.Errorf("expected X=1 (8-bit index), got X=0")
	}
	if ctx.X == "clear" && (state.P&0x10) != 0 {
		return fmt.Errorf("expected X=0 (16-bit index), got X=1")
	}
	if ctx.C == "set" && (state.P&0x01) == 0 {
		return fmt.Errorf("expected C=1 (carry set), got C=0")
	}
	if ctx.C == "clear" && (state.P&0x01) != 0 {
		return fmt.Errorf("expected C=0 (carry clear), got C=1")
	}
	return nil
}

// CanonicalBusAddress computes the canonical WRAM mirror address for comparison.
func CanonicalBusAddress(addr uint32) uint32 {
	addr &= 0xFFFFFF
	bank := (addr >> 16) & 0xFF
	offset := addr & 0xFFFF
	if (bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && offset < 0x2000 {
		return 0x7E0000 | offset
	}
	if bank == 0x7E && offset < 0x2000 {
		return 0x7E0000 | offset
	}
	return addr
}

// CompareWrites compares two slices of memory writes.
func CompareWrites(expected, actual []MemoryWrite) (bool, string) {
	if len(expected) != len(actual) {
		return false, fmt.Sprintf("write count mismatch: expected %d, got %d", len(expected), len(actual))
	}
	for i := range expected {
		if expected[i].Address != actual[i].Address || expected[i].Value != actual[i].Value {
			return false, fmt.Sprintf("write #%d mismatch: expected $%06X=0x%02X, got $%06X=0x%02X",
				i, expected[i].Address, expected[i].Value, actual[i].Address, actual[i].Value)
		}
	}
	return true, ""
}

// CompareCPUStates compares two CPUState values for full register equality.
func CompareCPUStates(expected, actual CPUState) (bool, string) {
	if expected.A != actual.A {
		return false, fmt.Sprintf("A mismatch: expected=0x%04X, actual=0x%04X", expected.A, actual.A)
	}
	if expected.X != actual.X {
		return false, fmt.Sprintf("X mismatch: expected=0x%04X, actual=0x%04X", expected.X, actual.X)
	}
	if expected.Y != actual.Y {
		return false, fmt.Sprintf("Y mismatch: expected=0x%04X, actual=0x%04X", expected.Y, actual.Y)
	}
	if expected.S != actual.S {
		return false, fmt.Sprintf("S mismatch: expected=0x%04X, actual=0x%04X", expected.S, actual.S)
	}
	if expected.D != actual.D {
		return false, fmt.Sprintf("D mismatch: expected=0x%04X, actual=0x%04X", expected.D, actual.D)
	}
	if expected.DB != actual.DB {
		return false, fmt.Sprintf("DB mismatch: expected=0x%02X, actual=0x%02X", expected.DB, actual.DB)
	}
	if expected.PB != actual.PB {
		return false, fmt.Sprintf("PB mismatch: expected=0x%02X, actual=0x%02X", expected.PB, actual.PB)
	}
	if expected.PC != actual.PC {
		return false, fmt.Sprintf("PC mismatch: expected=0x%04X, actual=0x%04X", expected.PC, actual.PC)
	}
	if expected.P != actual.P {
		return false, fmt.Sprintf("Flags mismatch: expected=0x%02X (%s), actual=0x%02X (%s)",
			expected.P, formatFlags(expected.P), actual.P, formatFlags(actual.P))
	}
	if expected.E != actual.E {
		return false, fmt.Sprintf("E mismatch: expected=%v, actual=%v", expected.E, actual.E)
	}
	return true, ""
}

// ExecuteThreeWayReplay executes a comprehensive three-way comparison:
// Observed Trace vs Reference Emulator vs Generated C.
// Binds runner identity and validates case integrity before execution.
func ExecuteThreeWayReplay(ctx context.Context, ir *BlockIR, c ReplayCase, runner *CompiledRunner, cfg VerifyConfig) ReplayReceipt {
	return defaultEvidenceVerifier.ExecuteThreeWayReplay(ctx, ir, c, runner, cfg)
}

// ExecuteThreeWayReplay permits captured proof only under this verifier's
// explicitly reviewed policy and verified admission.
func (v *EvidenceVerifier) ExecuteThreeWayReplay(ctx context.Context, ir *BlockIR, c ReplayCase, runner *CompiledRunner, cfg VerifyConfig) ReplayReceipt {
	receipt := ReplayReceipt{
		CaseID:       c.CaseID,
		BlockID:      c.BlockID,
		CaseIdentity: c.Identity(),
		TraceObserved: ExecResult{
			State:  c.ObservedExit,
			NextPC: c.ObservedNextPC,
			Writes: c.ObservedWrites,
		},
		EffectsStatus: "effects_not_captured_in_trace",
	}

	// 1. Reject nil IR immediately
	if ir == nil {
		receipt.Discrepancy = "nil block IR"
		return receipt
	}

	// 2. Validate case schema version
	if c.SchemaVersion != "snes-replay-case-v1" && c.SchemaVersion != "snes-replay-case-v2" {
		receipt.Discrepancy = fmt.Sprintf("unsupported case schema version: %q", c.SchemaVersion)
		return receipt
	}

	// 3. Validate block ID and address alignment
	if c.BlockID != ir.BlockID {
		receipt.Discrepancy = fmt.Sprintf("case block ID mismatch: case=%q, ir=%q", c.BlockID, ir.BlockID)
		return receipt
	}
	expectedPC := uint16(ir.StartAddress & 0xFFFF)
	if c.InitialState.PC != expectedPC {
		receipt.Discrepancy = fmt.Sprintf("case initial PC $%04X does not match IR start PC $%04X", c.InitialState.PC, expectedPC)
		return receipt
	}

	// 4. Validate case hash integrity and recompute canonical case hash
	recomputedCPU := ComputeCPUStateHash(c.InitialState)
	if c.InitialCPUHash != "" && c.InitialCPUHash != recomputedCPU {
		receipt.Discrepancy = fmt.Sprintf("case initial CPU state hash mismatch: recorded=%s, computed=%s", c.InitialCPUHash, recomputedCPU)
		return receipt
	}

	memMap := make(map[uint32]uint8, len(c.InitialMemory))
	for _, cell := range c.InitialMemory {
		memMap[cell.Address] = cell.Value
	}
	recomputedMem, _ := ComputeInitialMemory(memMap)
	if c.InitialMemHash != "" && c.InitialMemHash != recomputedMem {
		receipt.Discrepancy = fmt.Sprintf("case initial memory hash mismatch: recorded=%s, computed=%s", c.InitialMemHash, recomputedMem)
		return receipt
	}

	// Always recompute canonical case hash from canonical case contents; reject any tampered/inconsistent hash
	computedCaseHash := ComputeCaseHash(c)
	if c.CaseHash != "" && c.CaseHash != computedCaseHash {
		receipt.Discrepancy = fmt.Sprintf("tampered case fixture: recorded CaseHash %s does not match recomputed canonical hash %s", c.CaseHash, computedCaseHash)
		return receipt
	}
	receipt.CaseHash = computedCaseHash

	// 5. Code hashes and metadata
	codeHash := ComputeBlockCodeHash(ir)
	cCode, err := GenerateCompilableC(ir)
	cHash := ""
	if err == nil {
		cHash = ComputeCHash(cCode)
	}

	receipt.Metadata = ReceiptMetadata{
		BlockID:             c.BlockID,
		StartAddress:        ir.StartAddress,
		CodeHash:            codeHash,
		GeneratedCHash:      cHash,
		Compiler:            getObservedCompiler(),
		CompilerFlags:       "cc -O0 -Wall -Werror -Wno-unused-function -Wno-unused-label",
		ROMSHA256:           c.ROMSHA256,
		ProjectRevision:     cfg.ProjectRevision,
		Context:             ir.EntryContext,
		MemoryPolicy:        "snes_wram_mirror_v1",
		InitialMemHash:      recomputedMem,
		InitialCPUStateHash: recomputedCPU,
		Timestamp:           time.Now().UTC().Format(time.RFC3339),
	}

	if cfg.ROMSHA256 != "" && c.ROMSHA256 != cfg.ROMSHA256 {
		receipt.Discrepancy = fmt.Sprintf("ROM SHA256 mismatch: case=%s, cfg=%s", c.ROMSHA256, cfg.ROMSHA256)
		return receipt
	}

	// 6. Validate runner identity binding
	if runner != nil {
		if runner.BlockID != ir.BlockID {
			receipt.Discrepancy = fmt.Sprintf("runner block ID mismatch: runner=%q, ir=%q", runner.BlockID, ir.BlockID)
			return receipt
		}
		if runner.StartAddress != ir.StartAddress {
			receipt.Discrepancy = fmt.Sprintf("runner start address mismatch: runner=$%06X, ir=$%06X", runner.StartAddress, ir.StartAddress)
			return receipt
		}
		if runner.GeneratedCHash != cHash {
			receipt.Discrepancy = fmt.Sprintf("runner code hash mismatch: runner=%s, current=%s", runner.GeneratedCHash, cHash)
			return receipt
		}
		if runner.Context != ir.EntryContext {
			receipt.Discrepancy = fmt.Sprintf("runner context mismatch: runner=%+v, ir=%+v", runner.Context, ir.EntryContext)
			return receipt
		}
	}

	// 7. Enforce entry contract
	if cfg.EnforceContract {
		if err := EnforceEntryContract(ir, c.InitialState); err != nil {
			receipt.Discrepancy = fmt.Sprintf("entry contract: %v", err)
			return receipt
		}
	}

	// 8. Reference emulator execution
	emuRes, emuErr := RunEmulatorBlock(ctx, ir, c.InitialState, memMap)
	if emuErr != nil {
		receipt.Discrepancy = fmt.Sprintf("emulator error: %v", emuErr)
		return receipt
	}
	receipt.ReferenceEmu = emuRes

	// Check Reference Emulator vs Observed Exit CPU State
	emuVsObsMatched, emuVsObsDiscrepancy := CompareCPUStates(receipt.TraceObserved.State, emuRes.State)
	if emuVsObsMatched && receipt.TraceObserved.NextPC != emuRes.NextPC {
		emuVsObsMatched = false
		emuVsObsDiscrepancy = fmt.Sprintf("NextPC mismatch: observed=$%06X, emu=$%06X", receipt.TraceObserved.NextPC, emuRes.NextPC)
	}
	if emuVsObsMatched && c.ObservedEffectsCapture {
		writesMatched, writesDiscrepancy := CompareWrites(c.ObservedWrites, emuRes.Writes)
		if !writesMatched {
			emuVsObsMatched = false
			emuVsObsDiscrepancy = writesDiscrepancy
		}
	}
	receipt.EmulatorMatch = emuVsObsMatched
	if !emuVsObsMatched {
		receipt.Discrepancy = fmt.Sprintf("emulator vs observed mismatch: %s", emuVsObsDiscrepancy)
		return receipt
	}

	// 9. Compiled C execution
	var cRes ExecResult
	var cErr error
	if runner != nil {
		cRes, cErr = runner.RunCase(ctx, c)
	} else {
		cRes, cErr = RunCompiledCBlock(ctx, ir, c.InitialState, memMap)
	}
	if cErr != nil {
		receipt.Discrepancy = fmt.Sprintf("compiled C error: %v", cErr)
		return receipt
	}
	receipt.CompiledC = cRes

	// Check Compiled C vs Observed Exit CPU State
	cVsObsMatched, cVsObsDiscrepancy := CompareCPUStates(receipt.TraceObserved.State, cRes.State)
	if cVsObsMatched && receipt.TraceObserved.NextPC != cRes.NextPC {
		cVsObsMatched = false
		cVsObsDiscrepancy = fmt.Sprintf("NextPC mismatch: observed=$%06X, c=$%06X", receipt.TraceObserved.NextPC, cRes.NextPC)
	}
	if cVsObsMatched && c.ObservedEffectsCapture {
		writesMatched, writesDiscrepancy := CompareWrites(c.ObservedWrites, cRes.Writes)
		if !writesMatched {
			cVsObsMatched = false
			cVsObsDiscrepancy = writesDiscrepancy
		}
	}
	receipt.ObservedMatch = cVsObsMatched
	if !cVsObsMatched {
		receipt.Discrepancy = fmt.Sprintf("compiled C vs observed mismatch: %s", cVsObsDiscrepancy)
		return receipt
	}

	// Check Compiled C vs Reference Emulator
	cVsEmuMatched, cVsEmuDiscrepancy := CompareExecResults(emuRes, cRes)
	receipt.CMatch = cVsEmuMatched
	if !cVsEmuMatched {
		receipt.Discrepancy = fmt.Sprintf("compiled C vs emulator mismatch: %s", cVsEmuDiscrepancy)
		return receipt
	}

	// All 3 agreed on CPU transition
	receipt.CPUTransitionMatch = true

	// Check writes / effects comparison
	if c.ObservedEffectsCapture {
		// Verify that ObservedEffectsCapture was granted through valid admission
		validDigest := ""
		if c.Evidence != nil && c.Evidence.Fixture != nil && c.Evidence.Capture != nil && c.Evidence.History != nil {
			validDigest = ComputeAdmissionDigest(c.CaseID, c.BlockID, c.CaseHash,
				c.Evidence.Fixture.SHA256, c.Evidence.Capture.SHA256, c.Evidence.History.SHA256)
		}
		validDigest = policyDigest([]string{validDigest, v.PolicySHA256()})
		if c.AdmissionDigest == "" || c.AdmissionDigest != validDigest {
			receipt.CapturedProofEligible = false
			receipt.EffectsMatch = false
			receipt.EffectsStatus = "unadmitted_asserted_effects_rejected"
			receipt.Discrepancy = "self-asserted observed_effects_captured rejected: missing or invalid admission digest"
			return receipt
		}

		// Recomputed public digest alone does not authorize captured proof:
		// verify that EvidenceVerifier actually admitted this case.
		if !v.IsAdmitted(c.CaseHash, c.AdmissionDigest) {
			if c.Evidence != nil {
				_, _ = v.Admit(&c, nil, ir)
			}
		}
		if !v.IsAdmitted(c.CaseHash, c.AdmissionDigest) {
			receipt.CapturedProofEligible = false
			receipt.EffectsMatch = false
			receipt.EffectsStatus = "unadmitted_asserted_effects_rejected"
			receipt.Discrepancy = "self-asserted observed_effects_captured rejected: case has not passed verified evidence admission"
			return receipt
		}
		receipt.AdmissionDigest = c.AdmissionDigest

		// Both emulator and C writes must match observed writes
		writesMatched, writesDiscrepancy := CompareWrites(c.ObservedWrites, emuRes.Writes)
		if !writesMatched {
			receipt.EffectsMatch = false
			receipt.EffectsStatus = "effects_mismatched"
			receipt.Discrepancy = fmt.Sprintf("observed writes vs emulator mismatch: %s", writesDiscrepancy)
			return receipt
		}
		cWritesMatched, cWritesDiscrepancy := CompareWrites(c.ObservedWrites, cRes.Writes)
		if !cWritesMatched {
			receipt.EffectsMatch = false
			receipt.EffectsStatus = "effects_mismatched"
			receipt.Discrepancy = fmt.Sprintf("observed writes vs compiled C mismatch: %s", cWritesDiscrepancy)
			return receipt
		}
		receipt.EffectsMatch = true
		receipt.EffectsStatus = "effects_matched"
		receipt.CapturedProofEligible = true
	} else {
		// Trace does not independently capture data writes; do not claim observed-effects equality
		receipt.EffectsMatch = false
		receipt.EffectsStatus = "effects_not_captured_in_trace"
		receipt.CapturedProofEligible = false
	}

	receipt.Matched = true

	// Freshness and admission validation
	v.ValidateReplayReceiptFreshness(&receipt, &c, ir, cCode, c.ROMSHA256, cfg.ProjectRevision)
	return receipt
}

// ValidateReplayReceiptFreshness checks whether a replay receipt binds the exact current case,
// observed evidence, generated C, ROM identity, and project revision.
// It always recomputes the canonical case hash from the actual current case content.
func ValidateReplayReceiptFreshness(receipt *ReplayReceipt, currentCase *ReplayCase, ir *BlockIR, cCode string, expectedROM, expectedRev string) {
	defaultEvidenceVerifier.ValidateReplayReceiptFreshness(receipt, currentCase, ir, cCode, expectedROM, expectedRev)
}

// ValidateReplayReceiptFreshness binds a captured receipt to admission owned by
// this verifier. Another verifier's grant cannot establish freshness.
func (v *EvidenceVerifier) ValidateReplayReceiptFreshness(receipt *ReplayReceipt, currentCase *ReplayCase, ir *BlockIR, cCode string, expectedROM, expectedRev string) {
	if receipt == nil {
		return
	}
	defer func() {
		if receipt.Metadata.IsStale || !receipt.Eligible {
			receipt.Eligible = false
			receipt.CapturedProofEligible = false
		}
	}()
	if currentCase == nil {
		receipt.Metadata.IsStale = true
		receipt.Metadata.StaleReason = "missing current case fixture"
		receipt.Eligible = false
		receipt.CapturedProofEligible = false
		return
	}

	// 1. Recompute canonical case hash from actual currentCase contents
	computedCaseHash := ComputeCaseHash(*currentCase)
	if currentCase.CaseHash != "" && currentCase.CaseHash != computedCaseHash {
		receipt.Metadata.IsStale = true
		receipt.Metadata.StaleReason = fmt.Sprintf("tampered current case: recorded CaseHash %s does not match recomputed canonical hash %s",
			currentCase.CaseHash, computedCaseHash)
		receipt.Eligible = false
		return
	}

	if receipt.CaseHash != computedCaseHash {
		receipt.Metadata.IsStale = true
		receipt.Metadata.StaleReason = fmt.Sprintf("case fixture altered or substituted: hash mismatch (receipt=%s, computed=%s)",
			receipt.CaseHash, computedCaseHash)
		receipt.Eligible = false
		return
	}

	currIdentity := currentCase.Identity()
	if receipt.CaseIdentity != currIdentity {
		receipt.Metadata.IsStale = true
		receipt.Metadata.StaleReason = "case identity altered or substituted"
		receipt.Eligible = false
		return
	}

	// 2. Standard verification checks (code hash, C hash, ROM SHA, project revision)
	stdReceipt := ComparisonReceipt{
		Initial:    currentCase.InitialState,
		InitialMem: currentCase.InitialMemory,
		Metadata:   receipt.Metadata,
		Matched:    receipt.Matched,
	}
	memMap := make(map[uint32]uint8, len(currentCase.InitialMemory))
	for _, cell := range currentCase.InitialMemory {
		memMap[cell.Address] = cell.Value
	}

	ValidateReceiptFreshness(&stdReceipt, ir, cCode, memMap, expectedROM, expectedRev)
	receipt.Eligible = stdReceipt.Eligible
	receipt.Metadata = stdReceipt.Metadata

	if currentCase.ObservedEffectsCapture && !v.IsAdmitted(currentCase.CaseHash, currentCase.AdmissionDigest) {
		if currentCase.Evidence != nil {
			_, _ = v.Admit(currentCase, nil, ir)
		}
	}
	if currentCase.ObservedEffectsCapture && receipt.AdmissionDigest != currentCase.AdmissionDigest {
		receipt.Metadata.IsStale = true
		receipt.Metadata.StaleReason = "captured receipt admission authority differs"
		receipt.CapturedProofEligible = false
		return
	}
	receipt.CapturedProofEligible = receipt.Eligible && !receipt.Metadata.IsStale && receipt.Matched && currentCase.ObservedEffectsCapture && receipt.EffectsMatch && currentCase.AdmissionDigest != "" && v.IsAdmitted(currentCase.CaseHash, currentCase.AdmissionDigest)
}

// FuzzReport records the metrics and results of a bounded legal entry-state fuzz campaign.
type FuzzReport struct {
	BlockID    string        `json:"block_id"`
	Seed       int64         `json:"seed"`
	TotalCases int           `json:"total_cases"`
	Passed     int           `json:"passed"`
	Failed     int           `json:"failed"`
	Rejected   int           `json:"rejected"`
	Unexecuted int           `json:"unexecuted"`
	StopReason string        `json:"stop_reason,omitempty"`
	DurationMs int64         `json:"duration_ms"`
	Failures   []FuzzFailure `json:"failures,omitempty"`
}

// FuzzFailure records details of a discrepant input case as a self-contained replay fixture.
type FuzzFailure struct {
	CaseIndex      int              `json:"case_index"`
	Initial        CPUState         `json:"initial"`
	InitialMemory  []MemoryCell     `json:"initial_memory"`
	EntryContext   recovery.Context `json:"entry_context"`
	GeneratedCHash string           `json:"generated_c_hash"`
	Reason         string           `json:"reason"`
	Discrepancy    string           `json:"discrepancy"`
	EmulatorRes    ExecResult       `json:"emulator_res"`
	CRes           ExecResult       `json:"c_res"`
}

// RunFuzzCampaign executes a bounded fuzzing campaign constrained strictly to legal entry states.
// Accounts for passed, failed, rejected, and unexecuted cases with self-contained failure records.
func RunFuzzCampaign(ctx context.Context, ir *BlockIR, runner *CompiledRunner, numCases int, seed int64, requiredMem []MemoryCell) FuzzReport {
	start := time.Now()
	report := FuzzReport{
		BlockID:    ir.BlockID,
		Seed:       seed,
		TotalCases: numCases,
	}

	cCode, _ := GenerateCompilableC(ir)
	cHash := ComputeCHash(cCode)

	rng := mrand.New(mrand.NewSource(seed))

	baseMemMap := make(map[uint32]uint8, len(requiredMem))
	for _, m := range requiredMem {
		baseMemMap[m.Address] = m.Value
	}

	for i := 0; i < numCases; i++ {
		select {
		case <-ctx.Done():
			report.Unexecuted = numCases - (report.Passed + report.Failed + report.Rejected)
			report.StopReason = "context_canceled"
			report.DurationMs = time.Since(start).Milliseconds()
			return report
		default:
		}

		// Generate random CPU state adhering to legal entry context
		state := generateLegalCPUState(rng, ir.StartAddress, ir.EntryContext)

		// Validate contract
		if err := EnforceEntryContract(ir, state); err != nil {
			report.Rejected++
			continue
		}

		// Run emulator
		emuRes, emuErr := RunEmulatorBlock(ctx, ir, state, baseMemMap)
		if emuErr != nil {
			report.Failed++
			report.Failures = append(report.Failures, FuzzFailure{
				CaseIndex:      i,
				Initial:        state,
				InitialMemory:  requiredMem,
				EntryContext:   ir.EntryContext,
				GeneratedCHash: cHash,
				Reason:         "emulator_error",
				Discrepancy:    emuErr.Error(),
			})
			continue
		}

		// Run C runner
		var cRes ExecResult
		var cErr error
		if runner != nil {
			cInput := ReplayCaseInput{
				CaseID:  fmt.Sprintf("fuzz_%s_%d", ir.BlockID, i),
				Initial: state,
				Memory:  requiredMem,
			}
			resSlice, err := runner.RunBatch(ctx, []ReplayCaseInput{cInput})
			if err != nil {
				cErr = err
			} else if len(resSlice) > 0 {
				cRes = resSlice[0]
			}
		} else {
			cRes, cErr = RunCompiledCBlock(ctx, ir, state, baseMemMap)
		}

		if cErr != nil {
			report.Failed++
			report.Failures = append(report.Failures, FuzzFailure{
				CaseIndex:      i,
				Initial:        state,
				InitialMemory:  requiredMem,
				EntryContext:   ir.EntryContext,
				GeneratedCHash: cHash,
				Reason:         "compiled_c_error",
				Discrepancy:    cErr.Error(),
				EmulatorRes:    emuRes,
			})
			continue
		}

		matched, disc := CompareExecResults(emuRes, cRes)
		if !matched {
			report.Failed++
			report.Failures = append(report.Failures, FuzzFailure{
				CaseIndex:      i,
				Initial:        state,
				InitialMemory:  requiredMem,
				EntryContext:   ir.EntryContext,
				GeneratedCHash: cHash,
				Reason:         "discrepancy_mismatch",
				Discrepancy:    disc,
				EmulatorRes:    emuRes,
				CRes:           cRes,
			})
			continue
		}

		report.Passed++
	}

	report.DurationMs = time.Since(start).Milliseconds()
	return report
}

// generateLegalCPUState produces a random CPU state conforming strictly to the legal entry context.
// In 65816, when M=1 (8-bit accumulator), the upper 8 bits of A (the B accumulator) are preserved.
// We randomize the full 16-bit word of A so tests exercise preserving the high byte.
func generateLegalCPUState(rng *mrand.Rand, startAddr uint32, ctx recovery.Context) CPUState {
	s := CPUState{
		A:  uint16(rng.Intn(0x10000)),
		X:  uint16(rng.Intn(0x10000)),
		Y:  uint16(rng.Intn(0x10000)),
		S:  uint16(0x0100 + rng.Intn(0x0100)),
		PC: uint16(startAddr & 0xFFFF),
		D:  0x0000,
		DB: uint8(startAddr >> 16),
		PB: uint8(startAddr >> 16),
		P:  uint8(rng.Intn(0x100)) & ^uint8(0x08), // D=0 (decimal clear invariant)
	}

	// Emulation mode vs Native mode
	if ctx.E == "set" {
		s.E = true
		// In emulation mode, 65816 hardware forces M=1 and X=1
		s.P |= 0x30
		s.X &= 0x00FF
		s.Y &= 0x00FF
		s.S = 0x0100 | (s.S & 0x00FF)
		return s
	}

	s.E = false
	// Native mode: respect M and X contexts
	if ctx.M == "set" {
		s.P |= 0x20 // M=1: 8-bit accumulator. Notice s.A preserves full 16 bits!
	} else if ctx.M == "clear" {
		s.P &= ^uint8(0x20) // M=0: 16-bit accumulator
	}

	if ctx.X == "set" {
		s.P |= 0x10 // X=1: 8-bit index
		s.X &= 0x00FF
		s.Y &= 0x00FF
	} else if ctx.X == "clear" {
		s.P &= ^uint8(0x10) // X=0: 16-bit index
	}

	if ctx.C == "set" {
		s.P |= 0x01 // C=1
	} else if ctx.C == "clear" {
		s.P &= ^uint8(0x01) // C=0
	}

	return s
}

// CasePath returns the canonical path for a replay case fixture.
func CasePath(projectDir, blockID, caseID string) string {
	return filepath.Join(projectDir, "verification", "cases", fmt.Sprintf("case_%s_%s.json", blockID, caseID))
}

// SaveCase writes a ReplayCase fixture atomically using a unique temp file in the target directory.
func SaveCase(path string, c ReplayCase) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create case dir: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, "case_*.tmp")
	if err != nil {
		return fmt.Errorf("create case temp: %w", err)
	}
	tmpName := tmpFile.Name()
	defer func() {
		if err != nil {
			tmpFile.Close()
			os.Remove(tmpName)
		}
	}()

	enc := json.NewEncoder(tmpFile)
	enc.SetIndent("", "  ")
	if err = enc.Encode(c); err != nil {
		return fmt.Errorf("encode case: %w", err)
	}

	if err = tmpFile.Sync(); err != nil {
		return fmt.Errorf("sync case: %w", err)
	}
	if err = tmpFile.Close(); err != nil {
		return fmt.Errorf("close case: %w", err)
	}

	return os.Rename(tmpName, path)
}

// LoadCase loads a ReplayCase fixture from disk.
func LoadCase(path string) (ReplayCase, error) {
	f, err := os.Open(path)
	if err != nil {
		return ReplayCase{}, fmt.Errorf("open case: %w", err)
	}
	defer f.Close()
	var c ReplayCase
	if err := json.NewDecoder(f).Decode(&c); err != nil {
		return ReplayCase{}, fmt.Errorf("decode case: %w", err)
	}
	return c, nil
}

// ListCases returns all stored ReplayCases for a project and block.
func ListCases(projectDir, blockID string) ([]ReplayCase, error) {
	casesDir := filepath.Join(projectDir, "verification", "cases")
	entries, err := os.ReadDir(casesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read cases dir: %w", err)
	}

	var res []ReplayCase
	prefix := fmt.Sprintf("case_%s_", blockID)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		c, err := LoadCase(filepath.Join(casesDir, e.Name()))
		if err == nil {
			res = append(res, c)
		}
	}

	sort.Slice(res, func(i, j int) bool {
		return res[i].CaseID < res[j].CaseID
	})
	return res, nil
}

// ReplayReceiptPath returns the canonical file path for storing a block's replay receipt.
func ReplayReceiptPath(projectDir, blockID string) string {
	return filepath.Join(projectDir, "verification", fmt.Sprintf("replay_receipt_%s.json", blockID))
}

// SaveReplayReceipt writes a ReplayReceipt fixture atomically using a unique temp file in the target directory.
func SaveReplayReceipt(path string, receipt ReplayReceipt) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create receipt dir: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, "replay_receipt_*.tmp")
	if err != nil {
		return fmt.Errorf("create replay receipt temp: %w", err)
	}
	tmpName := tmpFile.Name()
	defer func() {
		if err != nil {
			tmpFile.Close()
			os.Remove(tmpName)
		}
	}()

	enc := json.NewEncoder(tmpFile)
	enc.SetIndent("", "  ")
	if err = enc.Encode(receipt); err != nil {
		return fmt.Errorf("encode replay receipt: %w", err)
	}

	if err = tmpFile.Sync(); err != nil {
		return fmt.Errorf("sync replay receipt: %w", err)
	}
	if err = tmpFile.Close(); err != nil {
		return fmt.Errorf("close replay receipt: %w", err)
	}

	return os.Rename(tmpName, path)
}

// LoadReplayReceipt loads a historical receipt. Eligibility remains false until
// a caller validates it against current project inputs.
func LoadReplayReceipt(path string) (ReplayReceipt, error) {
	f, err := os.Open(path)
	if err != nil {
		return ReplayReceipt{}, fmt.Errorf("open replay receipt: %w", err)
	}
	defer f.Close()
	var r ReplayReceipt
	if err := json.NewDecoder(f).Decode(&r); err != nil {
		return ReplayReceipt{}, fmt.Errorf("decode replay receipt: %w", err)
	}
	r.Eligible = false
	r.CapturedProofEligible = false
	return r, nil
}

// PopulateHashes updates InitialCPUHash, InitialMemHash, and CaseHash on a ReplayCase.
func PopulateHashes(c *ReplayCase) {
	c.InitialCPUHash = ComputeCPUStateHash(c.InitialState)
	memMap := make(map[uint32]uint8, len(c.InitialMemory))
	for _, cell := range c.InitialMemory {
		memMap[cell.Address] = cell.Value
	}
	hash, _ := ComputeInitialMemory(memMap)
	c.InitialMemHash = hash
	c.CaseHash = ComputeCaseHash(*c)
}

// TraceRecordFetch models an instruction operand or opcode fetch in a Schema-2 trace event.
type TraceRecordFetch struct {
	Addr      uint16 `json:"addr"`
	Value     uint8  `json:"value"`
	Role      string `json:"role"`
	ROMOffset uint32 `json:"rom_offset"`
}

// RetainedTraceEvent models the Schema-2 trace records extracted from the emulator run.
type RetainedTraceEvent struct {
	ID     uint64 `json:"id"`
	Schema int    `json:"schema"`
	Kind   string `json:"kind"`
	Cycle  uint64 `json:"cycle"`
	Frame  int    `json:"frame"`
	Insn   struct {
		Seq    uint64 `json:"seq"`
		Status string `json:"status"`
		Entry  struct {
			A      uint16 `json:"a"`
			X      uint16 `json:"x"`
			Y      uint16 `json:"y"`
			S      uint16 `json:"s"`
			PC     uint16 `json:"pc"`
			D      uint16 `json:"d"`
			DB     uint8  `json:"db"`
			PB     uint8  `json:"pb"`
			P      uint8  `json:"p"`
			E      bool   `json:"e"`
			Cycles uint64 `json:"cycles"`
		} `json:"entry"`
		Exit struct {
			A      uint16 `json:"a"`
			X      uint16 `json:"x"`
			Y      uint16 `json:"y"`
			S      uint16 `json:"s"`
			PC     uint16 `json:"pc"`
			D      uint16 `json:"d"`
			DB     uint8  `json:"db"`
			PB     uint8  `json:"pb"`
			P      uint8  `json:"p"`
			E      bool   `json:"e"`
			Cycles uint64 `json:"cycles"`
		} `json:"exit"`
		Fetches     []TraceRecordFetch `json:"fetches"`
		Length      int                `json:"length"`
		SuccessorPC struct {
			Addr uint16 `json:"addr"`
			Bank uint8  `json:"bank"`
		} `json:"successor_pc"`
		SequentialPC struct {
			Addr uint16 `json:"addr"`
			Bank uint8  `json:"bank"`
		} `json:"sequential_pc"`
	} `json:"insn"`
}

// ExtractionReceipt records the verified extraction proof for retained trace records.
type ExtractionReceipt struct {
	VerifiedSourcePath         string `json:"verified_source_path"`
	VerifiedCompressedSHA256   string `json:"verified_compressed_sha256"`
	VerifiedDecompressedSHA256 string `json:"verified_decompressed_sha256"`
	ExtractedAt                string `json:"extracted_at"`
}

// RetainedEventsFixture holds the retained records and verified stream identities.
type RetainedEventsFixture struct {
	CompressedStreamSHA256   string                          `json:"compressed_stream_sha256"`
	DecompressedStreamSHA256 string                          `json:"decompressed_stream_sha256"`
	RunID                    string                          `json:"run_id"`
	ROMSHA256                string                          `json:"rom_sha256"`
	ExtractionReceipt        ExtractionReceipt               `json:"extraction_receipt"`
	Header                   map[string]any                  `json:"header"`
	Blocks                   map[string][]RetainedTraceEvent `json:"blocks"`
}

// ValidateTraceRecordsForBlock rigorously validates a slice of trace events against basic block boundaries:
// checks schema=2, kind=cpu_insn, status=retired, contiguous seq, cycle monotonicity, CPU continuity,
// fetch-byte match against block instructions, and successor PC alignment.
func ValidateTraceRecordsForBlock(records []RetainedTraceEvent, block *structure.BasicBlock, ir *BlockIR) error {
	if len(records) == 0 {
		return errors.New("empty trace records")
	}

	for i, r := range records {
		if r.Schema != 2 {
			return fmt.Errorf("record %d (event %d): unsupported trace schema %d (expected 2)", i, r.ID, r.Schema)
		}
		if r.Kind != "cpu_insn" {
			return fmt.Errorf("record %d (event %d): unexpected event kind %q (expected cpu_insn)", i, r.ID, r.Kind)
		}
		if r.Insn.Status != "retired" {
			return fmt.Errorf("record %d (event %d): unretired instruction status %q", i, r.ID, r.Insn.Status)
		}

		if i > 0 {
			prev := records[i-1]
			// 1. Contiguous sequence check
			if r.Insn.Seq != prev.Insn.Seq+1 {
				return fmt.Errorf("sequence discontinuity between event %d (seq %d) and event %d (seq %d)",
					prev.ID, prev.Insn.Seq, r.ID, r.Insn.Seq)
			}
			// 2. Cycle monotonicity check
			if r.Cycle < prev.Cycle {
				return fmt.Errorf("cycle regression between event %d (cycle %d) and event %d (cycle %d)",
					prev.ID, prev.Cycle, r.ID, r.Cycle)
			}
			// 3. CPU continuity check
			pe := prev.Insn.Exit
			ce := r.Insn.Entry
			if pe.A != ce.A || pe.X != ce.X || pe.Y != ce.Y || pe.S != ce.S ||
				pe.PC != ce.PC || pe.D != ce.D || pe.DB != ce.DB || pe.PB != ce.PB ||
				pe.P != ce.P || pe.E != ce.E {
				return fmt.Errorf("CPU state continuity break between event %d (seq %d) exit and event %d (seq %d) entry",
					prev.ID, prev.Insn.Seq, r.ID, r.Insn.Seq)
			}
		}
	}

	if block != nil {
		firstPC := uint16(block.StartAddress & 0xFFFF)
		if records[0].Insn.Entry.PC != firstPC {
			return fmt.Errorf("block start address $%04X does not match initial trace PC $%04X",
				firstPC, records[0].Insn.Entry.PC)
		}
		if len(block.Instructions) > 0 && len(records) != len(block.Instructions) {
			return fmt.Errorf("instruction count mismatch: block has %d instructions, trace has %d records",
				len(block.Instructions), len(records))
		}

		// 4. Fetch-byte match against block instruction bytes
		for i, inst := range block.Instructions {
			if i >= len(records) {
				break
			}
			var fetchBytes []byte
			for _, f := range records[i].Insn.Fetches {
				fetchBytes = append(fetchBytes, f.Value)
			}
			gotHex := strings.ToLower(hex.EncodeToString(fetchBytes))
			wantHex := strings.ToLower(inst.Bytes)
			if gotHex != wantHex {
				return fmt.Errorf("instruction %d ($%06X) fetch byte mismatch: trace=%s, block=%s",
					i, inst.Address, gotHex, wantHex)
			}
		}

		// 5. Successor target alignment
		if len(block.Successors) > 0 {
			last := records[len(records)-1]
			succPC := (uint32(last.Insn.SuccessorPC.Bank) << 16) | uint32(last.Insn.SuccessorPC.Addr)
			matched := false
			for _, s := range block.Successors {
				if s == succPC {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("trace successor PC $%06X does not match any block successor: %v", succPC, block.Successors)
			}
		}
	}

	return nil
}

// ImportReplayCaseFromTraceRecords deterministically constructs a ReplayCase from verified trace records.
// Validates record continuity and IR binding before constructing the case.
func ImportReplayCaseFromTraceRecords(blockID string, records []RetainedTraceEvent, runID, streamSHA, decompSHA, romSHA string, derived []DerivedMemoryCell, block *structure.BasicBlock) (ReplayCase, error) {
	if err := ValidateTraceRecordsForBlock(records, block, nil); err != nil {
		return ReplayCase{}, fmt.Errorf("validate trace records for %s: %w", blockID, err)
	}

	first := records[0]
	last := records[len(records)-1]

	initialState := CPUState{
		A:  first.Insn.Entry.A,
		X:  first.Insn.Entry.X,
		Y:  first.Insn.Entry.Y,
		S:  first.Insn.Entry.S,
		PC: first.Insn.Entry.PC,
		D:  first.Insn.Entry.D,
		DB: first.Insn.Entry.DB,
		PB: first.Insn.Entry.PB,
		P:  first.Insn.Entry.P,
		E:  first.Insn.Entry.E,
	}

	observedExit := CPUState{
		A:  last.Insn.Exit.A,
		X:  last.Insn.Exit.X,
		Y:  last.Insn.Exit.Y,
		S:  last.Insn.Exit.S,
		PC: last.Insn.Exit.PC,
		D:  last.Insn.Exit.D,
		DB: last.Insn.Exit.DB,
		PB: last.Insn.Exit.PB,
		P:  last.Insn.Exit.P,
		E:  last.Insn.Exit.E,
	}

	observedNextPC := (uint32(last.Insn.SuccessorPC.Bank) << 16) | uint32(last.Insn.SuccessorPC.Addr)
	branch := "fallthrough"
	if last.Insn.SuccessorPC.Addr != last.Insn.SequentialPC.Addr {
		branch = "taken"
	}

	// Convert derived memory cells to initial memory cells
	initMem := make([]MemoryCell, len(derived))
	for i, d := range derived {
		initMem[i] = MemoryCell{Address: d.Address, Value: d.Value}
	}

	c := ReplayCase{
		SchemaVersion:            "snes-replay-case-v2",
		CaseID:                   fmt.Sprintf("trace_%s_seq_%d", strings.ReplaceAll(blockID, "-", "_"), first.Insn.Seq),
		BlockID:                  blockID,
		RunID:                    runID,
		StreamSHA256:             streamSHA,
		DecompressedStreamSHA256: decompSHA,
		ROMSHA256:                romSHA,
		Frame:                    first.Frame,
		EntrySeq:                 first.Insn.Seq,
		ExitSeq:                  last.Insn.Seq,
		StartEventID:             first.ID,
		EndEventID:               last.ID,
		StartCycle:               first.Cycle,
		EndCycle:                 last.Cycle,
		InitialState:             initialState,
		DerivedMemory:            derived,
		InitialMemory:            initMem,
		ObservedExit:             observedExit,
		ObservedNextPC:           observedNextPC,
		ObservedBranch:           branch,
		ObservedEffectsCapture:   false, // Schema-2 traces do not capture independent data bus writes
	}

	PopulateHashes(&c)
	return c, nil
}

// ImportReplayCaseFromStream scans an uncompressed trace JSONL stream, validates the header record,
// verifies stream hashes, rejects malformed lines, and extracts a block's trace events with continuity checks.
func ImportReplayCaseFromStream(r io.Reader, startEventID, endEventID uint64, blockID, expectedRunID, expectedROM, expectedDecompSHA string, derived []DerivedMemoryCell, block *structure.BasicBlock) (ReplayCase, error) {
	hasher := sha256.New()
	tee := io.TeeReader(r, hasher)
	scanner := bufio.NewScanner(tee)

	var records []RetainedTraceEvent
	var headerFound bool
	var actualRunID, actualROM string

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		// Strictly reject malformed JSON lines
		var generic map[string]any
		if err := json.Unmarshal(line, &generic); err != nil {
			return ReplayCase{}, fmt.Errorf("line %d: malformed JSON in trace stream: %w", lineNum, err)
		}

		eidFloat, hasID := generic["id"].(float64)
		if !hasID {
			return ReplayCase{}, fmt.Errorf("line %d: missing 'id' in trace record", lineNum)
		}
		eid := uint64(eidFloat)

		if eid == 0 && !headerFound {
			if generic["kind"] != "run" {
				return ReplayCase{}, fmt.Errorf("event 0: expected kind 'run', got %v", generic["kind"])
			}
			runObj, _ := generic["run"].(map[string]any)
			if runObj != nil {
				actualRunID, _ = runObj["engine_revision"].(string)
				actualROM, _ = runObj["rom_sha256"].(string)
			}
			headerFound = true
			if expectedRunID != "" && actualRunID != expectedRunID {
				return ReplayCase{}, fmt.Errorf("stream header run ID mismatch: got %s, want %s", actualRunID, expectedRunID)
			}
			if expectedROM != "" && actualROM != expectedROM {
				return ReplayCase{}, fmt.Errorf("stream header ROM SHA mismatch: got %s, want %s", actualROM, expectedROM)
			}
		}

		if eid >= startEventID && eid <= endEventID {
			var ev RetainedTraceEvent
			if err := json.Unmarshal(line, &ev); err != nil {
				return ReplayCase{}, fmt.Errorf("event %d: unmarshal RetainedTraceEvent: %w", eid, err)
			}
			records = append(records, ev)
		}

		if eid > endEventID {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return ReplayCase{}, fmt.Errorf("scan stream: %w", err)
	}

	// Consume remainder of stream to verify full stream hash if expectedDecompSHA is given
	if expectedDecompSHA != "" {
		if _, err := io.Copy(hasher, r); err != nil {
			return ReplayCase{}, fmt.Errorf("hash stream remainder: %w", err)
		}
		computedDecompSHA := hex.EncodeToString(hasher.Sum(nil))
		if computedDecompSHA != expectedDecompSHA {
			return ReplayCase{}, fmt.Errorf("decompressed stream SHA-256 mismatch: got %s, want %s",
				computedDecompSHA, expectedDecompSHA)
		}
	}

	if len(records) == 0 {
		return ReplayCase{}, fmt.Errorf("no trace records found in event range [%d, %d]", startEventID, endEventID)
	}

	return ImportReplayCaseFromTraceRecords(blockID, records, actualRunID, "", expectedDecompSHA, actualROM, derived, block)
}
