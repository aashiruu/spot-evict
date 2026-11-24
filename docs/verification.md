# Verification Log

## Local Reproducibility and Chunk Calibration

Measured locally on Ubuntu 22.04 (WSL2) with `chunks=200` and `rounds=10000` (`N = 3`).

- **200-chunk expected checksum:** `1a6314a6ddd9fae4bb5c5050455d1e12b7020ee0832b29fe9a0b01b6ee1bf5ca`
- **Chunk duration (`N = 3`):** min `8.661 ms`, median `8.935 ms`, max `9.691 ms`
- **100,000-chunk local ground-truth (`local-ground-truth`):** `659.42 s` (`6.594 ms/chunk`), checksum `666214f8b947cdf1b52b1c3ce753bc1c561e8b7c6e69f6bdc03c3d33cd53e99a`

## On-Demand Baseline Verification (`REAL AWS`)

- **Instance type:** `t3.micro` (`us-east-1`, Amazon Linux 2023 x86_64)
- **Looked-up On-Demand price (2026-11-23):** `$0.0104 per On Demand Linux t3.micro Instance Hour`
- **Smoke test (`od-smoke-1`, 1,000 chunks):** `2.587 s` (`2.587 ms/chunk`), checksum `d9724635921c8a5460868fc65d6382d3da6d68d7733ed35a6c704ddb8fc34bd3` (matched local `local-cal-1k`)
- **Full baseline (`od-base-1`, `od-base-2`, `od-base-3`, 100,000 chunks):** all 3 matched ground-truth checksum `666214f8b947cdf1b52b1c3ce753bc1c561e8b7c6e69f6bdc03c3d33cd53e99a`

## Stage 2: S3 Checkpointing & Hard-Kill Recovery Verification (`REAL AWS`)

- **Checkpointing protocol:** Versioned JSON envelopes (`checkpoints/<run_id>/ckpt-<next_chunk:012d>-<fencing:012d>.json`) with SHA-256 payload integrity digest, read-back verification (`Verify`), bounded retention (`keep-last`), and backward scan fallback on corrupted/truncated latest checkpoints.
- **Hard-kill + corruption fallback test (`kill9-ec2-clean`, 40,000 chunks, `checkpoint_every=2000`, `t3.micro`):**
  1. `INST1` (`i-0d2cfe9ce699e68b7`) launched, wrote periodic S3 checkpoints (`406–408 bytes` each) up to `ckpt-000000016000-f000000000001.json`, and was terminated mid-flight via `ec2:TerminateInstances`.
  2. Latest checkpoint `ckpt-000000016000-f000000000001.json` was overwritten with truncated JSON (`{"corrupted":true`).
  3. `INST2` (`i-02a000bbb5f09ade5`) detected the malformed JSON (`rejected checkpoints/kill9-ec2-clean/ckpt-000000016000-f000000000001.json: json decode failed: unexpected end of JSON input`), rolled back to `ckpt-000000014000-f000000000001.json`, and completed chunks `14,000 -> 40,000` in `71.25 s` (`2.740 ms/chunk`, `364.91 chunks/sec`).
  4. **Determinism proof:** Final checksum `fc546c0603cd1ab704a3b62b39dcd1901b3884beae1f435f39c4e2410aa20ca3` matched an uninterrupted local single-shot run (`verify-determinism`, `40,000` chunks) bit-for-bit.
