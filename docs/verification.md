# Verification Log

## Local Reproducibility and Chunk Calibration

Measured locally on Ubuntu 22.04 (WSL2) with `chunks=200` and `rounds=10000` (`N = 3`).

- **Expected checksum:** `1a6314a6ddd9fae4bb5c5050455d1e12b7020ee0832b29fe9a0b01b6ee1bf5ca`
- **Chunk duration (`N = 3`):** min `8.661 ms`, median `8.935 ms`, max `9.691 ms`
- **Selected full-job size:** `chunks = 100000`, `rounds = 10000` (targeting ~20 minutes on a single `t3.micro` On-Demand instance)

### Raw Command Output

```text
$ go run ./cmd/worker -chunks 200 -rounds 10000
chunks=200 rounds=10000 elapsed=1.938s ms_per_chunk=9.691 chunks_per_sec=103.19 checksum=1a6314a6ddd9fae4bb5c5050455d1e12b7020ee0832b29fe9a0b01b6ee1bf5ca

$ go run ./cmd/worker -chunks 200 -rounds 10000
chunks=200 rounds=10000 elapsed=1.787s ms_per_chunk=8.935 chunks_per_sec=111.92 checksum=1a6314a6ddd9fae4bb5c5050455d1e12b7020ee0832b29fe9a0b01b6ee1bf5ca

$ go run ./cmd/worker -chunks 200 -rounds 10000
chunks=200 rounds=10000 elapsed=1.732s ms_per_chunk=8.661 chunks_per_sec=115.46 checksum=1a6314a6ddd9fae4bb5c5050455d1e12b7020ee0832b29fe9a0b01b6ee1bf5ca
```
