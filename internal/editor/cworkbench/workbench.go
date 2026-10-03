// Package cworkbench keeps user annotations separate from generated executable C.
package cworkbench

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Input identifies an operator-selected local artifact.
type Input struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Reference is attributed comparison material, not recovered ROM semantics.
type Reference struct {
	Input       Input  `json:"input"`
	Attribution string `json:"attribution"`
	FirstLine   int    `json:"first_line"`
	LastLine    int    `json:"last_line"`
}

// Config selects immutable generated artifacts and an optional reference excerpt.
// NotesPath is the only file the workbench writes.
type Config struct {
	Source             Input      `json:"source"`
	IR                 Input      `json:"ir"`
	Receipt            Input      `json:"receipt"`
	AdditionalReceipts []Input    `json:"additional_receipts,omitempty"`
	NotesPath          string     `json:"notes_path"`
	Reference          *Reference `json:"reference,omitempty"`
}

// Note is a user hypothesis at an emitted instruction address. Type is a proposed
// type, not a change to the generated runner's memory or register semantics.
type Note struct {
	Address    uint32 `json:"address"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Hypothesis string `json:"hypothesis"`
}

// Line maps an emitted C source line back to its instruction, when present.
type Line struct {
	Number  int     `json:"number"`
	Address *uint32 `json:"address,omitempty"`
	Text    string  `json:"text"`
}

// Model is the read-only code and evidence view. Notes remain user assertions.
type Model struct {
	SourceSHA256         string   `json:"source_sha256"`
	IRSHA256             string   `json:"ir_sha256"`
	ReceiptSHA256        string   `json:"receipt_sha256"`
	Lines                []Line   `json:"lines"`
	Notes                []Note   `json:"notes"`
	ReferenceAttribution string   `json:"reference_attribution,omitempty"`
	ReferenceText        string   `json:"reference_text,omitempty"`
	Scope                string   `json:"scope"`
	Reports              []Report `json:"reports,omitempty"`
}

// Report describes a pinned report's own claim. Opening a workbench does not
// verify the capture or grant admission to any case.
type Report struct {
	SHA256       string   `json:"sha256"`
	Schema       string   `json:"schema"`
	Status       string   `json:"status"`
	Cases        int      `json:"cases"`
	Admitted     int      `json:"admitted"`
	Matched      int      `json:"matched"`
	PolicySHA256 string   `json:"policy_sha256"`
	RunnerSHA256 string   `json:"runner_sha256"`
	Limitations  []string `json:"limitations,omitempty"`
}

type notesFile struct {
	Schema       string `json:"schema"`
	SourceSHA256 string `json:"source_sha256"`
	Notes        []Note `json:"notes"`
}

// Workbench owns pinned artifacts and serializes annotation publication.
// Its zero value is not usable; call Open.
type Workbench struct {
	mu                  sync.Mutex
	config              Config
	model               Model
	source, ir, receipt []byte
	additionalReceipts  [][]byte
	addresses           map[uint32]bool
}

func hash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func pinned(in Input) ([]byte, error) {
	if !filepath.IsAbs(in.Path) || !validHash(in.SHA256) {
		return nil, fmt.Errorf("invalid artifact identity")
	}
	f, err := os.Open(in.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("artifact is not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, 16<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || len(b) > 16<<20 {
		return nil, fmt.Errorf("artifact outside size bound")
	}
	if hash(b) != in.SHA256 {
		return nil, fmt.Errorf("artifact identity differs")
	}
	return b, nil
}
func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

var instruction = regexp.MustCompile(`^\s*/\* \$([0-9A-Fa-f]{6}):`)
var caseAddress = regexp.MustCompile(`^case 0x([0-9a-fA-F]{6}):`)

// Open verifies all selected artifacts and restores source-scoped user notes.
// Receipt is displayed as pinned raw evidence; Open does not grant qualification.
func Open(c Config) (*Workbench, error) {
	if !filepath.IsAbs(c.NotesPath) {
		return nil, fmt.Errorf("notes path must be absolute")
	}
	if len(c.AdditionalReceipts) > 7 {
		return nil, fmt.Errorf("too many additional receipts")
	}
	inputs := []Input{c.Source, c.IR, c.Receipt}
	inputs = append(inputs, c.AdditionalReceipts...)
	if c.Reference != nil {
		inputs = append(inputs, c.Reference.Input)
	}
	for _, in := range inputs {
		if filepath.Clean(in.Path) == filepath.Clean(c.NotesPath) {
			return nil, fmt.Errorf("notes path aliases an immutable artifact")
		}
		a, ae := os.Stat(in.Path)
		b, be := os.Stat(c.NotesPath)
		if ae == nil && be == nil && os.SameFile(a, b) {
			return nil, fmt.Errorf("notes path aliases an immutable artifact")
		}
	}
	w := &Workbench{config: c, addresses: make(map[uint32]bool)}
	var err error
	if w.source, err = pinned(c.Source); err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}
	if w.ir, err = pinned(c.IR); err != nil {
		return nil, fmt.Errorf("IR: %w", err)
	}
	if !json.Valid(w.ir) {
		return nil, fmt.Errorf("invalid IR JSON")
	}
	if w.receipt, err = pinned(c.Receipt); err != nil {
		return nil, fmt.Errorf("receipt: %w", err)
	}
	if !json.Valid(w.receipt) {
		return nil, fmt.Errorf("invalid receipt JSON")
	}
	var receipt any
	json.Unmarshal(w.receipt, &receipt)
	if !boundReceipt(receipt, c.Source.SHA256, c.IR.SHA256) {
		return nil, fmt.Errorf("receipt does not bind selected source and IR")
	}
	w.model = Model{SourceSHA256: c.Source.SHA256, IRSHA256: c.IR.SHA256, ReceiptSHA256: c.Receipt.SHA256, Scope: "generated executable C; address mapping comes from emitter markers; names, types and hypotheses are user assertions; reference text is comparison material; the pinned receipt retains its original qualification limits"}
	if report, ok := connectedReport(receipt, c.Receipt.SHA256); ok {
		w.model.Reports = append(w.model.Reports, report)
		w.model.Scope = "generated executable C; each pinned report states its own bounded result; this view checks source and region identity but does not reverify capture or admission; notes are user assertions"
	}
	for _, in := range c.AdditionalReceipts {
		b, err := pinned(in)
		if err != nil {
			return nil, fmt.Errorf("additional receipt: %w", err)
		}
		if !json.Valid(b) {
			return nil, fmt.Errorf("invalid additional receipt JSON")
		}
		var value any
		if err := json.Unmarshal(b, &value); err != nil || !boundReceipt(value, c.Source.SHA256, c.IR.SHA256) {
			return nil, fmt.Errorf("additional receipt does not bind selected source and IR")
		}
		report, ok := connectedReport(value, in.SHA256)
		if !ok || len(w.model.Reports) == 0 {
			return nil, fmt.Errorf("additional receipt is not a connected report")
		}
		w.model.Reports = append(w.model.Reports, report)
		w.additionalReceipts = append(w.additionalReceipts, b)
	}
	for i, s := range strings.Split(string(w.source), "\n") {
		l := Line{Number: i + 1, Text: s}
		m := instruction.FindStringSubmatch(s)
		if m == nil {
			m = caseAddress.FindStringSubmatch(s)
		}
		if m != nil {
			v, _ := strconv.ParseUint(m[1], 16, 24)
			a := uint32(v)
			l.Address = &a
			w.addresses[a] = true
		}
		w.model.Lines = append(w.model.Lines, l)
	}
	if len(w.addresses) == 0 {
		return nil, fmt.Errorf("source has no instruction address markers")
	}
	if c.Reference != nil {
		r := c.Reference
		if strings.TrimSpace(r.Attribution) == "" || r.FirstLine < 1 || r.LastLine < r.FirstLine || r.LastLine-r.FirstLine >= 200 {
			return nil, fmt.Errorf("invalid reference excerpt")
		}
		b, err := pinned(r.Input)
		if err != nil {
			return nil, fmt.Errorf("reference: %w", err)
		}
		lines := strings.Split(string(b), "\n")
		if r.LastLine > len(lines) {
			return nil, fmt.Errorf("reference excerpt outside file")
		}
		w.model.ReferenceAttribution = r.Attribution
		w.model.ReferenceText = strings.Join(lines[r.FirstLine-1:r.LastLine], "\n")
	}
	f, err := os.Open(c.NotesPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("notes: %w", err)
	}
	if err == nil {
		b, readErr := io.ReadAll(io.LimitReader(f, 1<<20+1))
		f.Close()
		if readErr != nil {
			return nil, readErr
		}
		if len(b) > 1<<20 {
			return nil, fmt.Errorf("notes outside size bound")
		}
		var n notesFile
		if err := decode(b, &n); err != nil {
			return nil, err
		}
		if n.Schema != "snes-c-notes-v1" || n.SourceSHA256 != c.Source.SHA256 {
			return nil, fmt.Errorf("notes source identity differs")
		}
		if err := w.checkNotes(n.Notes); err != nil {
			return nil, err
		}
		w.model.Notes = n.Notes
	}
	return w, nil
}

// boundReceipt checks association only. It neither reruns nor authenticates a
// qualification; the operator still selects and pins the evidence artifact.
func boundReceipt(v any, source, ir string) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	if result, ok := m["result"].(map[string]any); ok {
		m = result
	}
	if m["schema"] == "snes-connected-queue-v1" {
		return m["source_sha256"] == source && m["region_sha256"] == ir
	}
	if m["schema"] != "snes-machine-branch-v1" || m["mode"] != "recovered_c" || m["captured_proof_eligible"] != false || m["replacement_executed"] != true {
		return false
	}
	c, ok := m["compiled"].(map[string]any)
	if !ok || c["semantics_origin"] != "generic_machine_ir" || c["source_sha256"] != source || (c["ir_sha256"] != ir && c["edited_ir_sha256"] != ir) {
		return false
	}
	s, ok := c["source"].(string)
	return ok && hash([]byte(s)) == source
}

func connectedReport(v any, sha string) (Report, bool) {
	m, ok := v.(map[string]any)
	if !ok || m["schema"] != "snes-connected-queue-v1" {
		return Report{}, false
	}
	getString := func(key string) string { s, _ := m[key].(string); return s }
	getCount := func(key string) int { n, _ := m[key].(float64); return int(n) }
	r := Report{SHA256: sha, Schema: getString("schema"), Status: getString("status"), Cases: getCount("cases"), Admitted: getCount("admitted"), Matched: getCount("matched"), PolicySHA256: getString("policy_sha256"), RunnerSHA256: getString("runner_hash")}
	if r.RunnerSHA256 == "" {
		r.RunnerSHA256 = getString("runner_sha256")
	}
	if limits, ok := m["limitations"].([]any); ok {
		for _, limit := range limits {
			if s, ok := limit.(string); ok {
				r.Limitations = append(r.Limitations, s)
			}
		}
	}
	return r, true
}
func (w *Workbench) checkNotes(notes []Note) error {
	if len(notes) > 256 {
		return fmt.Errorf("too many notes")
	}
	seen := map[uint32]bool{}
	for _, n := range notes {
		if !w.addresses[n.Address] || seen[n.Address] {
			return fmt.Errorf("unknown or duplicate instruction address")
		}
		seen[n.Address] = true
		if len(n.Name) > 128 || len(n.Type) > 128 || len(n.Hypothesis) > 4096 {
			return fmt.Errorf("note outside size bound")
		}
	}
	return nil
}

// SetNotes atomically replaces user annotations only when the expected source
// identity matches. It never edits C, IR, references or validation receipts.
func (w *Workbench) SetNotes(sourceSHA string, notes []Note) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if sourceSHA != w.config.Source.SHA256 {
		return fmt.Errorf("stale source identity")
	}
	if err := w.checkNotes(notes); err != nil {
		return err
	}
	b, err := json.MarshalIndent(notesFile{"snes-c-notes-v1", sourceSHA, notes}, "", "  ")
	if err != nil {
		return err
	}
	if len(b)+1 > 1<<20 {
		return fmt.Errorf("encoded notes outside size bound")
	}
	f, err := os.CreateTemp(filepath.Dir(w.config.NotesPath), ".snes-notes-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, w.config.NotesPath); err != nil {
		return err
	}
	w.model.Notes = append([]Note(nil), notes...)
	return nil
}

// Handler serves the code workbench and a same-origin annotation endpoint.
func (w *Workbench) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/model", func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(rw, "method not allowed", 405)
			return
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(rw).Encode(w.model)
	})
	for path, body := range map[string][]byte{"/api/source": w.source, "/api/ir": w.ir, "/api/receipt": w.receipt} {
		body := body
		mux.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				http.Error(rw, "method not allowed", 405)
				return
			}
			rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
			rw.Header().Set("Cache-Control", "no-store")
			rw.Write(body)
		})
	}
	for i, body := range w.additionalReceipts {
		path := fmt.Sprintf("/api/receipt/%d", i+1)
		body := body
		mux.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				http.Error(rw, "method not allowed", 405)
				return
			}
			rw.Header().Set("Content-Type", "application/json")
			rw.Header().Set("Cache-Control", "no-store")
			rw.Write(body)
		})
	}
	mux.HandleFunc("/api/notes", func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(rw, "method not allowed", 405)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(rw, "JSON required", 415)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
			http.Error(rw, "origin differs", 403)
			return
		}
		b, err := io.ReadAll(http.MaxBytesReader(rw, r.Body, 1<<20))
		if err != nil {
			http.Error(rw, "request too large", 413)
			return
		}
		var n struct {
			SourceSHA256 string `json:"source_sha256"`
			Notes        []Note `json:"notes"`
		}
		if err := decode(b, &n); err != nil {
			http.Error(rw, "invalid notes", 400)
			return
		}
		if err := w.SetNotes(n.SourceSHA256, n.Notes); err != nil {
			http.Error(rw, err.Error(), 409)
			return
		}
		rw.WriteHeader(204)
	})
	mux.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(rw, r)
			return
		}
		if r.Method != "GET" {
			http.Error(rw, "method not allowed", 405)
			return
		}
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'")
		io.WriteString(rw, page)
	})
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(rw, "loopback host required", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(rw, r)
	})
}
