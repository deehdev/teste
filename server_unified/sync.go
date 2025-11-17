// =====================
// === FILE: sync.go ===
// =====================
package main


import (
"fmt"
"log"
"time"
)


// when starting, request sync from coordinator (if not self)
func requestInitialSync() {
currentCoordinatorMu.Lock(); coord := currentCoordinator; currentCoordinatorMu.Unlock()
if coord == "" || coord == serverName {
return
}
list, err := requestList()
if err != nil { log.Println("sync: request list erro:", err); return }
p := 0
for _, e := range list {
if n, ok := e["name"].(string); ok && n == coord {
if pv, ok := e["port"].(float64); ok {
p = int(pv)
}
}
}
if p == 0 { log.Println("sync: coordinator port desconhecida"); return }
addr := fmt.Sprintf("tcp://%s:%d", coord, p)
req := Envelope{Service: "sync_request", Data: map[string]interface{}{}, Timestamp: nowISO(), Clock: incClockBeforeSend()}
rep, err := directReqZMQ(addr, req, 5*time.Second)
if err != nil { log.Println("sync request failed:", err); return }
applySyncResponse(rep)
log.Println("sync completed from coordinator")
}