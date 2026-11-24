# Interruption Testing and Cost Accounting

Every measurement in this document records `N`, spread (`min`, `median`, `max`), run IDs, and source label (`REAL AWS`, `MOCKED INTERRUPTION`, or `SIMULATED`).

## On-Demand Baseline (`REAL AWS`)

Measured on `t3.micro` in `us-east-1` at `$0.0104/hr` (`N = 3`, runs `od-base-1`, `od-base-2`, `od-base-3`, `100,000` chunks, `10,000` rounds/chunk).

| Metric (`N = 3`) | Min | Median | Max |
| --- | --- | --- | --- |
| Worker execution time | `260.99 s` | `262.28 s` | `266.65 s` |
| Total instance-seconds (launch to shutdown) | `275.49 s` | `277.46 s` | `279.45 s` |
| Boot + S3 binary download overhead | `11.0 s` | `12.8 s` | `13.0 s` |
| Computed On-Demand cost (`$0.0104/hr`) | `$0.000796` | `$0.000802` | `$0.000807` |

All 3 runs produced the ground-truth SHA-256 checksum `666214f8b947cdf1b52b1c3ce753bc1c561e8b7c6e69f6bdc03c3d33cd53e99a`. Hardware SHA-NI instructions on EC2 x86_64 processors executed chunks at `2.61–2.67 ms/chunk` (~2.5x faster than local WSL2).

## Hard-Termination & Corrupted Checkpoint Recovery (`REAL AWS`)

- **Run ID:** `kill9-ec2-clean` (`40,000` chunks, `checkpoint_every=2000`, `t3.micro` in `us-east-1`)
- **Simulated SIGKILL / eviction:** `ec2:TerminateInstances` issued mid-run after `16,000` chunks (`40%` progress), followed by deliberate corruption of `ckpt-000000016000-f000000000001.json`.
- **Recovery outcome:** Replacement instance (`i-02a000bbb5f09ade5`) rolled back 1 checkpoint interval (`2,000` chunks = `~5.48 s` of lost work) to chunk `14,000`, wrote `13` checkpoints (`408 bytes` each), and finished in `71.25 s` (`2.740 ms/chunk`) with exact checksum `fc546c0603cd1ab704a3b62b39dcd1901b3884beae1f435f39c4e2410aa20ca3`.
