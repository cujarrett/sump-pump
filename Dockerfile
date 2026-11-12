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
FROM ghcr.io/cujarrett/base-go:static-debian13@sha256:805eafa57ee884e338dfa8cfa7c7457df4ffe0b7a391e3a8f878bfb3db74d171

WORKDIR /app

COPY --from=builder /out/app .

EXPOSE 8080 9090

ENTRYPOINT ["./app"]
