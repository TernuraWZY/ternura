package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"

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

	emit := func(event ternura.AgentStreamEvent) error {
		return writeSSE(w, flusher, event)
	}

	if err := writeSSE(w, flusher, ternura.AgentStreamEvent{Type: "start"}); err != nil {
		return
	}

	if _, err := s.agent.RunStreaming(r.Context(), req.Message, emit); err != nil {
		_ = writeSSE(w, flusher, ternura.AgentStreamEvent{
			Type:  "error",
			Error: err.Error(),
		})
	}
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
