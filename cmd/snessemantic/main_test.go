package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRefusesMissingPins(t *testing.T) {
	for _, tc := range []struct{ name, config, want string }{
		{"missing", `{}`, "missing ROM sha256"},
		{"wrong ROM", `{"rom":"ROM","rom_sha256":"bad"}`, "ROM sha256 differs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			rom := filepath.Join(dir, "rom")
			if err := os.WriteFile(rom, []byte{1}, 0600); err != nil {
				t.Fatal(err)
			}
			config := strings.Replace(tc.config, "ROM", rom, 1)
			p := filepath.Join(dir, "config.json")
			if err := os.WriteFile(p, []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			err := run(p, filepath.Join(dir, "out"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got%v want%s", err, tc.want)
			}
			if _, err = os.Stat(filepath.Join(dir, "out")); !os.IsNotExist(err) {
				t.Fatal("refused generation published output")
			}
		})
	}
}
