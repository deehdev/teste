// server_sync.go (versão integrada e corrigida)
// Cole como server_sync/main.go (substitua tudo)

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"sync"
	"time"

	zmq "github.com/pebbe/zmq4"
	"github.com/vmihailenco/msgpack/v5"
)

// Envelope
type Envelope struct {
	Service   string                 `json:"service" msgpack:"service"`
	Data      map[string]interface{} `json:"data" msgpack:"data"`
	Timestamp string                 `json:"timestamp" msgpack:"timestamp"`
	Clock     int                    `json:"clock" msgpack:"clock"`
}

// Globals
var (
	mu           sync.Mutex
	logicalClock int

	name      string
	rank      int    = -1
	refAddr   string // ZMQ addr (ex: tcp://ref:6000)
	proxyAddr string // ZMQ (ex: tcp://proxy:5560)
	repPort   int    // local REP port

	clockOffset int64 = 0
	msgCount    int   = 0
	berkeleyMu  sync.Mutex

	currentCoordinatorMu sync.Mutex
	currentCoordinator   string

	globalCtx *zmq.Context
)
var serverHost string

// Helpers
func now() string {
	return time.Now().Format(time.RFC3339Nano)
}

func nowPhysical() int64 {
	berkeleyMu.Lock()
	defer berkeleyMu.Unlock()
	return time.Now().Unix() + clockOffset
}

func incClockBeforeSend() int {
	mu.Lock()
	logicalClock++
	v := logicalClock
	mu.Unlock()
	return v
}

func updateClockRecv(n int) {
	mu.Lock()
	if n > logicalClock {
		logicalClock = n
	}
	logicalClock++
	mu.Unlock()
}

// ----------------- ZMQ JSON REQ to REF -----------------
// REF runs a ZMQ REP that expects JSON payloads.
// This helper sends JSON bytes over ZMQ REQ and parses JSON reply.
func directReqZMQJSON(addr string, env Envelope, timeout time.Duration) (Envelope, error) {
	// create short-lived context/socket to avoid cross-thread usage
	ctx, _ := zmq.NewContext()
	defer ctx.Term()

	sock, err := ctx.NewSocket(zmq.REQ)
	if err != nil {
		return Envelope{}, err
	}
	defer sock.Close()
	sock.SetLinger(0)

	if err := sock.Connect(addr); err != nil {
		return Envelope{}, err
	}

	out, err := json.Marshal(env)
	if err != nil {
		return Envelope{}, err
	}

	if _, err := sock.SendBytes(out, 0); err != nil {
		return Envelope{}, err
	}
	if timeout > 0 {
		sock.SetRcvtimeo(timeout)
	}

	repBytes, err := sock.RecvBytes(0)
	if err != nil {
		return Envelope{}, err
	}

	var rep Envelope
	if err := json.Unmarshal(repBytes, &rep); err != nil {
		return Envelope{}, err
	}
	return rep, nil
}

// ----------------- MessagePack over ZMQ to peers -----------------
func directReqZMQ(addr string, env Envelope, timeout time.Duration) (Envelope, error) {
	// short-lived context/socket to avoid cross-thread usage
	ctx, _ := zmq.NewContext()
	defer ctx.Term()

	sock, err := ctx.NewSocket(zmq.REQ)
	if err != nil {
		return Envelope{}, err
	}
	defer sock.Close()
	sock.SetLinger(0)

	if err := sock.Connect(addr); err != nil {
		return Envelope{}, err
	}

	out, err := msgpack.Marshal(env)
	if err != nil {
		return Envelope{}, err
	}

	if _, err := sock.SendBytes(out, 0); err != nil {
		return Envelope{}, err
	}
	if timeout > 0 {
		sock.SetRcvtimeo(timeout)
	}
	repBytes, err := sock.RecvBytes(0)
	if err != nil {
		return Envelope{}, err
	}
	var rep Envelope
	if err := msgpack.Unmarshal(repBytes, &rep); err != nil {
		return Envelope{}, err
	}
	return rep, nil
}

// ----------------- REF interactions (rank/list/heartbeat) -----------------
func requestRank() {
	req := Envelope{
		Service:   "rank",
		Data:      map[string]interface{}{"user": serverHost, "port": repPort},
		Timestamp: now(),
		Clock:     incClockBeforeSend(),
	}
	rep, err := directReqZMQJSON(refAddr, req, 5*time.Second)
	if err != nil {
		log.Println("[REF] rank request erro:", err)
		return
	}
	updateClockRecv(rep.Clock)
	if v, ok := rep.Data["rank"]; ok {
		switch t := v.(type) {
		case float64:
			rank = int(t)
		case int:
			rank = t
		case int64:
			rank = int(t)
		}
	}
	log.Println("[REF] rank:", rank)
}

func requestList() ([]map[string]interface{}, error) {
	req := Envelope{
		Service:   "list",
		Data:      map[string]interface{}{},
		Timestamp: now(),
		Clock:     incClockBeforeSend(),
	}
	rep, err := directReqZMQJSON(refAddr, req, 5*time.Second)
	if err != nil {
		return nil, err
	}
	updateClockRecv(rep.Clock)

	rawList, ok := rep.Data["list"]
	if !ok {
		return nil, nil
	}
	if ilist, ok := rawList.([]interface{}); ok {
		out := make([]map[string]interface{}, 0, len(ilist))
		for _, it := range ilist {
			if m, ok := it.(map[string]interface{}); ok {
				out = append(out, m)
			}
		}
		return out, nil
	}
	return nil, nil
}

func sendHeartbeatLoop() {
	for {
		time.Sleep(5 * time.Second)
		req := Envelope{
			Service:   "heartbeat",
			Data:      map[string]interface{}{"user": serverHost, "port": repPort},
			Timestamp: now(),
			Clock:     incClockBeforeSend(),
		}
		if _, err := directReqZMQJSON(refAddr, req, 5*time.Second); err != nil {
			log.Println("[REF] heartbeat erro:", err)
		}
	}
}

// ----------------- REP loop (ZMQ) - receives MessagePack -----------------
func repLoop(rep *zmq.Socket) {
	for {
		bytesRecv, err := rep.RecvBytes(0)
		if err != nil {
			log.Println("[REP] recv erro:", err)
			continue
		}
		var req Envelope
		if err := msgpack.Unmarshal(bytesRecv, &req); err != nil {
			log.Println("[REP] msgpack decode erro:", err)
			continue
		}

		updateClockRecv(req.Clock)

		var resp Envelope
		switch req.Service {
		case "clock":
			resp = Envelope{
				Service:   "clock",
				Data:      map[string]interface{}{"time": nowPhysical()},
				Timestamp: now(),
				Clock:     incClockBeforeSend(),
			}
		case "adjust":
			adj := int64(0)
			if v, ok := req.Data["adjust"]; ok {
				switch t := v.(type) {
				case float64:
					adj = int64(t)
				case int:
					adj = int64(t)
				case int64:
					adj = t
				}
			}
			berkeleyMu.Lock()
			clockOffset += adj
			berkeleyMu.Unlock()
			resp = Envelope{
				Service:   "adjust",
				Data:      map[string]interface{}{"applied": adj, "new_time": nowPhysical()},
				Timestamp: now(),
				Clock:     incClockBeforeSend(),
			}
		case "election":
			resp = Envelope{
				Service:   "election",
				Data:      map[string]interface{}{"election": "OK"},
				Timestamp: now(),
				Clock:     incClockBeforeSend(),
			}
		default:
			resp = Envelope{
				Service:   "error",
				Data:      map[string]interface{}{"message": "unknown"},
				Timestamp: now(),
				Clock:     incClockBeforeSend(),
			}
		}
		outBytes, err := msgpack.Marshal(resp)
		if err != nil {
			log.Println("[REP] msgpack marshal erro:", err)
			continue
		}
		if _, err := rep.SendBytes(outBytes, 0); err != nil {
			log.Println("[REP] send erro:", err)
		}
	}
}

// ----------------- SUB for "servers" topic -----------------
func serversSubLoop(sub *zmq.Socket) {
	for {
		parts, err := sub.RecvMessageBytes(0)
		if err != nil {
			log.Println("[SUB servers] recv erro:", err)
			continue
		}
		if len(parts) < 2 {
			continue
		}
		topic := string(parts[0])
		if topic != "servers" {
			continue
		}
		var env Envelope
		if err := msgpack.Unmarshal(parts[1], &env); err != nil {
			log.Println("[SUB servers] decode erro:", err)
			continue
		}
		updateClockRecv(env.Clock)
		if env.Service == "election" {
			if c, ok := env.Data["coordinator"].(string); ok {
				currentCoordinatorMu.Lock()
				currentCoordinator = c
				currentCoordinatorMu.Unlock()
				log.Printf("[SUB servers] novo coordinator anunciado: %s (clock=%d)\n", c, env.Clock)
			}
		}
	}
}

// ----------------- publishCoordinator -----------------
func publishCoordinator(pub *zmq.Socket, coordinator string) {
	env := Envelope{
		Service:   "election",
		Data:      map[string]interface{}{"coordinator": coordinator},
		Timestamp: now(),
		Clock:     incClockBeforeSend(),
	}
	out, err := msgpack.Marshal(env)
	if err != nil {
		log.Println("[PUB] marshal erro:", err)
		return
	}
	if _, err := pub.SendMessage("servers", out); err != nil {
		log.Println("[PUB] send erro:", err)
	}
}

// ----------------- Berkeley (use peer port from REF list) -----------------
func runBerkeleyCoordinator(pub *zmq.Socket) {
	log.Println("[BERKELEY] coordinator triggered - collecting times")
	list, err := requestList()
	if err != nil {
		log.Println("[BERKELEY] erro obtendo lista do ref:", err)
		return
	}
	times := map[string]int64{}
	times[serverHost] = nowPhysical()

	for _, entry := range list {
		nRaw, ok := entry["name"]
		if !ok {
			continue
		}
		n, ok := nRaw.(string)
		if !ok || n == serverHost {
			continue
		}

		// get port from entry
		p := 0
		if pv, ok := entry["port"]; ok {
			switch v := pv.(type) {
			case float64:
				p = int(v)
			case int:
				p = v
			case int64:
				p = int(v)
			}
		}
		if p == 0 {
			log.Printf("[BERKELEY] porta desconhecida para %s, pulando\n", n)
			continue
		}

		addr := fmt.Sprintf("tcp://%s:%d", n, p)
		req := Envelope{
			Service:   "clock",
			Data:      map[string]interface{}{},
			Timestamp: now(),
			Clock:     incClockBeforeSend(),
		}
		rep, err := directReqZMQ(addr, req, 3*time.Second)
		if err != nil {
			log.Printf("[BERKELEY] não conseguiu falar com %s (%s): %v\n", n, addr, err)
			continue
		}
		if tval, ok := rep.Data["time"]; ok {
			switch tv := tval.(type) {
			case float64:
				times[n] = int64(tv)
			case int:
				times[n] = int64(tv)
			case int64:
				times[n] = tv
			}
		}
	}

	if len(times) == 0 {
		log.Println("[BERKELEY] nenhuma amostra coletada")
		return
	}

	var sum int64
	for _, v := range times {
		sum += v
	}
	avg := int64(math.Round(float64(sum) / float64(len(times))))
	log.Printf("[BERKELEY] média calculada (epoch secs) = %d (samples=%d)\n", avg, len(times))

	for srv, reported := range times {
		adjust := avg - reported
		if adjust == 0 {
			continue
		}
		if srv == serverHost {
			berkeleyMu.Lock()
			clockOffset += adjust
			berkeleyMu.Unlock()
			log.Printf("[BERKELEY] ajuste aplicado localmente: %d sec\n", adjust)
			continue
		}

		// find peer port in list
		p := 0
		for _, e := range list {
			if en, ok := e["name"].(string); ok && en == srv {
				if pv, ok := e["port"]; ok {
					switch v := pv.(type) {
					case float64:
						p = int(v)
					case int:
						p = v
					case int64:
						p = int(v)
					}
				}
			}
		}
		if p == 0 {
			log.Printf("[BERKELEY] porta desconhecida para %s, não ajustando\n", srv)
			continue
		}

		addr := fmt.Sprintf("tcp://%s:%d", srv, p)
		req := Envelope{
			Service:   "adjust",
			Data:      map[string]interface{}{"adjust": adjust},
			Timestamp: now(),
			Clock:     incClockBeforeSend(),
		}
		_, err := directReqZMQ(addr, req, 3*time.Second)
		if err != nil {
			log.Printf("[BERKELEY] falha ao enviar ajuste para %s: %v\n", srv, err)
		} else {
			log.Printf("[BERKELEY] ajuste enviado para %s: %d sec\n", srv, adjust)
		}
	}
}

// ----------------- maybeTriggerBerkeley -----------------
func maybeTriggerBerkeley(pub *zmq.Socket) {
	currentCoordinatorMu.Lock()
	coord := currentCoordinator
	currentCoordinatorMu.Unlock()

	if coord == "" {
		d, err := determineCoordinator()
		if err != nil {
			log.Println("[BERKELEY] erro ao determinar coordinator:", err)
			return
		}
		coord = d
	}

	if coord != serverHost {
		log.Println("[BERKELEY] não sou coordinator (coordinator=", coord, ")")
		return
	}
	runBerkeleyCoordinator(pub)
	publishCoordinator(pub, serverHost)
}

// ----------------- Election using peer ports -----------------
func probeElection(target string, list []map[string]interface{}) bool {
	// locate port for target in list
	p := 0
	for _, e := range list {
		if en, ok := e["name"].(string); ok && en == target {
			if pv, ok := e["port"]; ok {
				switch v := pv.(type) {
				case float64:
					p = int(v)
				case int:
					p = v
				case int64:
					p = int(v)
				}
			}
		}
	}
	if p == 0 {
		log.Printf("[ELECTION] porta desconhecida para %s\n", target)
		return false
	}

	addr := fmt.Sprintf("tcp://%s:%d", target, p)
	req := Envelope{
		Service:   "election",
		Data:      map[string]interface{}{},
		Timestamp: now(),
		Clock:     incClockBeforeSend(),
	}
	resp, err := directReqZMQ(addr, req, 3*time.Second)
	if err != nil {
		log.Printf("[ELECTION] Servidor %s NÃO respondeu: %v\n", target, err)
		return false
	}
	if val, ok := resp.Data["election"].(string); ok && val == "OK" {
		return true
	}
	return false
}

func determineCoordinator() (string, error) {
	list, err := requestList()
	if err != nil {
		return "", err
	}
	bestName := ""
	bestRank := int64(1<<60 - 1)
	for _, e := range list {
		n, ok1 := e["name"].(string)
		rv, ok2 := e["rank"]
		if !ok1 || !ok2 {
			continue
		}
		var rint int64
		switch t := rv.(type) {
		case float64:
			rint = int64(t)
		case int:
			rint = int64(t)
		case int64:
			rint = t
		default:
			continue
		}
		if rint < bestRank {
			bestRank = rint
			bestName = n
		}
	}
	if bestName == "" {
		return "", fmt.Errorf("nenhum coordinator encontrado")
	}
	return bestName, nil
}

func startElectionProcess(pub *zmq.Socket) {
	log.Println("[ELECTION] Iniciando eleição...")
	list, err := requestList()
	if err != nil {
		log.Println("[ELECTION] impossível obter lista:", err)
		return
	}
	myRank := rank
	for _, entry := range list {
		n, ok1 := entry["name"].(string)
		r, ok2 := entry["rank"]
		if !ok1 || !ok2 {
			continue
		}
		var rr int64
		switch t := r.(type) {
		case float64:
			rr = int64(t)
		case int:
			rr = int64(t)
		case int64:
			rr = t
		}
		if rr < int64(myRank) {
			if probeElection(n, list) {
				log.Printf("[ELECTION] Servidor %s com rank menor está vivo. Ele vai coordenar.\n", n)
				return
			}
		}
	}
	// nobody smaller responded: i'm coordinator
	log.Println("[ELECTION] Eu sou o novo coordenador:", serverHost)
	publishCoordinator(pub, serverHost)
}

// ----------------- publishLoop (example) -----------------
func publishLoop(pub *zmq.Socket) {
	localMsg := 0
	for {
		time.Sleep(3 * time.Second)
		env := Envelope{
			Service:   "sync_msg",
			Data:      map[string]interface{}{"text": fmt.Sprintf("msg %d from %s", localMsg, serverHost)},
			Timestamp: now(),
			Clock:     incClockBeforeSend(),
		}
		out, err := msgpack.Marshal(env)
		if err != nil {
			log.Println("[PUB] marshal erro:", err)
			continue
		}
		// send raw bytes (no topic) -> proxy will forward
		if _, err := pub.SendBytes(out, 0); err != nil {
			log.Println("[PUB] send erro:", err)
		}
		msgCount++
		localMsg++
		if msgCount%10 == 0 {
			go maybeTriggerBerkeley(pub)
		}
	}
}

// ----------------- main -----------------
func main() {
	name = os.Getenv("SERVER_NAME")
	if name == "" {
		name = "server_sync"
	}

	// serverHost is the Docker-resolvable hostname (what REF will store & Berkeley uses)
	serverHost = os.Getenv("SERVER_HOST")
	if serverHost == "" {
		// fallback: try to use container name derived from SERVER_NAME
		serverHost = name
	}

	refAddr = os.Getenv("REF_ADDR")
	if refAddr == "" {
		// must be ZMQ address for REF
		refAddr = "tcp://ref:6000"
	}
	proxyAddr = os.Getenv("PROXY_PUB_ADDR")
	if proxyAddr == "" {
		proxyAddr = "tcp://proxy:5560"
	}
	pstr := os.Getenv("SERVER_REP_PORT")
	if pstr == "" {
		repPort = 7000
	} else {
		v, err := strconv.Atoi(pstr)
		if err != nil {
			repPort = 7000
		} else {
			repPort = v
		}
	}

	log.Printf("[SYNC] iniciando: name=%s host=%s repPort=%d ref=%s proxy=%s\n", name, serverHost, repPort, refAddr, proxyAddr)

	ctx, err := zmq.NewContext()
	if err != nil {
		log.Fatal("[MAIN] erro criando contexto ZMQ:", err)
	}
	globalCtx = ctx
	defer globalCtx.Term()

	// PUB
	pub, err := ctx.NewSocket(zmq.PUB)
	if err != nil {
		log.Fatalf("[MAIN] erro criando PUB: %v", err)
	}
	defer pub.Close()
	pub.SetLinger(0)
	if err := pub.Connect(proxyAddr); err != nil {
		log.Println("[MAIN] warning: conectar PUB ao proxy falhou:", err)
	} else {
		log.Println("[MAIN] PUB conectado a", proxyAddr)
	}

	// REP
	rep, err := ctx.NewSocket(zmq.REP)
	if err != nil {
		log.Fatalf("[MAIN] erro criando REP: %v", err)
	}
	defer rep.Close()
	rep.SetLinger(0)
	bindAddr := fmt.Sprintf("tcp://*:%d", repPort)
	if err := rep.Bind(bindAddr); err != nil {
		log.Fatalf("[MAIN] erro bind REP %s: %v", bindAddr, err)
	}
	log.Println("[MAIN] REP bind em", bindAddr)

	// SUB servers
	subServers, err := ctx.NewSocket(zmq.SUB)
	if err != nil {
		log.Fatalf("[MAIN] erro criando SUB(servers): %v", err)
	}
	defer subServers.Close()
	subServers.SetLinger(0)
	if err := subServers.Connect(proxyAddr); err != nil {
		log.Println("[MAIN] warning: conectar SUB(servers) ao proxy falhou:", err)
	} else {
		if err := subServers.SetSubscribe("servers"); err != nil {
			log.Println("[MAIN] warning: SetSubscribe(servers) falhou:", err)
		}
		log.Println("[MAIN] SUB(servers) conectado a", proxyAddr)
	}

	// Start goroutines
	go repLoop(rep)
	go publishLoop(pub)
	go sendHeartbeatLoop()
	go serversSubLoop(subServers)

	// initial REF interactions
	requestRank()
	if lst, err := requestList(); err == nil {
		log.Println("[REF] lista inicial:", lst)
	} else {
		log.Println("[REF] request list erro:", err)
	}

	if coord, err := determineCoordinator(); err == nil {
		currentCoordinatorMu.Lock()
		currentCoordinator = coord
		currentCoordinatorMu.Unlock()
		log.Println("[MAIN] coordinator inicial:", coord)
	}

	select {}
}
