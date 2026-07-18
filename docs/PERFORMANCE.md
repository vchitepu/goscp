# GoSCP Performance Acceptance Report

Date: 2026-05-25

## Benchmark command

`go test -bench=. -benchmem -benchtime=30s -count=3 ./pkg/transfer/... | tee docs/bench_report.txt`

Raw report: `docs/bench_report.txt`

## Test environment

- OS/Arch: linux/amd64
- CPU: AMD Ryzen 7 6800H with Radeon Graphics
- Logical CPU cores: 8
- RAM: 24,608,692 kB (~23.47 GiB)
- Go version: go1.25.0

## Throughput results (MB/s)

Averages across `-count=3` runs:

- 1 worker: 181.21 MB/s
- 4 workers: 724.83 MB/s
- 8 workers: 1449.43 MB/s
- 16 workers: 1449.51 MB/s

Observations:
- Scaling is near-linear from 1 -> 4 workers.
- Throughput plateaus at 8 workers and 16 workers (no meaningful gain after 8 on this host).

## Scaling efficiency

- 4-worker speedup vs 1-worker baseline: 4.0000x
- 4-worker efficiency vs linear ideal: 100.00%

Acceptance target:
- Requirement: 4 workers >= 3.0x of 1 worker
- Result: PASS (4.0000x)

## Memory allocation stats (`-benchmem`)

Average B/op and allocs/op:

- 1 worker: 4,155 B/op, 48 allocs/op
- 4 workers: 4,779 B/op, 54 allocs/op
- 8 workers: 5,714 B/op, 62.3 allocs/op
- 16 workers: 7,288 B/op, 78.7 allocs/op

Acceptance target:
- Requirement: memory per chunk transfer < 2x chunk size
- Bench chunk size for worker-scaling scenario: 128 MiB/chunk (1 GiB / 8 chunks)
- Threshold: 256 MiB = 268,435,456 bytes
- Worst measured B/op (1/4/8/16): 7,288 bytes
- Result: PASS (7,288 << 268,435,456)

## Chunk size recommendation

Based on current benchmark and sanity tests:
- Prefer 128 MiB chunks for bulk throughput-oriented transfers.
- 16 MiB chunks increase scheduling/coordination overhead (higher allocs/op and lower efficiency in chunk-focused scenarios).

Operational recommendation:
- Default chunk size: 128 MiB
- Use smaller chunks only when fairness/latency across many tiny transfers is more important than peak throughput.

## Race safety

Validation command:
- `go test -race ./pkg/transfer/...`

Result:
- PASS (zero races detected)

Acceptance target:
- Requirement: zero data races under `-race`
- Result: PASS

## benchstat

`benchstat` was requested but not executed in this run because the binary was not present and installing it (`go install golang.org/x/perf/cmd/benchstat@latest`) was blocked by command security policy in this environment.

## Known bottlenecks and optimization notes

- Worker saturation point reached around 8 workers on this 8-core machine; 16 workers provide no additional throughput and add overhead.
- Allocation pressure increases with worker count (especially at 16 workers). Potential improvements:
  - Reuse buffers/chunk metadata where possible.
  - Reduce per-chunk bookkeeping allocations in scheduler hot paths.
- Small-file workloads remain much slower than large sequential transfer workloads; batching and metadata-path optimization remain key targets.
- For future tuning, add CPU/heap profiling during high-worker benchmarks to pinpoint allocator hot spots and lock contention.
