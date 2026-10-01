package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/tmc/snes/internal/extractor"
)

type record struct {
	StateSHA256 string `json:"state_sha256"`
	Previous    string `json:"previous"`
	State       State  `json:"state"`
}

func journalFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "journal"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			if e.IsDir() || e.Type()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("invalid journal file")
			}
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
func readJournal(dir string) (State, string, error) {
	var s State
	names, err := journalFiles(dir)
	if err != nil {
		return s, "", err
	}
	previous := ""
	for i, n := range names {
		if n != fmt.Sprintf("%06d.json", i+1) {
			return s, "", fmt.Errorf("journal sequence gap")
		}
		b, err := os.ReadFile(filepath.Join(dir, "journal", n))
		if err != nil {
			return s, "", err
		}
		var r record
		if err = strict(b, &r); err != nil {
			return s, "", err
		}
		encoded, err := json.Marshal(r.State)
		if err != nil {
			return s, "", err
		}
		if digest(encoded) != r.StateSHA256 {
			return s, "", fmt.Errorf("journal state digest mismatch")
		}
		if r.Previous != previous || r.State.Sequence != i+1 {
			return s, "", fmt.Errorf("journal chain mismatch")
		}
		if i > 0 && (r.State.Config != s.Config || r.State.CandidateID != s.CandidateID || r.State.Entry != s.Entry) {
			return s, "", fmt.Errorf("journal task identity changed")
		}
		s = r.State
		previous = digest(b)
	}
	if len(names) == 0 {
		return s, "", fmt.Errorf("journal has no committed state")
	}
	return s, previous, nil
}
func appendState(dir string, s *State) error {
	prev := ""
	if s.Sequence > 0 {
		old, h, err := readJournal(dir)
		if err != nil {
			return err
		}
		if old.Sequence != s.Sequence {
			return fmt.Errorf("stale journal generation")
		}
		prev = h
	}
	next := *s
	next.Sequence++
	b, err := json.MarshalIndent(record{Previous: prev, State: next, StateSHA256: stateDigest(next)}, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	journal := filepath.Join(dir, "journal")
	f, err := os.CreateTemp(journal, ".generation-")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(b); err != nil {
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
	if err = os.Link(temp, filepath.Join(journal, fmt.Sprintf("%06d.json", next.Sequence))); err != nil {
		return err
	}
	if err = syncDir(journal); err != nil {
		return err
	}
	*s = next
	return nil
}
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func openJournal(dir string, config Input, cand extractor.Candidate) (State, func(), error) {
	var zero State
	created := false
	if err := os.Mkdir(dir, 0700); err == nil {
		created = true
	} else if !os.IsExist(err) {
		return zero, nil, err
	}
	if !created {
		if _, err := os.Stat(filepath.Join(dir, "journal", "000001.json")); err != nil {
			return zero, nil, fmt.Errorf("existing directory is not a committed workflow")
		}
	}
	lock, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return zero, nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return zero, nil, fmt.Errorf("workflow already in use: %w", err)
	}
	unlock := func() { syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); lock.Close() }
	if created {
		if err = os.Mkdir(filepath.Join(dir, "journal"), 0700); err != nil {
			unlock()
			return zero, nil, err
		}
		runtimeSHA, err := runtimeIdentity()
		if err != nil {
			unlock()
			return zero, nil, err
		}
		s := State{RuntimeSHA256: runtimeSHA, Phase: "selected", Config: config, CandidateID: cand.ID, Entry: cand.Entry, Artifacts: make(map[string]string)}
		if err = appendState(dir, &s); err != nil {
			unlock()
			return zero, nil, err
		}
		return s, unlock, nil
	}
	s, _, err := readJournal(dir)
	if err != nil {
		unlock()
		return zero, nil, err
	}
	return s, unlock, nil
}

// runStep preserves an uncommitted publication as an orphan, then retries the
// step. It never adopts an output that lacks a committed journal generation.
func runStep(dir, name string, fn func(string) error) error {
	out := filepath.Join(dir, name)
	if _, err := os.Lstat(out); err == nil {
		for n := 1; ; n++ {
			orphan := filepath.Join(dir, fmt.Sprintf("orphan-%s-%d", name, n))
			if _, err := os.Lstat(orphan); os.IsNotExist(err) {
				if err = os.Rename(out, orphan); err != nil {
					return err
				}
				break
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	stage, err := os.MkdirTemp(dir, ".stage-"+name+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err = fn(stage); err != nil {
		return err
	}
	err = filepath.WalkDir(stage, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink output refused")
		}
		if d.IsDir() {
			return syncDir(p)
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		return f.Sync()
	})
	if err != nil {
		return err
	}
	if err = os.Rename(stage, out); err != nil {
		return err
	}
	return syncDir(dir)
}

func runtimeIdentity() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return digest(b), nil
}
func stateDigest(s State) string { b, _ := json.Marshal(s); return digest(b) }
