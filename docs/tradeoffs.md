# Design Trade-offs

## Stage 0: Workload, State, and Guardrails

- **CPU-bound SHA-256 chain workload:** Chosen over synthetic sleeps so runtime reflects real vCPU throughput and remains 100% reproducible via a chained SHA-256 digest.
- **Local Terraform state:** Chosen over a remote S3 backend to keep all project resources inside a single `terraform destroy` lifecycle without bootstrap dependencies.
- **Account-level budget filter:** Chosen because AWS Cost Allocation Tags require manual activation and up to 24 hours to propagate, whereas account-level alerts trigger immediately.

## Stage 2: S3 State Checkpointing & Corruption Recovery

- **Immutable chunk-keyed checkpoint objects (`ckpt-<chunk>-<fence>.json`) over in-place overwrites:** Writing each checkpoint to a distinct S3 key guarantees that a mid-upload crash or corrupted latest object never destroys the previous valid checkpoint, enabling deterministic backward fallback (`LoadLatest`).
- **SHA-256 payload digest inside the envelope:** Protects against silent truncation or bit-flip corruption independently of S3 transport checksums.
- **Synchronous `Save` + read-back `Verify` on checkpoint boundaries:** Adds ~20–40 ms per checkpoint interval (~0.4% overhead at `checkpoint_every=2000`) while guaranteeing durability before advancing state.
## Stage 7: Simulator Model and Optimal Interval Assumptions

### Trade-Offs
- **Calibrated discrete-event simulation vs. more real AWS runs**: Running 132 parameter combinations across 50 trials each (6,600 jobs) on real EC2 Spot instances would require hundreds of instance-hours and FIS action-minutes. Instead, we built a Go discrete-event simulator calibrated strictly from measured Stage 1 through Stage 6 parameters: baseline chunk processing rate, S3 PUT checkpoint latency (`66.9 ms` median), replacement launch-to-resume time, and IMDSv2 notice lead time (`120 s` window).
- **Exponential (Poisson) interruption arrivals vs. fixed schedules**: Stage 6 used fixed interruption timestamps (`t = 45 s`) so strategies could be compared head-to-head on identical fault schedules. The simulator uses seeded exponential inter-arrival times across rates from `0.25/hr` to `8.0/hr` to test how strategies behave when multiple interruptions or zero interruptions occur randomly.
- **Classical Young-Daly vs. notice-aware checkpointing**: Classical Young-Daly (`tau = sqrt(2 * delta * M)`) assumes unannounced crash-stop failures where every interruption loses all work since the last periodic checkpoint. When IMDSv2 delivers a 2-minute warning on 95% of interruptions and a final checkpoint completes in `66.9 ms`, periodic checkpoints only guard against the remaining 5% unannounced failures (`lambda_eff = 0.05 * lambda`), shifting the optimal periodic interval higher by `sqrt(1 / (1 - 0.95)) = 4.47x`.
- **Compute vs. S3 request cost ratio on small instances**: At `t3.micro` Spot pricing (`$0.0031/hr`, or `$0.00000086/s`), one S3 Standard PUT (`$0.005` per 1,000 requests = `$0.000005`) costs the equivalent of `5.8 seconds` of compute. On a 263-second job, frequent periodic checkpoints cost more in S3 PUT requests than recomputing lost chunks until unannounced eviction rates exceed `4.0/hr`.
