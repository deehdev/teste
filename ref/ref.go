package main

import (
    "encoding/json"
    "log"
    "sync"
    "time"

    zmq "github.com/pebbe/zmq4"
)

type Envelope struct {
    Service   string                 `json:"service"`
    Data      map[string]interface{} `json:"data"`
    Timestamp string                 `json:"timestamp"`
    Clock     int                    `json:"clock"`
}

type ServerInfo struct {
    Name     string `json:"name"`
    Rank     int    `json:"rank"`
    Port     int    `json:"port"`
    LastSeen int64  `json:"last_seen"`
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

// Remove servidores inativos após 15s
func pruneLoop() {
    for {
        time.Sleep(5 * time.Second)
        mu.Lock()
        now := time.Now().Unix()
        for k, s := range servers {
            if now-s.LastSeen > 15 {
                log.Println("[REF] Removendo servidor inativo:", k)
                delete(servers, k)
            }
        }
        mu.Unlock()
    }
}

func main() {
    log.Println("[REF] Iniciado em tcp://*:6000 (ZMQ REP)")

    go pruneLoop()

    ctx, _ := zmq.NewContext()
    defer ctx.Term()

    rep, _ := ctx.NewSocket(zmq.REP)
    defer rep.Close()
    rep.Bind("tcp://*:6000")

    for {
        raw, err := rep.Recv(0)
        if err != nil {
            log.Println("erro recv:", err)
            continue
        }

        var req Envelope
        if err := json.Unmarshal([]byte(raw), &req); err != nil {
            log.Println("[REF] JSON inválido recebido:", err)
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
            user, _ := req.Data["user"].(string)
            portVal, _ := req.Data["port"].(float64)
            port := int(portVal)

            mu.Lock()
            if _, exists := servers[user]; !exists {
                servers[user] = &ServerInfo{
                    Name:     user,
                    Rank:     nextRank,
                    Port:     port,
                    LastSeen: time.Now().Unix(),
                }
                log.Println("[REF] Novo servidor (rank):", user, "rank:", nextRank, "port:", port)
                nextRank++
            } else {
                servers[user].Port = port
                servers[user].LastSeen = time.Now().Unix()
            }
            resp.Data["rank"] = servers[user].Rank
            mu.Unlock()

        case "heartbeat":
            user, _ := req.Data["user"].(string)
            portVal, _ := req.Data["port"].(float64)
            port := int(portVal)

            mu.Lock()
            if _, exists := servers[user]; !exists {
                servers[user] = &ServerInfo{
                    Name:     user,
                    Rank:     nextRank,
                    Port:     port,
                    LastSeen: time.Now().Unix(),
                }
                log.Println("[REF] Novo servidor via heartbeat:", user, "rank:", nextRank, "port:", port)
                nextRank++
            } else {
                servers[user].Port = port
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
                    "port": s.Port,
                })
            }
            mu.Unlock()
            resp.Data["list"] = l

        default:
            resp.Data["error"] = "serviço desconhecido"
        }

        out, _ := json.Marshal(resp)
        rep.Send(string(out), 0)
    }
}