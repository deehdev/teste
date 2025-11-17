// =====================
}
case "subscribe":
if u, ok := payload["user"].(string); ok {
if t, ok2 := payload["topic"].(string); ok2 {
subMu.Lock()
arr := subscriptions[u]
found := false
for _, s := range arr {
if s == t {
found = true
break
}
}
if !found {
subscriptions[u] = append(arr, t)
go persistSubs()
}
subMu.Unlock()
}
}
case "unsubscribe":
if u, ok := payload["user"].(string); ok {
if t, ok2 := payload["topic"].(string); ok2 {
subMu.Lock()
arr := subscriptions[u]
new := []string{}
for _, s := range arr {
if s == t {
continue
}
new = append(new, s)
}
subscriptions[u] = new
go persistSubs()
subMu.Unlock()
}
}
case "publish":
// publish events are already forwarded via PUB by origin server
case "private":
// nothing extra required; logs already stored
}
}


// Sync handlers
func handleSyncRequest() Envelope {
logsMu.Lock()
copyLogs := make([]LogEntry, len(logs))
copy(copyLogs, logs)
logsMu.Unlock()
arr := make([]map[string]interface{}, 0, len(copyLogs))
for _, l := range copyLogs {
arr = append(arr, map[string]interface{}{"id": l.ID, "type": l.Type, "data": l.Data, "timestamp": l.Timestamp, "clock": l.Clock})
}
return Envelope{Service: "sync_response", Data: map[string]interface{}{"logs": arr}, Timestamp: nowISO(), Clock: incClockBeforeSend()}
}


func applySyncResponse(env Envelope) {
raw, ok := env.Data["logs"]
if !ok {
return
}
if arr, ok := raw.([]interface{}); ok {
for _, it := range arr {
if m, ok := it.(map[string]interface{}); ok {
le := LogEntry{ID: fmt.Sprintf("%v", m["id"]), Type: fmt.Sprintf("%v", m["type"]), Data: map[string]interface{}{}, Timestamp: fmt.Sprintf("%v", m["timestamp"]), Clock: 0}
if d, ok := m["data"].(map[string]interface{}); ok {
le.Data = d
}
if c, ok := m["clock"].(float64); ok {
le.Clock = int(c)
}
env := Envelope{Service: "replicate", Data: map[string]interface{}{"id": le.ID, "type": le.Type, "data": le.Data}, Timestamp: le.Timestamp, Clock: le.Clock}
applyReplication(env)
}
}
}
}