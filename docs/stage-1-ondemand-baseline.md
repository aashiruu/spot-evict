# Stage 1: On-Demand Baseline (`us-east-1`, `t3.micro`)

## Pricing Lookup at Run Time
- **Instance Type:** `t3.micro` (Linux/UNIX, Shared Tenancy, `us-east-1`)
- **On-Demand Rate:** `$0.0104 / hr` (`$0.000002889 / instance-second`)
- **Spot Reference Rate (`describe-spot-price-history`, 2026-11-24):** `$0.0024 – $0.0032 / hr` (`t3.micro`), `$0.0026 – $0.0043 / hr` (`t3a.micro`)

## Baseline Runs (`od-base-1` through `od-base-3`, 100,000 chunks)
| Run ID | Instance ID | Wall-Clock / Instance-Sec | Throughput (chunks/s) | Computed Cost (USD) | Checksum Match |
| :--- | :--- | :---: | :---: | :---: | :---: |
| `od-base-1` | `i-009b14973f9ba766b` | `260.99 s` | `383.16` | `$0.000754` | Verified |
| `od-base-2` | `i-0a61a67f6ffdec9da` | `262.28 s` | `381.27` | `$0.000758` | Verified |
| `od-base-3` | `i-0ecb3456260efcb95` | `266.65 s` | `375.02` | `$0.000771` | Verified |
| **Mean (100k)** | — | **`263.31 s` (`±1.1%` spread)** | **`379.82`** | **`$0.000761`** | **100% Match** |
| **Normalized (40k)** | — | **`105.32 s`** | **`379.82`** | **`$0.000304`** | **`fc546c06...20ca3`** |

## Key Takeaway
Execution speed on `t3.micro` is tight (`±1.1%` spread across 3 runs). Any runtime variance > `5%` in later stages reflects cloud scheduling, boot latency, or interruption recovery overhead rather than CPU jitter.
