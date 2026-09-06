# build context is the repo root: docker build -f deploy/docker/Dockerfile --target <t> .

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

FROM alpine:3.23 AS control-plane
RUN apk add --no-cache ca-certificates wget \
    && addgroup -g 65532 nonroot && adduser -D -u 65532 -G nonroot nonroot
COPY --from=build /out/api /usr/local/bin/api
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]

FROM alpine:3.23 AS runner
RUN apk add --no-cache ca-certificates git \
    && addgroup -g 65532 nonroot && adduser -D -u 65532 -G nonroot nonroot
COPY --from=build /out/worker /usr/local/bin/worker
COPY deploy/k8s/runner/job.yaml /etc/agent-trail/runner-job.yaml
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/worker"]

# git-daemon carries http-backend for the fixture's smart http
FROM alpine:3.23 AS tools
RUN apk add --no-cache ca-certificates git git-daemon wget \
    && addgroup -g 65532 nonroot && adduser -D -u 65532 -G nonroot nonroot
COPY --from=build /out/migrate /out/seed /out/fixture-github /usr/local/bin/
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/migrate"]
