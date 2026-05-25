package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ternura/agent"
	"ternura/tool"
)

const (
	memoryStoreVersion         = 1
	memoryDirName              = "memory"
	longTermMemoryFileName     = "long_term.json"
	sessionMemoryFileName      = "memory.json"
	toolArtifactsDirName       = "tool_artifacts"
	defaultMaxLongTermMemories = 120
	defaultShortTermTurnLimit  = 12
	defaultToolMemoryLimit     = 80
	defaultToolContextLimit    = 5
	maxMemoryContentRunes      = 500
	maxShortTermFieldRunes     = 700
	maxToolSummaryRunes        = 700
	maxToolArgumentRunes       = 500
	maxToolPreviewRunes        = 500
)

type memoryStore struct {
	mu                  sync.Mutex
	root                string
	maxLongTermMemories int
	shortTermTurnLimit  int
	toolMemoryLimit     int
	toolContextLimit    int
}

type longTermMemoryFile struct {
	Version   int            `json:"version"`
	UpdatedAt string         `json:"updated_at,omitempty"`
	Memories  []memoryRecord `json:"memories,omitempty"`
}

type memoryRecord struct {
	ID         string `json:"id"`
	Category   string `json:"category"`
	Content    string `json:"content"`
	Source     string `json:"source,omitempty"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	UseCount   int    `json:"use_count,omitempty"`
	LastUsedAt string `json:"last_used_at,omitempty"`
}

type shortTermMemoryFile struct {
	Version      int                `json:"version"`
	SessionID    string             `json:"session_id"`
	Summary      string             `json:"summary,omitempty"`
	UpdatedAt    string             `json:"updated_at,omitempty"`
	Turns        []shortTermTurn    `json:"turns,omitempty"`
	ToolMemories []toolMemoryRecord `json:"tool_memories,omitempty"`
}

type shortTermTurn struct {
	User      string `json:"user"`
	Assistant string `json:"assistant,omitempty"`
	At        string `json:"at"`
}

type toolMemoryRecord struct {
	ID          string `json:"id"`
	CallID      string `json:"call_id,omitempty"`
	Tool        string `json:"tool"`
	Arguments   string `json:"arguments,omitempty"`
	Summary     string `json:"summary"`
	RawRef      string `json:"raw_ref,omitempty"`
	OutputRunes int    `json:"output_runes,omitempty"`
	Error       string `json:"error,omitempty"`
	CreatedAt   string `json:"created_at"`
}

type memoryStatusResponse struct {
	CurrentSessionID string `json:"current_session_id"`
	LongTermCount    int    `json:"long_term_count"`
	ShortTermTurns   int    `json:"short_term_turns"`
	ToolMemoryCount  int    `json:"tool_memory_count,omitempty"`
	ShortTermSummary string `json:"short_term_summary,omitempty"`
}

type memoryDetailResponse struct {
	CurrentSessionID string              `json:"current_session_id"`
	LongTerm         []memoryRecord      `json:"long_term"`
	ShortTerm        shortTermMemoryFile `json:"short_term"`
}

func newMemoryStore(root string) *memoryStore {
	return &memoryStore{
		root:                root,
		maxLongTermMemories: defaultMaxLongTermMemories,
		shortTermTurnLimit:  defaultShortTermTurnLimit,
		toolMemoryLimit:     defaultToolMemoryLimit,
		toolContextLimit:    defaultToolContextLimit,
	}
}

func (m *memoryStore) Remember(ctx context.Context, item tool.MemoryItem) (tool.MemoryResult, error) {
	select {
	case <-ctx.Done():
		return tool.MemoryResult{}, ctx.Err()
	default:
	}

	normalized, err := tool.NormalizeMemoryItem(item)
	if err != nil {
		return tool.MemoryResult{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	file, err := m.loadLongTermLocked()
	if err != nil {
		return tool.MemoryResult{}, err
	}

	now := time.Now()
	timestamp := now.Format(time.RFC3339Nano)
	key := memoryContentKey(normalized.Content)
	for idx := range file.Memories {
		if memoryContentKey(file.Memories[idx].Content) != key {
			continue
		}
		file.Memories[idx].Category = normalized.Category
		file.Memories[idx].Source = normalized.Source
		file.Memories[idx].UpdatedAt = timestamp
		if err := m.saveLongTermLocked(file, now); err != nil {
			return tool.MemoryResult{}, err
		}
		return tool.MemoryResult{
			ID:       file.Memories[idx].ID,
			Category: file.Memories[idx].Category,
			Content:  file.Memories[idx].Content,
		}, nil
	}

	record := memoryRecord{
		ID:        newMemoryID(now),
		Category:  normalized.Category,
		Content:   truncateRunes(normalized.Content, maxMemoryContentRunes),
		Source:    normalized.Source,
		CreatedAt: timestamp,
		UpdatedAt: timestamp,
	}
	file.Memories = append(file.Memories, record)
	trimLongTermMemories(&file, m.maxLongTermMemories)
	if err := m.saveLongTermLocked(file, now); err != nil {
		return tool.MemoryResult{}, err
	}

	return tool.MemoryResult{
		ID:       record.ID,
		Category: record.Category,
		Content:  record.Content,
	}, nil
}

func (m *memoryStore) Forget(ctx context.Context, id string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("memory id is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	file, err := m.loadLongTermLocked()
	if err != nil {
		return err
	}
	for idx := range file.Memories {
		if file.Memories[idx].ID != id {
			continue
		}
		file.Memories = append(file.Memories[:idx], file.Memories[idx+1:]...)
		return m.saveLongTermLocked(file, time.Now())
	}
	return fmt.Errorf("memory %q not found", id)
}

func (m *memoryStore) RuntimeContext(sessionID string) (string, error) {
	return m.RuntimeContextForQuery(sessionID, "")
}

func (m *memoryStore) RuntimeContextForQuery(sessionID string, query string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	longTerm, err := m.loadLongTermLocked()
	if err != nil {
		return "", err
	}
	shortTerm, err := m.loadShortTermLocked(sessionID)
	if err != nil {
		return "", err
	}

	sections := make([]string, 0, 8)
	if len(longTerm.Memories) > 0 {
		sections = append(sections, "Long-term memory:")
		for _, memory := range sortMemoriesForContext(longTerm.Memories) {
			sections = append(sections, fmt.Sprintf("- [%s][%s] %s", memory.ID, memory.Category, memory.Content))
		}
	}
	if shortTerm.Summary != "" || len(shortTerm.Turns) > 0 {
		if len(sections) > 0 {
			sections = append(sections, "")
		}
		sections = append(sections, "Short-term session memory:")
		if shortTerm.Summary != "" {
			sections = append(sections, "Summary: "+shortTerm.Summary)
		}
		for _, turn := range shortTerm.Turns {
			line := "- User: " + turn.User
			if turn.Assistant != "" {
				line += " | Assistant: " + turn.Assistant
			}
			sections = append(sections, line)
		}
	}
	toolMemories := selectToolMemoriesForContext(shortTerm.ToolMemories, query, m.toolContextLimit)
	if len(toolMemories) > 0 {
		if len(sections) > 0 {
			sections = append(sections, "")
		}
		sections = append(sections, "Relevant tool memory:")
		for _, memory := range toolMemories {
			line := fmt.Sprintf("- [%s][%s] %s", memory.ID, memory.Tool, memory.Summary)
			if memory.RawRef != "" {
				line += fmt.Sprintf(" (raw_ref: %s)", memory.RawRef)
			}
			sections = append(sections, line)
		}
	}
	if len(sections) == 0 {
		return "", nil
	}
	sections = append(sections, "", "Memory rules: use these memories only when relevant; do not reveal memory ids unless the user asks; use remember only for durable explicit facts/preferences/instructions; use forget_memory when a stored memory is stale or the user asks to forget it; tool memory is a compact summary of previous tool results, and raw_ref can be read only when the original output is truly needed.")
	return strings.Join(sections, "\n"), nil
}

func (m *memoryStore) AppendShortTermTurn(sessionID string, userMessage string, result agent.AgentRunResult) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	user := truncateRunes(strings.Join(strings.Fields(userMessage), " "), maxShortTermFieldRunes)
	assistant := truncateRunes(strings.Join(strings.Fields(result.Content), " "), maxShortTermFieldRunes)
	if user == "" && assistant == "" {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	file, err := m.loadShortTermLocked(sessionID)
	if err != nil {
		return err
	}
	now := time.Now()
	file.SessionID = sessionID
	file.Version = memoryStoreVersion
	file.UpdatedAt = now.Format(time.RFC3339Nano)
	file.Turns = append(file.Turns, shortTermTurn{
		User:      user,
		Assistant: assistant,
		At:        file.UpdatedAt,
	})
	if len(file.Turns) > m.shortTermTurnLimit {
		file.Turns = file.Turns[len(file.Turns)-m.shortTermTurnLimit:]
	}
	file.Summary = summarizeShortTerm(file.Turns)
	return m.saveShortTermLocked(sessionID, file)
}

func (m *memoryStore) CaptureToolMemory(ctx context.Context, sessionID string, result agent.ToolResult) (toolMemoryRecord, bool, error) {
	select {
	case <-ctx.Done():
		return toolMemoryRecord{}, false, ctx.Err()
	default:
	}

	sessionID = strings.TrimSpace(sessionID)
	content := strings.TrimSpace(result.Content)
	if sessionID == "" || content == "" {
		return toolMemoryRecord{}, false, nil
	}

	now := time.Now()
	record := newToolMemoryRecord(now, sessionID, result)

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(m.toolArtifactsPath(sessionID), 0o700); err != nil {
		return toolMemoryRecord{}, false, err
	}
	if err := os.WriteFile(filepath.Join(m.root, filepath.FromSlash(record.RawRef)), []byte(result.Content), 0o600); err != nil {
		return toolMemoryRecord{}, false, err
	}
	return record, true, nil
}

func (m *memoryStore) AppendToolMemories(sessionID string, records []toolMemoryRecord) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || len(records) == 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	file, err := m.loadShortTermLocked(sessionID)
	if err != nil {
		return err
	}
	now := time.Now()
	file.SessionID = sessionID
	file.Version = memoryStoreVersion
	file.UpdatedAt = now.Format(time.RFC3339Nano)
	file.ToolMemories = append(file.ToolMemories, records...)
	trimToolMemories(&file, m.toolMemoryLimit)
	return m.saveShortTermLocked(sessionID, file)
}

func (m *memoryStore) Status(sessionID string) (memoryStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	longTerm, err := m.loadLongTermLocked()
	if err != nil {
		return memoryStatusResponse{}, err
	}
	shortTerm, err := m.loadShortTermLocked(sessionID)
	if err != nil {
		return memoryStatusResponse{}, err
	}
	return memoryStatusResponse{
		CurrentSessionID: sessionID,
		LongTermCount:    len(longTerm.Memories),
		ShortTermTurns:   len(shortTerm.Turns),
		ToolMemoryCount:  len(shortTerm.ToolMemories),
		ShortTermSummary: shortTerm.Summary,
	}, nil
}

func (m *memoryStore) Detail(sessionID string) (memoryDetailResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	longTerm, err := m.loadLongTermLocked()
	if err != nil {
		return memoryDetailResponse{}, err
	}
	shortTerm, err := m.loadShortTermLocked(sessionID)
	if err != nil {
		return memoryDetailResponse{}, err
	}
	return memoryDetailResponse{
		CurrentSessionID: sessionID,
		LongTerm:         sortMemoriesForContext(longTerm.Memories),
		ShortTerm:        shortTerm,
	}, nil
}

func (m *memoryStore) loadLongTermLocked() (longTermMemoryFile, error) {
	file := longTermMemoryFile{
		Version:  memoryStoreVersion,
		Memories: make([]memoryRecord, 0),
	}
	if err := readJSONFile(m.longTermPath(), &file); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return file, nil
		}
		return longTermMemoryFile{}, err
	}
	if file.Version == 0 {
		file.Version = memoryStoreVersion
	}
	if file.Memories == nil {
		file.Memories = make([]memoryRecord, 0)
	}
	return file, nil
}

func (m *memoryStore) saveLongTermLocked(file longTermMemoryFile, now time.Time) error {
	file.Version = memoryStoreVersion
	file.UpdatedAt = now.Format(time.RFC3339Nano)
	return writeJSONAtomic(m.longTermPath(), file)
}

func (m *memoryStore) loadShortTermLocked(sessionID string) (shortTermMemoryFile, error) {
	file := shortTermMemoryFile{
		Version:   memoryStoreVersion,
		SessionID: sessionID,
		Turns:     make([]shortTermTurn, 0),
	}
	if strings.TrimSpace(sessionID) == "" {
		return file, nil
	}
	if err := readJSONFile(m.shortTermPath(sessionID), &file); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return file, nil
		}
		return shortTermMemoryFile{}, err
	}
	if file.Version == 0 {
		file.Version = memoryStoreVersion
	}
	if file.SessionID == "" {
		file.SessionID = sessionID
	}
	if file.Turns == nil {
		file.Turns = make([]shortTermTurn, 0)
	}
	if file.ToolMemories == nil {
		file.ToolMemories = make([]toolMemoryRecord, 0)
	}
	return file, nil
}

func (m *memoryStore) saveShortTermLocked(sessionID string, file shortTermMemoryFile) error {
	return writeJSONAtomic(m.shortTermPath(sessionID), file)
}

func (m *memoryStore) longTermPath() string {
	return filepath.Join(m.root, memoryDirName, longTermMemoryFileName)
}

func (m *memoryStore) shortTermPath(sessionID string) string {
	return filepath.Join(m.root, sessionsDirName, sessionID, sessionMemoryFileName)
}

func (m *memoryStore) toolArtifactsPath(sessionID string) string {
	return filepath.Join(m.root, sessionsDirName, sessionID, toolArtifactsDirName)
}

func newMemoryID(now time.Time) string {
	return fmt.Sprintf("memory-%s", now.UTC().Format("20060102T150405.000000000"))
}

func memoryContentKey(content string) string {
	return strings.ToLower(strings.Join(strings.Fields(content), " "))
}

func trimLongTermMemories(file *longTermMemoryFile, limit int) {
	if limit <= 0 || len(file.Memories) <= limit {
		return
	}
	sort.SliceStable(file.Memories, func(i, j int) bool {
		return file.Memories[i].UpdatedAt < file.Memories[j].UpdatedAt
	})
	file.Memories = file.Memories[len(file.Memories)-limit:]
}

func trimToolMemories(file *shortTermMemoryFile, limit int) {
	if limit <= 0 || len(file.ToolMemories) <= limit {
		return
	}
	file.ToolMemories = file.ToolMemories[len(file.ToolMemories)-limit:]
}

func sortMemoriesForContext(memories []memoryRecord) []memoryRecord {
	sorted := append([]memoryRecord(nil), memories...)
	if sorted == nil {
		return make([]memoryRecord, 0)
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].UpdatedAt > sorted[j].UpdatedAt
	})
	return sorted
}

func summarizeShortTerm(turns []shortTermTurn) string {
	if len(turns) == 0 {
		return ""
	}
	start := len(turns) - 5
	if start < 0 {
		start = 0
	}
	parts := make([]string, 0, len(turns)-start)
	for _, turn := range turns[start:] {
		if turn.User != "" {
			parts = append(parts, turn.User)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return truncateRunes("Recent user intents: "+strings.Join(parts, " / "), 1200)
}

func newToolMemoryRecord(now time.Time, sessionID string, result agent.ToolResult) toolMemoryRecord {
	toolName := strings.TrimSpace(result.Call.Function.Name)
	if toolName == "" {
		toolName = "unknown"
	}
	callID := strings.TrimSpace(result.Call.ID)
	id := newToolMemoryID(now, toolName, callID)
	rawRef := filepath.ToSlash(filepath.Join(sessionsDirName, sessionID, toolArtifactsDirName, id+".txt"))
	arguments := truncateRunes(strings.Join(strings.Fields(result.Call.Function.Arguments), " "), maxToolArgumentRunes)
	return toolMemoryRecord{
		ID:          id,
		CallID:      callID,
		Tool:        toolName,
		Arguments:   arguments,
		Summary:     summarizeToolMemory(result, arguments),
		RawRef:      rawRef,
		OutputRunes: len([]rune(result.Content)),
		Error:       result.ErrorString(),
		CreatedAt:   now.Format(time.RFC3339Nano),
	}
}

func summarizeToolMemory(result agent.ToolResult, arguments string) string {
	toolName := strings.TrimSpace(result.Call.Function.Name)
	if toolName == "" {
		toolName = "unknown"
	}
	preview := truncateRunes(strings.Join(strings.Fields(result.Content), " "), maxToolPreviewRunes)
	parts := []string{
		fmt.Sprintf("Called %s", toolName),
		fmt.Sprintf("output %d characters", len([]rune(result.Content))),
	}
	if arguments != "" {
		parts = append(parts, "arguments: "+arguments)
	}
	if errText := result.ErrorString(); errText != "" {
		parts = append(parts, "error: "+truncateRunes(errText, 200))
	}
	if preview != "" {
		parts = append(parts, "preview: "+preview)
	}
	return truncateRunes(strings.Join(parts, "; "), maxToolSummaryRunes)
}

func newToolMemoryID(now time.Time, toolName string, callID string) string {
	parts := []string{
		"toolmem",
		now.UTC().Format("20060102T150405.000000000"),
		safeArtifactNamePart(toolName),
	}
	if safeCallID := safeArtifactNamePart(callID); safeCallID != "" {
		parts = append(parts, safeCallID)
	}
	return strings.Join(parts, "-")
}

func safeArtifactNamePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}
	return strings.Trim(builder.String(), "_")
}

func selectToolMemoriesForContext(records []toolMemoryRecord, query string, limit int) []toolMemoryRecord {
	if limit <= 0 || len(records) == 0 {
		return nil
	}

	sorted := append([]toolMemoryRecord(nil), records...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].CreatedAt > sorted[j].CreatedAt
	})

	type scoredRecord struct {
		record toolMemoryRecord
		score  int
	}
	scored := make([]scoredRecord, 0, len(sorted))
	for _, record := range sorted {
		score := scoreToolMemory(record, query)
		if score > 0 {
			scored = append(scored, scoredRecord{record: record, score: score})
		}
	}

	if len(scored) == 0 && looksLikeToolMemoryReference(query) {
		for idx, record := range sorted {
			if idx >= limit {
				break
			}
			scored = append(scored, scoredRecord{record: record})
		}
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].record.CreatedAt > scored[j].record.CreatedAt
	})

	selected := make([]toolMemoryRecord, 0, minInt(limit, len(scored)))
	for idx, item := range scored {
		if idx >= limit {
			break
		}
		selected = append(selected, item.record)
	}
	return selected
}

func scoreToolMemory(record toolMemoryRecord, query string) int {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 0
	}
	text := strings.ToLower(strings.Join([]string{
		record.Tool,
		record.Arguments,
		record.Summary,
		record.RawRef,
	}, " "))
	score := 0
	for _, token := range keywordTokens(query) {
		if strings.Contains(text, token) {
			score++
		}
	}
	return score
}

func keywordTokens(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z':
			return false
		case r >= '0' && r <= '9':
			return false
		case r == '_' || r == '-' || r == '.' || r == '/':
			return false
		default:
			return true
		}
	})
	tokens := make([]string, 0, len(fields)+1)
	seen := make(map[string]struct{}, len(fields)+1)
	for _, field := range fields {
		field = strings.Trim(field, " .-/")
		if len([]rune(field)) < 2 {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		tokens = append(tokens, field)
	}
	if len(tokens) == 0 && len([]rune(query)) >= 2 {
		tokens = append(tokens, query)
	}
	return tokens
}

func looksLikeToolMemoryReference(query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return false
	}
	for _, marker := range []string{
		"刚刚", "刚才", "之前", "前面", "上次", "那个", "结果", "输出", "工具", "命令", "文件",
		"previous", "last", "tool", "output", "result", "command", "file",
	} {
		if strings.Contains(query, marker) {
			return true
		}
	}
	return false
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}
