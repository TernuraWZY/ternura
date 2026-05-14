package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/joho/godotenv"

	"ternura"
	"ternura/shared"
	"ternura/tool"
)

func main() {
	_ = godotenv.Load()

	query := flag.String("q", "hello", "prompt text")
	serve := flag.Bool("serve", false, "run web console")
	addr := flag.String("addr", ":8080", "web server address")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	modelConf := shared.NewModelConfig()

	if *serve {
		server := newAgentServer(modelConf)
		log.Printf("serving Ternura console on http://localhost%s", *addr)
		if err := http.ListenAndServe(*addr, server.routes()); err != nil {
			log.Fatalf("server error: %v", err)
		}
		return
	}

	agent := ternura.NewAgent(modelConf, ternura.CodingAgentSystemPrompt, []tool.Tool{
		tool.NewReadTool(),
		tool.NewEditTool(),
		tool.NewWriteTool(),
		tool.NewBashTool(),
	})
	result, err := agent.RunWithTrace(ctx, *query)
	if err != nil {
		log.Printf("agent run error: %v", err)
		return
	}

	for _, item := range result.Trace {
		log.Printf("agent trace [%s] %s:\n%s", item.Type, item.Title, item.Content)
	}
	log.Printf("agent result: %s", result.Content)
}

type agentServer struct {
	modelConf shared.ModelConfig
	mu        sync.Mutex
	agent     *ternura.Agent
}

type chatRequest struct {
	Message string `json:"message"`
}

type chatResponse struct {
	Content    string                   `json:"content,omitempty"`
	Trace      []ternura.AgentTraceItem `json:"trace,omitempty"`
	RawContent string                   `json:"raw_content,omitempty"`
	Error      string                   `json:"error,omitempty"`
}

func newAgentServer(modelConf shared.ModelConfig) *agentServer {
	s := &agentServer{modelConf: modelConf}
	s.resetAgent()
	return s
}

func (s *agentServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat", s.handleChat)
	mux.HandleFunc("/api/chat/stream", s.handleChatStream)
	mux.HandleFunc("/api/reset", s.handleReset)
	mux.Handle("/", http.FileServer(http.Dir("web")))
	return mux
}

func (s *agentServer) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, chatResponse{Error: "invalid request"})
		return
	}
	if req.Message == "" {
		writeJSON(w, http.StatusBadRequest, chatResponse{Error: "message is required"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.agent.RunWithTrace(r.Context(), req.Message)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, chatResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, chatResponse{
		Content:    result.Content,
		Trace:      result.Trace,
		RawContent: result.RawContent,
	})
}

func (s *agentServer) handleChatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.Message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	s.mu.Lock()
	defer s.mu.Unlock()

	streamer := newSmoothStreamWriter(r.Context(), w, flusher)
	defer func() {
		_ = streamer.Close()
	}()
	emit := streamer.Emit

	if err := emit(ternura.AgentStreamEvent{Type: "start"}); err != nil {
		return
	}

	if _, err := s.agent.RunStreaming(r.Context(), req.Message, emit); err != nil {
		_ = emit(ternura.AgentStreamEvent{
			Type:  "error",
			Error: err.Error(),
		})
	}
}

const (
	defaultStreamChunkRunes = 3
	defaultStreamIntervalMS = 35
	streamChunkRunesEnvKey  = "TERNURA_STREAM_CHUNK_RUNES"
	streamIntervalMSEnvKey  = "TERNURA_STREAM_INTERVAL_MS"
	traceTypeThink          = "think"
	eventTypeContentDelta   = "content_delta"
	eventTypeTraceStart     = "trace_start"
	eventTypeTraceDelta     = "trace_delta"
	eventTypeTraceDone      = "trace_done"
)

type smoothStreamWriter struct {
	ctx            context.Context
	w              http.ResponseWriter
	flusher        http.Flusher
	chunkRunes     int
	interval       time.Duration
	events         chan ternura.AgentStreamEvent
	done           chan struct{}
	closeOnce      sync.Once
	errMu          sync.Mutex
	err            error
	lastSmoothSent time.Time
	traceTypes     map[string]string
}

func newSmoothStreamWriter(ctx context.Context, w http.ResponseWriter, flusher http.Flusher) *smoothStreamWriter {
	chunkRunes := envInt(streamChunkRunesEnvKey, defaultStreamChunkRunes)
	if chunkRunes < 1 {
		chunkRunes = defaultStreamChunkRunes
	}

	intervalMS := envInt(streamIntervalMSEnvKey, defaultStreamIntervalMS)
	if intervalMS < 0 {
		intervalMS = defaultStreamIntervalMS
	}

	streamer := &smoothStreamWriter{
		ctx:        ctx,
		w:          w,
		flusher:    flusher,
		chunkRunes: chunkRunes,
		interval:   time.Duration(intervalMS) * time.Millisecond,
		events:     make(chan ternura.AgentStreamEvent, 128),
		done:       make(chan struct{}),
		traceTypes: make(map[string]string),
	}
	go streamer.run()
	return streamer
}

func (s *smoothStreamWriter) Emit(event ternura.AgentStreamEvent) error {
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case <-s.done:
		return s.getErr()
	case s.events <- event:
		return nil
	}
}

func (s *smoothStreamWriter) Close() error {
	s.closeOnce.Do(func() {
		close(s.events)
	})
	<-s.done
	return s.getErr()
}

func (s *smoothStreamWriter) run() {
	defer close(s.done)
	for event := range s.events {
		if err := s.writeEvent(event); err != nil {
			s.setErr(err)
			return
		}
	}
}

func (s *smoothStreamWriter) writeEvent(event ternura.AgentStreamEvent) error {
	switch event.Type {
	case eventTypeContentDelta:
		return s.emitDelta(event, true)
	case eventTypeTraceStart:
		s.traceTypes[event.ID] = event.TraceType
		return writeSSE(s.w, s.flusher, event)
	case eventTypeTraceDelta:
		return s.emitDelta(event, s.traceTypes[event.ID] == traceTypeThink)
	case eventTypeTraceDone:
		delete(s.traceTypes, event.ID)
		return writeSSE(s.w, s.flusher, event)
	default:
		return writeSSE(s.w, s.flusher, event)
	}
}

func (s *smoothStreamWriter) setErr(err error) {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	s.err = err
}

func (s *smoothStreamWriter) getErr() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

func (s *smoothStreamWriter) emitDelta(event ternura.AgentStreamEvent, smooth bool) error {
	if event.Delta == "" {
		return nil
	}
	if !smooth || s.interval == 0 {
		return writeSSE(s.w, s.flusher, event)
	}

	chunks := chunkStringByRunes(event.Delta, s.chunkRunes)
	for _, chunk := range chunks {
		if err := s.waitForCadence(); err != nil {
			return err
		}
		next := event
		next.Delta = chunk
		if err := writeSSE(s.w, s.flusher, next); err != nil {
			return err
		}
		s.lastSmoothSent = time.Now()
	}
	return nil
}

func (s *smoothStreamWriter) waitForCadence() error {
	if s.interval == 0 || s.lastSmoothSent.IsZero() {
		return nil
	}

	delay := s.interval - time.Since(s.lastSmoothSent)
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case <-timer.C:
		return nil
	}
}

func chunkStringByRunes(value string, chunkRunes int) []string {
	if value == "" {
		return nil
	}
	if chunkRunes < 1 {
		chunkRunes = 1
	}

	runes := []rune(value)
	chunks := make([]string, 0, (len(runes)+chunkRunes-1)/chunkRunes)
	for start := 0; start < len(runes); start += chunkRunes {
		end := start + chunkRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}

func envInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func (s *agentServer) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	s.resetAgent()
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, chatResponse{Content: "ready"})
}

func (s *agentServer) resetAgent() {
	s.agent = ternura.NewAgent(s.modelConf, ternura.CodingAgentSystemPrompt, []tool.Tool{
		tool.NewReadTool(),
		tool.NewEditTool(),
		tool.NewWriteTool(),
		tool.NewBashTool(),
	})
}

func writeJSON(w http.ResponseWriter, status int, value chatResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write json response: %v", err)
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event ternura.AgentStreamEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
