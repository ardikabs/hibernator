# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS builder
WORKDIR /workspace

# Cache deps
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build controller
FROM builder AS build-controller
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /controller ./cmd/controller

# Build runner
FROM builder AS build-runner
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /runner ./cmd/runner

# Controller image
FROM gcr.io/distroless/static:nonroot AS controller
WORKDIR /
COPY --from=build-controller /controller /controller
USER 65532:65532
ENTRYPOINT ["/controller"]

# Runner image
FROM gcr.io/distroless/static:nonroot AS runner
WORKDIR /
COPY --from=build-runner /runner /runner
USER 65532:65532
ENTRYPOINT ["/runner"]
