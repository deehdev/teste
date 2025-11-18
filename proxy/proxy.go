package main

import (
	"log"
	"os"
	"time"

	zmq "github.com/pebbe/zmq4"
	"github.com/vmihailenco/msgpack/v5"
)

func main() {
	// Endereços do XSUB (onde os servidores publicam) e XPUB (onde clientes se inscrevem)
	xsubAddr := os.Getenv("XSUB_ADDR")
	if xsubAddr == "" {
		xsubAddr = "tcp://*:5557"
	}

	xpubAddr := os.Getenv("XPUB_ADDR")
	if xpubAddr == "" {
		xpubAddr = "tcp://*:5558"
	}

	log.Println("--- INICIANDO PROXY PUB/SUB ---")
	log.Printf("XSUB (Servidor publica em) %s", xsubAddr)
	log.Printf("XPUB (Cliente se inscreve em) %s", xpubAddr)

	ctx, err := zmq.NewContext()
	if err != nil {
		log.Fatal("Erro criando contexto ZMQ:", err)
	}
	defer ctx.Term()

	// Socket XSUB: servidores publicam aqui
	xsub, err := ctx.NewSocket(zmq.XSUB)
	if err != nil {
		log.Fatal("Erro criando XSUB:", err)
	}
	defer xsub.Close()
	if err := xsub.Bind(xsubAddr); err != nil {
		log.Fatal("Erro bind XSUB:", err)
	}

	// Socket XPUB: clientes/bots se inscrevem aqui
	xpub, err := ctx.NewSocket(zmq.XPUB)
	if err != nil {
		log.Fatal("Erro criando XPUB:", err)
	}
	defer xpub.Close()
	if err := xpub.Bind(xpubAddr); err != nil {
		log.Fatal("Erro bind XPUB:", err)
	}

	log.Println("✅ Proxy PUB/SUB iniciado")

	// Proxy puro do ZeroMQ: roteia mensagens do XSUB para XPUB e vice-versa
	// Aqui o ZeroMQ já faz todo o roteamento de tópicos automaticamente
	if err := zmq.Proxy(xsub, xpub, nil); err != nil {
		log.Println("⚠️ Proxy PUB/SUB encerrou com erro:", err)
		time.Sleep(1 * time.Second)
	}
}

// Função auxiliar para enviar mensagens serializadas via MessagePack
func sendMessage(socket *zmq.Socket, topic string, data any) error {
	// Serializa com MessagePack
	bytes, err := msgpack.Marshal(data)
	if err != nil {
		return err
	}

	// Envia multipart: primeiro o tópico, depois os dados
	_, err = socket.SendMessage(topic, bytes)
	return err
}

// Função auxiliar para receber mensagens serializadas via MessagePack
func receiveMessage(msgParts [][]byte, v any) error {
	if len(msgParts) < 2 {
		return nil
	}
	return msgpack.Unmarshal(msgParts[1], v)
}
