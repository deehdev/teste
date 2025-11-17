FROM golang:1.23 AS builder

# instalar dependências do ZMQ (OBRIGATÓRIO)
RUN apt-get update && apt-get install -y \
    libzmq3-dev \
    pkg-config \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# agora sim compila
RUN CGO_ENABLED=1 go build -o broker .

# ------------------------------
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y \
    libzmq5 && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /app/broker .

CMD ["./broker"]
