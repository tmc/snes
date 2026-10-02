package cworkbench

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	write := func(name, s string) Input {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
		return Input{p, hash([]byte(s))}
	}
	source := write("recovered.c", "case 0x008008: {\n    /* $008008: ADC (inst-008008) */\n    s.a += 6;\n}\n")
	ir := write("ir.json", `{"semantic":"add"}`)
	sourceBytes, _ := os.ReadFile(source.Path)
	rb, _ := json.Marshal(map[string]any{"schema": "snes-machine-branch-v1", "mode": "recovered_c", "captured_proof_eligible": false, "replacement_executed": true, "compiled": map[string]any{"semantics_origin": "generic_machine_ir", "source": string(sourceBytes), "source_sha256": source.SHA256, "edited_ir_sha256": ir.SHA256}})
	receipt := write("receipt.json", string(rb))
	return Config{Source: source, IR: ir, Receipt: receipt, NotesPath: filepath.Join(dir, "notes.json")}
}
func TestNotes(t *testing.T) {
	c := fixture(t)
	w, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	notes := []Note{{Address: 0x008008, Name: "test_increment", Type: "uint8_t", Hypothesis: "candidate interpretation, not a ROM fact"}}
	if err := w.SetNotes(c.Source.SHA256, notes); err != nil {
		t.Fatal(err)
	}
	notes[0].Name = "changed caller memory"
	if w.model.Notes[0].Name != "test_increment" {
		t.Fatal("notes not owned")
	}
	reopened, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.model.Notes[0].Name != "test_increment" {
		t.Fatal("notes not retained")
	}
	for _, in := range []Input{c.Source, c.IR, c.Receipt} {
		if _, err := pinned(in); err != nil {
			t.Fatal("immutable artifact changed:", err)
		}
	}
	if err := w.SetNotes(c.Source.SHA256, nil); err != nil {
		t.Fatal(err)
	}
	reopened, err = Open(c)
	if err != nil || len(reopened.model.Notes) != 0 {
		t.Fatal("remove notes:", err)
	}
}
func TestRefusals(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Config)
	}{
		{"source substitution", func(c *Config) { os.WriteFile(c.Source.Path, []byte("changed"), 0600) }},
		{"receipt binding", func(c *Config) {
			b := []byte(`{"source_sha256":"bad"}`)
			os.WriteFile(c.Receipt.Path, b, 0600)
			c.Receipt.SHA256 = hash(b)
		}},
		{"notes overwrite source", func(c *Config) { c.NotesPath = c.Source.Path }},
		{"notes hardlink source", func(c *Config) { os.Link(c.Source.Path, c.NotesPath) }},
		{"stale notes", func(c *Config) {
			os.WriteFile(c.NotesPath, []byte(`{"schema":"snes-c-notes-v1","source_sha256":"stale","notes":[]}`), 0600)
		}},
		{"unattributed reference", func(c *Config) { c.Reference = &Reference{Input: c.Source, FirstLine: 1, LastLine: 1} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := fixture(t)
			tt.change(&c)
			if _, err := Open(c); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	c := fixture(t)
	w, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, sha string
		notes     []Note
	}{
		{"stale", "stale", nil}, {"unknown", c.Source.SHA256, []Note{{Address: 1}}}, {"duplicate", c.Source.SHA256, []Note{{Address: 0x008008}, {Address: 0x008008}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := w.SetNotes(tt.sha, tt.notes); err == nil {
				t.Fatal("accepted")
			}
			if _, err := os.Stat(c.NotesPath); !os.IsNotExist(err) {
				t.Fatal("partial publication")
			}
		})
	}
}
func TestHTTP(t *testing.T) {
	c := fixture(t)
	w, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	h := w.Handler()
	for _, path := range []string{"/", "/api/model", "/api/source", "/api/ir", "/api/receipt"} {
		r := httptest.NewRequest("GET", "http://127.0.0.1"+path, nil)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		if out.Code != 200 {
			t.Fatalf("%s: %d", path, out.Code)
		}
	}
	for _, tt := range []struct {
		name, origin, body string
		want               int
	}{
		{"save", "", `{"source_sha256":"` + c.Source.SHA256 + `","notes":[{"address":32776,"name":"<script>bad</script>","type":"uint8_t","hypothesis":"user"}]}`, 204},
		{"cross origin", "http://evil.example", `{}`, 403},
		{"unknown field", "", `{"source_sha256":"x","command":"execute"}`, 400},
		{"stale", "", `{"source_sha256":"x","notes":[]}`, 409},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://127.0.0.1/api/notes", bytes.NewBufferString(tt.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", tt.origin)
			out := httptest.NewRecorder()
			h.ServeHTTP(out, r)
			if out.Code != tt.want {
				t.Fatalf("%d: %s", out.Code, out.Body.String())
			}
		})
	}
}
func TestEncodedNotesBound(t *testing.T) {
	c := fixture(t)
	w, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	var notes []Note
	for i := uint32(0); i < 64; i++ {
		w.addresses[i] = true
		notes = append(notes, Note{Address: i, Hypothesis: string(bytes.Repeat([]byte("<"), 4096))})
	}
	if err := w.SetNotes(c.Source.SHA256, notes); err == nil {
		t.Fatal("accepted notes that cannot reopen")
	}
	if _, err := os.Stat(c.NotesPath); !os.IsNotExist(err) {
		t.Fatal("partial publication")
	}
}
func TestReceiptAssociation(t *testing.T) {
	c := fixture(t)
	b, _ := os.ReadFile(c.Receipt.Path)
	var real any
	json.Unmarshal(b, &real)
	for _, tt := range []struct {
		name    string
		receipt any
	}{
		{"unrelated descendant", map[string]any{"schema": "failed", "unrelated_request": real}},
		{"wrong wrapper", map[string]any{"result": map[string]any{"schema": "failed"}, "notes": real}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if boundReceipt(tt.receipt, c.Source.SHA256, c.IR.SHA256) {
				t.Fatal("borrowed identity")
			}
		})
	}
	if !boundReceipt(map[string]any{"result": real}, c.Source.SHA256, c.IR.SHA256) {
		t.Fatal("actual wrapper refused")
	}
}
func TestHost(t *testing.T) {
	c := fixture(t)
	w, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"evil.example:8097", "localhost:8097", "192.168.1.1:8097"} {
		r := httptest.NewRequest("GET", "/api/model", nil)
		r.Host = host
		out := httptest.NewRecorder()
		w.Handler().ServeHTTP(out, r)
		if out.Code != 403 {
			t.Fatalf("accepted %s", host)
		}
	}
}
