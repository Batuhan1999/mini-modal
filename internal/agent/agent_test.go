package agent

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"

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
) error {
	return nil
}

type fakeControlPlane struct {
	mu        sync.Mutex
	claims    []claimResponse
	completed chan domain.Assignment
	failed    chan domain.Assignment
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

func (f *fakeControlPlane) StreamLogs(ctx context.Context, chunks <-chan domain.LogStream) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-chunks:
			if !ok {
				return nil
			}
		}
	}
}

type fakeExecutor struct {
	err error
}

func (f fakeExecutor) Run(context.Context, domain.Assignment, io.Writer, io.Writer) error {
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
	}
	agent := New("node-a", controlPlane, fakeExecutor{}, time.Millisecond)

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
