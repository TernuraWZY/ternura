package app

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/joho/godotenv"

	"ternura/agent"
	"ternura/config"
	"ternura/internal/cron"
	"ternura/internal/feishu"
	"ternura/tool"
)

func Run() {
	_ = godotenv.Load()

	query := flag.String("q", "hello", "prompt text")
	serve := flag.Bool("serve", false, "run daemon for Feishu and cron")
	addr := flag.String("addr", ":8080", "daemon HTTP address for callbacks and health checks")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	modelConf := config.NewModelConfig()

	if *serve {
		server := newAgentServer(modelConf)
		go newCronRunner(server).Run(ctx)
		if server.feishu.WebSocketEnabled() {
			go server.feishu.StartWebSocket(ctx)
		}
		log.Printf("serving Ternura daemon on http://localhost%s", *addr)
		if err := http.ListenAndServe(*addr, server.routes()); err != nil {
			log.Fatalf("server error: %v", err)
		}
		return
	}

	cliAgent := newAgentFromSkillRegistry(modelConf, newCLISkillRegistry(tool.NewCronTool(nil, nil, nil)))
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
	memory               *memoryStore
	activeMemoryKeywords activeMemoryKeywordExtractor
	cron                 *cron.Service
	cronTool             *tool.CronTool
	cronWake             chan struct{}
	feishu               *feishu.Service
}

func newAgentServer(modelConf config.ModelConfig) *agentServer {
	s := &agentServer{
		modelConf: modelConf,
		store:     newSessionStore(defaultSessionPath),
		cronWake:  make(chan struct{}, 1),
	}
	s.memory = newMemoryStore(s.store.root)
	s.activeMemoryKeywords = newEinoActiveMemoryKeywordExtractor(modelConf)
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

func (s *agentServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/feishu/events", s.feishu)
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
	runStatusRunning   = "running"
	runStatusSucceeded = "succeeded"
	runStatusFailed    = "failed"
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
	s.agent = newAgentFromSkillRegistry(s.modelConf, s.newSkillRegistry("", s.cronTool))
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
	restoreAgentConversation(s.agent, session.Messages)
	log.Printf("restored %d persisted conversation messages from %s", len(session.Messages), session.SessionID)
}

func restoreAgentConversation(agentInstance *agent.Agent, persisted []persistedMessage) {
	messages := make([]agent.ConversationMessage, 0, len(persisted))
	for _, message := range persisted {
		messages = append(messages, agent.ConversationMessage{
			Role:    message.Role,
			Content: message.Content,
		})
	}
	agentInstance.RestoreConversation(messages)
}
