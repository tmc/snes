package decomp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHelperBusSchemaPayload(t *testing.T) {
	state := cpuStateWithCycles{PC: 0x9000, P: 0x30, Cycles: 1}
	middle := state
	middle.PC = 0x9002
	middle.Cycles = 10
	exit := middle
	exit.PC = 0x9004
	exit.Cycles = 20
	insns := []captureCPUInsn{{Entry: state, Exit: middle, Fetches: []captureFetch{{Addr: 0x9000, Value: 0x84}, {Addr: 0x9001, Value: 3}}}, {Entry: middle, Exit: exit, Fetches: []captureFetch{{Addr: 0x9002, Value: 0xa4}, {Addr: 0x9003, Value: 3}}}}
	for _, kind := range []string{"valid zero", "missing write value", "null read", "null write", "conflicting write", "missing width", "zero width", "wide width", "unknown schema", "missing CPU opcode"} {
		t.Run(kind, func(t *testing.T) {
			writer := map[string]any{"schema": 2, "kind": "bus", "id": 1, "cycle": 2, "space": "wram", "addr": 3, "width": 1, "op": "write", "after": 0, "cpu": map[string]any{"pbr": 0, "pc": 0x9000, "opcode": 0x84, "bytes": []int{0x84, 3}}}
			reader := map[string]any{"schema": 2, "kind": "bus", "id": 2, "cycle": 12, "space": "wram", "addr": 3, "width": 1, "op": "read", "cpu": map[string]any{"pbr": 0, "pc": 0x9002, "opcode": 0xa4, "bytes": []int{0xa4, 3}}}
			switch kind {
			case "missing write value":
				delete(writer, "after")
			case "null read":
				reader["value"] = nil
			case "null write":
				writer["after"] = nil
			case "conflicting write":
				writer["value"] = 1
			case "missing width":
				delete(reader, "width")
			case "zero width":
				reader["width"] = 0
			case "wide width":
				reader["width"] = 2
			case "unknown schema":
				reader["schema"] = 1
			case "missing CPU opcode":
				delete(reader["cpu"].(map[string]any), "opcode")
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "capture.jsonl")
			w, _ := json.Marshal(writer)
			r, _ := json.Marshal(reader)
			if err := os.WriteFile(path, append(append(w, '\n'), append(r, '\n')...), 0600); err != nil {
				t.Fatal(err)
			}
			v := newEvidenceVerifier(dir)
			v.policy = &admissionPolicy{rom: make([]byte, 32768)}
			cd, err := v.loadCapture(path)
			if err != nil {
				t.Fatal(err)
			}
			err = v.verifyHelperBus(insns, cd.helperBus)
			if (err == nil) != (kind == "valid zero") {
				t.Fatalf("%s: %v", kind, err)
			}
		})
	}
}
