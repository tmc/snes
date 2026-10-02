package web

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/editor/statewrites"
)

func TestStateWritesRoutes(t *testing.T) {
	before := uint8(3)
	pc := uint32(0x0085fc)
	timeline := &statewrites.Timeline{Schema: "snes-state-writes-v1", From: 10, To: 12, WindowSHA256: strings.Repeat("a", 64), Writes: []statewrites.Write{
		{Frame: 10, PPUFrame: 9, Ordinal: 9007199254740993, Cycle: 9007199254740994, Actor: "cpu", WriterPC: &pc, RawAddress: 0x000010, Address: 0x7e0010, Before: &before, After: 4},
		{Frame: 10, PPUFrame: 9, Ordinal: 9007199254740995, Cycle: 9007199254740996, Actor: "cpu", WriterPC: &pc, RawAddress: 0x7e0010, Address: 0x7e0010, After: 5},
	}}
	h := StateWritesHandler(timeline)
	timeline.Writes[0].After = 99
	before = 99
	pc = 0
	for _, tt := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/statewrites", 200}, {"POST", "/statewrites", 405},
		{"GET", "/api/statewrites", 200},
		{"GET", "/api/statewrites?address=$00:0010&from=10&to=11", 200},
		{"GET", "/api/statewrites?address=7e0010&from=11&to=12", 200},
		{"POST", "/api/statewrites", 405},
		{"GET", "/api/statewrites?address=7e0010&from=9&to=11", 400},
		{"GET", "/api/statewrites?address=7e0010&from=10&to=13", 400},
		{"GET", "/api/statewrites?address=7e0010&from=10&to=10", 400},
		{"GET", "/api/statewrites?address=7e0010&from=10&to=11&from=10", 400},
		{"GET", "/api/statewrites?address=7e0010&from=10&to=11&path=/etc/passwd", 400},
		{"GET", "/api/statewrites?address=../../etc/passwd&from=10&to=11", 400},
	} {
		t.Run(tt.method+tt.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(tt.method, tt.path, nil))
			if w.Code != tt.status {
				t.Fatalf("status %d want %d: %s", w.Code, tt.status, w.Body.String())
			}
			if tt.path == "/api/statewrites?address=$00:0010&from=10&to=11" {
				var got struct {
					Count  int `json:"count"`
					Writes []struct {
						Ordinal  string  `json:"ordinal"`
						Before   *uint8  `json:"before"`
						After    uint8   `json:"after"`
						WriterPC *uint32 `json:"writer_pc"`
					} `json:"writes"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.Count != 2 || got.Writes[0].Ordinal != "9007199254740993" || got.Writes[0].After != 4 || *got.Writes[0].Before != 3 || *got.Writes[0].WriterPC != 0x0085fc || got.Writes[1].Before != nil {
					t.Fatalf("snapshot changed or repeats lost: %s", w.Body.String())
				}
			}
		})
	}
	w := httptest.NewRecorder()
	StateWritesHandler(nil).ServeHTTP(w, httptest.NewRequest("GET", "/api/statewrites", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestStateWritesResponseBound(t *testing.T) {
	writes := make([]statewrites.Write, 20001)
	for i := range writes {
		writes[i] = statewrites.Write{Frame: 0, Address: 0x7e0010, Ordinal: uint64(i)}
	}
	h := StateWritesHandler(&statewrites.Timeline{From: 0, To: 1, Writes: writes})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/statewrites?address=7e0010&from=0&to=1", nil))
	if w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestStateWritesPage(t *testing.T) {
	for _, want := range []string{"textContent", "unknown (not captured)", "To frame (excluded)", "Repeated writes remain separate", "No observed writes"} {
		if !strings.Contains(stateWritesPage, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(stateWritesPage, "innerHTML") {
		t.Fatal("unsafe HTML rendering")
	}
}

func ExampleStateWritesHandler() {
	h := StateWritesHandler(nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/statewrites", nil))
	fmt.Println(w.Code)
	// Output: 404
}

func TestParseStateAddress(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  uint32
		valid bool
	}{
		{"$7E:0010", 0x7e0010, true}, {"0x7e0010", 0x7e0010, true}, {"000000", 0, true},
		{"7:e0010", 0, false}, {"7E::0010", 0, false}, {"7e00100", 0, false}, {"ffffffg", 0, false}, {"", 0, false},
	} {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseStateAddress(tt.input)
			if (err == nil) != tt.valid || tt.valid && got != tt.want {
				t.Fatalf("got %#x, %v", got, err)
			}
		})
	}
}

func TestStateWritesMalformedQuery(t *testing.T) {
	h := StateWritesHandler(&statewrites.Timeline{From: 0, To: 1})
	r := httptest.NewRequest("GET", "/api/statewrites", nil)
	r.URL.RawQuery = "address=%zz"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
