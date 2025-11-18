# --------------------------------------------------------
# STAGE 1 — BUILD
# --------------------------------------------------------
FROM golang:1.22-alpine AS builder

WORKDIR /app

RUN apk add --no-cache \
    gcc g++ make pkgconfig zeromq zeromq-dev

# Copiar arquivos de módulos
COPY go.mod go.sum ./
RUN go mod download

# Copiar código
COPY . .

# Build
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o server .

# --------------------------------------------------------
# STAGE 2 — RUNTIME
# --------------------------------------------------------
FROM alpine:3.19

WORKDIR /app

RUN apk add --no-cache zeromq

COPY --from=builder /app/server /usr/local/bin/server

RUN mkdir -p /app/data

EXPOSE 7000 7001 7002

ENTRYPOINT ["server"]
