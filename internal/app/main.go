package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/joho/godotenv"

	"ternura/agent"
	"ternura/config"
	"ternura/internal/cron"
	"ternura/internal/feishu"
	"ternura/internal/mcpruntime"
	"ternura/tool"
)

func Run() {
	_ = godotenv.Load()

	query := flag.String("q", "hello", "prompt text")
	serve := flag.Bool("serve", false, "run daemon for Feishu and cron")
	addr := flag.String("addr", ":8080", "daemon HTTP address for callbacks and health checks")
	evalPath := flag.String("eval", "", "run a JSONL agent evaluation suite")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	modelConf := config.NewModelConfig()

	if *serve {
		server := newAgentServerWithContext(ctx, modelConf)
		defer server.close()
		go newCronRunner(server).Run(ctx)
		if server.feishu.WebSocketEnabled() {
			go server.feishu.StartWebSocket(ctx)
		}
		log.Printf("serving Ternura daemon on http://localhost%s", *addr)
		if err := http.ListenAndServe(*addr, server.routes()); err != nil && err != http.ErrServerClosed {
			log.Printf("server error: %v", err)
		}
		return
	}

	registry := newCLISkillRegistry(tool.NewCronTool(nil, nil, nil))
	mcpRuntime := loadMCPRuntime(ctx)
	defer mcpRuntime.Close()
	if mcpSkill := newMCPSkill(mcpRuntime.Tools()); mcpSkill != nil {
		registry.Register(mcpSkill)
	}
	if strings.TrimSpace(*evalPath) != "" {
		summary, err := runEvalSuite(ctx, *evalPath, func() *agent.Agent {
			return newAgentFromSkillRegistry(modelConf, registry)
		})
		if err != nil {
			log.Printf("agent eval error: %v", err)
			return
		}
		content, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Println(string(content))
		return
	}
	cliAgent := newAgentFromSkillRegistry(modelConf, registry)
	result, err := cliAgent.RunWithTrace(ctx, *query)
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
	modelConf            config.ModelConfig
	mu                   sync.Mutex
	agent                *agent.Agent
	store                *sessionStore
	checkpoints          *agent.FileCheckPointStore
	memory               *memoryStore
	activeMemoryKeywords activeMemoryKeywordExtractor
	activeMemorySummary  activeMemorySummarizer
	cron                 *cron.Service
	cronTool             *tool.CronTool
	cronWake             chan struct{}
	feishu               *feishu.Service
	mcpRuntime           *mcpruntime.Runtime
	ctx                  context.Context
	taskMu               sync.Mutex
	taskCancels          map[string]context.CancelFunc
	taskSessionLocks     map[string]*sync.Mutex
}

func newAgentServer(modelConf config.ModelConfig) *agentServer {
	return newAgentServerWithContext(context.Background(), modelConf)
}

func newAgentServerWithContext(ctx context.Context, modelConf config.ModelConfig) *agentServer {
	s := &agentServer{
		modelConf:        modelConf,
		store:            newSessionStore(defaultSessionPath),
		cronWake:         make(chan struct{}, 1),
		ctx:              ctx,
		taskCancels:      make(map[string]context.CancelFunc),
		taskSessionLocks: make(map[string]*sync.Mutex),
	}
	s.checkpoints = agent.NewFileCheckPointStore(filepath.Join(s.store.root, "checkpoints"))
	s.memory = newMemoryStore(s.store.root)
	s.activeMemoryKeywords = newEinoActiveMemoryKeywordExtractor(modelConf)
	s.activeMemorySummary = newEinoActiveMemorySummarizer(modelConf)
	s.mcpRuntime = loadMCPRuntime(ctx)
	s.cron = cron.NewService(s.store.root)
	s.cronTool = tool.NewCronTool(s.cronAdd, s.cronList, s.cronRemove)
	feishuConfig := feishu.NewConfigFromEnv()
	s.feishu = feishu.NewService(feishuConfig, s.handleFeishuMessage)
	if err := s.store.Load(); err != nil {
		log.Printf("load persisted session: %v", err)
	}
	if err := s.cron.Load(); err != nil {
		log.Printf("load cron jobs: %v", err)
	}
	s.resetAgentFromHistory()
	return s
}

func loadMCPRuntime(ctx context.Context) *mcpruntime.Runtime {
	path := mcpruntime.ResolveConfigPath(mcpruntime.ConfigPathFromEnv())
	runtime, err := mcpruntime.Load(ctx, path)
	if err != nil {
		log.Printf("load MCP integrations from %s: %v", path, err)
	}
	if runtime == nil {
		return &mcpruntime.Runtime{}
	}
	if count := len(runtime.Tools()); count > 0 {
		log.Printf("loaded %d MCP tools from %s", count, path)
	}
	return runtime
}

func (s *agentServer) close() {
	if s == nil {
		return
	}
	s.cancelAllTasks()
	if s.mcpRuntime != nil {
		if err := s.mcpRuntime.Close(); err != nil {
			log.Printf("close MCP integrations: %v", err)
		}
	}
}

func (s *agentServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/feishu/events", s.feishu)
	mux.HandleFunc("/api/tasks", s.handleTasks)
	mux.HandleFunc("/api/tasks/", s.handleTask)
	mux.HandleFunc("/api/agent-card", s.handleAgentCard)
	mux.HandleFunc("/.well-known/agent-card.json", s.handleAgentCard)
	mux.HandleFunc("/healthz", s.handleHealth)
	return mux
}

func (s *agentServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

const (
	runStatusRunning         = "running"
	runStatusWaitingApproval = "waiting_approval"
	runStatusApproved        = "approved"
	runStatusRejected        = "rejected"
	runStatusSucceeded       = "succeeded"
	runStatusFailed          = "failed"
	runStatusCancelled       = "cancelled"
)

var runSequence uint64

type runLifecycle struct {
	ID        string
	StartedAt time.Time
}

func newRunLifecycle() runLifecycle {
	startedAt := time.Now()
	sequence := atomic.AddUint64(&runSequence, 1)
	return runLifecycle{
		ID:        fmt.Sprintf("run-%s-%04d", startedAt.UTC().Format("20060102T150405"), sequence),
		StartedAt: startedAt,
	}
}

func durationMillis(startedAt time.Time, finishedAt time.Time) int64 {
	duration := finishedAt.Sub(startedAt).Milliseconds()
	if duration < 0 {
		return 0
	}
	return duration
}

func logRunStart(run runLifecycle) {
	log.Printf("run %s started at %s", run.ID, run.StartedAt.Format(time.RFC3339Nano))
}

func logRunFinish(run runLifecycle, status string, finishedAt time.Time) {
	log.Printf("run %s finished status=%s duration_ms=%d", run.ID, status, durationMillis(run.StartedAt, finishedAt))
}

func (s *agentServer) resetAgent() {
	s.agent = newAgentFromSkillRegistry(
		s.modelConf,
		s.newSkillRegistry("", s.cronTool),
		agent.WithCheckpointStore(s.checkpoints),
	)
}

func (s *agentServer) resetAgentFromHistory() {
	s.resetAgentFromSnapshot(s.store.Snapshot())
}

func (s *agentServer) resetAgentFromSnapshot(snapshot sessionSnapshot) {
	s.resetAgent()
	session, ok := currentSessionFromSnapshot(snapshot)
	if !ok || len(session.Messages) == 0 {
		return
	}
	restored := restoreAgentConversation(s.agent, session.Messages)
	log.Printf("restored %d/%d persisted conversation messages from %s", restored, len(session.Messages), session.SessionID)
}

func restoreAgentConversation(agentInstance *agent.Agent, persisted []persistedMessage) int {
	messages := cleanConversationForRestore(persisted, maxRestoredConversationMessages)
	agentInstance.RestoreConversation(messages)
	return len(messages)
}

const maxRestoredConversationMessages = 12

func cleanConversationForRestore(persisted []persistedMessage, maxMessages int) []agent.ConversationMessage {
	messages := make([]agent.ConversationMessage, 0, len(persisted))
	for _, message := range persisted {
		role := strings.TrimSpace(message.Role)
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		if role == "assistant" {
			continue
		}
		if role != "user" && role != "assistant" {
			continue
		}
		messages = append(messages, agent.ConversationMessage{
			Role:    role,
			Content: content,
		})
	}
	if maxMessages > 0 && len(messages) > maxMessages {
		messages = messages[len(messages)-maxMessages:]
	}
	return messages
}
