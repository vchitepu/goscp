#!/usr/bin/env python3
import re
import sys
from pathlib import Path


def fail(msg: str) -> None:
    print(f"FAIL: {msg}")
    raise SystemExit(1)


def main() -> None:
    report_path = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("docs/bench_report.txt")
    if not report_path.exists():
        fail(f"benchmark report not found: {report_path}")

    report = report_path.read_text()
    pattern = re.compile(
        r"^(Benchmark\S+)\s+\d+\s+[0-9.]+ ns/op\s+([0-9.]+) MB/s\s+([0-9.]+) B/op",
        re.M,
    )

    rows = {}
    for name, mbs, bop in pattern.findall(report):
        base = re.sub(r"-\d+$", "", name)
        rows.setdefault(base, {"mbs": [], "bop": []})
        rows[base]["mbs"].append(float(mbs))
        rows[base]["bop"].append(float(bop))

    required = [
        "BenchmarkScheduler_SingleWorker",
        "BenchmarkScheduler_4Workers",
        "BenchmarkScheduler_8Workers",
        "BenchmarkScheduler_16Workers",
    ]

    missing = [k for k in required if k not in rows]
    if missing:
        fail(f"missing benchmark rows: {missing}")

    avg_mbs = {k: sum(v["mbs"]) / len(v["mbs"]) for k, v in rows.items()}
    avg_bop = {k: sum(v["bop"]) / len(v["bop"]) for k, v in rows.items()}

    single = avg_mbs["BenchmarkScheduler_SingleWorker"]
    four = avg_mbs["BenchmarkScheduler_4Workers"]
    speedup = four / single

    chunk_size_bytes = 128 * 1024 * 1024
    mem_limit = 2 * chunk_size_bytes
    max_bop = max(avg_bop[k] for k in required)

    print(f"Throughput 1 worker: {single:.2f} MB/s")
    print(f"Throughput 4 workers: {four:.2f} MB/s")
    print(f"Speedup 4 vs 1: {speedup:.3f}x")
    print(f"Max B/op (1/4/8/16 workers): {max_bop:.0f} bytes")
    print(f"Memory threshold (2x128MiB): {mem_limit} bytes")

    if speedup < 3.0:
        fail(f"scaling threshold not met ({speedup:.3f}x < 3.0x)")
    if max_bop >= mem_limit:
        fail(f"memory threshold not met ({max_bop:.0f} >= {mem_limit})")

    print("PASS: scaling and memory thresholds met")


if __name__ == "__main__":
    main()
