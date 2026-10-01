package extractor

import (
	"encoding/json"
	"testing"
)

func TestHelperBusSchemaPayload(t *testing.T) {
	first := CPUState{PC: 0x9000, P: 0x30, Cycles: 1}
	middle := first
	middle.PC = 0x9002
	middle.Cycles = 10
	last := middle
	last.PC = 0x9004
	last.Cycles = 20
	insns := []*RawInsn{{Entry: first, Exit: middle, Fetches: []FetchRec{{Addr: 0x9000, Value: 0x84}, {Addr: 0x9001, Value: 3}}}, {Entry: middle, Exit: last, Fetches: []FetchRec{{Addr: 0x9002, Value: 0xa4}, {Addr: 0x9003, Value: 3}}}}
	for _, kind := range []string{"valid zero", "missing write value", "null read", "null write", "conflicting write", "missing width", "zero width", "wide width", "unknown schema"} {
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
			}
			var events BusEventList
			for _, m := range []map[string]any{writer, reader} {
				raw, _ := json.Marshal(m)
				var e RawEvent
				if err := json.Unmarshal(raw, &e); err != nil {
					t.Fatal(err)
				}
				e.Raw = raw
				events = append(events, &e)
			}
			err := verifyHelperBus(insns, events, make([]byte, 32768))
			if (err == nil) != (kind == "valid zero") {
				t.Fatalf("%s: %v", kind, err)
			}
		})
	}
}
