package main

import "log"

func applySyncResponse(logsRemote []LogEntry) {
    logsMu.Lock()
    logs = logsRemote
    logsMu.Unlock()

    persistLogs()

    log.Printf("[SYNC] Sincronizado %d logs.", len(logsRemote))
}
