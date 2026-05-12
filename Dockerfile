FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /server ./cmd/server

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /server /server

RUN mkdir -p /data
VOLUME /data

EXPOSE 9090

ENV GATEWAY_PORT=9090
ENV DATA_DIR=/data

ENTRYPOINT ["/server"]
