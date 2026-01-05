# [Dockerfile]
FROM --platform=$BUILDPLATFORM golang:1.21-alpine AS builder
WORKDIR /app

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN if [ "$TARGETARCH" = "arm" ]; then \
        export GOARM=${TARGETVARIANT#v}; \
    fi; \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o slack-commander

FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/slack-commander /usr/local/bin/slack-commander
ENTRYPOINT ["slack-commander"]
