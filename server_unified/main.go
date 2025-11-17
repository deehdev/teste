// =====================
// === FILE: main.go ===
// =====================
package main


import (
"log"
"os"
"strconv"
zmq "github.com/pebbe/zmq4"
"github.com/vmihailenco/msgpack/v5"
)


func main() {
// envs
serverName = os.Getenv("SERVER_NAME")
if serverName == "" { serverName = "server" }
pstr := os.Getenv("SERVER_REP_PORT")
if pstr == "" { repPort = 7000 } else { v, _ := strconv.Atoi(pstr); repPort = v }
refAddr = os.Getenv("REF_ADDR")
if refAddr == "" { refAddr = "tcp://ref:6000" }
proxyPubAddr = os.Getenv("PROXY_PUB_ADDR")
if proxyPubAddr == "" { proxyPubAddr = "tcp://proxy:5560" }


// load persisted
_ = loadJSON(usersFile, &users)
_ = loadJSON(channelsFile, &channels)
_ = loadJSON(subsFile, &subscriptions)
_ = loadJSON(logsFile, &logs)
if users == nil { users = []string{} }
if channels == nil { channels = []string{} }
if subscriptions == nil { subscriptions = map[string][]string{} }
if logs == nil { logs = []LogEntry{} }


ctx, err := zmq.NewContext()
if err != nil { log.Fatal(err) }
defer ctx.Term()


// PUB socket to proxy
pub, err := ctx.NewSocket(zmq.PUB)
if err != nil { log.Fatal(err) }
defer pub.Close()
pub.SetLinger(0)
if err := pub.Connect(proxyPubAddr); err != nil { log.Println("warning: connect PUB failed:", err) } else { log.Println("PUB connected to", proxyPubAddr) }


// REP socket to broker
rep, err := ctx.NewSocket(zmq.REP)
if err != nil { log.Fatal(err) }
defer rep.Close()
rep.SetLinger(0)
bind := "tcp://*:" + strconv.Itoa(repPort)
if err := rep.Bind(bind); err != nil { log.Fatal(err) }
log.Println("REP bound on", bind)


// SUB to proxy for replicate and servers
sub, err := ctx.NewSocket(zmq.SUB)
if err != nil { log.Fatal(err) }
defer sub.Close()
sub.SetLinger(0)
if err := sub.Connect(proxyPubAddr); err != nil { log.Println("warning: connect SUB failed:", err) } else {
sub.SetSubscribe("replicate")
sub.SetSubscribe("servers")
log.Println("SUB connected to", proxyPubAddr)
}


// start goroutines
go repLoop(rep, pub)
go subLoop(sub)
// heartbeat to REF
go func() {
for {
time.Sleep(5 * time.Second)
req := Envelope{Service: "heartbeat", Data: map[string]interface{}{"user": serverName, "port": repPort}, Timestamp: nowISO(), Clock: incClockBeforeSend()}
_, err := directReqZMQJSON(refAddr, req, 2*time.Second)
if err != nil { log.Println("[REF heartbeat] erro:", err) }
}
}()


// initial registration + sync
rank := requestRank()
log.Println("[REF] rank =", rank)
if lst, err := requestList(); err == nil { log.Println("[REF] list:", lst) }
// determine coordinator
if coord, err := determineCoordinator(); err == nil {
currentCoordinatorMu.Lock()
currentCoordinator = coord
currentCoordinatorMu.Unlock()
log.Println("[MAIN] coordinator:", coord)
}
// request initial sync (from coordinator)
requestInitialSync()


select {}
}