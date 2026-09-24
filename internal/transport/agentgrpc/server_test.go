package agentgrpc

import (
	"context"
	"testing"

	agentv1 "mini-modal/gen/agent/v1"
	"mini-modal/internal/controlplane"
	"mini-modal/internal/domain"
	"mini-modal/internal/store/sqlite"
	sqlitedb "mini-modal/internal/store/sqlite/sqlc"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestClaimReturnsNoAssignmentWhenNoWorkIsAvailable(t *testing.T) {
	server, _ := newTestServer(t)

	response, err := server.Claim(context.Background(), &agentv1.ClaimRequest{NodeId: "node-a"})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if response.GetAssignment() != nil {
		t.Fatalf("assignment = %+v, want nil", response.GetAssignment())
	}
}

func TestClaimAndComplete(t *testing.T) {
	server, service := newTestServer(t)
	job := sqlitedb.CreateJobParams{
		ID:       "job-1",
		Priority: 1,
		Cpus:     1,
		MemoryGb: 1,
	}
	if _, err := service.Submit(context.Background(), job); err != nil {
		t.Fatalf("submit job: %v", err)
	}

	claimResponse, err := server.Claim(
		context.Background(),
		&agentv1.ClaimRequest{NodeId: "node-a"},
	)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	assignment := claimResponse.GetAssignment()
	if assignment == nil {
		t.Fatal("assignment is nil, want claimed job")
	}
	if assignment.GetJobId() != job.ID || assignment.GetNodeId() != "node-a" {
		t.Fatalf("assignment = %+v, want job-1 on node-a", assignment)
	}
	if assignment.GetAttempt() <= 0 {
		t.Fatalf("assignment attempt = %d, want a positive attempt", assignment.GetAttempt())
	}

	if _, err := server.Complete(
		context.Background(),
		&agentv1.CompleteRequest{JobId: job.ID, Attempt: assignment.GetAttempt()},
	); err != nil {
		t.Fatalf("complete: %v", err)
	}

	record, err := service.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get completed job: %v", err)
	}
	if record.Status != controlplane.JobSucceeded {
		t.Fatalf("job status = %q, want %q", record.Status, controlplane.JobSucceeded)
	}
}

func TestRPCsRejectEmptyIdentifiers(t *testing.T) {
	server, _ := newTestServer(t)
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "claim",
			call: func() error {
				_, err := server.Claim(context.Background(), &agentv1.ClaimRequest{})
				return err
			},
		},
		{
			name: "complete",
			call: func() error {
				_, err := server.Complete(context.Background(), &agentv1.CompleteRequest{})
				return err
			},
		},
		{
			name: "fail",
			call: func() error {
				_, err := server.Fail(context.Background(), &agentv1.FailRequest{})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if code := status.Code(test.call()); code != codes.InvalidArgument {
				t.Fatalf("status code = %s, want %s", code, codes.InvalidArgument)
			}
		})
	}
}

func newTestServer(t *testing.T) (*Server, *controlplane.Service) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	service := controlplane.NewService(controlplane.NewRuntimeState(), sqlitedb.New(db))
	if err := service.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterNode(ctx, domain.Node{
		ID: "node-a", TotalCPUs: 4, TotalMemoryGB: 8,
	}); err != nil {
		t.Fatal(err)
	}

	return NewServer(service), service
}
