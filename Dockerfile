FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=0

RUN GOOS=$TARGETOS \
    GOARCH=$TARGETARCH \
    go build \
    -trimpath \
    -ldflags="-s -w -buildid=" \
    -o autoscaler ./cmd/autoscaler

FROM gcr.io/distroless/static-debian13

WORKDIR /app
COPY --from=builder /app/autoscaler .

USER root

ENTRYPOINT ["./autoscaler"]