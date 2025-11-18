package main

// ----------------------------------------------
// handler chamado no coordenador quando um nó pede sync_request
// ----------------------------------------------
func handleSyncRequest(env Envelope) Envelope {
    logsMu.Lock()
    full := make([]LogEntry, len(logs))
    copy(full, logs)
    logsMu.Unlock()

    return Envelope{
        Service:   "sync_response",
        Timestamp: nowISO(),
        Clock:     incClockBeforeSend(),
        Data: map[string]interface{}{
            "logs": full,
        },
    }
}
