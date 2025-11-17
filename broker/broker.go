package main

import (
	"log"

	zmq "github.com/pebbe/zmq4"
)

func main() {
	// Cria contexto
	ctx, err := zmq.NewContext()
	if err != nil {
		log.Fatal("Erro criando contexto ZMQ:", err)
	}
	defer ctx.Term()

	// Recebe SUB dos servidores (PUB deles)
	xsub, err := ctx.NewSocket(zmq.XSUB)
	if err != nil {
		log.Fatal("Erro criando XSUB:", err)
	}
	defer xsub.Close()

	// Envia PUB para bots/clients (SUB)
	xpub, err := ctx.NewSocket(zmq.XPUB)
	if err != nil {
		log.Fatal("Erro criando XPUB:", err)
	}
	defer xpub.Close()

	// Liga portas
	if err := xsub.Bind("tcp://*:5557"); err != nil {
		log.Fatal("Erro ao fazer bind XSUB:", err)
	}
	if err := xpub.Bind("tcp://*:5558"); err != nil {
		log.Fatal("Erro ao fazer bind XPUB:", err)
	}

	log.Println("Proxy XPUB/XSUB iniciado na rota 5557 <-> 5558")

	// SUBSCRIBE global (evita travar PUBs até o primeiro SUB real)
	xsub.SendBytes([]byte{1}, 0)

	// Loop do proxy
	for {
		if err := zmq.Proxy(xsub, xpub, nil); err != nil {
			log.Println("Erro no proxy — reiniciando:", err)
		}
	}
}
