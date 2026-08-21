# Multi-target build for the Go control plane and runner.
#   docker build --target control-plane -t agent-trail/control-plane .
#   docker build --target runner        -t agent-trail/runner .
#   docker build --target tools         -t agent-trail/tools .
# Build context is the repository root:
#   docker build -f deploy/docker/Dockerfile --target runner .

FROM golang:1.26-alpine3.23 AS build
WORKDIR /src
COPY apps/api/go.mod apps/api/go.sum ./
RUN go mod download
COPY apps/api/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/api ./cmd/api \
    && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/worker ./cmd/worker \
    && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/migrate ./cmd/migrate \
    && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/seed ./cmd/seed \
    && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/fixture-github ./cmd/fixture-github

# Control plane: static binary, no shell tools, non-root.
FROM alpine:3.23 AS control-plane
RUN apk add --no-cache ca-certificates wget \
    && addgroup -g 65532 nonroot && adduser -D -u 65532 -G nonroot nonroot
COPY --from=build /out/api /usr/local/bin/api
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]

# Runner: the worker plus git for mirror, worktree, and push operations.
# Runs read-only; /workspace and /tmp are pod-provided volumes.
FROM alpine:3.23 AS runner
RUN apk add --no-cache ca-certificates git \
    && addgroup -g 65532 nonroot && adduser -D -u 65532 -G nonroot nonroot
COPY --from=build /out/worker /usr/local/bin/worker
COPY deploy/k8s/runner/job.yaml /etc/agent-trail/runner-job.yaml
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/worker"]

# Tools: migrations, seeding, and the GitHub fixture for local verification.
# git-daemon carries git http-backend, which the fixture serves the sample
# repository through.
FROM alpine:3.23 AS tools
RUN apk add --no-cache ca-certificates git git-daemon wget \
    && addgroup -g 65532 nonroot && adduser -D -u 65532 -G nonroot nonroot
COPY --from=build /out/migrate /out/seed /out/fixture-github /usr/local/bin/
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/migrate"]
