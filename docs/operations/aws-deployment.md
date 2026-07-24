# AWS Deployment

### Control plane

Use:

- ECS Fargate or EKS
- Application Load Balancer
- RDS PostgreSQL
- S3
- SQS
- Secrets Manager
- CloudWatch
- OpenTelemetry

### Runner execution

Use:

- EKS Kubernetes Jobs
- Dedicated namespace
- Restricted service account
- TTL cleanup
- Network policies
- Resource limits
- No service-account token unless required

### IAM roles

Separate:

- Control plane role
- Runner controller role
- Runner task role
- CI deployment role

The runner must not have:

- Database administrator credentials
- GitHub App private key
- Broad cloud permissions

### Terraform modules

```text
network
database
object_storage
queue
secrets
container_registry
control_plane
runner_cluster
observability
dns_tls
github_oidc_ci
```

Environments:

- local
- dev
- prod


## Kubernetes Job Example

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: agent-trail-task-<attempt-id>
  namespace: agent-trail-runners
  labels:
    app.kubernetes.io/name: agent-trail-runner
    agent-trail.dev/task-attempt-id: "<attempt-id>"
spec:
  ttlSecondsAfterFinished: 3600
  backoffLimit: 0
  activeDeadlineSeconds: 2700
  template:
    metadata:
      labels:
        app.kubernetes.io/name: agent-trail-runner
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: runner
          image: <immutable-runner-image>
          resources:
            requests:
              cpu: "500m"
              memory: "1Gi"
            limits:
              cpu: "2"
              memory: "4Gi"
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          volumeMounts:
            - name: workspace
              mountPath: /workspace
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: workspace
          emptyDir:
            sizeLimit: 10Gi
        - name: tmp
          emptyDir:
            sizeLimit: 1Gi
```

Adjust based on the chosen agent CLI and package manager.

Do not mount the Docker socket.
