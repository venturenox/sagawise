FROM golang:1.22.2 AS build-stage

WORKDIR /usr/src/app

COPY backend/. .

RUN go mod tidy

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
RUN go env -w GOCACHE=/go-cache

RUN --mount=type=cache,target=/go-cache go build -o /output

# Now take that output file into the next stage
FROM build-stage AS run-stage
RUN go test -v ./...

# Deploy the application binary into a lean image
FROM gcr.io/distroless/base-debian11 AS build-release-stage

WORKDIR /

COPY --from=build-stage /output /output
COPY backend/sagawise/* /sagawise/
COPY services.json services.json

ENTRYPOINT [ "/output" ]
