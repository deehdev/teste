FROM node:18

WORKDIR /app

COPY package.json package-lock.json ./
RUN npm install

COPY . .

# IMPORTANTE: ENTRYPOINT faz rodar o client.js
ENTRYPOINT ["node", "client.js"]

# IMPORTANTE: CMD vazio permite STDIN interativo
CMD []
