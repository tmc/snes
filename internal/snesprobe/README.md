# Local control limits

The JSON and gRPC entry points share one service owner. A command, including a
stream, holds the machine until it finishes. Streaming callbacks must return on
cancellation and must not call back into the same service. Streams return a final
summary without retaining the frame summaries already delivered to the caller.
Step and run calls check cancellation before execution and between frames.

Requests accept at most 36,000 frames, 128 watches, 256 bytes per watch or
checkpoint name, and 16 event filters. JSON requests and watch files are limited
to 1 MiB; player protobuf messages are limited to 1 MiB (player JSON uses the
scanner's 64 KiB line limit). ROM and state files are limited to 64 MiB.
WRAM reads must fit entirely within 128 KiB, including empty reads at the end.

A non-streaming run accepts at most 1,024 sampled summaries and a conservative
3 MiB summary budget, leaving room for the final state hashes and image in a
4 MiB response. Increase `every` or use streaming for longer runs. Checkpoint
storage accepts 64 names and 256 MiB total; replacing an existing name reuses its
budget. Bounds are checked before advancing the machine or retaining output.

Unix sockets use mode 0600. Startup preserves files, symlinks and active sockets;
only a socket that refuses connections is replaced. Cleanup removes only the
socket created by that listener. Socket parent directories must be trusted.
These APIs permit caller-selected ROM, state, watch and export paths. The opt-in
TCP gRPC endpoint has no authentication or transport encryption and should only
be exposed to trusted callers.
