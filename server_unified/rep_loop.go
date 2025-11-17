package main

import (
    "log"

    zmq "github.com/pebbe/zmq4"
)

// ===============================================
//  REP LOOP — Recebe REQ dos clientes (login, publish, ...)
//
//  Este é o *coração* do servidor. Sem ele o server-unified
//  não responde e o client fica travado.
// ===============================================
func repLoop(rep *zmq.Socket, pub *zmq.Socket) {
    for {
        // ----------------------------------------
        // 1) RECEBE MENSAGEM DO CLIENTE (REQ → REP)
        // ----------------------------------------
        raw, err := rep.RecvMessageBytes(0)
        if err != nil {
            log.Println("[REP LOOP][ERRO] recv:", err)
            continue
        }
        if len(raw) == 0 {
            continue
        }

        // ----------------------------------------
        // 2) DECODIFICA ENVELOPE (MessagePack)
        // ----------------------------------------
        env, err := decodeEnvelope(raw[0])
        if err != nil {
            log.Println("[REP LOOP][ERRO] decode:", err)
            continue
        }

        // Atualiza Logical Clock
        updateClock(env.Clock)

        // ----------------------------------------
        // 3) PROCESSO DO SERVIÇO (dispatcher)
        // ----------------------------------------
        var resp Envelope

        switch env.Service {

        case "login":
            resp = handleLogin(env)

        case "channels":
            resp = handleListChannels(env)

        case "channel":
            // criar canal
            resp = handleCreateChannel(env)

        case "publish":
            // publicar mensagem no canal
            resp = handlePublish(env)

        case "message":
            // mensagem privada
            resp = handleMessage(env)

        case "subscribe":
            resp = handleSubscribe(env)

        case "unsubscribe":
            resp = handleUnsubscribe(env)

        case "heartbeat":
            resp = handleHeartbeat(env)

        case "sync_request":
            resp = handleSyncRequest(env)

        default:
            resp = Envelope{
                Service: env.Service,
                Timestamp: nowISO(),
                Clock:     incClockBeforeSend(),
                Data: map[string]interface{}{
                    "status": "erro",
                    "message": "serviço desconhecido",
                },
            }
        }

        // ----------------------------------------
        // 4) Envia resposta (REP → REQ)
        // ----------------------------------------
        encoded := msgpackMarshal(resp)
        rep.SendMessage(encoded)
    }
}
