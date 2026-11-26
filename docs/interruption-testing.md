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
## Simulator Validation and Optimal Interval Sweep (SIMULATED)

### Validation Gate Against Stage 1 and Stage 6 Real Runs
Before running any parameter sweep, the simulator was validated against the five real AWS configurations using a pre-declared tolerance of `+/- 5.0%` on total compute time (derived from the Stage 1 On-Demand baseline spread).

| Configuration | Source Label | Reference Run ID | Real Compute Time | Simulated Compute Time (SIMULATED) | Relative Error | Gate Result |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| Stage 1 On-Demand Baseline | REAL AWS | `stage1-od-baseline` | `263.31 s` | `263.31 s` | `0.00%` | PASS |
| Strategy A (`none`) | REAL AWS | `stage6-fis-A-none` | `427.42 s` | `428.31 s` | `+0.21%` | PASS |
| Strategy B (`notice-only`) | REAL AWS | `stage6-fis-B-notice` | `261.68 s` | `263.46 s` | `+0.68%` | PASS |
| Strategy C (`periodic-15k`) | REAL AWS | `stage6-fis-C-periodic` | `270.58 s` | `270.47 s` | `-0.04%` | PASS |
| Strategy D (`periodic+notice`) | REAL AWS | `stage6-fis-D-both` | `263.46 s` | `263.94 s` | `+0.18%` | PASS |

### Optimal Checkpoint Interval Sweep (SIMULATED)
- **Run ID**: `stage7-sim-sweep-132x50`
- **Sample size**: `N = 50` seeded Poisson trials per cell across 132 cells (`6,600` simulated jobs total)
- **Findings (SIMULATED)**:
  1. **Zero-notice regime (`0%` IMDSv2 warning)**: For wall-clock time, the empirical best checkpoint interval tracks the Young-Daly estimate (`sqrt(2 * delta * M)`). For total dollar cost on `t3.micro` (`$0.0031/hr`), disabling periodic checkpoints (`0 s`) wins up to `2.0 evictions/hr` because each S3 PUT (`$0.000005`) equals `5.8 s` of compute; at `4.0` to `8.0 evictions/hr`, `120 s` and `60 s` intervals become cheaper than recomputation.
  2. **Notice-enabled regime (`95%` IMDSv2 warning)**: Because the worker flushes state in `66.9 ms` during the 2-minute warning, periodic checkpoints only protect against the `5%` unannounced kills. The empirical optimal interval for wall-clock time shifts upward (`120 s` to `300 s` vs. `17 s` to `24 s` unadjusted Young-Daly), and Strategy B (`notice-only`) minimizes total dollar cost across all tested rates (`0.25/hr` to `8.0/hr`).
