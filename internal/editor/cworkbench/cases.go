package cworkbench

import (
	"encoding/json"
	"fmt"
)

// CaseFiles selects recorded cases and receipts for one pinned report.
type CaseFiles struct {
	ReportSHA256 string `json:"report_sha256"`
	Cases        Input  `json:"cases"`
	Receipts     Input  `json:"receipts"`
}

// Write is one byte write in its recorded order.
type Write struct {
	Address uint32 `json:"address"`
	Value   uint8  `json:"value"`
}

// CapturedCase presents one previously qualified replay without rerunning it.
type CapturedCase struct {
	CaseID           string  `json:"case_id"`
	Frame            int     `json:"frame"`
	EntrySeq         uint64  `json:"entry_seq"`
	InstructionCount int     `json:"instruction_count"`
	ReturnPC         uint32  `json:"return_pc"`
	NextPC           uint32  `json:"next_pc"`
	ObservedWrites   []Write `json:"observed_writes"`
	CWrites          []Write `json:"c_writes"`
}

// CaseSet keeps each report's cases separate, including shared early returns.
type CaseSet struct {
	ReportSHA256   string         `json:"report_sha256"`
	CasesSHA256    string         `json:"cases_sha256"`
	ReceiptsSHA256 string         `json:"receipts_sha256"`
	Cases          []CapturedCase `json:"cases"`
}

type recordedCase struct {
	CaseID           string  `json:"case_id"`
	CaseHash         string  `json:"case_hash"`
	AdmissionDigest  string  `json:"admission_digest"`
	RoutineID        string  `json:"routine_id"`
	ROMSHA256        string  `json:"rom_sha256"`
	RunID            string  `json:"run_id"`
	StreamSHA256     string  `json:"stream_sha256"`
	Frame            int     `json:"frame"`
	EntrySeq         uint64  `json:"entry_seq"`
	ExitSeq          uint64  `json:"exit_seq"`
	InstructionCount int     `json:"instruction_count"`
	ReturnPC         uint32  `json:"return_insn_pc"`
	NextPC           uint32  `json:"observed_next_pc"`
	ObservedWrites   []Write `json:"observed_writes"`
}

type recordedResult struct {
	NextPC uint32  `json:"next_pc"`
	Writes []Write `json:"writes"`
}

type recordedReceipt struct {
	CaseID                string `json:"case_id"`
	CaseHash              string `json:"case_hash"`
	AdmissionDigest       string `json:"admission_digest"`
	BlockID               string `json:"block_id"`
	Matched               bool   `json:"matched"`
	Eligible              bool   `json:"eligible"`
	CapturedProofEligible bool   `json:"captured_proof_eligible"`
	EffectsMatch          bool   `json:"effects_match"`
	ObservedMatch         bool   `json:"observed_match"`
	EmulatorMatch         bool   `json:"emulator_match"`
	CMatch                bool   `json:"c_match"`
	CaseIdentity          struct {
		CaseID       string `json:"case_id"`
		RunID        string `json:"run_id"`
		StreamSHA256 string `json:"stream_sha256"`
		ROMSHA256    string `json:"rom_sha256"`
		Frame        int    `json:"frame"`
		EntrySeq     uint64 `json:"entry_seq"`
		ExitSeq      uint64 `json:"exit_seq"`
		NextPC       uint32 `json:"observed_next_pc"`
	} `json:"case_identity"`
	Metadata struct {
		RunnerHash     string `json:"runner_hash"`
		GeneratedCHash string `json:"generated_c_hash"`
		ROMSHA256      string `json:"rom_sha256"`
		Revision       string `json:"project_revision"`
		IsStale        bool   `json:"is_stale"`
	} `json:"metadata"`
	TraceObserved recordedResult `json:"trace_observed"`
	CompiledC     recordedResult `json:"compiled_c"`
}

type caseReport struct {
	Schema        string `json:"schema"`
	Status        string `json:"status"`
	SourceSHA256  string `json:"source_sha256"`
	RegionSHA256  string `json:"region_sha256"`
	ProfileSHA256 string `json:"profile_sha256"`
	RunnerHash    string `json:"runner_hash"`
	RunnerSHA256  string `json:"runner_sha256"`
	ROMSHA256     string `json:"rom_sha256"`
	Revision      string `json:"revision"`
	RoutineID     string `json:"routine_id"`
	Cases         int    `json:"cases"`
	Admitted      int    `json:"admitted"`
	Matched       int    `json:"matched"`
	Refused       int    `json:"refused"`
	Mismatched    int    `json:"mismatched"`
	Unexecuted    int    `json:"unexecuted"`
}

func sameWrites(a, b []Write) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (w *Workbench) caseMatchesReport(c recordedCase, r recordedReceipt, report caseReport) bool {
	if c.CaseID == "" || len(c.CaseID) > 256 || !validHash(c.CaseHash) || !validHash(c.AdmissionDigest) || c.RunID == "" || !validHash(c.StreamSHA256) || c.Frame < 0 || c.EntrySeq == 0 || c.ExitSeq <= c.EntrySeq || c.InstructionCount < 1 || c.InstructionCount > 50000 || c.ReturnPC >= 1<<24 || c.NextPC >= 1<<24 || len(c.ObservedWrites) > 4096 {
		return false
	}
	if !w.addresses[c.ReturnPC] || c.RoutineID != report.RoutineID || c.ROMSHA256 != report.ROMSHA256 {
		return false
	}
	if r.CaseID != c.CaseID || r.CaseHash != c.CaseHash || r.AdmissionDigest != c.AdmissionDigest || r.BlockID != c.RoutineID {
		return false
	}
	id := r.CaseIdentity
	if id.CaseID != c.CaseID || id.RunID != c.RunID || id.StreamSHA256 != c.StreamSHA256 || id.ROMSHA256 != c.ROMSHA256 || id.Frame != c.Frame || id.EntrySeq != c.EntrySeq || id.ExitSeq != c.ExitSeq || id.NextPC != c.NextPC {
		return false
	}
	if r.Metadata.RunnerHash != report.RunnerHash || r.Metadata.GeneratedCHash != report.SourceSHA256 || r.Metadata.ROMSHA256 != report.ROMSHA256 || r.Metadata.Revision != report.Revision || r.Metadata.IsStale {
		return false
	}
	if !r.Matched || !r.Eligible || !r.CapturedProofEligible || !r.EffectsMatch || !r.ObservedMatch || !r.EmulatorMatch || !r.CMatch {
		return false
	}
	return r.TraceObserved.NextPC == c.NextPC && r.CompiledC.NextPC == c.NextPC && sameWrites(c.ObservedWrites, r.TraceObserved.Writes) && sameWrites(c.ObservedWrites, r.CompiledC.Writes)
}

func (w *Workbench) loadCapturedCases() error {
	if len(w.config.CapturedCases) == 0 {
		return nil
	}
	reports := map[string][]byte{w.config.Receipt.SHA256: w.receipt}
	for i, in := range w.config.AdditionalReceipts {
		reports[in.SHA256] = w.additionalReceipts[i]
	}
	seenReports := make(map[string]bool)
	for _, files := range w.config.CapturedCases {
		reportBytes, ok := reports[files.ReportSHA256]
		if !ok || seenReports[files.ReportSHA256] {
			return fmt.Errorf("captured cases lack a unique selected report")
		}
		seenReports[files.ReportSHA256] = true
		var report caseReport
		if err := json.Unmarshal(reportBytes, &report); err != nil {
			return fmt.Errorf("captured case report: %w", err)
		}
		if report.RunnerHash == "" {
			report.RunnerHash = report.RunnerSHA256
		}
		if report.Schema != "snes-connected-queue-v1" || report.Status != "qualified" || report.SourceSHA256 != w.config.Source.SHA256 || report.RegionSHA256 != w.config.IR.SHA256 || !validHash(report.ProfileSHA256) || !validHash(report.RunnerHash) || !validHash(report.ROMSHA256) || report.Revision == "" || report.RoutineID == "" || report.Cases < 1 || report.Cases > 2000 || report.Admitted != report.Cases || report.Matched != report.Cases || report.Refused != 0 || report.Mismatched != 0 || report.Unexecuted != 0 {
			return fmt.Errorf("captured case report lacks bounded qualification")
		}
		caseBytes, err := pinned(files.Cases)
		if err != nil {
			return fmt.Errorf("captured cases: %w", err)
		}
		receiptBytes, err := pinned(files.Receipts)
		if err != nil {
			return fmt.Errorf("captured receipts: %w", err)
		}
		var cases []recordedCase
		var receipts []recordedReceipt
		if err := json.Unmarshal(caseBytes, &cases); err != nil {
			return fmt.Errorf("captured cases: %w", err)
		}
		if err := json.Unmarshal(receiptBytes, &receipts); err != nil {
			return fmt.Errorf("captured receipts: %w", err)
		}
		if len(cases) != report.Cases || len(receipts) != report.Cases {
			return fmt.Errorf("captured case counts differ from report")
		}
		set := CaseSet{ReportSHA256: files.ReportSHA256, CasesSHA256: files.Cases.SHA256, ReceiptsSHA256: files.Receipts.SHA256}
		seenCases := make(map[string]bool)
		for i, c := range cases {
			r := receipts[i]
			if seenCases[c.CaseID] || !w.caseMatchesReport(c, r, report) {
				return fmt.Errorf("captured case %d differs from selected report or receipt", i)
			}
			seenCases[c.CaseID] = true
			set.Cases = append(set.Cases, CapturedCase{CaseID: c.CaseID, Frame: c.Frame, EntrySeq: c.EntrySeq, InstructionCount: c.InstructionCount, ReturnPC: c.ReturnPC, NextPC: c.NextPC, ObservedWrites: c.ObservedWrites, CWrites: r.CompiledC.Writes})
		}
		w.model.CapturedCases = append(w.model.CapturedCases, set)
	}
	w.model.Scope += "; captured cases display already qualified receipts and do not grant fresh admission or pixel ownership"
	return nil
}
