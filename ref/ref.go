package main

import (
	"log"
	"sync"
	"time"

	zmq "github.com/pebbe/zmq4"
	"github.com/vmihailenco/msgpack/v5"
)

type Envelope struct {
	Service   string                 `msgpack:"service"`
	Data      map[string]interface{} `msgpack:"data"`
	Timestamp string                 `msgpack:"timestamp"`
	Clock     int                    `msgpack:"clock"`
}

type ServerInfo struct {
	Name     string `msgpack:"name"`
	Rank     int    `msgpack:"rank"`
	LastSeen int64  `msgpack:"last_seen"`
}

var (
	mu           sync.Mutex
	servers      = map[string]*ServerInfo{}
	nextRank     = 1
	logicalClock = 0
)

func now() string {
	return time.Now().Format(time.RFC3339Nano)
}

func incClock() int {
	mu.Lock()
	logicalClock++
	v := logicalClock
	mu.Unlock()
	return v
}

func updateClock(n int) {
	mu.Lock()
	if n > logicalClock {
		logicalClock = n
	}
	logicalClock++
	mu.Unlock()
}

func pruneLoop() {
	for {
		time.Sleep(5 * time.Second)

		mu.Lock()
		now := time.Now().Unix()

		for k, s := range servers {
			if now-s.LastSeen > 15 {
				log.Printf("[REF] Removendo servidor inativo: %s", k)
				delete(servers, k)
			}
		}

		mu.Unlock()
	}
}

func main() {

	log.Println("[REF] Iniciado em tcp://*:5550 (ZMQ REP)")

	go pruneLoop()

	ctx, _ := zmq.NewContext()
	defer ctx.Term()

	rep, _ := ctx.NewSocket(zmq.REP)
	defer rep.Close()
	rep.Bind("tcp://*:5550")

	for {
		raw, err := rep.RecvBytes(0)
		if err != nil {
			log.Println("erro recv:", err)
			continue
		}

		var req Envelope
		if err := msgpack.Unmarshal(raw, &req); err != nil {
			log.Println("[REF] Erro ao decodificar MessagePack:", err)
			// REP precisa responder SEMPRE
			rep.SendMessage([]byte("ERR"))
			continue
		}

		updateClock(req.Clock)

		resp := Envelope{
			Service:   req.Service,
			Data:      map[string]interface{}{},
			Timestamp: now(),
			Clock:     incClock(),
		}

		switch req.Service {

		case "rank":
			user := req.Data["user"].(string)

			mu.Lock()
			if _, exists := servers[user]; !exists {
				servers[user] = &ServerInfo{
					Name:     user,
					Rank:     nextRank,
					LastSeen: time.Now().Unix(),
				}
				log.Printf("[REF] Novo servidor registrado: %s rank=%d", user, nextRank)
				nextRank++
			} else {
				servers[user].LastSeen = time.Now().Unix()
			}

			resp.Data["rank"] = servers[user].Rank
			mu.Unlock()

		case "heartbeat":
			user := req.Data["user"].(string)

			mu.Lock()
			if _, exists := servers[user]; !exists {
				servers[user] = &ServerInfo{
					Name:     user,
					Rank:     nextRank,
					LastSeen: time.Now().Unix(),
				}
				log.Printf("[REF] Novo servidor via heartbeat: %s rank=%d", user, nextRank)
				nextRank++
			} else {
				servers[user].LastSeen = time.Now().Unix()
			}
			resp.Data["status"] = "ok"
			mu.Unlock()

		case "list":
			mu.Lock()
			l := []map[string]interface{}{}
			for _, s := range servers {
				l = append(l, map[string]interface{}{
					"name": s.Name,
					"rank": s.Rank,
				})
			}
			mu.Unlock()
			resp.Data["list"] = l

		default:
			resp.Data["error"] = "serviço desconhecido"
		}

		out, _ := msgpack.Marshal(resp)
		rep.SendBytes(out, 0)
	}
}
