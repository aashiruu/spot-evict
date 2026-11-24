# Stage 0: Setup, Workload Determinism, and Cost Guardrails

## Target Environment
- **AWS Region:** `us-east-1` (N. Virginia)
- **AWS Account ID:** `529088258981`
- **Artifacts Bucket:** `spot-evict-529088258981-5791012d`

## Deterministic Workload Design (`internal/workload`)
- Fixed-size work units ("chunks"), each executing `10,000` iterations of SHA-256 over the previous digest, chunk index, and round counter.
- Any skipped, duplicated, or out-of-order chunk alters `RunningHashHex`, making final output verification a strict cryptographic invariant.
- **Proposed Job Size:** `40,000` chunks (`~105–110 s` on `t3.micro` at `~370–380 chunks/s`, long enough to inject mid-flight interruptions while costing `< $0.001` per run).

## Local Determinism Verification (3 Runs)
Three independent local executions produced the identical SHA-256 digest:
- `10,000` chunks (`10,000` rounds/chunk): `2df9175b26f39521efc53c2903f6f6f89e08f10d0f011cf277788663a127c4e4`
- `40,000` chunks (`10,000` rounds/chunk): `fc546c0603cd1ab704a3b62b39dcd1901b3884beae1f435f39c4e2410aa20ca3`

## Guardrails
- **Hard Chunk Ceiling:** `maxAllowedChunks = 100000` in `cmd/worker/main.go` prevents runaway CPU loops.
- **Terraform Default Tagging:** All resources inherit `project = "spot-evict"` and `managed = "terraform"`.
- **AWS Budgets:** Monthly cost cap (`spot-evict-monthly-cap`) configured in `terraform/`.
- **Teardown & Orphan Audit:** `scripts/teardown.sh` and `scripts/orphan-check.sh` enforce zero lingering EC2, ASG, EBS, EIP, or NAT resources.
