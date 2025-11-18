// =====================================================
// CLIENTE INTERATIVO (REPL) — TOTALMENTE CORRIGIDO
// =====================================================

const zmq = require("zeromq");
const msgpack = require("@msgpack/msgpack");
const readline = require("readline");

// -------------------------------
// Relógio lógico
// -------------------------------
let clock = 0;
function incClock() {
  clock++;
  return clock;
}
function updateClock(received) {
  clock = Math.max(clock, received) + 1;
}

// -------------------------------
// Conexão REQ → Broker
// -------------------------------
const REQ_ADDR = process.env.REQ_ADDR || "tcp://broker:5555";
const req = new zmq.Request();

let busy = false; // evita dois comandos ao mesmo tempo

async function connect() {
  await req.connect(REQ_ADDR);
  console.log("📡 Conectado ao broker em", REQ_ADDR);
}

// -------------------------------
// Envio de mensagens
// -------------------------------
async function send(service, data = {}) {
  if (busy) throw new Error("O socket ainda está processando o comando anterior.");
  busy = true;

  try {
    data.timestamp = new Date().toISOString();
    data.clock = incClock();

    const env = { service, data };

    await req.send(msgpack.encode(env));
    const reply = await req.receive();

    const decoded = msgpack.decode(reply[0]);
    updateClock(decoded.data.clock);

    return decoded;
  } finally {
    busy = false;
  }
}

// -------------------------------
// Comandos
// -------------------------------
async function cmdLogin(args) {
  const user = args[0];
  if (!user) return console.log("Uso: login <nome>");
  console.log(await send("login", { user }));
}

async function cmdChannel(args) {
  const name = args[0];
  if (!name) return console.log("Uso: channel <nome>");
  console.log(await send("channel", { channel: name }));
}

async function cmdChannels() {
  console.log(await send("channels"));
}

async function cmdUsers() {
  console.log(await send("users"));
}

async function cmdPublish(args) {
  const channel = args[0];
  const message = args.slice(1).join(" ");
  if (!channel || !message) return console.log("Uso: publish <canal> <mensagem>");
  console.log(await send("publish", { channel, message, user: "manual" }));
}

async function cmdMessage(args) {
  const dst = args[0];
  const message = args.slice(1).join(" ");
  if (!dst || !message) return console.log("Uso: message <destino> <mensagem>");
  console.log(await send("message", { src: "manual", dst, message }));
}

// -------------------------------
// Tabela de comandos
// -------------------------------
const commands = {
  login: cmdLogin,
  channel: cmdChannel,
  channels: cmdChannels,
  users: cmdUsers,
  publish: cmdPublish,
  message: cmdMessage
};

// -------------------------------
// REPL (com limpeza de caracteres invisíveis!)
// -------------------------------
const rl = readline.createInterface({
  input: process.stdin,
  output: process.stdout,
  prompt: "> "
});

async function startRepl() {
  rl.prompt();

  rl.on("line", async (line) => {
    // 🔥 CORREÇÃO CRÍTICA: remove lixo, tabs, \r, múltiplos espaços
    const clean = line.replace(/\s+/g, " ").trim();

    if (!clean.length) {
      console.log("Comando inválido.");
      return rl.prompt();
    }

    const tokens = clean.split(" ");
    const cmd = tokens[0].toLowerCase();
    const args = tokens.slice(1);

    const handler = commands[cmd];

    if (!handler) {
      console.log("Comando desconhecido.");
      return rl.prompt();
    }

    try {
      await handler(args); // garante ordem
    } catch (e) {
      console.log("Erro:", e.message);
    }

    rl.prompt();
  });
}

// -------------------------------
// Inicialização
// -------------------------------
(async () => {
  await connect();
  startRepl();
})();
