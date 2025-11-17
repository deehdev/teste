package main

import "log"

// chamado quando recebemos evento tópico "servers"
func applyCoordinatorUpdate(env Envelope) {
    coord, ok := env.Data["coordinator"].(string)
    if !ok {
        log.Println("[COORD] Erro: campo 'coordinator' inválido")
        return
    }

    currentCoordinatorMu.Lock()
    currentCoordinator = coord
    currentCoordinatorMu.Unlock()

    log.Println("[COORD] Atualizado para:", coord)
}
