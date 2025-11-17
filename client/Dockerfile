# --------------------------------------------------------
# Base Node.js leve
# --------------------------------------------------------
FROM node:20-alpine

# Diretório de trabalho
WORKDIR /app

# Instalar dependências do ZeroMQ
RUN apk add --no-cache \
    zeromq \
    zeromq-dev \
    python3 \
    make \
    g++ 

# Copiar apenas package.json para cache de dependências
COPY package*.json ./

# Instalar dependências do Node (zeromq + msgpack)
RUN npm install --production

# Copiar restante dos arquivos do cliente
COPY . .

# Comando padrão
CMD ["node", "client.js"]
