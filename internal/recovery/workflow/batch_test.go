package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func batchSetup(t *testing.T) (Input, string) {
	t.Helper()
	o, c := setup(t)
	root := filepath.Dir(o.Config.Path)
	var tasks []BatchTask
	for i := 0; i < 10; i++ {
		// Distinct actual addresses are required even if IDs differ.
		cand := []byte(fmtCandidate(i))
		c.Candidate = testInput(t, root, fmtName("candidate", i), cand)
		c.CandidateID = ""
		b, _ := json.Marshal(c)
		tasks = append(tasks, BatchTask{Config: testInput(t, root, fmtName("config", i), b)})
	}
	b, _ := json.Marshal(BatchConfig{Schema: "snes-recovery-batch-config-v1", Sources: []Input{c.ROM}, Tasks: tasks})
	return testInput(t, root, "batch.json", b), filepath.Join(root, "batch")
}
func fmtCandidate(i int) string {
	return fmt.Sprintf(`{"id":"leaf-%06x","entry":%d,"instruction_count":1,"byte_span":1,"returns":[%d]}`, 32768+i, 32768+i, 32768+i)
}
func fmtName(prefix string, i int) string { return fmt.Sprintf("%s-%02d.json", prefix, i) }

func TestBatchWaitingAndIdempotence(t *testing.T) {
	in, dir := batchSetup(t)
	r, err := RunBatch(context.Background(), dir, in)
	if err != nil {
		t.Fatal(err)
	}
	if r.Accepted != 0 || r.Refused != 0 || r.Unexecuted != 10 {
		t.Fatalf("accounting %+v", r)
	}
	before, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := ResumeBatch(context.Background(), dir, in, digest(before))
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Rows) != 10 {
		t.Fatal("lost rows")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if string(before) != string(after) {
		t.Fatal("rerun mutated manifest")
	}
	if err := os.WriteFile(filepath.Join(dir, r.Rows[0].Directory, "capture-request.json"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResumeBatch(context.Background(), dir, in, digest(before)); err == nil {
		t.Fatal("accepted changed artifact")
	}
}
func TestBatchRejectDuplicateEntry(t *testing.T) {
	in, dir := batchSetup(t)
	b, _ := os.ReadFile(in.Path)
	var c BatchConfig
	json.Unmarshal(b, &c)
	c.Tasks[1] = c.Tasks[0]
	b, _ = json.Marshal(c)
	in = testInput(t, filepath.Dir(in.Path), "duplicate.json", b)
	if _, err := RunBatch(context.Background(), dir, in); err == nil {
		t.Fatal("accepted renamed duplicate")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("published failed batch")
	}
}
func TestBatchRejectChangedSourceAndExistingDirectory(t *testing.T) {
	for _, name := range []string{"source", "existing", "cancel"} {
		t.Run(name, func(t *testing.T) {
			in, dir := batchSetup(t)
			ctx := context.Background()
			switch name {
			case "source":
				b, _ := os.ReadFile(in.Path)
				var c BatchConfig
				json.Unmarshal(b, &c)
				os.WriteFile(c.Sources[0].Path, []byte("changed"), 0600)
			case "existing":
				os.Mkdir(dir, 0700)
				os.WriteFile(filepath.Join(dir, "notes"), []byte("mine"), 0600)
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := RunBatch(ctx, dir, in); err == nil {
				t.Fatal("accepted invalid batch")
			}
		})
	}
}

func ExampleBatchConfig() {
	c := BatchConfig{Schema: "snes-recovery-batch-config-v1"}
	fmt.Println(c.Schema)
	// Output: snes-recovery-batch-config-v1
}
