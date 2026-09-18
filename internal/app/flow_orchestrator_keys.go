// Ключ провайдера, с которым запущен поток.
//
// Прогон длится дольше одного вызова, а ключ приходит с запуском. Он живёт в
// памяти процесса ровно столько, сколько идёт прогон, и снимается вместе с ним.
package app

import (
	"strings"
)

func (a *App) rememberFlowOrchestratorKey(flowRunID, apiKey string) {
	flowRunID = strings.TrimSpace(flowRunID)
	apiKey = strings.TrimSpace(apiKey)
	if flowRunID == "" || apiKey == "" {
		return
	}
	a.flowOrchestratorKeysMu.Lock()
	defer a.flowOrchestratorKeysMu.Unlock()
	if a.flowOrchestratorKeys == nil {
		a.flowOrchestratorKeys = map[string]string{}
	}
	a.flowOrchestratorKeys[flowRunID] = apiKey
}

func (a *App) flowOrchestratorKey(flowRunID string) string {
	flowRunID = strings.TrimSpace(flowRunID)
	if flowRunID == "" {
		return ""
	}
	a.flowOrchestratorKeysMu.Lock()
	defer a.flowOrchestratorKeysMu.Unlock()
	return a.flowOrchestratorKeys[flowRunID]
}

func (a *App) clearFlowOrchestratorKey(flowRunID string) {
	flowRunID = strings.TrimSpace(flowRunID)
	if flowRunID == "" {
		return
	}
	a.flowOrchestratorKeysMu.Lock()
	defer a.flowOrchestratorKeysMu.Unlock()
	delete(a.flowOrchestratorKeys, flowRunID)
}
