// client/client.js
// Cliente com Logical Clock + MessagePack + PUB/SUB + Envelopes
// Assinatura seletiva de tópicos (sem poluição)

const zmq = require("zeromq");
const msgpack = require("@msgpack/msgpack");
const readline = require("readline");

// ----------------------------------------------------
// RELÓGIO LÓGICO (Lamport)
// ----------------------------------------------------
let logicalClock = 0;

function incClock() {
  logicalClock++;
  return logicalClock;
}

function updateClock(recv) {
  logicalClock = Math.max(logicalClock, recv) + 1;
}

// ----------------------------------------------------
// CONFIG
// ----------------------------------------------------
const BROKER_REQ = "tcp://broker:5555";
const PROXY_SUB = "tcp://proxy:5560";

// ----------------------------------------------------
// SOCKETS
// ----------------------------------------------------
const req = new zmq.Request();
const sub = new zmq.Subscriber();
// ❗ NUNCA ASSINAR TUDO AQUI
// sub.subscribe();

// ----------------------------------------------------
// LOCK PARA EVITAR EBUSY
// ----------------------------------------------------
let busy = false;

// ----------------------------------------------------
// START
// ----------------------------------------------------
async function start() {
  console.log("Connected to server");

  await req.connect(BROKER_REQ);
  console.log("[CLIENT] REQ conectado ao broker");

  await sub.connect(PROXY_SUB);
  console.log("[CLIENT] SUB conectado ao proxy");

  startSubLoop();
  startInputLoop();
}

start();

// ----------------------------------------------------
// LOOP DO SUB
// ----------------------------------------------------
async function startSubLoop() {
  for await (const [msg] of sub) {
    try {
      const env = msgpack.decode(msg);

      updateClock(env.clock);

      console.log(
        `\n[SUB:${env.service}] recv_clock=${env.clock} local=${logicalClock}`,
        env.data
      );

      process.stdout.write("> ");
    } catch (err) {
      console.log("[CLIENT][SUB] Erro msgpack:", err);
    }
  }
}

// ----------------------------------------------------
// ENVIO COM LOCK (CORREÇÃO DO EBUSY)
// ----------------------------------------------------
async function send(service, data = {}) {
  if (busy) {
    console.log("⏳ Aguarde: a requisição anterior ainda está pendente");
    return;
  }
  busy = true;

  try {
    const envelope = {
      service,
      data,
      timestamp: new Date().toISOString(),
      clock: incClock()
    };

    const encoded = msgpack.encode(envelope);
    await req.send(encoded);

    const [replyBytes] = await req.receive();
    const reply = msgpack.decode(replyBytes);

    updateClock(reply.clock);

    console.log(
      `[REQ:${service}] local=${logicalClock} ← reply_clock=${reply.clock}`,
      reply.data
    );

    // -------------------------------------------
    // 🔥 ASSINA O PRÓPRIO USUÁRIO APÓS LOGIN
    // -------------------------------------------
    if (service === "login" && reply.data?.user) {
      sub.subscribe(reply.data.user);
      console.log(`📌 Assinado automaticamente o tópico privado: ${reply.data.user}`);
    }

    return reply;
  } catch (err) {
    console.log("[CLIENT][REQ ERRO]:", err);
  } finally {
    busy = false;
  }
}

// ----------------------------------------------------
// CLI
// ----------------------------------------------------
const rl = readline.createInterface({
  input: process.stdin,
  output: process.stdout
});

function startInputLoop() {
  rl.setPrompt("> ");
  rl.prompt();

  rl.on("line", async (line) => {
    const parts = line.trim().split(" ");
    const cmd = parts[0];

    try {
      switch (cmd) {
        // ---------------------
        // LOGIN
        // ---------------------
        case "login":
          await send("login", { user: parts[1] });
          break;

        // ---------------------
        // USERS
        // ---------------------
        case "users":
          await send("users");
          break;

        // ---------------------
        // CHANNELS
        // ---------------------
        case "channels":
          await send("channels");
          break;

        // ---------------------
        // CREATE CHANNEL
        // ---------------------
        case "channel":
          await send("channel", { name: parts[1] });
          break;

        // ---------------------
        // PUBLISH
        // ---------------------
        case "publish":
          await send("publish", {
            channel: parts[1],
            msg: parts.slice(2).join(" ")
          });
          break;

        // ---------------------
        // PRIVATE MESSAGE
        // ---------------------
        case "message":
          await send("message", {
            user: parts[1],
            msg: parts.slice(2).join(" ")
          });
          break;

        // ---------------------
        // SUBSCRIBE
        // ---------------------
        case "subscribe":
          await send("subscribe", { topic: parts[1] });
          sub.subscribe(parts[1]);
          console.log("📌 Agora ouvindo o tópico:", parts[1]);
          break;

        // ---------------------
        // UNSUBSCRIBE
        // ---------------------
        case "unsubscribe":
          await send("unsubscribe", { topic: parts[1] });
          sub.unsubscribe(parts[1]);
          console.log("❌ Cancelado tópico:", parts[1]);
          break;

        // ---------------------
        // MY SUBSCRIPTIONS
        // ---------------------
        case "mysubs":
          await send("mysubs");
          break;

        // ---------------------
        // LISTA DE TÓPICOS PUBLICÁVEIS
        // ---------------------
        case "publishable":
          await send("publishable");
          break;

        // ---------------------
        // CLOCK
        // ---------------------
        case "clock":
          console.log("Logical clock =", logicalClock);
          break;

        // ---------------------
        // EXIT
        // ---------------------
        case "exit":
          process.exit(0);

        // ---------------------
        default:
          console.log("Unknown command");
      }
    } catch (err) {
      console.log("[CLIENT] ERRO:", err);
    }

    rl.prompt();
  });
}
