# Design Trade-offs

## Stage 0: Workload, State, and Guardrails

- **CPU-bound SHA-256 chain workload:** Chosen over synthetic sleeps so runtime reflects real vCPU throughput and remains 100% reproducible via a chained SHA-256 digest.
- **Local Terraform state:** Chosen over a remote S3 backend to keep all project resources inside a single `terraform destroy` lifecycle without bootstrap dependencies.
- **Account-level budget filter:** Chosen because AWS Cost Allocation Tags require manual activation and up to 24 hours to propagate, whereas account-level alerts trigger immediately.
