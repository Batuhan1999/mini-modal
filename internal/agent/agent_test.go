package agent

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"

	agentv1 "mini-modal/gen/agent/v1"
	"mini-modal/internal/domain"
)

type claimResponse struct {
	assignment domain.Assignment
	found      bool
	err        error
}

func (f *fakeControlPlane) RenewAssignment(
	ctx context.Context,
	assignment domain.Assignment,
) (bool, error) {
	return f.shouldCancel, nil
}

type fakeControlPlane struct {
	mu           sync.Mutex
	claims       []claimResponse
	completed    chan domain.Assignment
	failed       chan domain.Assignment
	cancelled    chan domain.Assignment
	logs         chan domain.LogStream
	shouldCancel bool
}

func (f *fakeControlPlane) Claim(ctx context.Context, nodeID string) (domain.Assignment, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.claims) == 0 {
		return domain.Assignment{}, false, nil
	}

	response := f.claims[0]
	f.claims = f.claims[1:]
	return response.assignment, response.found, response.err
}

func (f *fakeControlPlane) Complete(ctx context.Context, assignment domain.Assignment) error {
	f.completed <- assignment
	return nil
}

func (f *fakeControlPlane) Fail(ctx context.Context, assignment domain.Assignment) error {
	f.failed <- assignment
	return nil
}

func (f *fakeControlPlane) Cancel(ctx context.Context, assignment domain.Assignment) error {
	f.cancelled <- assignment
	return nil
}

func (f *fakeControlPlane) StreamLogs(ctx context.Context, chunks <-chan domain.LogStream) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, ok := <-chunks:
			if !ok {
				return nil
			}
			if f.logs != nil {
				f.logs <- chunk
			}
		}
	}
}

type fakeExecutor struct {
	err                 error
	systemOutput        string
	waitForCancellation bool
}

func (f fakeExecutor) Run(
	ctx context.Context,
	_ domain.Assignment,
	_ io.Writer,
	_ io.Writer,
	systemOut io.Writer,
) error {
	if f.systemOutput != "" {
		_, _ = io.WriteString(systemOut, f.systemOutput)
	}
	if f.waitForCancellation {
		<-ctx.Done()
		return ctx.Err()
	}
	return f.err
}

func TestAgentRetriesClaimAndCompletesWork(t *testing.T) {
	assignment := domain.Assignment{JobID: "job-1", NodeID: "node-a", Attempt: 1}
	controlPlane := &fakeControlPlane{
		claims: []claimResponse{
			{err: errors.New("temporary claim failure")},
			{assignment: assignment, found: true},
		},
		completed: make(chan domain.Assignment, 1),
		failed:    make(chan domain.Assignment, 1),
		logs:      make(chan domain.LogStream, 1),
	}
	agent := New("node-a", controlPlane, fakeExecutor{systemOutput: "pulling image\n"}, time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		agent.Run(ctx)
		close(done)
	}()

	select {
	case completed := <-controlPlane.completed:
		if !reflect.DeepEqual(completed, assignment) {
			t.Fatalf("completed assignment %#v, want %#v", completed, assignment)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for job completion")
	}

	select {
	case chunk := <-controlPlane.logs:
		if chunk.Source != agentv1.LogSource_LOG_SOURCE_SYSTEM || string(chunk.Data) != "pulling image\n" {
			t.Fatalf("system log = %+v, want system pull output", chunk)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for system log")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for agent shutdown")
	}
}

func TestAgentReportsExecutionFailure(t *testing.T) {
	assignment := domain.Assignment{JobID: "job-1", NodeID: "node-a", Attempt: 1}
	controlPlane := &fakeControlPlane{
		claims:    []claimResponse{{assignment: assignment, found: true}},
		completed: make(chan domain.Assignment, 1),
		failed:    make(chan domain.Assignment, 1),
	}
	agent := New("node-a", controlPlane, fakeExecutor{err: errors.New("executor failed")}, time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		agent.Run(ctx)
		close(done)
	}()

	select {
	case failed := <-controlPlane.failed:
		if !reflect.DeepEqual(failed, assignment) {
			t.Fatalf("failed assignment %#v, want %#v", failed, assignment)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for job failure")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for agent shutdown")
	}
}

func TestAgentCancelsExecutionWhenRenewalRequestsIt(t *testing.T) {
	assignment := domain.Assignment{JobID: "job-1", NodeID: "node-a", Attempt: 2}
	controlPlane := &fakeControlPlane{
		completed:    make(chan domain.Assignment, 1),
		failed:       make(chan domain.Assignment, 1),
		cancelled:    make(chan domain.Assignment, 1),
		logs:         make(chan domain.LogStream, 2),
		shouldCancel: true,
	}
	agent := New("node-a", controlPlane, fakeExecutor{waitForCancellation: true}, time.Second)
	agent.renewInterval = time.Millisecond

	done := make(chan struct{})
	go func() {
		agent.runAssignment(context.Background(), assignment)
		close(done)
	}()

	select {
	case cancelled := <-controlPlane.cancelled:
		if !reflect.DeepEqual(cancelled, assignment) {
			t.Fatalf("cancelled assignment %#v, want %#v", cancelled, assignment)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cancellation acknowledgement")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for assignment shutdown")
	}

	first := <-controlPlane.logs
	second := <-controlPlane.logs
	if first.Source != agentv1.LogSource_LOG_SOURCE_SYSTEM || string(first.Data) != "Cancellation requested\n" {
		t.Fatalf("first cancellation log = %+v", first)
	}
	if second.Source != agentv1.LogSource_LOG_SOURCE_SYSTEM || string(second.Data) != "Execution stopped after cancellation\n" {
		t.Fatalf("second cancellation log = %+v", second)
	}

	select {
	case failed := <-controlPlane.failed:
		t.Fatalf("cancelled assignment was reported failed: %+v", failed)
	default:
	}
	select {
	case completed := <-controlPlane.completed:
		t.Fatalf("cancelled assignment was reported complete: %+v", completed)
	default:
	}
}
