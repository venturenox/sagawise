# =============================================================================
# Stage 1: Build Stage
# Compiles the Go application with optimized caching:
# - Dependencies (go.mod/go.sum) are copied and downloaded first
# - Source code is copied separately to invalidate cache only on code changes
# - Build cache is mounted to speed up subsequent builds
# =============================================================================
FROM golang:1.22.2 AS build-stage

WORKDIR /usr/src/app

<<<<<<< HEAD
=======
# Disable CGO for a static binary (no external libc dependency)
# Set target OS and architecture for cross-compilation
>>>>>>> main
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
ENV GOCACHE=/go-cache

<<<<<<< HEAD
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY backend/. .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/go-cache \
    go build -o /output
=======
# Copy dependency files first (cached unless go.mod/go.sum change)
COPY backend/go.mod backend/go.sum ./
>>>>>>> main

# Download Go dependencies (cached unless dependencies change)
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Copy source code (changes here invalidate this layer and subsequent layers)
COPY backend/. .

# Build the application binary
# - Cache mounts preserve build artifacts across builds for faster rebuilds
# - Output binary is placed at /output for the next stage
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/go-cache \
    go build -o /output

# =============================================================================
# Stage 2: Run Stage (Test Only)
# Runs tests using the compiled binary from build-stage
# This stage validates the build before creating the release image
# =============================================================================
FROM build-stage AS run-stage
<<<<<<< HEAD
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/go-cache \
    go test -v ./...
=======
>>>>>>> main

# Run tests (cached unless source code changes)
# Note: This runs "go test" which compiles and executes tests
# The cache mounts optimize repeated test runs
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/go-cache \
    go test -v ./...

# =============================================================================
# Stage 3: Release Stage
# Creates a minimal production image with only the runtime dependencies:
# - Alpine Linux base (small footprint)
# - Only the compiled binary, DSL files, and services.json
# - No Go toolchain or source code included
# =============================================================================
FROM alpine AS build-release-stage

<<<<<<< HEAD
=======
# Install curl for health checks (liveness/readiness probes in Kubernetes)
>>>>>>> main
RUN apk add curl

WORKDIR /

# Copy the compiled binary from the build-stage
COPY --from=build-stage /output /output

# Copy workflow DSL files (JSON definitions for sagawise workflows)
COPY backend/sagawise/* /sagawise/

# Copy services configuration (failure webhook URLs for each service)
COPY services.json services.json

# The executable runs when the container starts
# ENTRYPOINT is the final command executed at container runtime
ENTRYPOINT [ "/output" ]
