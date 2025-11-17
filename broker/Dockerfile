FROM golang:1.23-bullseye AS builder

RUN apt-get update && apt-get install -y \
    libzmq3-dev \
    libsodium-dev \
    pkg-config \
    git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o broker .

FROM debian:bullseye-slim

RUN apt-get update && apt-get install -y \
    libzmq5 \
    libsodium23

WORKDIR /app
COPY --from=builder /app/broker .

CMD ["./broker"]
