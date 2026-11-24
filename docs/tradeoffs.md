# Design Trade-offs

## Stage 0: Workload, State, and Guardrails

- **CPU-bound SHA-256 chain workload:** Chosen over synthetic sleeps so runtime reflects real vCPU throughput and remains 100% reproducible via a chained SHA-256 digest.
- **Local Terraform state:** Chosen over a remote S3 backend to keep all project resources inside a single `terraform destroy` lifecycle without bootstrap dependencies.
- **Account-level budget filter:** Chosen because AWS Cost Allocation Tags require manual activation and up to 24 hours to propagate, whereas account-level alerts trigger immediately.

## Stage 2: S3 State Checkpointing & Corruption Recovery

- **Immutable chunk-keyed checkpoint objects (`ckpt-<chunk>-<fence>.json`) over in-place overwrites:** Writing each checkpoint to a distinct S3 key guarantees that a mid-upload crash or corrupted latest object never destroys the previous valid checkpoint, enabling deterministic backward fallback (`LoadLatest`).
- **SHA-256 payload digest inside the envelope:** Protects against silent truncation or bit-flip corruption independently of S3 transport checksums.
- **Synchronous `Save` + read-back `Verify` on checkpoint boundaries:** Adds ~20–40 ms per checkpoint interval (~0.4% overhead at `checkpoint_every=2000`) while guaranteeing durability before advancing state.
