package libretro

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadGameRejectsEmptyROM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.sfc")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	p := &Bridge{retroLoadGame: func(*RetroGameInfo) bool {
		t.Fatal("empty ROM reached native core")
		return true
	}}
	if p.LoadGame(path) {
		t.Fatal("empty ROM accepted")
	}
}

func TestCloseLifecycle(t *testing.T) {
	for _, tt := range []struct {
		name                string
		initialized, loaded bool
		want                []string
	}{
		{"uninitialized", false, false, nil},
		{"initialized", true, false, []string{"deinit"}},
		{"loaded", true, true, []string{"unload", "deinit"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			p := &Bridge{initialized: tt.initialized, gameLoaded: tt.loaded, retroUnloadGame: func() { calls = append(calls, "unload") }, retroDeinit: func() { calls = append(calls, "deinit") }}
			for range 2 {
				if err := p.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(calls, tt.want) {
				t.Fatalf("calls=%v, want %v", calls, tt.want)
			}
		})
	}
}

func ExampleBridge_Close() {
	// A bridge with no library acquired has no resources to release.
	var bridge Bridge
	fmt.Println(bridge.Close())
	// Output: <nil>
}
