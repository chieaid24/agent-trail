# MVP Completion Checklist

### Product

- [ ] GitHub App installs successfully
- [ ] Repository can be enabled
- [ ] `/agent-trail run` creates a task
- [ ] Task appears in dashboard
- [ ] User can cancel task
- [ ] Successful task creates draft PR
- [ ] PR contains evidence report

### Backend

- [ ] Webhook signatures validated
- [ ] Duplicate deliveries deduplicated
- [ ] State transitions enforced
- [ ] Task attempts use leases
- [ ] Runner heartbeats stored
- [ ] Publishing idempotent
- [ ] Organization authorization enforced

### Runner

- [ ] Workspace isolated
- [ ] Branch unique
- [ ] Commands captured
- [ ] Timeout works
- [ ] Cancellation works
- [ ] Trusted validation works
- [ ] Credentials removed
- [ ] Workspace cleanup works

### Security

- [ ] Non-root runner
- [ ] No Docker socket
- [ ] No host paths
- [ ] Secrets redacted
- [ ] Protected branch pushes blocked
- [ ] Invalid signatures rejected
- [ ] Path traversal tests pass
- [ ] Threat model documented

### Operations

- [ ] Metrics exported
- [ ] Traces work
- [ ] Dashboards exist
- [ ] Alerts exist
- [ ] Backup approach documented
- [ ] Runbooks exist
- [ ] Failure tests pass

### Documentation

- [ ] Architecture diagram
- [ ] ER diagram
- [ ] API documentation
- [ ] GitHub App setup
- [ ] Local development guide
- [ ] Cloud deployment guide
- [ ] Security limitations
- [ ] Benchmark methodology
- [ ] Demo script
