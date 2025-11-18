package main

import (
	"log"

	zmq "github.com/pebbe/zmq4"
)

func main() {
	log.Println("[BROKER] INICIANDO...")

	ctx, _ := zmq.NewContext()
	defer ctx.Term()

	frontend, _ := ctx.NewSocket(zmq.ROUTER) // recebe REQ
	frontend.Bind("tcp://*:5555")
	log.Println("[BROKER] ROUTER 5555 OK")

	backend, _ := ctx.NewSocket(zmq.DEALER) // entrega aos servidores
	backend.Bind("tcp://*:6000")
	log.Println("[BROKER] DEALER 6000 OK")

	for {
		log.Println("[BROKER] Iniciando ciclo do Proxy...")
		err := zmq.Proxy(frontend, backend, nil)
		if err != nil {
			log.Println("[BROKER] ERRO NO PROXY:", err)
		} else {
			log.Println("[BROKER] Proxy retornou sem erro (NÃO DEVERIA)")
		}
	}
}
