# Local Development

Docker Compose services:

- api
- worker
- web
- postgres
- redis
- minio
- otel-collector
- prometheus
- grafana

Recommended commands:

```bash
make dev
make migrate
make seed
make test
make integration-test
make e2e
make demo
make clean
```

Create a small demo repository containing:

- Backend service
- Tests
- Known issue
- Validation file
- Agent instructions
- Deterministic sample task
