package decomp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tmc/snes/internal/bus"
	"github.com/tmc/snes/internal/cartridge"
	"github.com/tmc/snes/internal/cpu"
)

// RoutineBlock defines a single basic block in a routine CFG using explicit half-open ranges [Start, End).
type RoutineBlock struct {
	ID                  string   `json:"id"`
	Start               uint32   `json:"start"`
	StartHex            string   `json:"start_hex"`
	End                 uint32   `json:"end"` // half-open: [start, end)
	EndHex              string   `json:"end_hex"`
	Length              int      `json:"length"`
	TerminalInstruction string   `json:"terminal_instruction"`
	TerminalType        string   `json:"terminal_type"` // "fallthrough" | "branch" | "return"
	Successors          []string `json:"successors"`
}

// RoutineManifest defines the durable metadata binding routine decompilation C code,
// ROM provenance, basic block decomposition, compiler configuration, and qualification scope.
type RoutineManifest struct {
	SchemaVersion string `json:"schema_version"`
	RoutineID     string `json:"routine_id"`
	ROM           struct {
		SHA256 string `json:"sha256"`
		Title  string `json:"title"`
		Region string `json:"region"`
	} `json:"rom"`
	EntryPC      uint32 `json:"entry_pc"`
	EntryPCHex   string `json:"entry_pc_hex"`
	CallerPC     uint32 `json:"caller_pc"`
	CallerPCHex  string `json:"caller_pc_hex"`
	ReturnInsnPC uint32 `json:"return_insn_pc"`
	ReturnPC     uint32 `json:"return_pc"`
	ReturnPCHex  string `json:"return_pc_hex"`
	ByteSpan     struct {
		Start    uint32 `json:"start"`
		StartHex string `json:"start_hex"`
		End      uint32 `json:"end"`
		EndHex   string `json:"end_hex"`
		Length   int    `json:"length"`
	} `json:"byte_span"`
	BasicBlocks   []RoutineBlock `json:"basic_blocks"`
	EntryContract struct {
		Mode                 string `json:"mode"`
		E                    bool   `json:"e"`
		M                    bool   `json:"m"`
		X                    bool   `json:"x"`
		CAllowed             []bool `json:"c_allowed"`
		IAllowed             []bool `json:"i_allowed"`
		DAllowed             []bool `json:"d_allowed"`
		StackPushDescription string `json:"stack_push_description"`
	} `json:"entry_contract"`
	ExitContract struct {
		PExact     uint8  `json:"p_exact"`
		PExactHex  string `json:"p_exact_hex"`
		E          bool   `json:"e"`
		M          bool   `json:"m"`
		X          bool   `json:"x"`
		SDelta     int    `json:"s_delta"`
		PCExact    uint32 `json:"pc_exact"`
		PCExactHex string `json:"pc_exact_hex"`
	} `json:"exit_contract"`
	CSource struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"c_source"`
	AutomatedCSource struct {
		Path       string `json:"path"`
		SHA256     string `json:"sha256"`
		Provenance string `json:"provenance,omitempty"`
		Generator  string `json:"generator,omitempty"`
		Status     string `json:"status,omitempty"`
	} `json:"automated_c_source,omitempty"`
	Compiler struct {
		Identity string `json:"identity"`
		Flags    string `json:"flags"`
	} `json:"compiler"`
	QualificationScope RoutineQualificationScope `json:"qualification_scope"`
}

// RoutineQualificationScope documents the verified inventory, observed execution paths,
// and explicitly unqualified paths for a decompiled routine.
type RoutineQualificationScope struct {
	Inventory struct {
		Corpora                []string       `json:"corpora"`
		CasesByCorpus          map[string]int `json:"cases_by_corpus,omitempty"`
		TotalInventoryCases    int            `json:"total_inventory_cases"`
		UnsupportedOccurrences []string       `json:"unsupported_occurrences"`
	} `json:"inventory"`
	ObservedPaths    []RoutinePath     `json:"observed_paths"`
	UnqualifiedPaths []UnqualifiedPath `json:"unqualified_paths"`
}

// RoutinePath documents an observed execution path bound to an exact edge/instruction sequence.
type RoutinePath struct {
	ID               string   `json:"id"`
	InstructionCount int      `json:"instruction_count"`
	InventoryCount   int      `json:"inventory_count"`
	EdgeSequence     []string `json:"edge_sequence"`
	Status           string   `json:"status"`
}

// UnqualifiedPath documents a branch outcome or path that was never observed in trace corpora.
type UnqualifiedPath struct {
	ID        string `json:"id"`
	Condition string `json:"condition"`
	Status    string `json:"status"`
}

// LoadRoutineManifest loads and decodes a routine manifest JSON file from path.
func LoadRoutineManifest(path string) (*RoutineManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read routine manifest: %w", err)
	}
	var m RoutineManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal routine manifest: %w", err)
	}
	return &m, nil
}

// CompiledRoutineRunner manages a native compiled batch runner executable for a whole routine.
type CompiledRoutineRunner struct {
	mu             sync.Mutex
	RoutineID      string
	EntryPC        uint32
	GeneratedCHash string
	Compiler       string
	CompilerFlags  string
	RunDir         string
	BinPath        string
	Closed         bool

	sourceCode       string
	wrapperHash      string
	binaryHash       string
	observedCompiler string
	observedFlags    string
	boundRegion      *RegionIR
	boundRevision    string

	romBytes  []byte
	romSHA256 string
	romPath   string
}

// ROMSHA256 returns the SHA-256 digest of the bound ROM, or "" if unbound.
func (r *CompiledRoutineRunner) ROMSHA256() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.romSHA256
}

// ROMPath returns the filesystem path to the bound verified ROM, or "" if unbound.
func (r *CompiledRoutineRunner) ROMPath() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.romPath
}

// ROMBytes returns an owned copy of the bound verified ROM bytes, or nil if unbound.
func (r *CompiledRoutineRunner) ROMBytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.romBytes == nil {
		return nil
	}
	cp := make([]byte, len(r.romBytes))
	copy(cp, r.romBytes)
	return cp
}

// snapshotROM returns a private snapshot of the bound ROM fields under lock.
func (r *CompiledRoutineRunner) snapshotROM() ([]byte, string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.romBytes, r.romSHA256, r.romPath
}

// SetROM binds verified ROM bytes to the runner. It validates size bounds (0 < len <= 16MB)
// before any file I/O, retains an owned copy to isolate against caller-slice mutation,
// writes to a private temporary file with 0600 permissions, atomically renames to the
// target verified ROM path, and atomically publishes the binding only upon complete success.
func (r *CompiledRoutineRunner) SetROM(rom []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Closed {
		return errors.New("runner is closed")
	}
	if len(rom) == 0 {
		return errors.New("empty ROM bytes")
	}
	if len(rom) > 16*1024*1024 {
		return fmt.Errorf("ROM size %d exceeds 16MB limit", len(rom))
	}

	owned := make([]byte, len(rom))
	copy(owned, rom)

	h := sha256.Sum256(owned)
	sha := fmt.Sprintf("%x", h)

	tmpPath := filepath.Join(r.RunDir, fmt.Sprintf("rom-%s.tmp", sha[:8]))
	if err := os.WriteFile(tmpPath, owned, 0600); err != nil {
		return fmt.Errorf("write verified rom to runner dir: %w", err)
	}

	targetPath := filepath.Join(r.RunDir, "verified_rom.bin")
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publish verified rom: %w", err)
	}

	r.romBytes = owned
	r.romSHA256 = sha
	r.romPath = targetPath
	return nil
}

// NewCompiledRegionRunner compiles any routine or region C source into a persistent native batch runner calling entryFnName.
func NewCompiledRegionRunner(ctx context.Context, cSourcePath string, entryFnName string) (*CompiledRoutineRunner, error) {
	return NewCompiledRegionRunnerWithROM(ctx, cSourcePath, entryFnName, nil)
}

// NewCompiledRegionRunnerWithROM compiles any routine or region C source into a persistent native batch runner
// calling entryFnName and binds verified ROM bytes if supplied.
func NewCompiledRegionRunnerWithROM(ctx context.Context, cSourcePath string, entryFnName string, rom []byte) (*CompiledRoutineRunner, error) {
	if entryFnName == "" {
		entryFnName = "execute_region"
	}
	cBytes, err := os.ReadFile(cSourcePath)
	if err != nil {
		return nil, fmt.Errorf("read routine c source: %w", err)
	}
	cCode := string(cBytes)
	cHash := fmt.Sprintf("%x", sha256.Sum256(cBytes))

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("user home dir: %w", err)
	}
	baseTmp := filepath.Join(home, "tmp", "snes", time.Now().UTC().Format("20060102")+"-compiled-runners")
	if err := os.MkdirAll(baseTmp, 0755); err != nil {
		return nil, fmt.Errorf("create base tmp: %w", err)
	}

	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("rand nonce: %w", err)
	}
	runDir := filepath.Join(baseTmp, fmt.Sprintf("snes-region-runner-%s", hex.EncodeToString(nonce[:])))
	if err := os.MkdirAll(runDir, 0700); err != nil {
		return nil, fmt.Errorf("create runner dir: %w", err)
	}

	srcPath := filepath.Join(runDir, "region_runner.c")
	binPath := filepath.Join(runDir, "region_runner")

	runnerSrc := GenerateRegionMultiCaseRunnerC(cCode, entryFnName)
	if err := os.WriteFile(srcPath, []byte(runnerSrc), 0644); err != nil {
		os.RemoveAll(runDir)
		return nil, fmt.Errorf("write runner src: %w", err)
	}

	compileCtx, cancelCompile := context.WithTimeout(ctx, 10*time.Second)
	defer cancelCompile()

	compilerPath, err := exec.LookPath("cc")
	if err != nil {
		os.RemoveAll(runDir)
		return nil, fmt.Errorf("resolve routine compiler: %w", err)
	}
	versionOutput, err := exec.CommandContext(compileCtx, compilerPath, "--version").Output()
	if err != nil {
		os.RemoveAll(runDir)
		return nil, fmt.Errorf("observe routine compiler: %w", err)
	}
	compilerIdentity := strings.TrimSpace(strings.SplitN(string(versionOutput), "\n", 2)[0])
	if compilerIdentity == "" {
		os.RemoveAll(runDir)
		return nil, errors.New("routine compiler reported empty identity")
	}
	compilerFlags := "cc -O0 -Wall -Werror -Wno-unused-function -Wno-unused-label"
	cmdCompile := exec.CommandContext(compileCtx, compilerPath, "-O0", "-Wall", "-Werror", "-Wno-unused-function", "-Wno-unused-label", srcPath, "-o", binPath)
	if out, err := cmdCompile.CombinedOutput(); err != nil {
		os.RemoveAll(runDir)
		return nil, fmt.Errorf("compile routine runner: %w (output: %s)", err, string(out))
	}

	binaryBytes, err := os.ReadFile(binPath)
	if err != nil {
		os.RemoveAll(runDir)
		return nil, fmt.Errorf("read compiled runner: %w", err)
	}
	runner := &CompiledRoutineRunner{
		sourceCode:       cCode,
		wrapperHash:      ComputeCHash(runnerSrc),
		binaryHash:       fmt.Sprintf("%x", sha256.Sum256(binaryBytes)),
		observedCompiler: compilerIdentity,
		observedFlags:    compilerFlags,
		RoutineID:        entryFnName,
		EntryPC:          0,
		GeneratedCHash:   cHash,
		Compiler:         compilerIdentity,
		CompilerFlags:    compilerFlags,
		RunDir:           runDir,
		BinPath:          binPath,
	}

	if len(rom) > 0 {
		if err := runner.SetROM(rom); err != nil {
			runner.Close()
			return nil, fmt.Errorf("bind verified ROM: %w", err)
		}
	}

	return runner, nil
}

// GenerateRegionMultiCaseRunnerC wraps any region C code with a batch runner calling entryFnName.
func GenerateRegionMultiCaseRunnerC(routineC string, entryFnName string) string {
	if entryFnName == "" {
		entryFnName = "execute_region"
	}
	return fmt.Sprintf(`/* SNES Routine Compiled Multi-Case Batch Runner */
#include <stdio.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <stdlib.h>
#include <unistd.h>
#include <ctype.h>

#define MAX_CELLS 512

%s

typedef struct {
    uint32_t addr;
    uint8_t val;
} runner_cell_t;

typedef struct {
    runner_cell_t cells[MAX_CELLS];
    int count;
} runner_mem_t;

static uint8_t *runner_rom_bytes = NULL;
static size_t runner_rom_size = 0;

#define ROTR(x, n) (((x) >> (n)) | ((x) << (32 - (n))))
#define CH(x, y, z) (((x) & (y)) ^ (~(x) & (z)))
#define MAJ(x, y, z) (((x) & (y)) ^ ((x) & (z)) ^ ((y) & (z)))
#define EP0(x) (ROTR(x, 2) ^ ROTR(x, 13) ^ ROTR(x, 22))
#define EP1(x) (ROTR(x, 6) ^ ROTR(x, 11) ^ ROTR(x, 25))
#define SIG0(x) (ROTR(x, 7) ^ ROTR(x, 18) ^ ((x) >> 3))
#define SIG1(x) (ROTR(x, 17) ^ ROTR(x, 19) ^ ((x) >> 10))

static const uint32_t K[64] = {
    0x428a2f98,0x71374491,0xb5c0fbcf,0xe9b5dba5,0x3956c25b,0x59f111f1,0x923f82a4,0xab1c5ed5,
    0xd807aa98,0x12835b01,0x243185be,0x550c7dc3,0x72be5d74,0x80deb1fe,0x9bdc06a7,0xc19bf174,
    0xe49b69c1,0xefbe4786,0x0fc19dc6,0x240ca1cc,0x2de92c6f,0x4a7484aa,0x5cb0a9dc,0x76f988da,
    0x983e5152,0xa831c66d,0xb00327c8,0xbf597fc7,0xc6e00bf3,0xd5a79147,0x06ca6351,0x14292967,
    0x27b70a85,0x2e1b2138,0x4d2c6dfc,0x53380d13,0x650a7354,0x766a0abb,0x81c2c92e,0x92722c85,
    0xa2bfe8a1,0xa81a664b,0xc24b8b70,0xc76c51a3,0xd192e819,0xd6990624,0xf40e3585,0x106aa070,
    0x19a4c116,0x1e376c08,0x2748774c,0x34b0bcb5,0x391c0cb3,0x4ed8aa4a,0x5b9cca4f,0x682e6ff3,
    0x748f82ee,0x78a5636f,0x84c87814,0x8cc70208,0x90befffa,0xa4506ceb,0xbef9a3f7,0xc67178f2
};

static int runner_sha256(const uint8_t *data, size_t len, char out_hex[65]) {
    out_hex[0] = '\0';
    uint32_t h[8] = {
        0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
        0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19
    };
    uint64_t bitlen = (uint64_t)len * 8;
    size_t new_len = (len + 8 + 64) / 64 * 64;
    if (new_len - len < 9) new_len += 64;
    uint8_t *msg = (uint8_t *)calloc(new_len, 1);
    if (!msg) return -1;
    memcpy(msg, data, len);
    msg[len] = 0x80;
    for (int i = 0; i < 8; i++) {
        msg[new_len - 1 - i] = (uint8_t)((bitlen >> (i * 8)) & 0xFF);
    }
    for (size_t chunk = 0; chunk < new_len; chunk += 64) {
        uint32_t w[64];
        for (int i = 0; i < 16; i++) {
            w[i] = ((uint32_t)msg[chunk + i * 4] << 24) |
                   ((uint32_t)msg[chunk + i * 4 + 1] << 16) |
                   ((uint32_t)msg[chunk + i * 4 + 2] << 8) |
                   ((uint32_t)msg[chunk + i * 4 + 3]);
        }
        for (int i = 16; i < 64; i++) {
            w[i] = SIG1(w[i - 2]) + w[i - 7] + SIG0(w[i - 15]) + w[i - 16];
        }
        uint32_t a = h[0], b = h[1], c = h[2], d = h[3];
        uint32_t e = h[4], f = h[5], g = h[6], h_val = h[7];
        for (int i = 0; i < 64; i++) {
            uint32_t t1 = h_val + EP1(e) + CH(e, f, g) + K[i] + w[i];
            uint32_t t2 = EP0(a) + MAJ(a, b, c);
            h_val = g; g = f; f = e; e = d + t1;
            d = c; c = b; b = a; a = t1 + t2;
        }
        h[0] += a; h[1] += b; h[2] += c; h[3] += d;
        h[4] += e; h[5] += f; h[6] += g; h[7] += h_val;
    }
    free(msg);
    for (int i = 0; i < 8; i++) {
        sprintf(out_hex + i * 8, "%%08x", h[i]);
    }
    out_hex[64] = '\0';
    return 0;
}

static int load_explicit_rom(const char *path, const char *expected_sha256) {
    if (!path || path[0] == '\0') {
        return 0;
    }
    if (!expected_sha256 || strlen(expected_sha256) != 64) {
        fprintf(stderr, "runner: expected sha256 must be exactly 64 hex characters\n");
        return 17;
    }
    for (int i = 0; i < 64; i++) {
        char c = expected_sha256[i];
        if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'))) {
            fprintf(stderr, "runner: expected sha256 contains invalid character '%%c'\n", c);
            return 17;
        }
    }
    FILE *f = fopen(path, "rb");
    if (!f) {
        fprintf(stderr, "runner: cannot open rom file '%%s'\n", path);
        return 10;
    }
    if (fseek(f, 0, SEEK_END) != 0) {
        fprintf(stderr, "runner: fseek error on rom file\n");
        fclose(f);
        return 11;
    }
    long sz = ftell(f);
    if (sz <= 0 || sz > 16 * 1024 * 1024) {
        fprintf(stderr, "runner: invalid rom size %%ld\n", sz);
        fclose(f);
        return 12;
    }
    if (fseek(f, 0, SEEK_SET) != 0) {
        fprintf(stderr, "runner: fseek reset error on rom file\n");
        fclose(f);
        return 13;
    }
    runner_rom_bytes = (uint8_t *)malloc((size_t)sz);
    if (!runner_rom_bytes) {
        fprintf(stderr, "runner: malloc failed for %%ld bytes\n", sz);
        fclose(f);
        return 14;
    }
    size_t n = fread(runner_rom_bytes, 1, (size_t)sz, f);
    fclose(f);
    if (n != (size_t)sz) {
        fprintf(stderr, "runner: truncated read (got %%zu, want %%ld)\n", n, sz);
        free(runner_rom_bytes);
        runner_rom_bytes = NULL;
        return 15;
    }
    runner_rom_size = (size_t)sz;
    char computed_hex[65];
    if (runner_sha256(runner_rom_bytes, runner_rom_size, computed_hex) != 0) {
        fprintf(stderr, "runner: sha256 calculation failed\n");
        free(runner_rom_bytes);
        runner_rom_bytes = NULL;
        return 18;
    }
    if (strcasecmp(computed_hex, expected_sha256) != 0) {
        fprintf(stderr, "runner: rom sha256 mismatch: got %%s, want %%s\n", computed_hex, expected_sha256);
        free(runner_rom_bytes);
        runner_rom_bytes = NULL;
        return 16;
    }
    return 0;
}

static uint8_t test_read_cb(void *user_data, uint32_t addr, bool *missing) {
    runner_mem_t *m = (runner_mem_t *)user_data;
    uint32_t c_addr = bus_canonical_addr(addr);
    for (int i = 0; i < m->count; i++) {
        if (m->cells[i].addr == c_addr) {
            if (missing) *missing = false;
            return m->cells[i].val;
        }
    }
    if (runner_rom_bytes && runner_rom_size >= 0x8000) {
        uint8_t bank = (uint8_t)((c_addr >> 16) & 0xFF);
        uint16_t offset = (uint16_t)(c_addr & 0xFFFF);
        if ((bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && offset >= 0x8000) {
            uint32_t rom_off = ((uint32_t)(bank & 0x7F) * 0x8000) + (offset - 0x8000);
            if (rom_off < runner_rom_size) {
                if (missing) *missing = false;
                return runner_rom_bytes[rom_off];
            }
        }
    }
    if (missing) *missing = true;
    return 0;
}

int main(int argc, char **argv) {
    alarm(30);
    const char *rom_path = NULL;
    const char *rom_sha256 = NULL;
    for (int i = 1; i < argc; i++) {
        if (strcmp(argv[i], "--rom") == 0) {
            if (rom_path != NULL) {
                fprintf(stderr, "runner: duplicate --rom argument\n");
                return 2;
            }
            if (i + 1 >= argc) {
                fprintf(stderr, "runner: missing path after --rom\n");
                return 2;
            }
            rom_path = argv[++i];
        } else if (strcmp(argv[i], "--rom-sha256") == 0) {
            if (rom_sha256 != NULL) {
                fprintf(stderr, "runner: duplicate --rom-sha256 argument\n");
                return 2;
            }
            if (i + 1 >= argc) {
                fprintf(stderr, "runner: missing hash after --rom-sha256\n");
                return 2;
            }
            rom_sha256 = argv[++i];
        } else {
            fprintf(stderr, "runner: unknown argument '%%s'\n", argv[i]);
            return 2;
        }
    }
    if (rom_path && !rom_sha256) {
        fprintf(stderr, "runner: --rom supplied without --rom-sha256\n");
        return 2;
    }
    if (!rom_path && rom_sha256) {
        fprintf(stderr, "runner: --rom-sha256 supplied without --rom\n");
        return 2;
    }
    if (rom_path) {
        int err = load_explicit_rom(rom_path, rom_sha256);
        if (err != 0) {
            return err;
        }
    }

    printf("[\n");
    bool first_out = true;
    char line[4096];

    while (fgets(line, sizeof(line), stdin)) {
        if (strncmp(line, "CASE", 4) != 0) {
            continue;
        }

        int case_idx = 0;
        unsigned int a=0, x=0, y=0, s=0, pc=0, d=0, db=0, pb=0, p=0, e=0, cell_count=0;
        int matched = sscanf(line, "CASE %%d A=%%u X=%%u Y=%%u S=%%u PC=%%u D=%%u DB=%%u PB=%%u P=%%u E=%%u CELLS=%%u",
                             &case_idx, &a, &x, &y, &s, &pc, &d, &db, &pb, &p, &e, &cell_count);
        if (matched != 12) {
            fprintf(stderr, "protocol error: malformed CASE header: %%s\n", line);
            return 2;
        }
        if (cell_count > MAX_CELLS) {
            fprintf(stderr, "protocol error: cell count %%u exceeds MAX_CELLS %%d\n", cell_count, MAX_CELLS);
            return 3;
        }

        cpu_state_t state = {0};
        state.a = (uint16_t)a;
        state.x = (uint16_t)x;
        state.y = (uint16_t)y;
        state.s = (uint16_t)s;
        state.pc = (uint16_t)pc;
        state.d = (uint16_t)d;
        state.db = (uint8_t)db;
        state.pb = (uint8_t)pb;
        state.p = (uint8_t)p;
        state.e = (e != 0);

        runner_mem_t mem;
        memset(&mem, 0, sizeof(mem));

        for (unsigned int i = 0; i < cell_count; i++) {
            if (!fgets(line, sizeof(line), stdin)) {
                fprintf(stderr, "protocol error: unexpected EOF reading memory cell %%u/%%u\n", i, cell_count);
                return 4;
            }
            uint32_t caddr = 0;
            unsigned int cval = 0;
            if (sscanf(line, "%%x %%x", &caddr, &cval) != 2) {
                fprintf(stderr, "protocol error: invalid memory cell line: %%s\n", line);
                return 5;
            }
            uint32_t can_addr = bus_canonical_addr(caddr);
            for (int k = 0; k < mem.count; k++) {
                if (mem.cells[k].addr == can_addr) {
                    if (mem.cells[k].val != (uint8_t)cval) {
                        fprintf(stderr, "protocol error: conflicting memory cell alias for $%%06X: $%%02X vs $%%02X\n",
                                can_addr, mem.cells[k].val, (uint8_t)cval);
                        return 6;
                    }
                    goto skip_dup;
                }
            }
            mem.cells[mem.count].addr = can_addr;
            mem.cells[mem.count].val = (uint8_t)cval;
            mem.count++;
        skip_dup:;
        }

        exec_result_t res = %s(state, test_read_cb, &mem);

        if (!first_out) printf(",\n");
        first_out = false;
        printf("{\"state\":{\"a\":%%u,\"x\":%%u,\"y\":%%u,\"s\":%%u,\"pc\":%%u,\"d\":%%u,\"db\":%%u,\"pb\":%%u,\"p\":%%u,\"e\":%%s},"
               "\"next_pc\":%%u,\"total_writes\":%%u,\"write_overflow\":%%s,\"missing_read\":%%s,\"missing_addr\":%%u,\"mmio_access\":%%s,\"mmio_addr\":%%u,\"writes\":[",
               res.state.a, res.state.x, res.state.y, res.state.s, res.state.pc, res.state.d,
               res.state.db, res.state.pb, res.state.p, res.state.e ? "true" : "false",
               res.next_pc, res.total_writes, res.write_overflow ? "true" : "false",
               res.uninitialized_read ? "true" : "false", res.uninitialized_addr,
               res.mmio_access ? "true" : "false", res.mmio_addr);

        for (int i = 0; i < res.num_writes; i++) {
            if (i > 0) printf(",");
            printf("{\"address\":%%u,\"value\":%%u}", res.writes[i].address, res.writes[i].value);
        }
        printf("]}");
    }
    printf("\n]\n");
    return 0;
}
`, routineC, entryFnName)
}

// RunBatch executes a batch of routine replay cases through the native compiled batch runner.
func (r *CompiledRoutineRunner) RunBatch(ctx context.Context, cases []ReplayCase) ([]ExecResult, error) {
	r.mu.Lock()
	if r.Closed {
		r.mu.Unlock()
		return nil, errors.New("runner is closed")
	}
	entryPC := r.EntryPC
	binPath := r.BinPath
	snapROMSHA := r.romSHA256
	snapROMPath := r.romPath
	r.mu.Unlock()

	if len(cases) == 0 {
		return nil, nil
	}

	var buf bytes.Buffer
	for idx, c := range cases {
		if entryPC != 0 {
			expectedPC := uint16(entryPC & 0xFFFF)
			if c.InitialState.PC != expectedPC {
				return nil, fmt.Errorf("case %d (%s) entry PC mismatch: got $%04X, want $%04X", idx, c.CaseID, c.InitialState.PC, expectedPC)
			}

			if c.InitialState.E {
				return nil, fmt.Errorf("case %d (%s) requires native 65816 mode (E=false)", idx, c.CaseID)
			}
			if c.InitialState.D != 0 {
				return nil, fmt.Errorf("case %d (%s) requires direct page D=0 on entry", idx, c.CaseID)
			}
			if c.InitialState.DB > 0x3F && (c.InitialState.DB < 0x80 || c.InitialState.DB > 0xBF) {
				return nil, fmt.Errorf("case %d (%s) requires data bank DB in $00-$3F or $80-$BF on entry", idx, c.CaseID)
			}
			if (c.InitialState.P & 0x08) != 0 {
				return nil, fmt.Errorf("case %d (%s) requires decimal mode clear (D=0 in P) on entry", idx, c.CaseID)
			}
			if (c.InitialState.P & 0x20) == 0 {
				return nil, fmt.Errorf("case %d (%s) requires 8-bit accumulator on entry (M=1)", idx, c.CaseID)
			}
			if (c.InitialState.P & 0x10) == 0 {
				return nil, fmt.Errorf("case %d (%s) requires 8-bit index registers on entry (X=1)", idx, c.CaseID)
			}
			if (c.InitialState.P&0x10) != 0 && (c.InitialState.X > 0xFF || c.InitialState.Y > 0xFF) {
				return nil, fmt.Errorf("case %d (%s) has malformed 8-bit index state: X=$%04X, Y=$%04X", idx, c.CaseID, c.InitialState.X, c.InitialState.Y)
			}
			if c.InitialState.S > 0x1FFD {
				return nil, fmt.Errorf("case %d (%s) requires stack S <= $1FFD on entry to avoid non-WRAM pull on RTS (got $%04X)", idx, c.CaseID, c.InitialState.S)
			}
		}
		if snapROMSHA != "" && c.ROMSHA256 != "" && c.ROMSHA256 != snapROMSHA {
			return nil, fmt.Errorf("case %d (%s) ROM SHA256 mismatch: case requires %s, runner bound to %s",
				idx, c.CaseID, c.ROMSHA256, snapROMSHA)
		}

		eInt := 0
		if c.InitialState.E {
			eInt = 1
		}
		fmt.Fprintf(&buf, "CASE %d A=%d X=%d Y=%d S=%d PC=%d D=%d DB=%d PB=%d P=%d E=%d CELLS=%d\n",
			idx, c.InitialState.A, c.InitialState.X, c.InitialState.Y, c.InitialState.S, c.InitialState.PC,
			c.InitialState.D, c.InitialState.DB, c.InitialState.PB, c.InitialState.P, eInt, len(c.InitialMemory))

		for _, cell := range c.InitialMemory {
			fmt.Fprintf(&buf, "%06X %02X\n", cell.Address, cell.Value)
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var args []string
	if snapROMPath != "" {
		args = append(args, "--rom", snapROMPath, "--rom-sha256", snapROMSHA)
	}

	cmd := exec.CommandContext(runCtx, binPath, args...)
	cmd.Stdin = &buf
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run routine batch: %w (stderr: %s)", err, stderr.String())
	}
	if stderr.Len() > 0 {
		// Log runner stderr
		_, _ = os.Stderr.Write(stderr.Bytes())
	}

	type runnerOutputItem struct {
		State struct {
			A  uint16 `json:"a"`
			X  uint16 `json:"x"`
			Y  uint16 `json:"y"`
			S  uint16 `json:"s"`
			PC uint16 `json:"pc"`
			D  uint16 `json:"d"`
			DB uint8  `json:"db"`
			PB uint8  `json:"pb"`
			P  uint8  `json:"p"`
			E  bool   `json:"e"`
		} `json:"state"`
		NextPC        uint32 `json:"next_pc"`
		TotalWrites   uint32 `json:"total_writes"`
		WriteOverflow bool   `json:"write_overflow"`
		MissingRead   bool   `json:"missing_read"`
		MissingAddr   uint32 `json:"missing_addr"`
		MMIOAccess    bool   `json:"mmio_access"`
		MMIOAddr      uint32 `json:"mmio_addr"`
		Writes        []struct {
			Address uint32 `json:"address"`
			Value   uint8  `json:"value"`
		} `json:"writes"`
	}

	var items []runnerOutputItem
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		return nil, fmt.Errorf("unmarshal routine runner output: %w (raw: %s)", err, stdout.String())
	}
	if len(items) != len(cases) {
		return nil, fmt.Errorf("routine runner returned %d items, expected %d", len(items), len(cases))
	}

	results := make([]ExecResult, len(cases))
	for i, item := range items {
		var writes []MemoryWrite
		for _, w := range item.Writes {
			writes = append(writes, MemoryWrite{Address: w.Address, Value: w.Value})
		}

		results[i] = ExecResult{
			State: CPUState{
				A:  item.State.A,
				X:  item.State.X,
				Y:  item.State.Y,
				S:  item.State.S,
				PC: item.State.PC,
				D:  item.State.D,
				DB: item.State.DB,
				PB: item.State.PB,
				P:  item.State.P,
				E:  item.State.E,
			},
			Writes:        writes,
			NextPC:        item.NextPC,
			TotalWrites:   item.TotalWrites,
			WriteOverflow: item.WriteOverflow,
			MissingRead:   item.MissingRead,
			MissingAddr:   item.MissingAddr,
			MMIOAccess:    item.MMIOAccess,
			MMIOAddr:      item.MMIOAddr,
		}
	}

	return results, nil
}

// Close terminates and cleans up temporary runner binaries and build directories.
func (r *CompiledRoutineRunner) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Closed {
		return nil
	}
	r.Closed = true
	if r.RunDir != "" {
		_ = os.RemoveAll(r.RunDir)
	}
	return nil
}

// RunEmulatorRoutineWithROM executes a bounded native routine using the reference Go 65816 CPU emulator with an explicit ROM byte slice.
func RunEmulatorRoutineWithROM(ctx context.Context, c ReplayCase, rom []byte) (ExecResult, error) {
	if err := ctx.Err(); err != nil {
		return ExecResult{}, err
	}
	if len(rom) == 0 {
		return ExecResult{}, errors.New("empty ROM bytes for reference emulator")
	}
	if len(rom) > 16*1024*1024 {
		return ExecResult{}, fmt.Errorf("rom size %d exceeds 16MB limit", len(rom))
	}
	romSHA := fmt.Sprintf("%x", sha256.Sum256(rom))
	if c.ROMSHA256 != "" && romSHA != c.ROMSHA256 {
		return ExecResult{}, fmt.Errorf("reference emulator ROM SHA256 mismatch: case requires %s, rom has %s", c.ROMSHA256, romSHA)
	}
	if c.ROMSHA256 == "" {
		return ExecResult{}, errors.New("reference emulator requires explicit case ROM identity")
	}
	if c.InitialState.E {
		return ExecResult{}, fmt.Errorf("case %s requires native 65816 mode (E=false)", c.CaseID)
	}
	if c.InitialState.D != 0 {
		return ExecResult{}, fmt.Errorf("case %s requires direct page D=0 on entry", c.CaseID)
	}
	if c.InitialState.DB > 0x3F && (c.InitialState.DB < 0x80 || c.InitialState.DB > 0xBF) {
		return ExecResult{}, fmt.Errorf("case %s requires data bank DB in $00-$3F or $80-$BF on entry", c.CaseID)
	}
	if (c.InitialState.P & 0x08) != 0 {
		return ExecResult{}, fmt.Errorf("case %s requires decimal mode clear (D=0 in P) on entry", c.CaseID)
	}
	if (c.InitialState.P & 0x20) == 0 {
		return ExecResult{}, fmt.Errorf("case %s requires 8-bit accumulator on entry (M=1)", c.CaseID)
	}
	if (c.InitialState.P & 0x10) == 0 {
		return ExecResult{}, fmt.Errorf("case %s requires 8-bit index registers on entry (X=1)", c.CaseID)
	}
	if (c.InitialState.P&0x10) != 0 && (c.InitialState.X > 0xFF || c.InitialState.Y > 0xFF) {
		return ExecResult{}, fmt.Errorf("case %s has malformed 8-bit index state: X=$%04X, Y=$%04X", c.CaseID, c.InitialState.X, c.InitialState.Y)
	}
	if c.InitialState.S > 0x1FFD {
		return ExecResult{}, fmt.Errorf("case %s requires stack S <= $1FFD on entry to avoid non-WRAM pull on RTS (got $%04X)", c.CaseID, c.InitialState.S)
	}

	b := bus.NewBus()

	// 1. Map 128KB WRAM to $7E0000-$7FFFFF
	wram := bus.NewWRAMDevice()
	b.Map(0x7E0000, 0x7FFFFF, wram)

	// 2. Map mirror Low RAM $0000-$1FFF to wram for all mirror banks in $00-$3F and $80-$BF
	for bank := uint32(0x00); bank <= 0x3F; bank++ {
		b.Map(bank<<16, (bank<<16)|0x1FFF, wram)
	}
	for bank := uint32(0x80); bank <= 0xBF; bank++ {
		b.Map(bank<<16, (bank<<16)|0x1FFF, wram)
	}

	// 3. Map Cartridge to Bus
	cart := cartridge.New(rom)
	cart.MapToBus(b)

	// 4. Populate initial memory
	initializedMem := make(map[uint32]bool)
	writtenAddrs := make(map[uint32]bool)

	for _, cell := range c.InitialMemory {
		cAddr := BusCanonicalAddr(cell.Address)
		b.Write(cAddr, cell.Value)
		initializedMem[cAddr] = true
		initializedMem[cell.Address] = true
	}

	var (
		recordedWrites []MemoryWrite
		totalWrites    uint32
		writeOverflow  bool
		missingRead    bool
		missingAddr    uint32
		mmioAccess     bool
		mmioAddr       uint32
	)

	b.WriteHook = func(address uint32, value uint8) {
		if IsMMIOAddr(address) {
			mmioAccess = true
			mmioAddr = address
		}
		totalWrites++
		cAddr := BusCanonicalAddr(address)
		writtenAddrs[cAddr] = true
		writtenAddrs[address] = true
		if len(recordedWrites) < 256 {
			recordedWrites = append(recordedWrites, MemoryWrite{
				Address: cAddr,
				Value:   value,
			})
		} else {
			writeOverflow = true
		}
	}

	b.ReadHook = func(address uint32, value uint8) {
		if IsMMIOAddr(address) {
			mmioAccess = true
			mmioAddr = address
		}
		bank := uint8((address >> 16) & 0xFF)
		offset := uint16(address & 0xFFFF)
		isWRAM := (bank == 0x7E || bank == 0x7F) ||
			((bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && offset < 0x2000)
		if isWRAM {
			cAddr := BusCanonicalAddr(address)
			if !initializedMem[address] && !initializedMem[cAddr] && !writtenAddrs[address] && !writtenAddrs[cAddr] {
				if !missingRead {
					missingAddr = address
				}
				missingRead = true
			}
		}
	}

	cpuInstance := cpu.NewCPU(b)
	cpuInstance.A = c.InitialState.A
	cpuInstance.X = c.InitialState.X
	cpuInstance.Y = c.InitialState.Y
	cpuInstance.S = c.InitialState.S
	cpuInstance.D = c.InitialState.D
	cpuInstance.DB = c.InitialState.DB
	cpuInstance.PB = c.InitialState.PB
	cpuInstance.PC = c.InitialState.PC
	cpuInstance.P = c.InitialState.P
	cpuInstance.E = c.InitialState.E

	const maxSteps = 2000
	stepCount := 0

	for {
		if err := ctx.Err(); err != nil {
			return ExecResult{}, fmt.Errorf("reference emulator timeout: %w", err)
		}
		currPC := (uint32(cpuInstance.PB) << 16) | uint32(cpuInstance.PC)
		if c.InstructionCount > 0 && stepCount == c.InstructionCount {
			break
		}
		if c.InstructionCount <= 0 && stepCount > 0 && currPC == c.ObservedNextPC {
			break
		}
		if stepCount >= maxSteps {
			return ExecResult{}, fmt.Errorf("reference emulator step bound %d exceeded", maxSteps)
		}
		cpuInstance.Step()
		stepCount++
	}

	if mmioAccess {
		return ExecResult{}, fmt.Errorf("unsupported MMIO access to address $%06X", mmioAddr)
	}
	if missingRead {
		return ExecResult{}, fmt.Errorf("read from uninitialized memory address $%06X", missingAddr)
	}

	nextPC := (uint32(cpuInstance.PB) << 16) | uint32(cpuInstance.PC)
	finalState := CPUState{
		A:  cpuInstance.A,
		X:  cpuInstance.X,
		Y:  cpuInstance.Y,
		S:  cpuInstance.S,
		D:  cpuInstance.D,
		DB: cpuInstance.DB,
		PB: cpuInstance.PB,
		P:  cpuInstance.P,
		E:  cpuInstance.E,
		PC: cpuInstance.PC,
	}

	return ExecResult{
		State:         finalState,
		NextPC:        nextPC,
		Writes:        recordedWrites,
		TotalWrites:   totalWrites,
		WriteOverflow: writeOverflow,
	}, nil
}

// ExecuteThreeWayRoutineReplay performs differential execution verification of a routine replay case,
// checking exact exit CPU state (A, X, Y, S, P, D, DB, PB, E, PC), stack return address pull,
// and ordered memory write effects across Observed Trace, Reference Emulator, and Compiled C.
func ExecuteThreeWayRoutineReplay(ctx context.Context, runner *CompiledRoutineRunner, c ReplayCase) (ReplayReceipt, error) {
	return defaultEvidenceVerifier.ExecuteThreeWayRoutineReplay(ctx, runner, c)
}

// ExecuteThreeWayRoutineReplay uses only admission granted by this verifier.
func (v *EvidenceVerifier) ExecuteThreeWayRoutineReplay(ctx context.Context, runner *CompiledRoutineRunner, c ReplayCase) (ReplayReceipt, error) {
	if runner == nil {
		return ReplayReceipt{}, errors.New("missing routine runner")
	}
	runnerROMBytes, runnerROMSHA, _ := runner.snapshotROM()
	binding, bindingErr := runner.replayBinding(c)

	receipt := ReplayReceipt{
		CaseID:          c.CaseID,
		BlockID:         c.RoutineID,
		CaseHash:        c.CaseHash,
		CaseIdentity:    c.Identity(),
		AdmissionDigest: c.AdmissionDigest,
		TraceObserved:   ExecResult{State: c.ObservedExit, NextPC: c.ObservedNextPC, Writes: append([]MemoryWrite(nil), c.ObservedWrites...), TotalWrites: uint32(len(c.ObservedWrites))},
		Metadata: ReceiptMetadata{
			ROMSHA256:      runnerROMSHA,
			BlockID:        c.RoutineID,
			GeneratedCHash: runner.GeneratedCHash,
			Compiler:       runner.Compiler,
			CompilerFlags:  runner.CompilerFlags,
			Timestamp:      time.Now().UTC().Format(time.RFC3339),
		},
	}

	if bindingErr != nil {
		receipt.Metadata.IsStale = true
		receipt.Metadata.StaleReason = bindingErr.Error()
		receipt.Discrepancy = bindingErr.Error()
		return receipt, nil
	}
	receipt.Metadata = binding.metadata
	receipt.Metadata.Timestamp = time.Now().UTC().Format(time.RFC3339)

	// Verify ROM SHA256 freshness and binding between case, runner, and trust root
	if c.ROMSHA256 == "" {
		receipt.Matched = false
		receipt.Discrepancy = "missing case ROM SHA256 identity"
		return receipt, nil
	}
	if runnerROMSHA == "" {
		receipt.Matched = false
		receipt.Discrepancy = "runner is not bound to a verified ROM"
		return receipt, nil
	}
	if c.ROMSHA256 != runnerROMSHA {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("ROM SHA256 mismatch: case requires %s, runner bound to %s",
			c.ROMSHA256, runnerROMSHA)
		return receipt, nil
	}

	// 1. Recompute case hash and enforce verified evidence admission
	computedCaseHash := ComputeCaseHash(c)
	if c.CaseHash != "" && c.CaseHash != computedCaseHash {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("case_hash mismatch: expected %s, got %s", computedCaseHash, c.CaseHash)
		return receipt, nil
	}
	c.CaseHash = computedCaseHash
	receipt.CaseHash = computedCaseHash

	if !v.IsAdmitted(c.CaseHash, c.AdmissionDigest) {
		receipt.Matched = false
		receipt.Discrepancy = "self-asserted observed_effects_captured rejected: case has not passed verified evidence admission"
		return receipt, nil
	}

	admRec, hasRec := v.GetAdmissionRecord(c.CaseHash, c.AdmissionDigest)
	if !hasRec || !admRec.Admitted {
		receipt.Matched = false
		receipt.Discrepancy = "self-asserted observed_effects_captured rejected: admission record not found"
		return receipt, nil
	}
	if admRec.ROMSHA256 != "" && admRec.ROMSHA256 != c.ROMSHA256 {
		receipt.Discrepancy = "admission ROM identity mismatch"
		return receipt, nil
	}
	if admRec.CaseID != c.CaseID || (admRec.RoutineID != "" && admRec.RoutineID != c.RoutineID) {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("admission record identity mismatch: admitted %s/%s, replaying %s/%s",
			admRec.CaseID, admRec.RoutineID, c.CaseID, c.RoutineID)
		return receipt, nil
	}

	// 2. Reference emulator execution from verified snapshot
	if len(runnerROMBytes) == 0 {
		receipt.Matched = false
		receipt.Discrepancy = "runner missing ROM bytes for reference emulator"
		return receipt, nil
	}
	emuRes, emuErr := RunEmulatorRoutineWithROM(ctx, c, runnerROMBytes)
	if emuErr != nil {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("emulator error: %v", emuErr)
		return receipt, nil
	}
	receipt.ReferenceEmu = emuRes

	emuVsObsMatched, emuVsObsDisc := CompareCPUStates(c.ObservedExit, emuRes.State)
	if !emuVsObsMatched {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("emulator vs observed CPU mismatch: %s", emuVsObsDisc)
		return receipt, nil
	}
	if emuRes.NextPC != c.ObservedNextPC {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("emulator NextPC mismatch: expected $%06X, got $%06X", c.ObservedNextPC, emuRes.NextPC)
		return receipt, nil
	}
	wEmuMatched, wEmuDisc := CompareWrites(c.ObservedWrites, emuRes.Writes)
	if !wEmuMatched {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("emulator vs observed write mismatch: %s", wEmuDisc)
		return receipt, nil
	}
	receipt.EmulatorMatch = true

	// 3. Compiled C execution
	batchRes, err := runner.RunBatch(ctx, []ReplayCase{c})
	if err != nil {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("runner execution error: %v", err)
		return receipt, nil
	}
	cRes := batchRes[0]
	receipt.CompiledC = cRes

	if cRes.WriteOverflow {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("write overflow: %d writes exceeds limit", cRes.TotalWrites)
		return receipt, nil
	}
	if cRes.MissingRead {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("uninitialized memory read at $%06X", cRes.MissingAddr)
		return receipt, nil
	}
	if cRes.MMIOAccess {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("disallowed MMIO access at $%06X", cRes.MMIOAddr)
		return receipt, nil
	}

	cVsObsMatched, cVsObsDisc := CompareCPUStates(c.ObservedExit, cRes.State)
	if !cVsObsMatched {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("compiled C vs observed CPU mismatch: %s", cVsObsDisc)
		return receipt, nil
	}
	if cRes.NextPC != c.ObservedNextPC {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("compiled C NextPC mismatch: expected $%06X, got $%06X", c.ObservedNextPC, cRes.NextPC)
		return receipt, nil
	}
	wCMatched, wCDisc := CompareWrites(c.ObservedWrites, cRes.Writes)
	if !wCMatched {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("compiled C vs observed write mismatch: %s", wCDisc)
		return receipt, nil
	}
	receipt.CMatch = true

	// 4. Verify Compiled C vs Reference Emulator
	cVsEmuMatched, cVsEmuDisc := CompareCPUStates(emuRes.State, cRes.State)
	if !cVsEmuMatched {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("compiled C vs emulator CPU mismatch: %s", cVsEmuDisc)
		return receipt, nil
	}
	wCEmuMatched, wCEmuDisc := CompareWrites(emuRes.Writes, cRes.Writes)
	if !wCEmuMatched {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("compiled C vs emulator write mismatch: %s", wCEmuDisc)
		return receipt, nil
	}

	// 5. Perfect 3-way match!
	receipt.Matched = true
	receipt.EffectsMatch = true
	receipt.EffectsStatus = "effects_matched"
	receipt.Eligible = true
	receipt.CapturedProofEligible = true
	receipt.ObservedMatch = true
	v.ValidateRoutineReplayReceiptFreshness(&receipt, &c, binding.region, binding.sourceCode, binding.metadata.ROMSHA256, binding.metadata.ProjectRevision, runner)
	return receipt, nil
}
