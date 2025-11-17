package main

import (
    "log"
    "os"
    "sync/atomic"

    zmq "github.com/pebbe/zmq4"
)

var counter uint64

func main() {
    xsubPort := os.Getenv("XSUB_PORT")
    xpubPort := os.Getenv("XPUB_PORT")

    if xsubPort == "" {
        xsubPort = "5557"
    }
    if xpubPort == "" {
        xpubPort = "5558"
    }

    ctx, err := zmq.NewContext()
    if err != nil {
        log.Fatal("[BROKER] Erro contexto:", err)
    }

    xsub, err := ctx.NewSocket(zmq.XSUB)
    if err != nil {
        log.Fatal("[BROKER] Erro XSUB:", err)
    }
    defer xsub.Close()

    xpub, err := ctx.NewSocket(zmq.XPUB)
    if err != nil {
        log.Fatal("[BROKER] Erro XPUB:", err)
    }
    defer xpub.Close()

    if err := xsub.Bind("tcp://*:" + xsubPort); err != nil {
        log.Fatal("[BROKER] Erro bind XSUB:", err)
    }
    if err := xpub.Bind("tcp://*:" + xpubPort); err != nil {
        log.Fatal("[BROKER] Erro bind XPUB:", err)
    }

    log.Printf("Broker XPUB/XSUB ativo — roteando %s <-> %s\n", xsubPort, xpubPort)

    // Subscrição global para não travar PUB antes do primeiro SUB
    xsub.SendBytes([]byte{1}, 0)

    poller := zmq.NewPoller()
    poller.Add(xsub, zmq.POLLIN)
    poller.Add(xpub, zmq.POLLIN)

    for {
        events, err := poller.Poll(-1)
        if err != nil {
            log.Println("[BROKER] Erro no poll:", err)
            continue
        }

        for _, evt := range events {

            sock := evt.Socket

            // Mensagens vindo dos servidores → enviadas aos clientes
            if sock == xsub {
                msg, _ := sock.RecvBytes(0)
                atomic.AddUint64(&counter, 1)
                xpub.SendBytes(msg, 0)
            }

            // Mensagens de SUBSCRIÇÃO vindo dos clientes → enviadas aos servidores
            if sock == xpub {
                msg, _ := sock.RecvBytes(0)
                xsub.SendBytes(msg, 0)
            }
        }
    }
}
