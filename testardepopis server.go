package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	zmq "github.com/pebbe/zmq4"
	"github.com/google/uuid"
	"github.com/vmihailenco/msgpack/v5"
)

// ------------------------
// Envelope & event types
// ------------------------
type Envelope struct {
	Service   string                 `msgpack:"service" json:"service"`
	Data      map[string]interface{} `msgpack:"data" json:"data"`
	Timestamp string                 `msgpack:"timestamp" json:"timestamp"`
	Clock     int                    `msgpack:"clock" json:"clock"`
}

type LogEntry struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Data      map[string]interface{} `json:"data"`
	Timestamp string                 `json:"timestamp"`
	Clock     int                    `json:"clock"`
}

// ------------------------
// Config / state files
// ------------------------
const dataDir = "/app/data"
const usersFile = dataDir + "/users.json"
const channelsFile = dataDir + "/channels.json"
const subsFile = dataDir + "/subscriptions.json"
const logsFile = dataDir + "/logs.json"

// ------------------------
// Global state
// ------------------------
var (
	users         []string
	channels      []string
	subscriptions = map[string][]string{}
	logs          = []LogEntry{}

	// persistence mutexes
	usersMu    sync.Mutex
	channelsMu sync.Mutex
	subMu      sync.Mutex
	logsMu     sync.Mutex

	// logical clock
	clockMu      sync.Mutex
	logicalClock int

	// berkeley
	berkeleyMu sync.Mutex
	clockOffset int64 = 0 // seconds offset applied to physical clock
	msgCount    int

	// REF / coordinator
	refAddr string
	proxyPubAddr string
	serverName string
	repPort int
	currentCoordinator string
	currentCoordinatorMu sync.Mutex
)

// ------------------------
// Utilities
// ------------------------
func nowISO() string {
	return time.Now().Format(time.RFC3339Nano)
}

func nowPhysicalSeconds() int64 {
	berkeleyMu.Lock()
	off := clockOffset
	berkeleyMu.Unlock()
	return time.Now().Unix() + off
}

func normalize(s string) string {
	return strings.TrimSpace(strings.ToLower(s))
}

func saveJSON(path string, data interface{}) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		f.Close()
		return err
	}
	f.Sync()
	f.Close()
	return os.Rename(tmp, path)
}

func loadJSON(path string, dest interface{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	return dec.Decode(dest)
}

// ------------------------
// Logical clock (Lamport)
// ------------------------
func incClockBeforeSend() int {
	clockMu.Lock()
	logicalClock++
	v := logicalClock
	clockMu.Unlock()
	return v
}

func updateClockRecv(n int) {
	clockMu.Lock()
	if n > logicalClock {
		logicalClock = n
	}
	logicalClock++
	clockMu.Unlock()
}

// ------------------------
// Persistence helpers
// ------------------------
func persistUsers() { usersMu.Lock(); defer usersMu.Unlock(); saveJSON(usersFile, users) }
func persistChannels(){ channelsMu.Lock(); defer channelsMu.Unlock(); saveJSON(channelsFile, channels) }
func persistSubs(){ subMu.Lock(); defer subMu.Unlock(); saveJSON(subsFile, subscriptions) }
func persistLogs(){ logsMu.Lock(); defer logsMu.Unlock(); saveJSON(logsFile, logs) }

// ------------------------
// Replication helper
// ------------------------
func publishReplication(pub *zmq.Socket, ev LogEntry) {
	env := Envelope{
		Service: "replicate",
		Data: map[string]interface{}{
			"id": ev.ID,
			"type": ev.Type,
			"data": ev.Data,
		},
		Timestamp: ev.Timestamp,
		Clock: ev.Clock,
	}
	out, err := msgpack.Marshal(env)
	if err != nil {
		log.Println("[REPL] marshal erro:", err)
		return
	}
	if _, err := pub.SendMessage("replicate", out); err != nil {
		log.Println("[REPL] send erro:", err)
	}
}

func applyReplication(env Envelope) {
	// idempotent apply using ID
	idRaw, _ := env.Data["id"]
	id, _ := idRaw.(string)
	typRaw, _ := env.Data["type"]
	typ, _ := typRaw.(string)
	payloadRaw, _ := env.Data["data"]
	payload, _ := payloadRaw.(map[string]interface{})

	if id == "" || typ == "" {
		return
	}

	logsMu.Lock()
	for _, l := range logs {
		if l.ID == id {
			logsMu.Unlock()
			return // already applied
		}
	}
	// append
	le := LogEntry{ ID: id, Type: typ, Data: payload, Timestamp: nowISO(), Clock: env.Clock }
	logs = append(logs, le)
	logsMu.Unlock()
	// persist async
	go persistLogs()

	// If the event is a publish/message we also forward to local PUB so subscribed clients receive it
	if typ == "publish" || typ == "message" {
		// build envelope to pub
		pubEnv := Envelope{ Service: typ, Data: payload, Timestamp: nowISO(), Clock: incClockBeforeSend() }
		out, err := msgpack.Marshal(pubEnv)
		if err == nil {
			// use a transient context to publish directly to proxy? we rely on existing PUB socket in main to publish
			// In this simplified apply we do not open a new socket (main SUB will receive replicate and local clients will
			// connect to proxy which receives publications). So here we only ensure logs are applied.
			_ = out
		}
	}
}

// ------------------------
// REF interaction (JSON REQ/REP)
// ------------------------
func directReqZMQJSON(addr string, env Envelope, timeout time.Duration) (Envelope, error) {
	ctx, _ := zmq.NewContext()
	defer ctx.Term()
	sock, err := ctx.NewSocket(zmq.REQ)
	if err != nil { return Envelope{}, err }
	defer sock.Close()
	sock.SetLinger(0)
	if err := sock.Connect(addr); err != nil { return Envelope{}, err }
	b, _ := json.Marshal(env)
	if _, err := sock.SendBytes(b, 0); err != nil { return Envelope{}, err }
	if timeout>0 { sock.SetRcvtimeo(timeout) }
	r, err := sock.RecvBytes(0)
	if err != nil { return Envelope{}, err }
	var rep Envelope
	if err := json.Unmarshal(r, &rep); err != nil { return Envelope{}, err }
	return rep, nil
}

func requestRank() int {
	req := Envelope{ Service: "rank", Data: map[string]interface{}{ "user": serverName, "port": repPort }, Timestamp: nowISO(), Clock: incClockBeforeSend() }
	rep, err := directReqZMQJSON(refAddr, req, 5*time.Second)
	if err != nil {
		log.Println("[REF] rank request erro:", err)
		return -1
	}
	updateClockRecv(rep.Clock)
	if v, ok := rep.Data["rank"]; ok {
		switch t := v.(type) {
		case float64:
			return int(t)
		case int:
			return t
		}
	}
	return -1
}

func requestList() ([]map[string]interface{}, error) {
	req := Envelope{ Service: "list", Data: map[string]interface{}{}, Timestamp: nowISO(), Clock: incClockBeforeSend() }
	rep, err := directReqZMQJSON(refAddr, req, 5*time.Second)
	if err != nil { return nil, err }
	updateClockRecv(rep.Clock)
	raw, ok := rep.Data["list"]
	if !ok { return nil, nil }
	if il, ok := raw.([]interface{}); ok {
		out := make([]map[string]interface{}, 0, len(il))
		for _, it := range il {
			if m, ok := it.(map[string]interface{}); ok { out = append(out, m) }
		}
		return out, nil
	}
	return nil, nil
}

// ------------------------
// Handlers (application logic)
// ------------------------
func handleLogin(env Envelope) Envelope {
	data := env.Data
	raw := fmt.Sprintf("%v", data["user"])
	user := normalize(raw)
	resp := Envelope{ Service: "login", Data: map[string]interface{}{}, Timestamp: nowISO(), Clock: incClockBeforeSend() }
	if user == "" { resp.Data["status"] = "erro"; resp.Data["message"] = "usuário inválido"; return resp }

	usersMu.Lock()
	for _, u := range users { if u==user { usersMu.Unlock(); resp.Data["status"]="erro"; resp.Data["message"]="usuário já existe"; return resp } }
	users = append(users, user)
	usersMu.Unlock()

	subMu.Lock()
	if _, ok := subscriptions[user]; !ok { subscriptions[user] = []string{} }
	subMu.Unlock()

	// persist
	persistUsers(); persistSubs()

	// log
	le := LogEntry{ ID: uuid.NewString(), Type: "login", Data: map[string]interface{}{"user":user}, Timestamp: nowISO(), Clock: env.Clock }
	logsMu.Lock(); logs = append(logs, le); logsMu.Unlock(); go persistLogs()

	// replicate
	// publishReplication called by main using pub socket (can't here)

	resp.Data["status"] = "sucesso"
	resp.Data["user"] = user
	return resp
}

func handleUsers(env Envelope) Envelope {
	usersMu.Lock(); list := append([]string{}, users...); usersMu.Unlock()
	resp := Envelope{ Service: "users", Data: map[string]interface{}{"timestamp": nowISO(), "users": list}, Timestamp: nowISO(), Clock: incClockBeforeSend() }
	return resp
}

func handleChannels(env Envelope) Envelope {
	channelsMu.Lock(); list := append([]string{}, channels...); channelsMu.Unlock()
	resp := Envelope{ Service: "channels", Data: map[string]interface{}{"timestamp": nowISO(), "channels": list}, Timestamp: nowISO(), Clock: incClockBeforeSend() }
	return resp
}

func handleCreateChannel(env Envelope) Envelope {
	raw := fmt.Sprintf("%v", env.Data["channel"])
	ch := normalize(raw)
	resp := Envelope{ Service: "channel", Data: map[string]interface{}{}, Timestamp: nowISO(), Clock: incClockBeforeSend() }
	if ch=="" { resp.Data["status"]="erro"; resp.Data["message"]="nome inválido"; return resp }
	channelsMu.Lock()
	for _, c := range channels { if c==ch { channelsMu.Unlock(); resp.Data["status"]="erro"; resp.Data["message"]="canal já existe"; return resp } }
	channels = append(channels, ch)
	channelsMu.Unlock()
	persistChannels()
	// log
	le := LogEntry{ ID: uuid.NewString(), Type: "create_channel", Data: map[string]interface{}{"channel":ch}, Timestamp: nowISO(), Clock: env.Clock }
	logsMu.Lock(); logs = append(logs, le); logsMu.Unlock(); go persistLogs()

	resp.Data["status"] = "sucesso"
	resp.Data["channel"] = ch
	return resp
}

func handleSubscribe(env Envelope) Envelope {
	user := normalize(fmt.Sprintf("%v", env.Data["user"]))
	topic := normalize(fmt.Sprintf("%v", env.Data["topic"]))
	resp := Envelope{ Service: "subscribe", Data: map[string]interface{}{}, Timestamp: nowISO(), Clock: incClockBeforeSend() }
	usersMu.Lock()
	found := false
	for _, u := range users { if u==user { found = true; break } }
	usersMu.Unlock()
	if !found { resp.Data["status"]="erro"; resp.Data["message"]="usuário não existe"; return resp }

	channelsMu.Lock()
	ok := false
	for _, c := range channels { if c==topic { ok=true; break } }
	channelsMu.Unlock()
	if !ok { resp.Data["status"]="erro"; resp.Data["message"]="canal não existe"; return resp }

	subMu.Lock()
	for _, s := range subscriptions[user] { if s==topic { subMu.Unlock(); resp.Data["status"]="erro"; resp.Data["message"]="já inscrito"; return resp } }
	subscriptions[user] = append(subscriptions[user], topic)
	subMu.Unlock()
	persistSubs()
	resp.Data["status"] = "sucesso"
	return resp
}

func handleUnsubscribe(env Envelope) Envelope {
	user := normalize(fmt.Sprintf("%v", env.Data["user"]))
	topic := normalize(fmt.Sprintf("%v", env.Data["topic"]))
	resp := Envelope{ Service: "unsubscribe", Data: map[string]interface{}{}, Timestamp: nowISO(), Clock: incClockBeforeSend() }

	subMu.Lock()
	subs := subscriptions[user]
	new := []string{}
	found := false
	for _, s := range subs { if s==topic { found=true; continue } ; new = append(new, s) }
	if !found { subMu.Unlock(); resp.Data["status"]="erro"; resp.Data["message"]="não inscrito"; return resp }
	subscriptions[user] = new
	subMu.Unlock()
	persistSubs()
	resp.Data["status"] = "sucesso"
	return resp
}

func handlePublish(env Envelope, pub *zmq.Socket) Envelope {
	channel := normalize(fmt.Sprintf("%v", env.Data["channel"]))
	msg := fmt.Sprintf("%v", env.Data["message"])
	user := normalize(fmt.Sprintf("%v", env.Data["user"]))
	resp := Envelope{ Service: "publish", Data: map[string]interface{}{}, Timestamp: nowISO(), Clock: incClockBeforeSend() }

	channelsMu.Lock()
	ok := false
	for _, c := range channels { if c==channel { ok=true; break } }
	channelsMu.Unlock()
	if !ok { resp.Data["status"]="erro"; resp.Data["message"]="canal inexistente"; return resp }

	// check user subscription
	subMu.Lock()
	subs := append([]string{}, subscriptions[user]...)
	subMu.Unlock()
	allowed := false
	for _, s := range subs { if s==channel { allowed=true; break } }
	if !allowed { resp.Data["status"]="erro"; resp.Data["message"]="você não está inscrito neste canal"; return resp }

	// publish to proxy: topic = channel, payload = Envelope (msgpack)
	pubEnv := Envelope{ Service: "publish", Data: map[string]interface{}{ "channel": channel, "user": user, "msg": msg }, Timestamp: nowISO(), Clock: incClockBeforeSend() }
	out, _ := msgpack.Marshal(pubEnv)
	if _, err := pub.SendMessage(channel, out); err != nil { log.Println("[PUB] erro:", err) }

	// persist log and replicate
	le := LogEntry{ ID: uuid.NewString(), Type: "publish", Data: map[string]interface{}{"channel":channel,"user":user,"message":msg}, Timestamp: nowISO(), Clock: env.Clock }
	logsMu.Lock(); logs = append(logs, le); logsMu.Unlock(); go persistLogs()

	// replication publish
	publishReplication(pub, le)

	msgCount++
	if msgCount%10==0 { go maybeTriggerBerkeley(pub) }

	resp.Data["status"] = "sucesso"
	return resp
}

func handleMessage(env Envelope, pub *zmq.Socket) Envelope {
	dst := normalize(fmt.Sprintf("%v", env.Data["dst"]))
	src := normalize(fmt.Sprintf("%v", env.Data["src"]))
	msg := fmt.Sprintf("%v", env.Data["message"])
	resp := Envelope{ Service: "message", Data: map[string]interface{}{}, Timestamp: nowISO(), Clock: incClockBeforeSend() }

	usersMu.Lock()
	exists := false
	for _, u := range users { if u==dst { exists=true; break } }
	usersMu.Unlock()
	if !exists { resp.Data["status"]="erro"; resp.Data["message"]="usuário destino não existe"; return resp }

	pubEnv := Envelope{ Service: "message", Data: map[string]interface{}{"src": src, "msg": msg}, Timestamp: nowISO(), Clock: incClockBeforeSend() }
	out, _ := msgpack.Marshal(pubEnv)
	if _, err := pub.SendMessage(dst, out); err != nil { log.Println("[PUB PM] erro:", err) }

	le := LogEntry{ ID: uuid.NewString(), Type: "private", Data: map[string]interface{}{"src":src,"dst":dst,"message":msg}, Timestamp: nowISO(), Clock: env.Clock }
	logsMu.Lock(); logs = append(logs, le); logsMu.Unlock(); go persistLogs()
	publishReplication(pub, le)

	msgCount++
	if msgCount%10==0 { go maybeTriggerBerkeley(pub) }

	resp.Data["status"] = "sucesso"
	return resp
}

// ------------------------
// REP loop (MessagePack REQ/REP from broker)
// ------------------------
func repLoop(rep *zmq.Socket, pub *zmq.Socket) {
	for {
		b, err := rep.RecvBytes(0)
		if err != nil { log.Println("[REP] recv erro:", err); continue }
		var env Envelope
		if err := msgpack.Unmarshal(b, &env); err != nil { log.Println("[REP] unmarshal erro:", err); continue }

		updateClockRecv(env.Clock)

		var resp Envelope
		svc := env.Service
		switch svc {
		case "login": resp = handleLogin(env)
		case "users": resp = handleUsers(env)
		case "channels": resp = handleChannels(env)
		case "channel": resp = handleCreateChannel(env)
		case "subscribe": resp = handleSubscribe(env)
		case "unsubscribe": resp = handleUnsubscribe(env)
		case "publish": resp = handlePublish(env, pub)
		case "message": resp = handleMessage(env, pub)
		default:
			resp = Envelope{ Service: "error", Data: map[string]interface{}{"message":"serviço desconhecido"}, Timestamp: nowISO(), Clock: incClockBeforeSend() }
		}

		out, err := msgpack.Marshal(resp)
		if err != nil { log.Println("[REP] marshal resp erro:", err); continue }
		if _, err := rep.SendBytes(out, 0); err != nil { log.Println("[REP] send erro:", err) }
	}
}

// ------------------------
// SUB loop for replicate & servers topics
// ------------------------
func subLoop(sub *zmq.Socket) {
	for {
		parts, err := sub.RecvMessageBytes(0)
		if err != nil { log.Println("[SUB] recv erro:", err); time.Sleep(time.Second); continue }
		if len(parts) < 2 { continue }
		topic := string(parts[0])
		var env Envelope
		if err := msgpack.Unmarshal(parts[1], &env); err != nil { log.Println("[SUB] unmarshal erro:", err); continue }
		updateClockRecv(env.Clock)
		if topic == "replicate" {
			applyReplication(env)
		} else if topic == "servers" {
			if env.Service == "election" {
				if c, ok := env.Data["coordinator"].(string); ok {
					currentCoordinatorMu.Lock(); currentCoordinator = c; currentCoordinatorMu.Unlock()
					log.Println("[SUB servers] novo coordinator:", c)
				}
			}
		}
	}
}

// ------------------------
// Berkeley algorithm and election
// ------------------------
func maybeTriggerBerkeley(pub *zmq.Socket) {
	currentCoordinatorMu.Lock(); coord := currentCoordinator; currentCoordinatorMu.Unlock()
	if coord == "" {
		d, err := determineCoordinator()
		if err != nil { log.Println("[BERKELEY] determinar coordinator erro:", err); return }
		coord = d
	}
	if coord != serverName { log.Println("[BERKELEY] não sou coordinator") ; return }
	runBerkeleyCoordinator(pub)
	publishCoordinator(pub, serverName)
}

func requestListServers() ([]map[string]interface{}, error) { return requestList() }

func runBerkeleyCoordinator(pub *zmq.Socket) {
	log.Println("[BERKELEY] coordenador coletando tempos")
	list, err := requestList()
	if err != nil { log.Println("[BERKELEY] request list erro:", err); return }
	imes := map[string]int64{}
	times[serverName] = nowPhysicalSeconds()
	for _, e := range list {
		nRaw, ok := e["name"]
		if !ok { continue }
		n, _ := nRaw.(string)
		if n==serverName { continue }
		p := 0
		if pv, ok := e["port"]; ok {
			switch v := pv.(type) {
			case float64: p = int(v)
			case int: p = v
			}
		}
		if p==0 { continue }
		addr := fmt.Sprintf("tcp://%s:%d", n, p)
		req := Envelope{ Service: "clock", Data: map[string]interface{}{}, Timestamp: nowISO(), Clock: incClockBeforeSend() }
		rep, err := directReqZMQ(addr, req, 3*time.Second)
		if err != nil { log.Println("[BERKELEY] não conseguiu falar com", n, err); continue }
		if tv, ok := rep.Data["time"]; ok {
			switch t := tv.(type) {
			case float64: times[n] = int64(t)
			case int64: times[n] = t
			case int: times[n] = int64(t)
			}
		}
	}
	if len(times)==0 { log.Println("[BERKELEY] nenhuma amostra"); return }
	var sum int64
	for _, v := range times { sum += v }
	avg := int64(math.Round(float64(sum)/float64(len(times))))
	log.Println("[BERKELEY] avg =", avg)
	for srv, reported := range times {
		adjust := avg - reported
		if adjust==0 { continue }
		if srv == serverName {
			berkeleyMu.Lock(); clockOffset += adjust; berkeleyMu.Unlock(); log.Println("[BERKELEY] ajuste local aplicado:", adjust)
			continue
		}
		// find port
		p := 0
		for _, e := range list {
			if en, ok := e["name"].(string); ok && en==srv {
				if pv, ok := e["port"]; ok {
					switch v := pv.(type) {
					case float64: p = int(v)
					case int: p = v
					}
				}
			}
		}
		if p==0 { continue }
		addr := fmt.Sprintf("tcp://%s:%d", srv, p)
		req := Envelope{ Service: "adjust", Data: map[string]interface{}{"adjust": adjust}, Timestamp: nowISO(), Clock: incClockBeforeSend() }
		_, err := directReqZMQ(addr, req, 3*time.Second)
		if err!=nil { log.Println("[BERKELEY] falha enviar ajuste para", srv, err) }
	}
}

func publishCoordinator(pub *zmq.Socket, coordinator string) {
	env := Envelope{ Service: "election", Data: map[string]interface{}{"coordinator": coordinator}, Timestamp: nowISO(), Clock: incClockBeforeSend() }
	out, _ := msgpack.Marshal(env)
	if _, err := pub.SendMessage("servers", out); err != nil { log.Println("[PUB] failed publish coordinator:", err) }
}

func probeElection(target string, list []map[string]interface{}) bool {
	p := 0
	for _, e := range list {
		if en, ok := e["name"].(string); ok && en==target {
			if pv, ok := e["port"]; ok { switch v := pv.(type) { case float64: p=int(v); case int: p=v } }
		}
	}
	if p==0 { return false }
	addr := fmt.Sprintf("tcp://%s:%d", target, p)
	req := Envelope{ Service: "election", Data: map[string]interface{}{}, Timestamp: nowISO(), Clock: incClockBeforeSend() }
	rep, err := directReqZMQ(addr, req, 3*time.Second)
	if err!=nil { return false }
	if val, ok := rep.Data["election"].(string); ok && val=="OK" { return true }
	return false
}

func determineCoordinator() (string, error) {
	list, err := requestList()
	if err!=nil { return "", err }
	best := ""
	bestRank := int64(1<<60 -1)
	for _, e := range list {
		n, ok1 := e["name"].(string)
		rv, ok2 := e["rank"]
		if !ok1 || !ok2 { continue }
		var rint int64
		switch t := rv.(type) { case float64: rint=int64(t); case int: rint=int64(t); case int64: rint=t }
		if rint < bestRank { bestRank = rint; best = n }
	}
	if best=="" { return "", fmt.Errorf("nenhum coordinator") }
	return best, nil
}

func startElection(pub *zmq.Socket) {
	log.Println("[ELECTION] iniciando")
	list, err := requestList()
	if err!=nil { log.Println("[ELECTION] request list erro:", err); return }
	myRank := requestRank()
	for _, e := range list {
		n, _ := e["name"].(string)
		rv, _ := e["rank"]
		var rr int64
		switch t := rv.(type) { case float64: rr=int64(t); case int: rr=int64(t); case int64: rr=t }
		if rr < int64(myRank) {
			if probeElection(n, list) { log.Println("[ELECTION] servidor com rank menor vivo:", n); return }
		}
	}
	log.Println("[ELECTION] sou o novo coordinator")
	publishCoordinator(pub, serverName)
}

// ------------------------
// Direct msgpack REQ to peers
// ------------------------
func directReqZMQ(addr string, env Envelope, timeout time.Duration) (Envelope, error) {
	ctx, _ := zmq.NewContext()
	defer ctx.Term()
	sock, err := ctx.NewSocket(zmq.REQ)
	if err!=nil { return Envelope{}, err }
	defer sock.Close()
	sock.SetLinger(0)
	if err := sock.Connect(addr); err!=nil { return Envelope{}, err }
	out, _ := msgpack.Marshal(env)
	if _, err := sock.SendBytes(out, 0); err!=nil { return Envelope{}, err }
	if timeout>0 { sock.SetRcvtimeo(timeout) }
	r, err := sock.RecvBytes(0)
	if err!=nil { return Envelope{}, err }
	var rep Envelope
	if err := msgpack.Unmarshal(r, &rep); err!=nil { return Envelope{}, err }
	return rep, nil
}

// ------------------------
// Main
// ------------------------
func main() {
	serverName = os.Getenv("SERVER_NAME")
	if serverName=="" { serverName = "server" }
	pstr := os.Getenv("SERVER_REP_PORT")
	if pstr=="" { repPort = 5555 } else { v,_ := strconv.Atoi(pstr); repPort=v }
	refAddr = os.Getenv("REF_ADDR")
	if refAddr=="" { refAddr = "tcp://ref:6000" }
	proxyPubAddr = os.Getenv("PROXY_PUB_ADDR")
	if proxyPubAddr=="" { proxyPubAddr = "tcp://proxy:5560" }

	// load persisted state
	_ = loadJSON(usersFile, &users)
	_ = loadJSON(channelsFile, &channels)
	_ = loadJSON(subsFile, &subscriptions)
	_ = loadJSON(logsFile, &logs)
	if users==nil { users=[]string{} }
	if channels==nil { channels=[]string{} }
	if subscriptions==nil { subscriptions = map[string][]string{} }
	if logs==nil { logs = []LogEntry{} }

	ctx, err := zmq.NewContext()
	if err!=nil { log.Fatal(err) }
	defer ctx.Term()

	// PUB socket to proxy (publish messages to clients)
	pub, err := ctx.NewSocket(zmq.PUB)
	if err!=nil { log.Fatal(err) }
	defer pub.Close()
	pub.SetLinger(0)
	if err := pub.Connect(proxyPubAddr); err!=nil { log.Println("warning: connect PUB failed:", err) } else { log.Println("PUB connected to", proxyPubAddr) }

	// REP socket to broker
	rep, err := ctx.NewSocket(zmq.REP)
	if err!=nil { log.Fatal(err) }
	defer rep.Close()
	rep.SetLinger(0)
	bind := fmt.Sprintf("tcp://*:%d", repPort)
	if err := rep.Bind(bind); err!=nil { log.Fatal(err) }
	log.Println("REP bound on", bind)

	// SUB to proxy for replicate and servers announcements
	sub, err := ctx.NewSocket(zmq.SUB)
	if err!=nil { log.Fatal(err) }
	defer sub.Close()
	sub.SetLinger(0)
	if err := sub.Connect(proxyPubAddr); err!=nil { log.Println("warning: connect SUB failed:", err) } else {
		sub.SetSubscribe("replicate")
		sub.SetSubscribe("servers")
		log.Println("SUB connected to", proxyPubAddr)
	}

	// start goroutines
	go repLoop(rep, pub)
	go subLoop(sub)

	// start heartbeat to REF
	go func(){ for { time.Sleep(5*time.Second); req := Envelope{ Service:"heartbeat", Data: map[string]interface{}{"user":serverName, "port": repPort}, Timestamp: nowISO(), Clock: incClockBeforeSend() }; _, err := directReqZMQJSON(refAddr, req, 2*time.Second); if err!=nil { log.Println("[REF heartbeat] erro:", err) } } }()

	// request rank and list
	rank := requestRank()
	log.Println("[REF] rank =", rank)
	if lst, err := requestList(); err==nil { log.Println("[REF] list:", lst) }

	// initial coordinator determination
	if coord, err := determineCoordinator(); err==nil { currentCoordinatorMu.Lock(); currentCoordinator = coord; currentCoordinatorMu.Unlock(); log.Println("[MAIN] coordinator:", coord) }

	// block
	select {}
}
