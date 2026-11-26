#!/usr/bin/env python3
import glob
import json
import os
from datetime import datetime, timezone

# AWS us-east-1 reference hourly rates for t3.micro (Linux)
ON_DEMAND_HOURLY_USD = 0.0104
SPOT_HOURLY_USD = 0.0031  # ~70% discount typical for t3.micro in us-east-1
# S3 Standard us-east-1 pricing: $0.005 per 1,000 PUT/LIST requests, $0.0004 per 1,000 GET requests
S3_PUT_COST_PER_REQ = 0.005 / 1000.0
S3_GET_COST_PER_REQ = 0.0004 / 1000.0


def load_results(results_dir: str) -> dict:
    runs = {}
    for path in sorted(glob.glob(os.path.join(results_dir, "*.json"))):
        name = os.path.splitext(os.path.basename(path))[0]
        if name == "comparison":
            continue
        with open(path, "r", encoding="utf-8") as f:
            runs[name] = json.load(f)
    return runs


def estimate_cost(run: dict, is_spot: bool, extra_instance_sec: float = 0.0) -> dict:
    hourly = SPOT_HOURLY_USD if is_spot else ON_DEMAND_HOURLY_USD
    total_compute_sec = float(run.get("elapsed_seconds", 0.0)) + extra_instance_sec
    compute_usd = (total_compute_sec / 3600.0) * hourly

    ckpts = int(run.get("checkpoints_written", 0))
    # Each checkpoint save does 1 PUT (tmp) + 1 COPY/PUT (final) + 1 DELETE (tmp) + ~1 LIST/DELETE retention
    s3_puts = ckpts * 2
    s3_gets = 2 if run.get("resumed_from_chunk", 0) > 0 else 1
    s3_usd = (s3_puts * S3_PUT_COST_PER_REQ) + (s3_gets * S3_GET_COST_PER_REQ)

    return {
        "hourly_rate_usd": hourly,
        "billed_compute_sec": round(total_compute_sec, 2),
        "compute_cost_usd": round(compute_usd, 6),
        "s3_checkpoint_cost_usd": round(s3_usd, 6),
        "total_cost_usd": round(compute_usd + s3_usd, 6),
    }


def main():
    root = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
    results_dir = os.path.join(root, "results")
    runs = load_results(results_dir)

    # Identify 40k-chunk runs for apples-to-apples comparison
    full_runs = {k: v for k, v in runs.items() if v.get("target_chunks") == 40000 and v.get("completed")}
    checksums = {v["checksum"] for v in full_runs.values() if "checksum" in v}

    comparison = {
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "checksum_invariant_verified": len(checksums) == 1,
        "expected_40k_checksum": next(iter(checksums)) if len(checksums) == 1 else None,
        "runs": {},
    }

    for name, data in sorted(runs.items()):
        is_spot = data.get("instance_lifecycle") == "spot" or "spot" in name or "itn" in name
        # For spot-asg-evict-1, account for ~53s of SPOT1 compute before termination
        prior_sec = 53.3 if name == "spot-asg-evict-1" else 0.0
        cost = estimate_cost(data, is_spot=is_spot, extra_instance_sec=prior_sec)
        comparison["runs"][name] = {
            "instance_id": data.get("instance_id", "local"),
            "instance_type": data.get("instance_type", "local"),
            "lifecycle": data.get("instance_lifecycle", "unknown"),
            "fencing_token": data.get("fencing_token", 1),
            "resumed_from_chunk": data.get("resumed_from_chunk", 0),
            "chunks": f"{data.get('chunks')}/{data.get('target_chunks')}",
            "completed": data.get("completed", False),
            "interrupted": data.get("interrupted", False),
            "elapsed_seconds": round(float(data.get("elapsed_seconds", 0.0)), 2),
            "chunks_per_sec": round(float(data.get("chunks_per_sec", 0.0)), 2),
            "checkpoints_written": data.get("checkpoints_written", 0),
            "checksum": data.get("checksum", ""),
            "cost_estimate": cost,
        }

    out_json = os.path.join(results_dir, "comparison.json")
    with open(out_json, "w", encoding="utf-8") as f:
        json.dump(comparison, f, indent=2)

    lines = [
        "# Empirical Benchmark Summary (`spot-evict`)",
        "",
        f"- **40,000-Chunk Checksum Invariant Verified:** `{comparison['checksum_invariant_verified']}` (`{comparison['expected_40k_checksum']}`)",
        "",
        "| Run ID | Instance | Lifecycle | Fence | Resumed From | Elapsed (s) | Throughput (chunks/s) | Est. Total Cost (USD) | Completed |",
        "| :--- | :--- | :--- | :---: | :---: | :---: | :---: | :---: | :---: |",
    ]
    for name, r in comparison["runs"].items():
        lines.append(
            f"| `{name}` | `{r['instance_id']}` ({r['instance_type']}) | `{r['lifecycle']}` | "
            f"`{r['fencing_token']}` | `{r['resumed_from_chunk']}` | `{r['elapsed_seconds']}` | "
            f"`{r['chunks_per_sec']}` | `${r['cost_estimate']['total_cost_usd']:.6f}` | `{r['completed']}` |"
        )

    out_md = os.path.join(results_dir, "summary.md")
    with open(out_md, "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")

    print("\n".join(lines))


if __name__ == "__main__":
    main()
