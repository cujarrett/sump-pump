# One Dockerfile for both binaries - they differ only in which cmd is built.
# Pass --build-arg BINARY=bridge or consumer.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

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
FROM alpine:3.24

RUN addgroup -S app && adduser -S app -G app

WORKDIR /app

COPY --from=builder /out/app .

# Numeric, not the name - the kubelet cannot verify runAsNonRoot for a user it
# cannot resolve, and fails the container closed. Same uid the account already has.
USER 100

EXPOSE 8080 9090

ENTRYPOINT ["./app"]
