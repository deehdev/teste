const zmq = require("zeromq");

async function main() {
  const sub = new zmq.Subscriber();

  sub.connect("tcp://proxy:5558");

  const topic = process.argv[2];
  if (!topic) {
    console.error("Use: node subscriber.js <topic>");
    process.exit(1);
  }

  sub.subscribe(topic);

  console.log("Subscribed to:", topic);

  for await (const [t, msg] of sub) {
    console.log(`[${t.toString()}] ${msg.toString()}`);
  }
}

main();
