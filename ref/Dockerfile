# ----------------------------
# STAGE 1 — Build
# ----------------------------
FROM golang:1.23 AS builder

WORKDIR /app

# Dependências ZeroMQ via APT (Ubuntu base)
RUN apt-get update && apt-get install -y \
    libzmq3-dev pkg-config build-essential && \
    apt-get clean

# Copiar dependências do Go
COPY go.mod go.sum ./
RUN go mod download

# Copiar código
COPY . .

# Compilar binário
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o ref .

# ----------------------------
# STAGE 2 — Runtime
# ----------------------------
FROM debian:12-slim

RUN apt-get update && apt-get install -y libzmq5 && apt-get clean

WORKDIR /app

COPY --from=builder /app/ref /usr/local/bin/ref

EXPOSE 5550

ENTRYPOINT ["ref"]
