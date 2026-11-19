# One Dockerfile for both binaries - they differ only in which cmd is built.
# Pass --build-arg BINARY=bridge or consumer.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS builder

ARG TARGETOS
ARG TARGETARCH
ARG BINARY

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=0.1.0" -o /out/app ./cmd/${BINARY}

# ---- runtime ----
FROM ghcr.io/cujarrett/base-distroless:static-debian13@sha256:a359c6ce58f19d70594119fa4d2233f0d27d93bce787328a4936fd1f995cc779

WORKDIR /app

COPY --from=builder /out/app .

EXPOSE 8080 9090

ENTRYPOINT ["./app"]
