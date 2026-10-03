package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const reviewAuthenticBase = "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/synthetic-oam-capture/complete"

func reviewHTTP(t *testing.T, project string) (int, string) {
	t.Helper()
	s, e := NewServer(project)
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/api/provenance?frame=1&x=101&y=51", nil))
	return w.Code, w.Body.String()
}
func reviewFixture(t *testing.T, raw []byte, removeReceipt bool, reSeal bool) string {
	t.Helper()
	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "project"), 0700)
	os.MkdirAll(filepath.Join(d, "frames"), 0700)
	rec, e := os.ReadFile(filepath.Join(reviewAuthenticBase, "project/recovery.json"))
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(d, "project/recovery.json"), rec, 0600)
	tracePath := filepath.Join(d, "trace.jsonl")
	os.WriteFile(tracePath, raw, 0600)
	m, e := os.ReadFile(filepath.Join(reviewAuthenticBase, "frames/frames.jsonl"))
	if e != nil {
		t.Fatal(e)
	}
	i := bytes.IndexByte(m, '\n')
	var head map[string]any
	json.Unmarshal(m[:i], &head)
	head["trace"] = tracePath
	newHead, _ := json.Marshal(head)
	m = append(append(newHead, '\n'), m[i+1:]...)
	os.WriteFile(filepath.Join(d, "frames/frames.jsonl"), m, 0600)
	if !removeReceipt {
		r, e := os.ReadFile(filepath.Join(reviewAuthenticBase, "frames/frames.receipt.json"))
		if e != nil {
			t.Fatal(e)
		}
		var receipt map[string]any
		json.Unmarshal(r, &receipt)
		receipt["manifest_sha256"] = fmt.Sprintf("%x", sha256.Sum256(m))
		r, _ = json.Marshal(receipt)
		os.WriteFile(filepath.Join(d, "frames/frames.receipt.json"), r, 0600)
		r, e = os.ReadFile(filepath.Join(reviewAuthenticBase, "receipt.json"))
		if e != nil {
			t.Fatal(e)
		}
		if reSeal {
			var q map[string]any
			json.Unmarshal(r, &q)
			q["stream_sha256"] = fmt.Sprintf("%x", sha256.Sum256(raw))
			r, _ = json.Marshal(q)
		}
		os.WriteFile(filepath.Join(d, "receipt.json"), r, 0600)
	}
	return filepath.Join(d, "project")
}
func TestIndependentAuthenticHTTP(t *testing.T) {
	status, body := reviewHTTP(t, filepath.Join(reviewAuthenticBase, "project"))
	if status != 200 {
		t.Fatalf("positive=%d %s", status, body)
	}
	os.WriteFile("/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/api-recheck-cdf3/http-frame1.json", []byte(body), 0600)
	t.Logf("genuine production loader frame1=%d", status)
}
func TestIndependentLoaderIntegrityNegatives(t *testing.T) {
	base, e := os.ReadFile(filepath.Join(reviewAuthenticBase, "trace.jsonl"))
	if e != nil {
		t.Fatal(e)
	}
	tests := []struct {
		name                  string
		mutate                func([]byte) []byte
		removeReceipt, reSeal bool
	}{
		{"trace digest changed", func(b []byte) []byte { return append(append([]byte(nil), b...), '\n') }, false, false},
		{"trace run ROM mismatch", func(b []byte) []byte {
			i := bytes.IndexByte(b, '\n')
			var h map[string]any
			json.Unmarshal(b[:i], &h)
			h["run"].(map[string]any)["rom_sha256"] = strings.Repeat("0", 64)
			x, _ := json.Marshal(h)
			return append(append(x, '\n'), b[i+1:]...)
		}, false, true},
		{"missing both receipts", func(b []byte) []byte { return b }, true, false},
		{"malformed authenticated record", func(b []byte) []byte { return append(append([]byte(nil), b...), []byte("{BROKEN\n")...) }, false, true},
		{"declared gap", func(b []byte) []byte {
			return append(append([]byte(nil), b...), []byte("{\"id\":136731,\"schema\":2,\"kind\":\"gap\",\"frame\":0,\"cycle\":60000,\"gap\":{\"reason\":\"missing_bus_evidence\"}}\n")...)
		}, false, true},
		{"out of order OAM versions", func(b []byte) []byte {
			lines := bytes.Split(b, []byte("\n"))
			a, z := -1, -1
			for i, l := range lines {
				if bytes.Contains(l, []byte("\"kind\":\"ppu\"")) && bytes.Contains(l, []byte("\"space\":\"oam\"")) {
					var ev map[string]any
					json.Unmarshal(l, &ev)
					addr, _ := ev["addr"].(float64)
					if addr == 0 {
						if a < 0 {
							a = i
						}
						z = i
					}
				}
			}
			if a < 0 || z == a {
				t.Fatal("need two OAM0 versions")
			}
			lines[a], lines[z] = lines[z], lines[a]
			return bytes.Join(lines, []byte("\n"))
		}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project := reviewFixture(t, tt.mutate(base), tt.removeReceipt, tt.reSeal)
			status, body := reviewHTTP(t, project)
			if status == 200 {
				t.Errorf("unsupported/unbound evidence accepted HTTP200: %.700s", body)
			} else {
				t.Logf("negative rejected status%d", status)
			}
		})
	}
}
