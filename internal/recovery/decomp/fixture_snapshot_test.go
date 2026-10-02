package decomp

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func snapshotFixture(t *testing.T) (string, CorpusTrustRoot, ReplayCase) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.jsonl.gz")
	var decoded bytes.Buffer
	enc := json.NewEncoder(&decoded)
	input := "input"
	if err := enc.Encode(map[string]any{"run": captureRunHeader{ROMSHA256: "rom", Start: "power_on", ReplayInputSHA256: &input}}); err != nil {
		t.Fatal(err)
	}
	for _, seq := range []uint64{10, 11, 12, 13} {
		in := captureCPUInsn{Seq: seq, Length: 1, Fetches: []captureFetch{{Addr: 0x808000, Value: 0xea}}}
		if err := enc.Encode(map[string]any{"kind": "cpu_insn", "insn": in}); err != nil {
			t.Fatal(err)
		}
	}
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	if _, err := gz.Write(decoded.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	hash := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	root := CorpusTrustRoot{Label: "snapshot", ROMSHA256: "rom", FixtureSHA256: hash(raw.Bytes()), DecompressedSHA: hash(decoded.Bytes())}
	ref := &EvidenceFileRef{Path: path, SHA256: root.FixtureSHA256, DecompressedSHA256: root.DecompressedSHA}
	c := ReplayCase{SchemaVersion: "snes-routine-case-v1", EntrySeq: 11, ExitSeq: 12, CallSeq: 10, ReturnSeq: 13, RunID: root.FixtureSHA256, StreamSHA256: root.FixtureSHA256, Evidence: &CaseEvidence{Corpus: "test-snapshot", Fixture: ref, Capture: ref, History: ref}}
	ref.Receipt = &EvidenceFileRef{Path: path, SHA256: root.FixtureSHA256}
	ref.Summary = ref.Receipt
	testCorpora[c.Evidence.Corpus] = root
	t.Cleanup(func() { delete(testCorpora, c.Evidence.Corpus) })
	return path, root, c
}

func TestVerifiedSnapshotIsolation(t *testing.T) {
	path, root, c := snapshotFixture(t)
	v := newTestEvidenceVerifier("")
	if err := v.PrefetchFixtures([]ReplayCase{c}); err != nil {
		t.Fatal(err)
	}
	fd, ok := v.verifiedFixtureFor(path, root, seqSet(11))
	if !ok {
		t.Fatal("missing snapshot")
	}
	*fd.header.ReplayInputSHA256 = "substituted input"
	in := fd.recs[11]
	in.Exit.A = 99
	in.Fetches[0].Value = 0
	fd.recs[11] = in
	delete(fd.recs, 11)
	again, ok := v.verifiedFixtureFor(path, root, seqSet(11))
	if !ok {
		t.Fatal("snapshot lost")
	}
	if *again.header.ReplayInputSHA256 != "input" || again.recs[11].Exit.A != 0 || again.recs[11].Fetches[0].Value != 0xea {
		t.Fatal("caller mutated immutable snapshot")
	}
	if len(again.recs) != 1 {
		t.Fatalf("retained %d records, want requested subset", len(again.recs))
	}
}

func TestVerifiedSnapshotCompleteRootIdentity(t *testing.T) {
	path, root, c := snapshotFixture(t)
	v := newTestEvidenceVerifier("")
	if err := v.PrefetchFixtures([]ReplayCase{c}); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"label", "engine", "decoded", "capture"} {
		t.Run(field, func(t *testing.T) {
			changed := root
			switch field {
			case "label":
				changed.Label += "changed"
			case "engine":
				changed.EngineRevision = "other"
			case "decoded":
				changed.DecompressedSHA = root.FixtureSHA256
			case "capture":
				changed.CaptureSHA256 = "other"
			}
			if _, ok := v.verifiedFixtureFor(path, changed, seqSet(11)); ok {
				t.Fatal("reused snapshot under changed trust root")
			}
		})
	}
	if _, ok := v.verifiedFixtureFor(path, root, seqSet(14)); ok {
		t.Fatal("unrequested sequence treated as covered")
	}
}

func TestVerifiedSnapshotFreshRawAndEmbeddedPins(t *testing.T) {
	for _, mode := range []string{"source_changed", "source_removed", "embedded_changed", "root_decoded_changed"} {
		t.Run(mode, func(t *testing.T) {
			path, root, c := snapshotFixture(t)
			v := newTestEvidenceVerifier("")
			if err := v.PrefetchFixtures([]ReplayCase{c}); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "source_changed":
				if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "source_removed":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "embedded_changed":
				c.Evidence.Fixture.DecompressedSHA256 = root.FixtureSHA256
			case "root_decoded_changed":
				root.DecompressedSHA = root.FixtureSHA256
			}
			_, err := v.checkFiles(&c, root)
			if err == nil {
				t.Fatal("changed source or pin accepted")
			}
			if mode == "embedded_changed" && !strings.Contains(err.Error(), "embedded decompressed") {
				t.Fatalf("wrong rejection: %v", err)
			}
			if mode == "root_decoded_changed" && !strings.Contains(err.Error(), "decompressed") {
				t.Fatalf("wrong rejection: %v", err)
			}
			if len(v.admittedCases) != 0 || c.DecompressedStreamSHA256 != "" {
				t.Fatal("failed check published case admission")
			}
		})
	}
}

func TestVerifiedSnapshotFallbackAndMissingSeq(t *testing.T) {
	path, root, c := snapshotFixture(t)
	c.ExitSeq = 11
	c.ReturnSeq = 12
	v := newTestEvidenceVerifier("")
	if err := v.PrefetchFixtures([]ReplayCase{c}); err != nil {
		t.Fatal(err)
	}
	// Seq13 exists but was not prefetched: admission must read and verify it.
	if _, ok := v.verifiedFixtureFor(path, root, seqSet(13)); ok {
		t.Fatal("unprefetched record covered")
	}
	fd, err := v.admissionFixture(path, root, seqSet(13))
	if err != nil {
		t.Fatal(err)
	}
	if fd.recs[13].Seq != 13 {
		t.Fatal("fallback failed to load actual record")
	}
	if _, err := v.admissionFixture(path, root, seqSet(14)); err == nil || !strings.Contains(err.Error(), "lacks requested") {
		t.Fatalf("missing record: %v", err)
	}
	changed := root
	changed.DecompressedSHA = root.FixtureSHA256
	if _, err := v.admissionFixture(path, changed, seqSet(13)); err == nil {
		t.Fatal("fallback accepted wrong decoded pin")
	}
	// A requested-but-absent sequence is recorded as absent at EOF, never fabricated.
	c.ExitSeq = 13
	c.ReturnSeq = 14
	if err := v.PrefetchFixtures([]ReplayCase{c}); err != nil {
		t.Fatal(err)
	}
	if _, err := v.admissionFixture(path, root, seqSet(14)); err == nil || !strings.Contains(err.Error(), "lacks requested") {
		t.Fatalf("cached absent record: %v", err)
	}
}
