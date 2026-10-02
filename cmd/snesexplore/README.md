# snesexplore

Measure pinned capture gaps before spending time on input search:

    snesexplore -config campaign.json -config-sha256 SHA -out /absolute/new-plan
    snesexplore -config campaign.json -config-sha256 SHA -out /absolute/new-run -execute

The configuration pins ROM, discovery report, its declared coverage source,
previous batch manifest and capture summary, source files, and the trace tool.
It supplies one target, an explicit capture PC union, at most240 baseline frames,
eight30-frame port0 schedules, and instruction/site/trace byte/event budgets.

The runtime uses the existing core and CPU observation API. It captures three
power-on streams with the existing trace tool, calls the existing extractor,
and restores one complete checkpoint for each schedule and two winner repeats.
Novelty counts instruction address plus E/M/X/C, relative to the baseline.
State hashes check repeatability; they never award novelty. Input search is
discovery only. Extracted evidence remains proposed until a separately reviewed
policy is explicitly supplied. Optional queue qualification uses actual generated
C and retains the raw report. Missing policy or extraction failures are explicit
nonqualifying capture outcomes, even if the discovery campaign completed.

Outputs are staged and manifest.json is published last. Existing output refuses;
use new pins and a new directory for a new campaign. There is no authenticated
cached-return API. Cancellation prevents publication. Raw producer command logs,
receipts, captures, cases and checkpoint bytes are retained with artifact hashes.
