# spot-evict: Fault-Tolerant Batch Execution & Split-Brain Fencing on AWS Spot

An empirically benchmarked, fault-tolerant batch compute engine written in **Go** and provisioned with **Terraform** on AWS (EC2 Auto Scaling, AWS Fault Injection Service, IMDSv2, EventBridge, S3, and DynamoDB).

Every failure mode—real 2-minute AWS FIS Spot interruptions, IMDSv2 vs. EventBridge arrival lag, SIGSTOP worker stalls, corrupted/truncated S3 checkpoints, and multi-eviction recovery chains—was tested live on AWS and verified bit-for-bit against deterministic SHA-256 workload baselines.

---

## Architecture

    +--------------------+       500 ms poll        +------------------------------+
    |  Go Batch Worker   | -----------------------> | EC2 IMDSv2 (169.254.169.254) |
    | (Deterministic     |   2-min ITN detected     | /spot/instance-action        |
    |  SHA-256 Chunks)   | <----------------------- +------------------------------+
    +--------------------+
       |              |
       | 1. Conditional Lease & Pre-Write Fence
       |    (atomic monotonic fencing_token++)
       v
    +----------------------------------------+
    | Amazon DynamoDB (spot-evict-leases)    |  <-- Rejects stale workers after SIGSTOP/stall
    +----------------------------------------+
       |
       | 2. Immutable Checkpoints + SHA-256 Envelope
       |    ckpt-<chunk>-f<fencing_token>.json
       v
    +----------------------------------------+
    | Amazon S3 (spot-evict-*)               |  <-- Verifies checksum; falls back on corruption
    +----------------------------------------+
       ^
       | 3. Replacement Launch (13.64 s total)
    +----------------------------------------+
    | EC2 Auto Scaling Group (Mixed Spot)    |  <-- Triggered by real AWS FIS Spot interruption
    +----------------------------------------+

---

## Key Empirical Results (Live AWS Benchmarks)

### 1. Real AWS FIS Interruption: Periodic-Only vs. IMDSv2 Notice Handling
Tested on t3.micro Spot instances running 100,000 deterministic SHA-256 chunks (10,000 rounds/chunk) interrupted via **AWS Fault Injection Service (aws:ec2:send-spot-instance-interruptions)**:

| Mode | FIS Experiment | Interruption Behavior | Replacement Boot-to-Resume | Wasted Chunks | Final SHA-256 Match |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Periodic Only (watch_itn=false)** | EXP4UdjQg3XE1xhTG6 | Ignored 2-min notice; terminated mid-interval after chunk=65,000 | **13.64 s** (6.00s ASG/EC2 + 6.28s OS + 1.36s init + 7ms S3 load) | Up to 4,999 chunks | **PASS** (666214f8...) |
| **IMDSv2 Notice (watch_itn=true)** | EXPwEpC4Nyrod4bnxz | Caught notice at chunk=47,030, flushed 411 B checkpoint in **60.31 ms**, exited with 116.45 s margin | **13.61 s** | **0 chunks** | **PASS** (666214f8...) |

### 2. Signal Arrival Race: EventBridge vs. IMDSv2
- **EventBridge -> CloudWatch Logs:** Emitted 15:28:09.000Z, ingested 15:28:11.550Z (**+2.550 s lag**, 117.45 s remaining).
- **IMDSv2 Local Poller (500 ms):** Observed at 15:28:12.490Z (**+3.490 s lag**, 116.51 s remaining).
- **Takeaway:** EventBridge arrives **0.94 s earlier** and is ideal for control-plane orchestration (pre-warming capacity or draining targets), while local IMDSv2 polling allows self-contained workers to halt compute and flush state without external push plumbing.

### 3. Split-Brain Fencing (SIGSTOP Frozen-Worker Test)
- W1 acquired DynamoDB lease fencing_token=1 (ttl=15s), processed 111 chunks, and was frozen mid-flight via SIGSTOP.
- After lease expiration (16s), W2 acquired the lease with monotonic fencing_token=2 and completed all 20,000 chunks (8d01dc8f...).
- Upon SIGCONT, W1 immediately failed its conditional DynamoDB check (HEARTBEAT FENCED OUT, exit code 3) and wrote **0** post-fence objects to S3.

### 4. Chaos & Failure Matrix
| Fault Scenario | Recovery Behavior | Checksum Verification |
| :--- | :--- | :--- |
| **Corrupted S3 Checkpoint** | Rejected SHA-256/schema mismatch on newest object; fell back to prior valid checkpoint | **PASS** |
| **Truncated 0-Byte S3 Object** | Rejected JSON decode failure; fell back to prior valid checkpoint | **PASS** |
| **3 Consecutive Evictions** | Chained token=1 -> 2 -> 3 -> 4 across 4 workers with emergency flushes | **PASS** |
| **Duplicate / Replay Run** | token=5 resumed at chunk=1500 (100% done) and executed 0 duplicate chunks | **PASS** |

### 5. Cost Model & Checkpoint Cadence (us-east-1 t3.micro + 8 GB gp3 EBS)
- **Break-even eviction rate:** **243 evictions/hr** (when paired with 500 ms IMDSv2 notice capture and 300s periodic backstop checkpoints).
- **Optimal checkpoint interval:** **300 s – 600 s** — saves **~56.1%** total cost vs. On-Demand ($0.0099 vs. $0.0226 for a 2-hour job), whereas 10 s checkpointing erodes savings to 36.4% due to S3 PUT request fees.

---

## Repository Structure

    cmd/
      worker/         # Fault-tolerant batch worker (IMDSv2 watcher, DynamoDB lease, S3 checkpoints)
      sim/            # Monte Carlo checkpoint-interval & Spot vs. On-Demand cost simulator
    internal/
      workload/       # Deterministic SHA-256 chunk compute engine
      checkpoint/     # S3 & local checkpoint persistence with SHA-256 verification & fallback
      imds/           # EC2 IMDSv2 token & Spot Instance Interruption Notice poller
      lease/          # DynamoDB conditional-write lease & monotonic fencing token manager
      sim/            # Quantitative Spot eviction & cost model
    terraform/        # VPC, S3, DynamoDB lease table, IAM, EventBridge, AWS FIS, Spot ASG
    scripts/
      replay_demo.sh  # One-command offline verification & simulation replay ($0.00 AWS cost)
    docs/             # Stage-by-stage engineering notes & benchmark tables (Stages 0–10)
    results/          # Raw JSON/CSV benchmark outputs

---

## Quickstart & Reproducible Replay

Run the offline test suite and Monte Carlo cost simulation ($0.00 AWS spend):

    ./scripts/replay_demo.sh

Ensure zero active EC2 compute instances remain when idle:

    terraform -chdir=terraform apply -var="enable_baseline_instance=false" -var="enable_spot_asg=false" -auto-approve
