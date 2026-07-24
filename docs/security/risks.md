# Major Risks

### Unsafe repository execution

Mitigation:

- Trusted repositories only
- Restricted containers
- Minimal credentials
- Egress controls
- Non-root execution

### Agent CLI changes

Mitigation:

- Adapter interface
- Version pinning
- Contract tests
- API fallback

### Log leakage

Mitigation:

- Minimal secrets
- Redaction
- Access control
- Short retention

### Git race conditions

Mitigation:

- Base SHA
- Unique branches
- Fetch locks
- Safe retries
- Integration tests

### Duplicate processing

Mitigation:

- Delivery IDs
- Unique constraints
- Leases
- Idempotent publishing

### Scope growth

Mitigation:

- Freeze MVP
- Require acceptance criteria
- Delay semantic conflict detection
- Maintain non-goals
