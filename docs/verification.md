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
