package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"sync"
	"time"

	zmq "github.com/pebbe/zmq4"
	"github.com/vmihailenco/msgpack/v5"
)

// -----------------------
// Estruturas e variáveis
// -----------------------

var (
	serverName  string
	serverRank  int
	clock       int
	coordinator string

	serversMutex sync.Mutex
	serversList  = make(map[string]int) // nome -> rank

	usersMutex sync.Mutex
	users      = make(map[string]string) // user -> timestamp

	channelsMutex sync.Mutex
	channels      = make(map[string]bool) // canal -> existe

	messagesMutex sync.Mutex
	messages      = []Envelope{} // histórico persistido

	refAddr       string
	brokerAddr    string
	proxyPubAddr  string
	proxySubAddr  string
	isCoordinator bool

	pubSocket *zmq.Socket
	subSocket *zmq.Socket
)

// -----------------------
// Mensagens
// -----------------------

type Envelope struct {
	Service string                 `msgpack:"service"`
	Data    map[string]interface{} `msgpack:"data"`
}

// -----------------------
// Replication message format (JSON)
// -----------------------

type ReplicateMsg struct {
	Origin    string                 `json:"origin"`
	Action    string                 `json:"action"` // add_user, add_channel, add_message
	Payload   map[string]interface{} `json:"payload"`
	Timestamp string                 `json:"timestamp"`
	Clock     int                    `json:"clock"`
}

// -----------------------
// Relógio lógico
// -----------------------

func incClock() int {
	clock++
	return clock
}

func updateClock(received int) {
	if received > clock {
		clock = received
	}
	// opcional incremento
}

// -----------------------
// Funções auxiliares
// -----------------------

func randomString(n int) string {
	letters := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	s := make([]rune, n)
	for i := range s {
		s[i] = letters[rand.Intn(len(letters))]
	}
	return string(s)
}

func getStringFromInterface(v interface{}) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func ensureDataDir() error {
	if _, err := os.Stat("data"); os.IsNotExist(err) {
		return os.Mkdir("data", 0755)
	}
	return nil
}

// -----------------------
// Persistência (JSON simples)
// -----------------------

func loadJSON(path string, v interface{}) error {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, v)
}

func saveJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, data, 0644)
}

func persistUsers() {
	usersMutex.Lock()
	defer usersMutex.Unlock()
	list := []map[string]string{}
	for u, ts := range users {
		list = append(list, map[string]string{"user": u, "timestamp": ts})
	}
	_ = saveJSON("data/users.json", list)
}

func persistChannels() {
	channelsMutex.Lock()
	defer channelsMutex.Unlock()
	list := []string{}
	for c := range channels {
		list = append(list, c)
	}
	_ = saveJSON("data/channels.json", list)
}

func persistMessages() {
	messagesMutex.Lock()
	defer messagesMutex.Unlock()
	_ = saveJSON("data/messages.json", messages)
}

func loadPersistentState() {
	// users
	var ulist []map[string]string
	if err := loadJSON("data/users.json", &ulist); err == nil {
		usersMutex.Lock()
		for _, item := range ulist {
			if name, ok := item["user"]; ok {
				users[name] = item["timestamp"]
			}
		}
		usersMutex.Unlock()
	} else {
		log.Println("[PERSIST] erro carregando users:", err)
	}

	// channels
	var clist []string
	if err := loadJSON("data/channels.json", &clist); err == nil {
		channelsMutex.Lock()
		for _, c := range clist {
			channels[c] = true
		}
		channelsMutex.Unlock()
	} else {
		log.Println("[PERSIST] erro carregando channels:", err)
	}

	// messages
	var mlist []Envelope
	if err := loadJSON("data/messages.json", &mlist); err == nil {
		messagesMutex.Lock()
		messages = mlist
		messagesMutex.Unlock()
	} else {
		log.Println("[PERSIST] erro carregando messages:", err)
	}
}

// -----------------------
// Comunicação com Referência
// -----------------------

func requestRank(socket *zmq.Socket) int {
	req := Envelope{
		Service: "rank",
		Data: map[string]interface{}{
			"user":      serverName,
			"timestamp": time.Now().Format(time.RFC3339),
			"clock":     incClock(),
		},
	}
	reqBytes, _ := msgpack.Marshal(req)

	if _, err := socket.SendBytes(reqBytes, 0); err != nil {
		log.Println("Erro enviando rank:", err)
		return 0
	}

	replyBytes, err := socket.RecvBytes(0)
	if err != nil {
		log.Println("Erro recebendo rank:", err)
		return 0
	}

	var rep Envelope
	if err := msgpack.Unmarshal(replyBytes, &rep); err != nil {
		log.Println("Erro decodificando reply rank:", err)
		return 0
	}

	if r, ok := rep.Data["rank"].(int64); ok {
		return int(r)
	}
	if r, ok := rep.Data["rank"].(int); ok {
		return r
	}
	return 0
}

func sendHeartbeat(socket *zmq.Socket) {
	req := Envelope{
		Service: "heartbeat",
		Data: map[string]interface{}{
			"user":      serverName,
			"timestamp": time.Now().Format(time.RFC3339),
			"clock":     incClock(),
		},
	}
	reqBytes, _ := msgpack.Marshal(req)
	if _, err := socket.SendBytes(reqBytes, 0); err != nil {
		log.Println("Erro enviando heartbeat:", err)
		return
	}
	_, err := socket.RecvBytes(0)
	if err != nil {
		log.Println("Erro recebendo heartbeat reply:", err)
	}
}

// -----------------------
// Eleição de coordenador
// -----------------------

func electCoordinator() {
	serversMutex.Lock()
	defer serversMutex.Unlock()
	maxRank := 0
	for _, r := range serversList {
		if r > maxRank {
			maxRank = r
		}
	}
	if maxRank == serverRank {
		isCoordinator = true
		coordinator = serverName
		log.Println("🟢 Sou o coordenador!")
	} else {
		isCoordinator = false
	}
}

// -----------------------
// Replicação: publicar e aplicar
// -----------------------

func publishReplicate(action string, payload map[string]interface{}) {
	if pubSocket == nil {
		return
	}
	rm := ReplicateMsg{
		Origin:    serverName,
		Action:    action,
		Payload:   payload,
		Timestamp: time.Now().Format(time.RFC3339),
		Clock:     incClock(),
	}
	b, _ := json.Marshal(rm)
	_, err := pubSocket.SendMessage("replicate", b)
	if err != nil {
		log.Println("Erro enviando replicate:", err)
	}
}

func applyReplication(b []byte) {
	var rm ReplicateMsg
	if err := json.Unmarshal(b, &rm); err != nil {
		log.Println("replicate: json decode error:", err)
		return
	}
	// ignore origin self
	if rm.Origin == serverName {
		return
	}
	// update logical clock
	updateClock(rm.Clock)

	switch rm.Action {
	case "add_user":
		if u, ok := rm.Payload["user"].(string); ok && u != "" {
			usersMutex.Lock()
			if _, exists := users[u]; !exists {
				users[u] = rm.Payload["timestamp"].(string)
				usersMutex.Unlock()
				persistUsers()
				log.Printf("[replicate] added user %s (from %s)", u, rm.Origin)
			} else {
				usersMutex.Unlock()
			}
		}
	case "add_channel":
		if c, ok := rm.Payload["channel"].(string); ok && c != "" {
			channelsMutex.Lock()
			if _, exists := channels[c]; !exists {
				channels[c] = true
				channelsMutex.Unlock()
				persistChannels()
				log.Printf("[replicate] added channel %s (from %s)", c, rm.Origin)
			} else {
				channelsMutex.Unlock()
			}
		}
	case "add_message":
		msgIface := rm.Payload["message"]
		if mmap, ok := msgIface.(map[string]interface{}); ok {
			env := Envelope{
				Service: getStringFromInterface(mmap["service"]),
				Data:    map[string]interface{}{},
			}
			if dataMap, ok2 := mmap["data"].(map[string]interface{}); ok2 {
				env.Data = dataMap
			}
			dup := false
			messagesMutex.Lock()
			for _, ex := range messages {
				if getStringFromInterface(ex.Data["timestamp"]) == getStringFromInterface(env.Data["timestamp"]) &&
					getStringFromInterface(ex.Data["message"]) == getStringFromInterface(env.Data["message"]) {
					dup = true
					break
				}
			}
			if !dup {
				messages = append(messages, env)
				persistMessages()
				log.Printf("[replicate] added message (from %s)", rm.Origin)
			}
			messagesMutex.Unlock()
		}
	default:
		// ignore unknown action
	}
}

// -----------------------
// SUB listener for replication and channel/user topics
// -----------------------

func subListener() {
	for {
		parts, err := subSocket.RecvMessageBytes(0)
		if err != nil {
			log.Println("sub recv err:", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if len(parts) < 2 {
			continue
		}

		topic := string(parts[0])
		payload := parts[1]

		// replicate topic handled as JSON
		if topic == "replicate" {
			applyReplication(payload)
			continue
		}

		// other topics -> payload is msgpack Envelope
		var env Envelope
		if err := msgpack.Unmarshal(payload, &env); err != nil {
			// not a msgpack envelope, ignore
			continue
		}

		// If envelope has an origin and it's us, ignore (we already processed it when handling the REQ)
		if orig, ok := env.Data["origin"].(string); ok && orig == serverName {
			continue
		}

		switch env.Service {
		case "publish":
			// persist message if not duplicate
			channel := getStringFromInterface(env.Data["channel"])
			dup := false
			messagesMutex.Lock()
			for _, ex := range messages {
				if getStringFromInterface(ex.Data["timestamp"]) == getStringFromInterface(env.Data["timestamp"]) &&
					getStringFromInterface(ex.Data["message"]) == getStringFromInterface(env.Data["message"]) {
					dup = true
					break
				}
			}
			if !dup {
				messages = append(messages, env)
				persistMessages()
				log.Printf("[sub] persisted publish on channel %s (origin=%s)", channel, getStringFromInterface(env.Data["origin"]))
			}
			messagesMutex.Unlock()

		case "message":
			// private message to user (topic is dst username)
			dst := getStringFromInterface(env.Data["dst"])
			dup := false
			messagesMutex.Lock()
			for _, ex := range messages {
				if getStringFromInterface(ex.Data["timestamp"]) == getStringFromInterface(env.Data["timestamp"]) &&
					getStringFromInterface(ex.Data["message"]) == getStringFromInterface(env.Data["message"]) {
					dup = true
					break
				}
			}
			if !dup {
				messages = append(messages, env)
				persistMessages()
				log.Printf("[sub] persisted private message to %s (origin=%s)", dst, getStringFromInterface(env.Data["origin"]))
			}
			messagesMutex.Unlock()

		default:
			// ignore other services
		}
	}
}

// -----------------------
// Processamento de requisições
// -----------------------

func handleRequest(reqBytes []byte) ([]byte, error) {
	var req Envelope
	if err := msgpack.Unmarshal(reqBytes, &req); err != nil {
		log.Println("Erro unmarshal request:", err)
		resp := Envelope{
			Service: "error",
			Data: map[string]interface{}{
				"timestamp": time.Now().Format(time.RFC3339),
				"clock":     incClock(),
				"status":    "erro",
				"message":   "msgpack inválido",
			},
		}
		return msgpack.Marshal(resp)
	}

	// Atualiza relógio lógico
	recvClock := 0
	if c64, ok := req.Data["clock"].(int64); ok {
		recvClock = int(c64)
	} else if c, ok := req.Data["clock"].(int); ok {
		recvClock = c
	}
	updateClock(recvClock)

	resp := Envelope{
		Service: req.Service,
		Data: map[string]interface{}{
			"timestamp": time.Now().Format(time.RFC3339),
			"clock":     incClock(),
		},
	}

	switch req.Service {

	case "login":
		user := getStringFromInterface(req.Data["user"])
		if user == "" {
			resp.Data["status"] = "erro"
			resp.Data["description"] = "usuário inválido"
		} else {
			usersMutex.Lock()
			if _, exists := users[user]; !exists {
				users[user] = resp.Data["timestamp"].(string)
				usersMutex.Unlock()
				persistUsers()
				// replicate user creation to other servers
				payload := map[string]interface{}{
					"user":      user,
					"timestamp": resp.Data["timestamp"].(string),
				}
				publishReplicate("add_user", payload)
			} else {
				usersMutex.Unlock()
			}
			resp.Data["status"] = "sucesso"
		}

	case "users":
		usersMutex.Lock()
		list := []string{}
		for u := range users {
			list = append(list, u)
		}
		usersMutex.Unlock()
		resp.Data["users"] = list

	case "channel":
		ch := getStringFromInterface(req.Data["channel"])
		if ch == "" {
			resp.Data["status"] = "erro"
			resp.Data["description"] = "canal inválido"
		} else {
			channelsMutex.Lock()
			if _, exists := channels[ch]; !exists {
				channels[ch] = true
				channelsMutex.Unlock()
				persistChannels()
				// replicate channel creation
				payload := map[string]interface{}{
					"channel":   ch,
					"timestamp": resp.Data["timestamp"].(string),
				}
				publishReplicate("add_channel", payload)
			} else {
				channelsMutex.Unlock()
			}
			resp.Data["status"] = "sucesso"
		}

	case "channels":
		channelsMutex.Lock()
		list := []string{}
		for c := range channels {
			list = append(list, c)
		}
		channelsMutex.Unlock()
		resp.Data["channels"] = list

	case "publish":
		// handle client REQ -> publish to proxy XSUB
		user := getStringFromInterface(req.Data["user"])
		channel := getStringFromInterface(req.Data["channel"])
		message := getStringFromInterface(req.Data["message"])

		channelsMutex.Lock()
		_, exists := channels[channel]
		channelsMutex.Unlock()
		if !exists {
			resp.Data["status"] = "erro"
			resp.Data["message"] = "canal não existe"
			break
		}

		// include origin so subscribers/servers can ignore if needed
		pubEnv := Envelope{
			Service: "publish",
			Data: map[string]interface{}{
				"user":      user,
				"channel":   channel,
				"message":   message,
				"timestamp": time.Now().Format(time.RFC3339),
				"clock":     incClock(),
				"origin":    serverName,
			},
		}

		payload, _ := msgpack.Marshal(pubEnv)
		if pubSocket != nil {
			if _, err := pubSocket.SendMessage(channel, payload); err != nil {
				log.Println("Erro publicando no proxy:", err)
			}
		}

		// persist locally (server that handled the REQ)
		messagesMutex.Lock()
		messages = append(messages, pubEnv)
		messagesMutex.Unlock()
		persistMessages()

		// NOTE: we DON'T call publishReplicate here because other servers will receive the published envelope
		// through the proxy (they are subscribed) and persist it in subListener.

		resp.Data["status"] = "OK"

	case "message":
		// private message REQ
		src := getStringFromInterface(req.Data["src"])
		dst := getStringFromInterface(req.Data["dst"])
		message := getStringFromInterface(req.Data["message"])

		usersMutex.Lock()
		_, existsUser := users[dst]
		usersMutex.Unlock()
		if !existsUser {
			resp.Data["status"] = "erro"
			resp.Data["message"] = "usuário não existe"
			break
		}

		pubEnv := Envelope{
			Service: "message",
			Data: map[string]interface{}{
				"src":       src,
				"dst":       dst,
				"message":   message,
				"timestamp": time.Now().Format(time.RFC3339),
				"clock":     incClock(),
				"origin":    serverName,
			},
		}
		payload, _ := msgpack.Marshal(pubEnv)
		if pubSocket != nil {
			if _, err := pubSocket.SendMessage(dst, payload); err != nil {
				log.Println("Erro publicando mensagem privada no proxy:", err)
			}
		}

		// persist locally
		messagesMutex.Lock()
		messages = append(messages, pubEnv)
		messagesMutex.Unlock()
		persistMessages()

		// NOT sending replicate; other servers and the recipient will receive the envelope via proxy
		resp.Data["status"] = "OK"

	default:
		resp.Data["status"] = "erro"
		resp.Data["message"] = "serviço desconhecido"
	}

	return msgpack.Marshal(resp)
}

// -----------------------
// Heartbeat
// -----------------------

func heartbeatRoutine(refSocket *zmq.Socket) {
	time.Sleep(time.Duration(rand.Intn(1500)) * time.Millisecond)
	for {
		time.Sleep(5 * time.Second)
		sendHeartbeat(refSocket)
	}
}

// -----------------------
// Main
// -----------------------

func main() {
	rand.Seed(time.Now().UnixNano())
	serverName = "server-" + randomString(4)

	refAddr = os.Getenv("REF_ADDR")
	brokerAddr = os.Getenv("BROKER_DEALER_ADDR")
	proxyPubAddr = os.Getenv("PROXY_PUB_ADDR")
	proxySubAddr = os.Getenv("PROXY_SUB_ADDR")

	// validar envs
	if brokerAddr == "" || refAddr == "" || proxyPubAddr == "" || proxySubAddr == "" {
		log.Fatalf("Faltam variáveis de ambiente obrigatórias: REF_ADDR, BROKER_DEALER_ADDR, PROXY_PUB_ADDR, PROXY_SUB_ADDR")
	}

	// garantir pasta data
	if err := ensureDataDir(); err != nil {
		log.Println("Erro criando data dir:", err)
	}

	// carregar estado persistido
	loadPersistentState()

	// === REQ/REP com o broker (connect) ===
	socket, err := zmq.NewSocket(zmq.REP)
	if err != nil {
		log.Fatal("Erro criando socket REP:", err)
	}
	defer socket.Close()
	if err := socket.Connect(brokerAddr); err != nil {
		log.Fatalf("Erro conectando ao broker %s: %v", brokerAddr, err)
	}
	log.Printf("%s conectado ao broker %s", serverName, brokerAddr)

	// === PUBLICAÇÃO PARA PROXY (CONNECT -> XSUB) ===
	pub, err := zmq.NewSocket(zmq.PUB)
	if err != nil {
		log.Fatal("Erro criando socket PUB:", err)
	}
	// set linger small to avoid blocking on shutdown
	pub.SetLinger(0)
	if err := pub.Connect(proxyPubAddr); err != nil {
		log.Fatalf("Erro conectando ao proxy PUB %s: %v", proxyPubAddr, err)
	}
	pubSocket = pub
	log.Printf("%s conectado ao proxy PUB %s", serverName, proxyPubAddr)

	// === SUBSCRIBE PARA REPLICAÇÕES E OUTROS TOPICOS (CONNECT -> XPUB) ===
	sub, err := zmq.NewSocket(zmq.SUB)
	if err != nil {
		log.Fatal("Erro criando socket SUB:", err)
	}
	// set linger small to avoid blocking on shutdown
	sub.SetLinger(0)
	if err := sub.Connect(proxySubAddr); err != nil {
		log.Fatalf("Erro conectando ao proxy SUB %s: %v", proxySubAddr, err)
	}
	// subscribe to everything (Mode B)
	if err := sub.SetSubscribe(""); err != nil {
		log.Fatalf("Erro no SetSubscribe: %v", err)
	}
	subSocket = sub
	go subListener()
	log.Printf("%s conectado ao proxy SUB %s (subscribed all)", serverName, proxySubAddr)

	// === comunicação com servidor de referência ===
	refSocket, err := zmq.NewSocket(zmq.REQ)
	if err != nil {
		log.Fatal("Erro criando socket REQ(ref):", err)
	}
	refSocket.SetLinger(0)
	defer refSocket.Close()
	if err := refSocket.Connect(refAddr); err != nil {
		log.Fatalf("Erro conectando ao ref %s: %v", refAddr, err)
	}

	// rank
	serverRank = requestRank(refSocket)
	log.Printf("Rank do servidor: %d", serverRank)

	// guardar lista
	serversMutex.Lock()
	serversList[serverName] = serverRank
	serversMutex.Unlock()
	electCoordinator()

	// heartbeat
	go heartbeatRoutine(refSocket)

	for {
		msgBytes, err := socket.RecvMessageBytes(0)
		if err != nil {
			log.Println("Erro recebendo mensagem:", err)
			continue
		}

		respBytes, err := handleRequest(msgBytes[0])
		if err != nil {
			log.Println("Erro processando mensagem:", err)
			continue
		}

		socket.SendBytes(respBytes, 0)
	}
}
