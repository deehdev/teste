# --------------------------------------------------------
# STAGE 1 — BUILD
# --------------------------------------------------------
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Dependências necessárias para ZMQ + CGO
RUN apk add --no-cache \
    gcc \
    g++ \
    make \
    pkgconfig \
    zeromq \
    zeromq-dev

# Baixar dependências do Go
COPY go.mod go.sum ./
RUN go mod download

# Copiar código do servidor
COPY . .

# Compilar binário (CGO ENABLED, necessário para libzmq)
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o server_unified .

# --------------------------------------------------------
# STAGE 2 — RUNTIME
# --------------------------------------------------------
FROM alpine:3.19

WORKDIR /app

# Biblioteca runtime do ZeroMQ
RUN apk add --no-cache zeromq

# Copiar binário compilado
COPY --from=builder /app/server_unified /usr/local/bin/server_unified

# Criar diretório persistente para dados
RUN mkdir -p /app/data

# Porta REP (servidor unificado usa variável SERVER_REP_PORT)
EXPOSE 7000
EXPOSE 7001
EXPOSE 7002

ENTRYPOINT ["server_unified"]
