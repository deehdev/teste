# --------------------------------------------------------
# Python base leve
# --------------------------------------------------------
FROM python:3.12-alpine

WORKDIR /app

# Dependências necessárias para ZeroMQ (compilar pyzmq)
RUN apk add --no-cache \
    gcc \
    g++ \
    make \
    musl-dev \
    zeromq \
    zeromq-dev

# Copiar arquivos do bot
COPY requirements.txt .
COPY . .

# Instalar libs Python (pyzmq + msgpack)
RUN pip install --no-cache-dir -r requirements.txt

# Expor nada (bot não recebe conexões externas)
CMD ["python", "-u", "bot.py"]

