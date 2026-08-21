FROM golang:1.22.2 AS build-stage

WORKDIR /usr/src/app

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
ENV GOCACHE=/go-cache

COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY backend/. .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/go-cache \
    go build -o /output

# Now take that output file into the next stage
FROM build-stage AS run-stage
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/go-cache \
    go test -v ./...

# Deploy the application binary into a lean image
FROM alpine AS build-release-stage

RUN apk add curl

WORKDIR /

COPY --from=build-stage /output /output
COPY backend/sagawise/* /sagawise/
COPY services.json services.json

ENTRYPOINT [ "/output" ]
