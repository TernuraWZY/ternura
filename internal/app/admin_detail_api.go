package app

import (
	"net/http"
	"strings"
)

const adminRecentRunLimit = 20

func (s *agentServer) handleSystemDetail(w http.ResponseWriter, r *http.Request) {
	if !authorizeTaskAPI(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	section := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/system/"), "/")
	if section == "" || strings.Contains(section, "/") {
		http.NotFound(w, r)
		return
	}

	switch section {
	case "skills":
		inventory := s.runtimeInventoryDetails()
		writeJSON(w, http.StatusOK, map[string]any{
			"section": "skills",
			"skills":  inventory.Skills,
		})
	case "tools":
		inventory := s.runtimeInventoryDetails()
		writeJSON(w, http.StatusOK, map[string]any{
			"section": "tools",
			"tools":   inventory.Tools,
		})
	case "mcp-tools":
		inventory := s.runtimeInventoryDetails()
		tools := make([]runtimeToolView, 0)
		for _, currentTool := range inventory.Tools {
			if currentTool.MCP {
				tools = append(tools, currentTool)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"section": "mcp-tools",
			"tools":   tools,
		})
	case "session":
		s.handleCurrentSessionDetail(w, r)
	case "long-term-memory", "tool-memory", "short-term-memory":
		s.handleMemoryDetail(w, r, section)
	default:
		http.NotFound(w, r)
	}
}

func (s *agentServer) handleCurrentSessionDetail(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.store == nil {
		http.Error(w, "session store unavailable", http.StatusServiceUnavailable)
		return
	}
	snapshot := s.store.Snapshot()
	session := findSession(snapshot.Sessions, snapshot.CurrentSessionID)
	if session == nil {
		http.NotFound(w, r)
		return
	}
	recentRuns := make([]taskSummary, 0, min(len(session.Runs), adminRecentRunLimit))
	for idx := len(session.Runs) - 1; idx >= 0 && len(recentRuns) < adminRecentRunLimit; idx-- {
		recentRuns = append(recentRuns, summarizeTask(session.SessionID, session.Runs[idx]))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"section":     "session",
		"session":     summarizeSession(*session, true),
		"recent_runs": recentRuns,
		"todos":       append([]persistedTodo(nil), session.Todos...),
	})
}

func (s *agentServer) handleMemoryDetail(w http.ResponseWriter, r *http.Request, section string) {
	if s == nil || s.store == nil || s.memory == nil {
		http.Error(w, "memory store unavailable", http.StatusServiceUnavailable)
		return
	}
	sessionID := s.store.CurrentSessionID()
	detail, err := s.memory.Detail(sessionID)
	if err != nil {
		http.Error(w, "read memory detail: "+err.Error(), http.StatusInternalServerError)
		return
	}
	switch section {
	case "long-term-memory":
		writeJSON(w, http.StatusOK, map[string]any{
			"section":    section,
			"session_id": sessionID,
			"memories":   detail.LongTerm,
		})
	case "tool-memory":
		writeJSON(w, http.StatusOK, map[string]any{
			"section":    section,
			"session_id": sessionID,
			"memories":   reversedToolMemories(detail.ShortTerm.ToolMemories),
		})
	case "short-term-memory":
		writeJSON(w, http.StatusOK, map[string]any{
			"section":    section,
			"session_id": sessionID,
			"summary":    detail.ShortTerm.Summary,
			"updated_at": detail.ShortTerm.UpdatedAt,
			"turns":      reversedShortTermTurns(detail.ShortTerm.Turns),
		})
	}
}

func reversedToolMemories(source []toolMemoryRecord) []toolMemoryRecord {
	reversed := make([]toolMemoryRecord, len(source))
	for idx := range source {
		reversed[len(source)-1-idx] = source[idx]
	}
	return reversed
}

func reversedShortTermTurns(source []shortTermTurn) []shortTermTurn {
	reversed := make([]shortTermTurn, len(source))
	for idx := range source {
		reversed[len(source)-1-idx] = source[idx]
	}
	return reversed
}
