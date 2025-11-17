# ----------------------------
# STAGE 1 — Build
# ----------------------------
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Instalar dependências necessárias
RUN apk add --no-cache gcc g++ make pkgconfig zeromq zeromq-dev

# Copiar código
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Compilar binário estático (importante para Alpine)
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o ref .

# ----------------------------
# STAGE 2 — Runtime
# ----------------------------
FROM alpine:3.19

WORKDIR /app

# Instalar libs runtime do ZeroMQ
RUN apk add --no-cache zeromq

COPY --from=builder /app/ref /usr/local/bin/ref

EXPOSE 6000

ENTRYPOINT ["ref"]
