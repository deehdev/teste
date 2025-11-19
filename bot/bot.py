#!/usr/bin/env python3
import os
import zmq
import msgpack
import random
import threading
import time
from datetime import datetime, timezone
import sys

# Saída instantânea
sys.stdout.reconfigure(line_buffering=True)

# ---------------------------------------------------
# CONFIG
# ---------------------------------------------------
REQ_ADDR = os.environ.get("REQ_ADDR", "tcp://broker:5555")
SUB_ADDR = os.environ.get("SUB_ADDR", "tcp://proxy:5558")

# ---------------------------------------------------
# RELÓGIO LÓGICO
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
            r = int(recv)
            logical_clock = max(logical_clock, r) + 1
        except:
            logical_clock += 1
        return logical_clock

def now_iso():
    return datetime.now(timezone.utc).isoformat()


# ---------------------------------------------------
# SEND REQUEST (REQ → REP)
# ---------------------------------------------------
def send_req(sock, service, data=None, timeout=5.0):
    if data is None:
        data = {}

    env = {
        "service": service,
        "data": data,
        "timestamp": now_iso(),
        "clock": inc_clock(),
    }

    encoded = msgpack.packb(env, use_bin_type=True)

    try:
        sock.send(encoded)
    except Exception as e:
        return {"service": "error", "data": {"status": str(e)}}

    poller = zmq.Poller()
    poller.register(sock, zmq.POLLIN)

    try:
        socks = dict(poller.poll(int(timeout * 1000)))
        if socks.get(sock) == zmq.POLLIN:
            raw = sock.recv()
            reply = msgpack.unpackb(raw, raw=False)
            update_clock(reply.get("clock", 0))
            return reply
        else:
            return {"service": "error", "data": {"status": "timeout"}}
    except:
        return {"service": "error", "data": {"status": "socket-fail"}}


# ---------------------------------------------------
# SUB Listener — imprime mensagens recebidas
# ---------------------------------------------------
def sub_listener(sub):
    while True:
        try:
            parts = sub.recv_multipart()
            if len(parts) < 2:
                continue

            topic = parts[0].decode().strip()
            env = msgpack.unpackb(parts[1], raw=False)

            clk = env.get("clock", 0)
            update_clock(clk)

            svc = env.get("service", "")
            data = env.get("data", {})
            ts = data.get("timestamp", "")

            if svc == "publish":
                print(f"[# {topic}] {data.get('user')}: {data.get('message')}   (ts={ts}, clock={clk})")

            elif svc == "message":
                print(f"💌 {data.get('src')} → você: {data.get('message')}   (ts={ts}, clock={clk})")

        except Exception as e:
            print("Erro SUB:", e)
            time.sleep(0.3)


# ---------------------------------------------------
# BOT PRINCIPAL
# ---------------------------------------------------
def main():
    NOMES = [
        "Ana","Pedro","Rafael","Deise","Camila","Victor","Paula",
        "Juliana","Lucas","Marcos","Mateus","João","Carla","Bruno",
        "Renata","Sofia"
    ]

    FRASES = [
        "Alguém viu algum filme bom?",
        "Preciso de uma recomendação urgente.",
        "Esse mês saiu muito filme bom!",
        "Vocês preferem dublado ou legendado?",
        "Interstellar é perfeito.",
        "Quero algo leve!",
        "Alguém entendeu Tenet?",
        "Recomendações de terror psicológico?"
    ]

    username = random.choice(NOMES)
    print(f"BOT iniciado como {username}")

    ctx = zmq.Context()

    # REQ
    req = ctx.socket(zmq.REQ)
    req.connect(REQ_ADDR)
    time.sleep(0.1)

    # SUB
    sub = ctx.socket(zmq.SUB)
    sub.connect(SUB_ADDR)
    time.sleep(0.1)

    # Login
    r = send_req(req, "login", {"user": username})
    print("LOGIN:", r.get("data", {}).get("status"))

    # Sempre ouvir mensagens privadas
    sub.setsockopt_string(zmq.SUBSCRIBE, username)

    # LISTAR canais do servidor
    r = send_req(req, "channels")
    canais = r.get("data", {}).get("channels", [])

    # Se não houver canais, cria "geral"
    if not canais:
        send_req(req, "channel", {"name": "geral"})
        canais = ["geral"]

    # Escolhe um canal para assinar
    canal_escolhido = random.choice(canais)
    sub.setsockopt_string(zmq.SUBSCRIBE, canal_escolhido)

    subscribed = set([username, canal_escolhido])
    print("Inscrito no canal:", canal_escolhido)

    # Thread para receber mensagens
    threading.Thread(target=sub_listener, args=(sub,), daemon=True).start()

    # LOOP principal
    while True:
        # Mensagem privada (40%)
        if random.random() < 0.4:
            dest = random.choice([n for n in NOMES if n != username])
            txt = random.choice(FRASES)

            send_req(req, "message", {"src": username, "dst": dest, "message": txt})
            print(f"💌 {username} → {dest}: {txt}")

        # Mensagem em canal (60%)
        else:
            if canal_escolhido not in subscribed:
                print(f"⚠ Voce não está inscrito no canal: {canal_escolhido}")
            else:
                txt = random.choice(FRASES)
                send_req(req, "publish", {
                    "user": username,
                    "channel": canal_escolhido,
                    "message": txt
                })
                print(f"[# {canal_escolhido}] {username}: {txt}")

        time.sleep(random.uniform(2.5, 5.5))


if __name__ == "__main__":
    main()
