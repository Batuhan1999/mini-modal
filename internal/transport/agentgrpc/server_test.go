package agentgrpc

import (
	"context"
	"errors"
	"testing"
	"time"

	agentv1 "mini-modal/gen/agent/v1"
	"mini-modal/internal/controlplane"
	"mini-modal/internal/domain"
	jobmodel "mini-modal/internal/job"
	postgresdb "mini-modal/internal/store/postgres/sqlc"
	"mini-modal/internal/store/postgres/testdb"

	"github.com/prometheus/client_golang/prometheus"
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

func TestRegisterNodeReturnsPersistedDrainFlag(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)
	testdb.SeedNodePool(t, db)
	queries := postgresdb.New(db)
	service := controlplane.NewService(controlplane.NewRuntimeState(), queries, db, controlplane.NewMetrics(prometheus.NewRegistry()), nil)
	if err := service.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	server := NewServer(service)
	request := &agentv1.RegisterNodeRequest{NodeId: "node-a", PoolId: testdb.NodePoolID, TotalCpus: 4, TotalMemoryGb: 8}

	response, err := server.RegisterNode(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetShouldDrain() {
		t.Fatal("new node unexpectedly requested to drain")
	}
	if err := service.SetNodeDrain(ctx, "node-a", true); err != nil {
		t.Fatal(err)
	}

	response, err = server.RegisterNode(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !response.GetShouldDrain() {
		t.Fatal("re-registered node did not receive its persisted drain request")
	}
}

func TestClaimAndComplete(t *testing.T) {
	server, service := newTestServer(t)
	job := jobmodel.Spec{FunctionVersionID: testdb.FunctionVersionID,
		ID:       "job-1",
		Priority: 1,
	}
	if _, err := service.Submit(context.Background(), job); err != nil {
		t.Fatalf("submit job: %v", err)
	}
	scheduleTestJobs(t, service, job.ID)

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
	if assignment.GetImage() != "python:3.13-slim" || assignment.GetCpus() != 1 || assignment.GetMemoryGb() != 1 || assignment.GetEntrypoint() != "hello_function:greet" {
		t.Fatalf("assignment runtime configuration = %+v", assignment)
	}

	if _, err := server.Complete(
		context.Background(),
		&agentv1.CompleteRequest{JobId: job.ID, Attempt: assignment.GetAttempt(), OutputJson: []byte(`null`)},
	); err != nil {
		t.Fatalf("complete: %v", err)
	}

	record, err := service.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get completed job: %v", err)
	}
	if jobmodel.Status(record.Status) != jobmodel.Succeeded {
		t.Fatalf("job status = %q, want %q", record.Status, jobmodel.Succeeded)
	}
	if _, err := server.Complete(context.Background(), &agentv1.CompleteRequest{
		JobId: job.ID, Attempt: assignment.GetAttempt(), OutputJson: []byte(`null`),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("repeat completion code = %s, want %s", status.Code(err), codes.FailedPrecondition)
	}
}

func TestAssignmentRPCErrorClassifiesOnlyStaleAsPrecondition(t *testing.T) {
	if code := status.Code(assignmentRPCError("complete", controlplane.ErrStaleAssignment)); code != codes.FailedPrecondition {
		t.Fatalf("stale error code = %s, want %s", code, codes.FailedPrecondition)
	}
	if code := status.Code(assignmentRPCError("complete", errors.New("database is locked"))); code != codes.Unavailable {
		t.Fatalf("storage error code = %s, want %s", code, codes.Unavailable)
	}
}

func TestRenewAndAcknowledgeCancellation(t *testing.T) {
	ctx := context.Background()
	server, service := newTestServer(t)
	if _, err := service.Submit(ctx, jobmodel.Spec{FunctionVersionID: testdb.FunctionVersionID,
		ID: "job-1",
	}); err != nil {
		t.Fatal(err)
	}
	scheduleTestJobs(t, service, "job-1")
	claim, err := server.Claim(ctx, &agentv1.ClaimRequest{NodeId: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	assignment := claim.GetAssignment()
	if assignment == nil {
		t.Fatal("assignment is nil")
	}
	if err := service.CancelJob(ctx, assignment.GetJobId()); err != nil {
		t.Fatal(err)
	}

	renewal, err := server.RenewAssignment(ctx, &agentv1.RenewAssignmentRequest{
		Assignment: assignment,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !renewal.GetShouldCancel() {
		t.Fatal("renewal did not request cancellation")
	}
	if _, err := server.Cancel(ctx, &agentv1.CancelRequest{
		JobId: assignment.GetJobId(), Attempt: assignment.GetAttempt(),
	}); err != nil {
		t.Fatal(err)
	}

	record, err := service.Job(ctx, assignment.GetJobId())
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(record.Status) != jobmodel.Cancelled {
		t.Fatalf("status = %q, want %q", record.Status, jobmodel.Cancelled)
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
		{
			name: "cancel",
			call: func() error {
				_, err := server.Cancel(context.Background(), &agentv1.CancelRequest{})
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
	db := testdb.Open(t)
	testdb.SeedFunctionVersion(t, db)
	testdb.SeedNodePool(t, db)

	metrics := controlplane.NewMetrics(prometheus.NewRegistry())
	service := controlplane.NewService(controlplane.NewRuntimeState(), postgresdb.New(db), db, metrics, nil)
	if err := service.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterNode(ctx, domain.Node{
		ID: "node-a", PoolID: testdb.NodePoolID, TotalCPUs: 4, TotalMemoryGB: 8,
	}); err != nil {
		t.Fatal(err)
	}

	return NewServer(service), service
}

func scheduleTestJobs(t *testing.T, service *controlplane.Service, jobID string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for {
		job, err := service.Job(ctx, jobID)
		if err != nil {
			t.Fatal(err)
		}
		if jobmodel.Status(job.Status) == jobmodel.Assigned {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatalf("job %q stayed in status %q instead of becoming assigned", jobID, job.Status)
		}
	}
}
