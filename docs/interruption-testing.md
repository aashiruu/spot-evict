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
