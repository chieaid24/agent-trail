# Reliability Targets

Initial targets:

- Webhook endpoint availability: 99.5%
- Webhook acknowledgment P95: under 500 ms
- Task update latency: under 2 seconds
- Live event display latency: under 2 seconds
- No duplicate tasks from duplicate delivery IDs
- No simultaneous ownership of one attempt
- Cleanup within the retention window
- Every terminal task has a machine-readable result

Do not claim 99.99% uptime without long-term monitoring.
