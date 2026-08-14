package agent

import (
	"context"
	"errors"
	"testing"
)

func TestShouldFailoverModel(t *testing.T) {
	t.Parallel()

	if !shouldFailoverModel(context.Background(), errors.New("status code: 503")) {
		t.Fatal("expected transient provider error to trigger failover")
	}
	if shouldFailoverModel(context.Background(), errors.New("context length exceeded")) {
		t.Fatal("context overflow should be compacted rather than sent to another model")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if shouldFailoverModel(cancelled, errors.New("status code: 503")) {
		t.Fatal("cancelled run should not fail over")
	}
}
