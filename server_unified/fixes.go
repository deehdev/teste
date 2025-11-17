package main

// decodeEnvelope usado por rep_loop & sub_loop
func decodeEnvelope(b []byte) (Envelope, error) {
	env := msgpackUnmarshal(b)
	if env.Service != "" {
		return env, nil
	}
	// fallback: JSON
	return jsonUnmarshal(b)
}

// updateClock — Lamport thread-safe
func updateClock(recv int) int {
	logicalClockMu.Lock()
	defer logicalClockMu.Unlock()

	if recv > logicalClock {
		logicalClock = recv
	}
	logicalClock++
	return logicalClock
}

// heartbeat para REP
func handleHeartbeat(env Envelope) Envelope {
	return Envelope{
		Service:   "heartbeat",
		Timestamp: nowISO(),
		Clock:     incClockBeforeSend(),
		Data:      map[string]interface{}{"status": "ok"},
	}
}
