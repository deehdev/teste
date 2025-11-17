#!/usr/bin/env python3
import os 
import zmq
import msgpack
import random
import threading
import time
from datetime import datetime, timezone
import sys
sys.stdout.reconfigure(line_buffering=True)

# ---------------------------------------------------
# CONFIG (padrões com override por env)
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
            rc = int(recv)
            logical_clock = max(logical_clock, rc) + 1
        except:
            logical_clock += 1
        return logical_clock

def now_iso():
    return datetime.now(timezone.utc).isoformat()

# ---------------------------------------------------
# REQUEST (REQ → REP) com poller/timeout
# ---------------------------------------------------
def send_req(sock, service, data=None, timeout=5.0):
    if data is None:
        data = {}

    env = {
        "service": service,
        "data": data,
        "timestamp": now_iso(),
        "clock": inc_clock()
    }

    encoded = msgpack.packb(env, use_bin_type=True)
    try:
        # enviar
        sock.send(encoded)
    except Exception as e:
        return {"service":"error","data":{"status":str(e)}}

    # esperar reply com poller
    try:
        poller = zmq.Poller()
        poller.register(sock, zmq.POLLIN)
        socks = dict(poller.poll(int(timeout*1000)))
        if socks.get(sock) == zmq.POLLIN:
            raw = sock.recv()
            reply = msgpack.unpackb(raw, raw=False)
            update_clock(reply.get("clock", 0))
            return reply
        else:
            return {"service":"error","data":{"status":"timeout"}}
    except Exception as e:
        return {"service":"error","data":{"status":str(e)}}

# ---------------------------------------------------
# SUB LISTENER (trata multipart [topic, payload])
# ---------------------------------------------------
def sub_listener(sub):
    while True:
        try:
            parts = sub.recv_multipart()
            if len(parts) < 2:
                continue
            topic = parts[0].decode('utf-8', errors='ignore')
            payload = parts[1]
            env = msgpack.unpackb(payload, raw=False)
            update_clock(env.get("clock", 0))

            svc = env.get("service", "")
            data = env.get("data", {})

            # mensagens de publicação em canal
            if svc == "publish":
                # servidor pode usar keys "message" ou "msg" dependendo do código
                user = data.get("user") or data.get("src") or "?"
                message = data.get("message") or data.get("msg") or ""
                print(f"[{topic}] {user}: {message}")

            # mensagens privadas (tópico = username)
            elif svc == "message":
                src = data.get("src") or data.get("user") or "?"
                message = data.get("message") or data.get("msg") or ""
                print(f"💌 PRIVADA de {src}: {message}")

            # replicação / servidores / outros tópicos
            else:
                print(f"[{topic}][{svc}] {data}")

        except Exception as e:
            # não mata a thread em caso de erro temporário
            print("Erro no SUB:", e)
            time.sleep(0.5)

# ---------------------------------------------------
# HEARTBEAT (opcional)
# ---------------------------------------------------
def heartbeat(username):
    ctx = zmq.Context()
    hb = ctx.socket(zmq.REQ)
    hb.setsockopt(zmq.LINGER, 0)
    hb.connect(REQ_ADDR)
    # espera curta para estabilizar
    time.sleep(0.05)
    while True:
        try:
            send_req(hb, "heartbeat", {"user": username}, timeout=2.0)
        except Exception:
            pass
        time.sleep(5)

# ---------------------------------------------------
# Frases e nomes
# ---------------------------------------------------
def frase():
    frases = [
        "Alguém viu algum filme bom?",
        "Preciso de uma recomendação urgente.",
        "Esse mês saiu muito filme bom!",
        "Vocês preferem dublado ou legendado?",
        "Interstellar é perfeito.",
        "Quero algo leve!",
        "Alguém entendeu Tenet?",
        "Recomendações de terror psicológico?"
    ]
    return random.choice(frases)

NOMES = ["Ana","Pedro","Rafael","Deise","Camila","Victor","Paula","Juliana","Lucas","Marcos","Mateus","João","Carla","Bruno","Renata","Sofia"]

# ---------------------------------------------------
# BOT
# ---------------------------------------------------
def main():
    username = random.choice(NOMES)
    print(f"\nBOT iniciado como {username}\n")

    ctx = zmq.Context()

    # REQ socket (clientes -> servidor)
    req = ctx.socket(zmq.REQ)
    req.setsockopt(zmq.LINGER, 0)
    req.connect(REQ_ADDR)
    time.sleep(0.05)  # dar tempo para o connect estabilizar

    # SUB socket (recebe do proxy XPUB)
    sub = ctx.socket(zmq.SUB)
    sub.setsockopt(zmq.LINGER, 0)
    sub.connect(SUB_ADDR)
    time.sleep(0.05)

    # login
    r = send_req(req, "login", {"user": username})
    status = r.get("data", {}).get("status", "erro")
    print("LOGIN:", status)

    # subscreve tópicos privados e canais
    try:
        sub.setsockopt_string(zmq.SUBSCRIBE, username)
    except Exception as e:
        print("Erro subscribe user:", e)

    # heartbeat (opcional)
    threading.Thread(target=heartbeat, args=(username,), daemon=True).start()

    # lista canais
    r = send_req(req, "channels")
    channels = r.get("data", {}).get("channels", [])
    if not channels:
        # o servidor espera a key "channel" para criar canal
        send_req(req, "channel", {"channel": "geral"})
        channels = ["geral"]

    # escolhe subscrições e solicita ao servidor subscribe
    # usar pelo menos 1 canal
    salas = random.sample(channels, k=max(1, min(len(channels), 1)))
    for c in salas:
        try:
            sub.setsockopt_string(zmq.SUBSCRIBE, c)
        except Exception as e:
            print("Erro subscribe canal:", e)
        send_req(req, "subscribe", {"user": username, "topic": c})

    print("Canais inscritos:", salas)

    # start sub listener
    threading.Thread(target=sub_listener, args=(sub,), daemon=True).start()

    # loop publica
    while True:
        canal = random.choice(salas)
        text = frase()
        send_req(req, "publish", {"user": username, "channel": canal, "message": text})
        print(f"[{canal}] {username}: {text}")
        time.sleep(random.uniform(3, 6))

if __name__ == "__main__":
    main()
