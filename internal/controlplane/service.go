package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"mini-modal/internal/domain"
	"mini-modal/internal/scheduler"
	sqlitedb "mini-modal/internal/store/sqlite/sqlc"
)

const (
	JobPending       = "pending"
	JobAssigned      = "assigned"
	JobRunning       = "running"
	JobSucceeded     = "succeeded"
	JobFailed        = "failed"
	JobUnschedulable = "unschedulable"
)

const assignmentLeaseDuration = 15 * time.Second

type Service struct {
	mu      sync.Mutex
	runtime *RuntimeState
	queries sqlitedb.Querier
	policy  *scheduler.Policy
}

func NewService(runtime *RuntimeState, queries sqlitedb.Querier) *Service {
	return &Service{
		runtime: runtime,
		queries: queries,
		policy:  scheduler.NewFirstFitPolicy(),
	}
}

func (s *Service) Initialize(ctx context.Context) error {
	_ = ctx
	return nil
}

func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.mu.Lock()
			expired, expireErr := s.expireAssignmentsLocked(ctx, now)
			if expireErr != nil {
				log.Printf("failed to expire assignments: %v", expireErr)
			}
			if expired > 0 {
				log.Printf("expired %d assignments", expired)
			}
			if err := s.scheduleLocked(ctx); err != nil {
				log.Printf("failed to schedule jobs: %v", err)
			}
			s.mu.Unlock()
		}
	}
}

func (s *Service) RegisterNode(ctx context.Context, node domain.Node) error {
	if err := validateNode(node); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.queries.UpsertNode(ctx, sqlitedb.UpsertNodeParams{
		ID:            node.ID,
		GpuType:       node.GPUType,
		TotalGpus:     node.TotalGPUs,
		TotalCpus:     node.TotalCPUs,
		TotalMemoryGb: node.TotalMemoryGB,
	}); err != nil {
		return fmt.Errorf("store node %q: %w", node.ID, err)
	}
	s.runtime.RegisterNode(node.ID, time.Now())

	if _, err := s.queries.RequeueUnschedulableJobs(ctx); err != nil {
		return fmt.Errorf("requeue unschedulable jobs: %w", err)
	}
	return s.scheduleLocked(ctx)
}

func (s *Service) Heartbeat(nodeID string) error {
	return s.runtime.Heartbeat(nodeID, time.Now())
}

func (s *Service) Submit(ctx context.Context, params sqlitedb.CreateJobParams) (sqlitedb.Job, error) {
	if err := validateJob(params); err != nil {
		return sqlitedb.Job{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	created, err := s.queries.CreateJob(ctx, params)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlitedb.Job{}, fmt.Errorf("job %q already exists", params.ID)
	}
	if err != nil {
		return sqlitedb.Job{}, fmt.Errorf("create job %q: %w", params.ID, err)
	}
	if err := s.scheduleLocked(ctx); err != nil {
		log.Printf("job %q was stored but could not be scheduled: %v", params.ID, err)
		return created, nil
	}

	return s.queries.GetJob(ctx, params.ID)
}

func (s *Service) Claim(ctx context.Context, nodeID string) (domain.Assignment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	registered, healthy := s.runtime.NodeStatus(nodeID, time.Now())
	if !registered {
		return domain.Assignment{}, false, fmt.Errorf("node %q has not registered", nodeID)
	}
	if !healthy {
		return domain.Assignment{}, false, nil
	}

	job, err := s.queries.ClaimJob(ctx, sqlitedb.ClaimJobParams{
		LeaseExpiresAt: time.Now().Add(assignmentLeaseDuration).UnixMilli(),
		ClaimNodeID:    nodeID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Assignment{}, false, nil
	}
	if err != nil {
		return domain.Assignment{}, false, fmt.Errorf("claim job for node %q: %w", nodeID, err)
	}

	var args []string
	if err := json.Unmarshal([]byte(job.Args), &args); err != nil {
		return domain.Assignment{}, false, fmt.Errorf(
			"decode arguments for job %q: %w",
			job.ID,
			err,
		)
	}

	return domain.Assignment{JobID: job.ID, NodeID: job.NodeID, Attempt: job.Attempt, Command: job.Command, Args: args}, true, nil
}

func (s *Service) Complete(ctx context.Context, assignment domain.Assignment) error {
	return s.finish(ctx, assignment, JobSucceeded)
}

func (s *Service) Fail(ctx context.Context, assignment domain.Assignment) error {
	return s.finish(ctx, assignment, JobFailed)
}

func (s *Service) finish(ctx context.Context, assignment domain.Assignment, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		job sqlitedb.Job
		err error
	)
	switch status {
	case JobSucceeded:
		job, err = s.queries.CompleteJob(ctx, sqlitedb.CompleteJobParams{
			JobID:             assignment.JobID,
			AssignmentAttempt: assignment.Attempt,
		})
	case JobFailed:
		job, err = s.queries.FailJob(ctx, sqlitedb.FailJobParams{
			JobID:             assignment.JobID,
			AssignmentAttempt: assignment.Attempt,
		})
	default:
		return fmt.Errorf("unsupported terminal job status %q", status)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("job %q is not running with attempt %d", assignment.JobID, assignment.Attempt)
	}
	if err != nil {
		return fmt.Errorf("finish job %q: %w", assignment.JobID, err)
	}

	if err := s.scheduleLocked(ctx); err != nil {
		log.Printf("job %q finished but scheduling failed: %v", job.ID, err)
	}
	return nil
}

func (s *Service) RenewAssignment(ctx context.Context, assignment domain.Assignment) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	updated, err := s.queries.RenewAssignment(ctx, sqlitedb.RenewAssignmentParams{
		LeaseExpiresAt:    time.Now().Add(assignmentLeaseDuration).UnixMilli(),
		JobID:             assignment.JobID,
		AssignmentNodeID:  assignment.NodeID,
		AssignmentAttempt: assignment.Attempt,
	})
	if err != nil {
		return fmt.Errorf("renew assignment for job %q: %w", assignment.JobID, err)
	}
	if updated == 0 {
		return fmt.Errorf("assignment for job %q is stale", assignment.JobID)
	}
	return nil
}

func (s *Service) Job(ctx context.Context, jobID string) (sqlitedb.Job, error) {
	job, err := s.queries.GetJob(ctx, jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlitedb.Job{}, fmt.Errorf("job %q not found", jobID)
	}
	if err != nil {
		return sqlitedb.Job{}, fmt.Errorf("get job %q: %w", jobID, err)
	}
	return job, nil
}

func (s *Service) Jobs(ctx context.Context) ([]sqlitedb.Job, error) {
	jobs, err := s.queries.ListJobs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	return jobs, nil
}

func (s *Service) Nodes(ctx context.Context) ([]domain.Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	nodes, _, err := s.capacityNodesLocked(ctx, time.Now())
	return nodes, err
}

func (s *Service) NodeHealthy(nodeID string) bool {
	return s.runtime.NodeHealthy(nodeID, time.Now())
}

func (s *Service) scheduleLocked(ctx context.Context) error {
	jobs, err := s.queries.ListPendingJobs(ctx)
	if err != nil {
		return fmt.Errorf("list pending jobs: %w", err)
	}
	if len(jobs) == 0 {
		return nil
	}

	now := time.Now()
	allNodes, healthyNodes, err := s.capacityNodesLocked(ctx, now)
	if err != nil {
		return err
	}
	assignments, _, possiblyUnschedulable := s.policy.Schedule(jobs, healthyNodes)

	for _, assignment := range assignments {
		_, err := s.queries.AssignJob(ctx, sqlitedb.AssignJobParams{
			TargetNodeID:   assignment.NodeID,
			LeaseExpiresAt: now.Add(assignmentLeaseDuration).UnixMilli(),
			JobID:          assignment.JobID,
		})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("assign job %q: %w", assignment.JobID, err)
		}
	}

	for _, job := range possiblyUnschedulable {
		if len(allNodes) == 0 || canAnyNodePossiblyRun(allNodes, job) {
			continue
		}
		if _, err := s.queries.MarkJobUnschedulable(ctx, job.ID); err != nil {
			return fmt.Errorf("mark job %q unschedulable: %w", job.ID, err)
		}
	}
	return nil
}

func (s *Service) capacityNodesLocked(ctx context.Context, now time.Time) ([]domain.Node, []domain.Node, error) {
	storedNodes, err := s.queries.ListNodes(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list nodes: %w", err)
	}
	activeJobs, err := s.queries.ListActiveJobs(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list active jobs: %w", err)
	}

	nodeByID := make(map[string]domain.Node, len(storedNodes))
	for _, stored := range storedNodes {
		nodeByID[stored.ID] = domain.Node{
			ID:                stored.ID,
			GPUType:           stored.GpuType,
			TotalGPUs:         stored.TotalGpus,
			AvailableGPUs:     stored.TotalGpus,
			TotalCPUs:         stored.TotalCpus,
			AvailableCPUs:     stored.TotalCpus,
			TotalMemoryGB:     stored.TotalMemoryGb,
			AvailableMemoryGB: stored.TotalMemoryGb,
		}
	}
	for _, job := range activeJobs {
		node, exists := nodeByID[job.NodeID]
		if !exists {
			continue
		}
		node.Reserve(job)
		nodeByID[node.ID] = node
	}

	all := make([]domain.Node, 0, len(nodeByID))
	healthy := make([]domain.Node, 0, len(nodeByID))
	for _, stored := range storedNodes {
		node := nodeByID[stored.ID]
		all = append(all, node)
		if s.runtime.NodeHealthy(node.ID, now) {
			healthy = append(healthy, node)
		}
	}
	return all, healthy, nil
}

func (s *Service) expireAssignmentsLocked(ctx context.Context, now time.Time) (int, error) {
	expired, err := s.queries.RequeueExpiredAssignments(ctx, now.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("requeue expired assignments: %w", err)
	}
	return len(expired), nil
}

func validateJob(job sqlitedb.CreateJobParams) error {
	if strings.TrimSpace(job.ID) == "" {
		return fmt.Errorf("job ID is required")
	}
	if job.Cpus <= 0 {
		return fmt.Errorf("job %q must request at least one CPU", job.ID)
	}
	if job.MemoryGb <= 0 {
		return fmt.Errorf("job %q must request positive memory", job.ID)
	}
	if job.Gpus < 0 {
		return fmt.Errorf("job %q cannot request negative GPUs", job.ID)
	}
	if job.DurationSeconds < 0 {
		return fmt.Errorf("job %q cannot have a negative duration", job.ID)
	}
	if strings.TrimSpace(job.Command) == "" {
		return fmt.Errorf("job %q requires a command", job.ID)
	}

	var args []string
	if err := json.Unmarshal([]byte(job.Args), &args); err != nil {
		return fmt.Errorf("job %q has invalid arguments: %w", job.ID, err)
	}
	return nil
}

func validateNode(node domain.Node) error {
	if strings.TrimSpace(node.ID) == "" {
		return fmt.Errorf("node ID is required")
	}
	if node.TotalGPUs < 0 {
		return fmt.Errorf("node %q cannot have negative GPUs", node.ID)
	}
	if node.TotalCPUs <= 0 {
		return fmt.Errorf("node %q must have at least one CPU", node.ID)
	}
	if node.TotalMemoryGB <= 0 {
		return fmt.Errorf("node %q must have positive memory", node.ID)
	}
	return nil
}

func canAnyNodePossiblyRun(nodes []domain.Node, job sqlitedb.Job) bool {
	for _, node := range nodes {
		if node.CanPossiblyRun(job) {
			return true
		}
	}
	return false
}
