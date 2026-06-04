# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26.4

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder

RUN apk add --no-cache ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
ARG REVISION=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
      -mod=readonly \
      -trimpath \
      -ldflags="-s -w -X github.com/xciber/imperva-exporter/cmd.version=${VERSION} -X github.com/prometheus/common/version.Version=${VERSION} -X github.com/prometheus/common/version.Revision=${REVISION}" \
      -o /out/imperva-exporter .

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /out/imperva-exporter /app/imperva-exporter

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/app/imperva-exporter"]
