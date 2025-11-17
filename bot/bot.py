import zmq
import msgpack
import random
import threading
import time
from datetime import datetime, timezone

# ---------------------------------------------------
# CONFIG
# ---------------------------------------------------
REQ_ADDR = "tcp://broker:5555"
SUB_ADDR = "tcp://proxy:5560"

# ---------------------------------------------------
# RELOGIO LOGICO
# ---------------------------------------------------
logical_clock = 0
clock_lock = threading.Lock()

def inc_clock():
    global logical_clock
    with clock_lock:
        logical_clock += 1
        return logical_clock

def update_clock(recv):
    global logical_clock
    with clock_lock:
        try:
            rc = int(recv)
            logical_clock = max(logical_clock, rc) + 1
        except:
            pass
        return logical_clock

def now_iso():
    return datetime.now(timezone.utc).isoformat()

# ---------------------------------------------------
# REQUEST (REQ → REP)
# ---------------------------------------------------
def send_req(sock, service, data=None):
    if data is None:
        data = {}

    env = {
        "service": service,
        "data": data,
        "timestamp": now_iso(),
        "clock": inc_clock()
    }

    encoded = msgpack.packb(env, use_bin_type=True)
    sock.send(encoded)

    raw = sock.recv()
    reply = msgpack.unpackb(raw, raw=False)

    # clock da resposta
    update_clock(reply.get("clock", 0))

    return reply

# ---------------------------------------------------
# SUB LISTENER (somente mensagens úteis)
# ---------------------------------------------------
def sub_listener(sub, username):
    while True:
        try:
            raw = sub.recv()
            env = msgpack.unpackb(raw, raw=False)

            service = env.get("service", "")
            data = env.get("data", {})

            update_clock(env.get("clock", 0))

            # PUBLICAÇÃO EM CANAL
            if service == "publish":
                canal = data.get("channel")
                user = data.get("user")
                msg = data.get("msg")
                print(f"[{canal}] {user}: {msg}")

            # MENSAGEM PRIVADA
            elif service == "message":
                src = data.get("src")
                msg = data.get("msg")
                print(f"💌 PRIVADA de {src}: {msg}")

        except Exception as e:
            print("Erro SUB:", e)
            time.sleep(1)

# ---------------------------------------------------
# HEARTBEAT
# ---------------------------------------------------
def heartbeat(username):
    ctx = zmq.Context()
    hb = ctx.socket(zmq.REQ)
    hb.connect(REQ_ADDR)

    while True:
        try:
            send_req(hb, "heartbeat", {"user": username})
        except:
            pass
        time.sleep(5)

# ---------------------------------------------------
# FRASES ALEATÓRIAS
# ---------------------------------------------------
def frase():
    frases = [
        "Alguém viu algum filme bom?",
        "Preciso de uma recomendação urgente.",
        "Esse mês saiu muito filme bom!",
        "Vocês preferem dublado ou legendado?",
        "Estou revendo um clássico hoje.",
        "Recomendações de terror psicológico?",
        "Interstellar é perfeito.",
        "Quero algo leve!",
        "Alguém entendeu Tenet?",
        "La La Land tem a melhor trilha sonora!"
    ]
    return random.choice(frases)

# ---------------------------------------------------
# BOT PRINCIPAL
# ---------------------------------------------------
NOMES = [
    "Ana","Pedro","Rafael","Deise","Camila","Victor","Paula","Juliana",
    "Lucas","Marcos","Mateus","João","Carla","Bruno","Renata","Sofia"
]

def main():
    username = random.choice(NOMES)

    print("\n======================================")
    print(f"🤖 BOT iniciado como {username}")
    print("======================================\n")

    ctx = zmq.Context()

    req = ctx.socket(zmq.REQ)
    req.connect(REQ_ADDR)

    sub = ctx.socket(zmq.SUB)
    sub.connect(SUB_ADDR)
    # ❗ SOMENTE tópicos necessários (nada de subscribir tudo)
    
    # LOGIN
    r = send_req(req, "login", {"user": username})
    ok = r.get("data", {}).get("status", "erro")

    print("LOGIN:", ok)

    # ASSINA O PRÓPRIO USUÁRIO (mensagens privadas)
    sub.setsockopt_string(zmq.SUBSCRIBE, username)

    # HEARTBEAT
    threading.Thread(target=heartbeat, args=(username,), daemon=True).start()

    # LISTAR CANAIS
    r = send_req(req, "channels")
    channels = r.get("data", {}).get("channels", [])

    # cria canal default caso não exista
    if not channels:
        send_req(req, "channel", {"name": "Geral"})
        channels = ["Geral"]

    # escolhe alguns canais
    salas = random.sample(channels, random.randint(1, len(channels)))

    print("Canais inscritos:", salas)

    # SUBSCRIBE nos canais
    for c in salas:
        sub.setsockopt_string(zmq.SUBSCRIBE, c)
        send_req(req, "subscribe", {"topic": c})

    # inicia thread SUB
    threading.Thread(target=sub_listener, args=(sub, username), daemon=True).start()

    # LOOP PRINCIPAL DO BOT
    while True:
        canal = random.choice(salas)
        text = frase()

        send_req(req, "publish", {
            "user": username,
            "channel": canal,
            "msg": text
        })

        print(f"[{canal}] {username}: {text}")

        time.sleep(random.uniform(3, 7))

if __name__ == "__main__":
    main()
