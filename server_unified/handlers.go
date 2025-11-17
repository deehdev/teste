// =====================
// === FILE: handlers.go ===
// =====================
package main


import (
"fmt"
"log"
"github.com/google/uuid"
zmq "github.com/pebbe/zmq4"
)


// Note: functions assume pub socket exists and is provided


func handleCreateChannel(env Envelope, pub *zmq.Socket) Envelope {
raw := fmt.Sprintf("%v", env.Data["channel"])
ch := normalize(raw)
resp := Envelope{Service: "channel", Data: map[string]interface{}{}, Timestamp: nowISO(), Clock: incClockBeforeSend()}
if ch == "" { resp.Data["status"] = "erro"; resp.Data["message"] = "nome inválido"; return resp }
channelsMu.Lock()
for _, c := range channels { if c == ch { channelsMu.Unlock(); resp.Data["status"] = "erro"; resp.Data["message"] = "canal já existe"; return resp } }
channels = append(channels, ch)
channelsMu.Unlock()
go persistChannels()
le := LogEntry{ID: uuid.NewString(), Type: "create_channel", Data: map[string]interface{}{"channel": ch}, Timestamp: nowISO(), Clock: env.Clock}
logsMu.Lock(); logs = append(logs, le); logsMu.Unlock(); go persistLogs()
publishReplicationEvent(pub, le)
resp.Data["status"] = "sucesso"; resp.Data["channel"] = ch
return resp
}


func handlePublish(env Envelope, pub *zmq.Socket) Envelope {
channel := normalize(fmt.Sprintf("%v", env.Data["channel"]))
msg := fmt.Sprintf("%v", env.Data["message"])
user := normalize(fmt.Sprintf("%v", env.Data["user"]))
resp := Envelope{Service: "publish", Data: map[string]interface{}{}, Timestamp: nowISO(), Clock: incClockBeforeSend()}
channelsMu.Lock(); ok := false; for _, c := range channels { if c == channel { ok = true; break } }; channelsMu.Unlock()
if !ok { resp.Data["status"] = "erro"; resp.Data["message"] = "canal inexistente"; return resp }
subMu.Lock(); subs := append([]string{}, subscriptions[user]...); subMu.Unlock()
allowed := false
for _, s := range subs { if s == channel { allowed = true; break } }
if !allowed { resp.Data["status"] = "erro"; resp.Data["message"] = "você não está inscrito neste canal"; return resp }
pubEnv := Envelope{Service: "publish", Data: map[string]interface{}{"channel": channel, "user": user, "msg": msg}, Timestamp: nowISO(), Clock: incClockBeforeSend()}
out, _ := msgpack.Marshal(pubEnv)
if _, err := pub.SendMessage(channel, out); err != nil { log.Println("[PUB] erro:", err) }
le := LogEntry{ID: uuid.NewString(), Type: "publish", Data: map[string]interface{}{"channel": channel, "user": user, "message": msg}, Timestamp: nowISO(), Clock: env.Clock}
logsMu.Lock(); logs = append(logs, le); logsMu.Unlock(); go persistLogs()
publishReplicationEvent(pub, le)
msgCount++
if msgCount%10 == 0 { go maybeTriggerBerkeley(pub) }
resp.Data["status"] = "sucesso"
return resp
}


func handleMessage(env Envelope, pub *zmq.Socket) Envelope {
dst := normalize(fmt.Sprintf("%v", env.Data["dst"]))
src := normalize(fmt.Sprintf("%v", env.Data["src"]))
msg := fmt.Sprintf("%v", env.Data["message"])
resp := Envelope{Service: "message", Data: map[string]interface{}{}, Timestamp: nowISO(), Clock: incClockBeforeSend()}
usersMu.Lock(); exists := false; for _, u := range users { if u == dst { exists = true; break } }; usersMu.Unlock()
if !exists { resp.Data["status"] = "erro"; resp.Data["message"] = "usuário destino não existe"; return resp }
pubEnv := Envelope{Service: "message", Data: map[string]interface{}{"src": src, "msg": msg}, Timestamp: nowISO(), Clock: incClockBeforeSend()}
out, _ := msgpack.Marshal(pubEnv)
if _, err := pub.SendMessage(dst, out); err != nil { log.Println("[PUB PM] erro:", err) }
le := LogEntry{ID: uuid.NewString(), Type: "private", Data: map[string]interface{}{"src": src, "dst": dst, "message": msg}, Timestamp: nowISO(), Clock: env.Clock}
logsMu.Lock(); logs = append(logs, le); logsMu.Unlock(); go persistLogs()
publishReplicationEvent(pub, le)
msgCount++
if msgCount%10 == 0 { go maybeTriggerBerkeley(pub) }
resp.Data["status"] = "sucesso"
return resp
}