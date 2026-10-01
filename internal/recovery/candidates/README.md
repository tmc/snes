# Candidate mining

Status: proposal generation only. No generation, admission, or proof eligibility
is granted by this command.

```
snesdasm candidates -project project -format json -limit 0
snesdasm candidates -project project -cases routine-cases.jsonl
```

The command reads recovery.json, optional coverage.json, and optional routine-case
JSONL. It writes its report to stdout and does not modify the project. The report
includes hashes of the bytes read, instruction/evidence IDs, reported occurrence
identities and entry/exit status contexts, candidate ranges, flags, and frontiers.
Ranges are half open. A span may contain unrecovered bytes; those bytes are not
classified as data, decoded, or supplied to a generator.

## Derivation

Routine entries come from recovered call/interrupt edges, encoded direct calls,
reported case entries, and the first recovered instruction. The miner follows
encoded conditional branches and their fallthroughs even when only one path was
observed. Calls produce separate routine and caller/callee proposals. Missing
instructions, unknown control transfers, and bounds produce explicit frontiers.

Different C values at entry are retained as alternative status contexts; they do
not change immediate operand widths. Conflicting bytes or E/M/X contexts stop
traversal at an ambiguity frontier. The traversal has instruction, byte-span,
and call-depth bounds. Cycles, external calls, dynamic status restoration,
decimal enabling, potential MMIO, and indirect accesses are flagged for review.

Ranking is deterministic: reported complete execution count descending, flags
ascending, liftable instruction count descending, span ascending, call count
ascending, then candidate ID. Liftability uses the existing machine-IR lifter;
it is not an emitter correctness or executable support claim.

## Evidence limits

Coverage entry hits are exact counts from the supplied index; they are not
routine completions. The index ROM identity must match recovery.json. Inventory
counts use reported sequence bounds, instruction count, and a recovered return
inside the proposed closure. They are labeled reported and unverified. Neither
a case digest nor an eligibility field grants any authority. Interrupt absence,
call-stack consistency, trace continuity, bus effects, and capture authenticity
still require independent admission and replay.

Encoded absolute and indirect operands cannot establish effective DB/D/indexed
addresses. Absence of a potential-MMIO flag is not proof of absence of hardware
side effects. Return M/X summaries and memory contracts are not inferred by this
slice. Frontiers are proposed review inputs, not approved refusal contracts.

## Next experiments

1. Join raw call/return transitions by sequence and stack state to count complete
   noninterrupted occurrences independently, retaining incomplete calls too.
2. Rank the next capture by which cold branch outcomes and unsupported opcode
   families it would exercise, rather than by total entry hits.
3. Resolve effective memory footprints from verified bus history and classify
   devices, DMA/HDMA, and scheduling boundaries before proposing generation.
4. Run the bounded region decoder as a separate dry-run qualification stage and
   retain explicit context/closure failures alongside each proposal.
