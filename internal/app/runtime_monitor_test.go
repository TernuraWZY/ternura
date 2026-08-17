package app

import (
	"errors"
	"testing"
	"time"

	"ternura/agent"
)

func TestRuntimeMonitorTracksLifecycleAndEvents(t *testing.T) {
	t.Parallel()

	monitor := newRuntimeMonitor()
	events, unsubscribe := monitor.Subscribe()
	defer unsubscribe()
	run := runLifecycle{ID: "run-runtime-monitor", StartedAt: time.Now().Add(-time.Second)}

	monitor.Start(run, "session-1", "api", "inspect runtime", runtimeStageQueued)
	started := waitRuntimeEvent(t, events)
	if started.Type != "run_started" || started.Run.RunID != run.ID || started.Run.Stage != runtimeStageQueued {
		t.Fatalf("unexpected start event: %+v", started)
	}

	monitor.Update(run.ID, runtimeStageTool, "running read", agent.RunMetrics{ModelCalls: 1, ToolCalls: 1})
	updated := waitRuntimeEvent(t, events)
	if updated.Type != "run_updated" || updated.Run.ToolCalls != 1 || updated.Run.Detail != "running read" {
		t.Fatalf("unexpected update event: %+v", updated)
	}
	active := monitor.Active()
	if len(active) != 1 || active[0].RunID != run.ID || active[0].ElapsedMS < 900 {
		t.Fatalf("unexpected active runs: %+v", active)
	}

	monitor.Finish(run.ID, runStatusFailed, agent.AgentRunResult{Metrics: agent.RunMetrics{ModelCalls: 2, ToolCalls: 1}}, errors.New("model unavailable"))
	finished := waitRuntimeEvent(t, events)
	if finished.Type != "run_finished" || finished.Run.Status != runStatusFailed || finished.Run.ModelCalls != 2 {
		t.Fatalf("unexpected finish event: %+v", finished)
	}
	if active := monitor.Active(); len(active) != 0 {
		t.Fatalf("finished run remained active: %+v", active)
	}
}

func waitRuntimeEvent(t *testing.T, events <-chan runtimeEvent) runtimeEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runtime event")
		return runtimeEvent{}
	}
}
