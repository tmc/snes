package cworkbench_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/tmc/snes/internal/editor/cworkbench"
	"net/http/httptest"
	"os"
	"path/filepath"
)

func ExampleOpen() {
	_, err := cworkbench.Open(cworkbench.Config{})
	fmt.Println(err)
	// Output: notes path must be absolute
}
func ExampleNote() {
	n := cworkbench.Note{Address: 0x008008, Name: "test_delta", Type: "uint8_t", Hypothesis: "proposed meaning"}
	fmt.Printf("$%06X: %s (%s)\n", n.Address, n.Name, n.Type)
	// Output: $008008: test_delta (uint8_t)
}

func ExampleWorkbench_SetNotes() {
	w, sourceSHA, cleanup := exampleWorkbench()
	defer cleanup()
	err := w.SetNotes(sourceSHA, []cworkbench.Note{{Address: 0x008008, Name: "test_delta", Type: "uint8_t"}})
	fmt.Println(err == nil)
	// Output: true
}
func ExampleWorkbench_Handler() {
	w, _, cleanup := exampleWorkbench()
	defer cleanup()
	out := httptest.NewRecorder()
	w.Handler().ServeHTTP(out, httptest.NewRequest("GET", "http://127.0.0.1/api/model", nil))
	fmt.Println(out.Code)
	// Output: 200
}
func exampleWorkbench() (*cworkbench.Workbench, string, func()) {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	dir, err := os.MkdirTemp(filepath.Join(home, "tmp"), "snes-workbench-example-")
	if err != nil {
		panic(err)
	}
	write := func(name string, b []byte) cworkbench.Input {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0600); err != nil {
			panic(err)
		}
		return cworkbench.Input{Path: p, SHA256: fmt.Sprintf("%x", sha256.Sum256(b))}
	}
	source := write("recovered.c", []byte("case 0x008008: { /* emitted instruction */ }\n"))
	ir := write("ir.json", []byte(`{"operation":"add"}`))
	receipt, _ := json.Marshal(map[string]any{"schema": "snes-machine-branch-v1", "mode": "recovered_c", "captured_proof_eligible": false, "replacement_executed": true, "compiled": map[string]any{"source": "case 0x008008: { /* emitted instruction */ }\n", "source_sha256": source.SHA256, "ir_sha256": ir.SHA256, "semantics_origin": "generic_machine_ir"}})
	w, err := cworkbench.Open(cworkbench.Config{Source: source, IR: ir, Receipt: write("receipt.json", receipt), NotesPath: filepath.Join(dir, "notes.json")})
	if err != nil {
		panic(err)
	}
	return w, source.SHA256, func() { os.RemoveAll(dir) }
}
