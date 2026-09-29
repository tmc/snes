package watches

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadWatches reads and parses a watches.json file.
func LoadWatches(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("watches: read %s: %w", path, err)
	}

	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("watches: unmarshal %s: %w", path, err)
	}

	if f.Format != FileFormatWatches {
		return nil, fmt.Errorf("watches: unexpected format %q (expected %q)", f.Format, FileFormatWatches)
	}

	for _, w := range f.Watches {
		if err := w.Validate(); err != nil {
			return nil, fmt.Errorf("watches: definition %q: %w", w.ID, err)
		}
	}

	if err := CheckCycles(f.Watches); err != nil {
		return nil, err
	}

	return &f, nil
}

// SaveWatches writes a watches file as indented JSON.
func SaveWatches(path string, f *File) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("watches: marshal: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("watches: write %s: %w", path, err)
	}
	return nil
}

// LoadSnapshots loads snapshots from a file (single JSON or JSONL) or a directory of JSON files.
func LoadSnapshots(path string) ([]*Snapshot, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("watches: stat snapshots %s: %w", path, err)
	}

	if fi.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, fmt.Errorf("watches: read dir %s: %w", path, err)
		}
		var snapshots []*Snapshot
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			filePath := filepath.Join(path, e.Name())
			snap, err := loadSnapshotFile(filePath)
			if err != nil {
				return nil, err
			}
			snapshots = append(snapshots, snap)
		}
		return snapshots, nil
	}

	// Single file: could be array of snapshots or single snapshot
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("watches: read %s: %w", path, err)
	}

	// Try array of snapshots
	var snaps []*Snapshot
	if err := json.Unmarshal(data, &snaps); err == nil && len(snaps) > 0 && snaps[0].Format == FileFormatSnapshot {
		for _, s := range snaps {
			validateSnapshot(s)
		}
		return snaps, nil
	}

	// Try single snapshot
	var single Snapshot
	if err := json.Unmarshal(data, &single); err == nil && single.Format == FileFormatSnapshot {
		validateSnapshot(&single)
		return []*Snapshot{&single}, nil
	}

	// Try JSON lines
	lines := strings.Split(string(data), "\n")
	var lineSnaps []*Snapshot
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		var s Snapshot
		if err := json.Unmarshal([]byte(l), &s); err == nil && s.Format == FileFormatSnapshot {
			validateSnapshot(&s)
			lineSnaps = append(lineSnaps, &s)
		}
	}
	if len(lineSnaps) > 0 {
		return lineSnaps, nil
	}

	return nil, fmt.Errorf("watches: could not parse snapshots from %s", path)
}

func loadSnapshotFile(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("watches: read snapshot %s: %w", path, err)
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("watches: unmarshal snapshot %s: %w", path, err)
	}
	if s.Format != FileFormatSnapshot {
		return nil, fmt.Errorf("watches: unexpected snapshot format %q in %s", s.Format, path)
	}
	validateSnapshot(&s)
	return &s, nil
}

func validateSnapshot(s *Snapshot) {
	if s.ContentSHA256 == "" && len(s.Data) > 0 {
		h := sha256.Sum256(s.Data)
		s.ContentSHA256 = hex.EncodeToString(h[:])
	}
	if s.Length == 0 && len(s.Data) > 0 {
		s.Length = uint32(len(s.Data))
	}
}
