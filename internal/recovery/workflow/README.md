# Bounded recovery tasks

Build `go build ./cmd/snesrecover`. Every input path is absolute and paired with
its exact SHA-256. The task directory must be absent on first use. Keep the
same executable for resumes; an executable or project revision change refuses
reuse of the previous task.

A configuration selects one candidate proposal and pins the ROM. Required
fields are `candidate` and `rom` (`path`, `sha256`), `candidate_id`,
`project_dir`, `project_revision`, `corpus_root`, `label`, `corpus`,
`max_frames`, `max_cases`, `max_steps`, and `queue_limit`. Limits are positive:
10,000 frames, 10,000 sampled cases, 1,000,000 region steps, and 100 candidate
attempts. Streams are regular files at most 2 GiB; metadata and ROM have
smaller limits. Existing extractor event/line limits also apply.

```sh
snesrecover -task /owned/task -config /owned/config.json -config-sha256 "$CONFIG_SHA"
```

The first run writes a capture request and waits. It does not run an emulator.
A capture delivery JSON has `fixture`, `capture`, and `history` objects, each
with `trace`, `receipt`, and `summary` pinned inputs. Frame summaries must fit
the configured frame budget. The extractor still verifies its own supported
coverage contract, instruction membership, ROM mapping, and memory provenance.

```sh
snesrecover -task /owned/task -config /owned/config.json -config-sha256 "$CONFIG_SHA" \
  -evidence /owned/evidence.json -evidence-sha256 "$EVIDENCE_SHA" -timeout 10m
```

Evidence delivery is committed as `extracting` before costly work, so a crash
does not lose the pinned delivery. After extraction the task waits for policy review. `extraction/proposed-trust-root.json`
is a proposal. It is never loaded as authority. Supplying `-policy` and
`-policy-sha256` is an explicit operator action using the existing reviewed
AdmissionPolicy format.

```sh
snesrecover -task /owned/task -config /owned/config.json -config-sha256 "$CONFIG_SHA" \
  -policy /owned/reviewed-policy.json -policy-sha256 "$POLICY_SHA" -timeout 10m
```

Generation files under `journal/` are immutable full state snapshots with a
previous-generation hash and state digest. Artifacts are durable before the
next generation is committed. A process lock releases on exit. Interrupted
uncommitted step output is preserved under `orphan-<step>-<number>` and the step
is retried; it is never silently adopted. Temporary incomplete journal files
are ignored. Input substitution, artifact substitution, changed deliveries,
changed executable, and chain corruption refuse resume.

`qualified` requires the selected physical routine entry to have fresh matching
queue receipts and zero selected refusals, mismatches, or unexecuted cases.
Another candidate's qualification cannot finish this task. Qualification is
sampled CPU and ordered WRAM agreement, not timing, frame rendering, or whole
ROM recovery. The reference emulator shares Go CPU ancestry; these receipts
are not an independent CPU implementation. `blocked` is a terminal retained blocker; changed inputs require
a new task. `await_capture` and `await_policy` are resumable waiting states.
Cancellation is observed between extractor phases because the existing
in-process extractor has no context API; queue validation receives context.

Qualification on resume is a cache, not fresh proof. Without an explicit policy
argument, a previously qualified task returns `recorded_qualification` without
advancing the journal or granting admission. Resupplying the same reviewed policy
starts a fresh validation and admission pass. Each pass publishes under
`validation-<generation>/queue/`; previous receipts remain immutable. A resumed
`validation` phase also requires the policy argument. Journal phase transitions,
required evidence and artifact sets are checked independently of their checksums.
The read bound applies to bytes actually consumed, including a file that grows
while it is read.
