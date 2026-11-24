# Stages 3–6: Spot Fleet Provisioning, Real AWS FIS Interruptions & Fencing

## Stage 3: Spot Fleet Provisioning & Periodic-Only Recovery (`spot_watch_itn=false`)

### Fleet Design
- **Provisioner:** EC2 Auto Scaling Group (`min=1`, `max=1`, `desired=1`) with `mixed_instances_policy` (`on_demand_base_capacity=0`, `on_demand_percentage_above_base_capacity=0`, `spot_allocation_strategy="capacity-optimized-prioritized"`).
- **Diversified Pool:** `t3.micro` and `t3a.micro` across `us-east-1a` and `us-east-1b`.
- **Real Interruption Mechanism:** AWS Fault Injection Service (`aws:ec2:send-spot-instance-interruptions`, `durationBeforeInterruption="PT2M"`, target `aws:ec2:spot-instance`).

### Live AWS FIS Run (`stage3-fis-periodic`, 100,000 chunks, `checkpoint_every=5000`, `spot_watch_itn=false`)
- **Experiment ID:** `EXP4UdjQg3XE1xhTG6` against `SPOT1` (`i-0e37bcfa9969eb667`).
- **Behavior:** With IMDSv2 polling disabled (`spot_watch_itn=false`), `SPOT1` ignored the 2-minute notice at `15:10:06Z` and continued computing until EC2 terminated it at `15:13:05.000Z`, losing up to 4,999 uncommitted chunks since its last periodic checkpoint (`chunk=65000`).
- **Replacement Breakdown (`SPOT1` terminated → `SPOT2` `i-074bfa75f574a11d5` resuming compute):**

| Replacement Phase | Start Timestamp (UTC) | End Timestamp (UTC) | Duration |
| :--- | :--- | :--- | :--- |
| **ASG Detection + EC2 Launch** | `15:13:05.000Z` | `15:13:11.000Z` | **6.00 s** (`1.55 s` ASG detection + `4.46 s` EC2 launch) |
| **OS Boot (`cloud-init` start)** | `15:13:11.000Z` | `15:13:17.275Z` | **6.28 s** |
| **User-Data Init (IMDSv2 + S3 binary)** | `15:13:17.275Z` | `15:13:18.633Z` | **1.36 s** |
| **Worker Startup + S3 State Load** | `15:13:18.633Z` | `15:13:18.640Z` | **0.007 s** (`7 ms`) |
| **Total Interruption-to-Resume** | `15:13:05.000Z` | `15:13:18.640Z` | **13.64 s** |

- **Verification:** `SPOT2` resumed at `resumed_from_chunk=65000` (`fencing_token=2`), finished at `367.12 chunks/s`, and produced the exact 100,000-chunk SHA-256 checksum `666214f8b947cdf1b52b1c3ce753bc1c561e8b7c6e69f6bdc03c3d33cd53e99a`.

---

## Stage 4: Interruption Notice Handling (`IMDSv2` vs. `EventBridge` Race)

### Design Decisions
- **IMDSv2 Polling Interval:** `500 ms` against link-local `http://169.254.169.254/latest/meta-data/spot/instance-action` (zero network egress cost, negligible CPU overhead, `0.5 s` worst-case detection latency).
- **Action Inside the 2-Minute Window:** Immediate halt + emergency S3 checkpoint flush + clean exit (rather than continuing to work until `T+115s`), eliminating wasted compute while guaranteeing checkpoint durability well ahead of hard termination.

### Live AWS FIS Run (`stage4-fis-notice`, 100,000 chunks, `checkpoint_every=20000`, `spot_watch_itn=true`)
- **Experiment ID:** `EXPwEpC4Nyrod4bnxz` against `SPOT1` (`i-0d417b5c348a7d8ab`), termination scheduled for `15:30:09Z`.
- **IMDSv2 vs. EventBridge Arrival Race:**

| Signal Path | Event Emitted (`time`) | Observed / Ingested (UTC) | Arrival Lag | Remaining Window |
| :--- | :--- | :--- | :--- | :--- |
| **EventBridge → CloudWatch Logs** | `15:28:09.000Z` | `15:28:11.550Z` (CW ingestion) | **+2.550 s** | `117.45 s` |
| **IMDSv2 Local Poller (`500 ms`)** | `15:28:09.000Z` | `15:28:12.490Z` (`detected_at`) | **+3.490 s** | `116.51 s` |

- **Takeaway:** EventBridge reached CloudWatch Logs **0.94 s earlier** than the local IMDSv2 poll—making EventBridge ideal for **control-plane actions** (e.g., pre-warming a replacement instance or draining load balancer targets), while local IMDSv2 polling remains essential inside the worker to halt execution without external push infrastructure.
- **Zero-Waste Flush & Recovery:**
  - `SPOT1` caught the notice at `chunk=47030` (between periodic checkpoints `40,000` and `60,000`), flushed a `411 B` emergency checkpoint in **`60.314 ms`**, and exited cleanly with **116.45 s** of margin.
  - Replacement `SPOT2` (`i-07e8322e604cf8570`) booted in **`13.61 s`**, resumed at **`resumed_from_chunk=47030`** (`fencing_token=2`), and completed all 100,000 chunks (`369.94 chunks/s`) with exact checksum `666214f8b947cdf1b52b1c3ce753bc1c561e8b7c6e69f6bdc03c3d33cd53e99a`.

---

## Stage 5: Fencing & Split-Brain Prevention (In Progress)
- *Next:* DynamoDB conditional-write lease (`spot-evict-leases`), monotonically increasing `fencing_token`, heartbeat expiration, and `SIGSTOP` frozen-worker split-brain test.
