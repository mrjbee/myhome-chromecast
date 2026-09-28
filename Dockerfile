# syntax=docker/dockerfile:1

ARG GO_VERSION=1.25
ARG ALPINE_VERSION=3.22

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" \
    -o /out/myhome-chromecast ./cmd/myhome-chromecast

FROM alpine:${ALPINE_VERSION}

RUN apk add --no-cache ca-certificates

COPY --from=build /out/myhome-chromecast /usr/local/bin/myhome-chromecast

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/myhome-chromecast"]
