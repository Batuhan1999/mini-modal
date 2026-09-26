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

	agentv1 "mini-modal/gen/agent/v1"
	"mini-modal/internal/domain"
	jobmodel "mini-modal/internal/job"
	"mini-modal/internal/scheduler"
	sqlitedb "mini-modal/internal/store/sqlite/sqlc"

	"github.com/google/uuid"
)

const (
	assignmentLeaseDuration    = 15 * time.Second
	defaultMaxAttempts         = 3
	defaultRetryBackoffSeconds = 1
)

type Service struct {
	mu      sync.Mutex
	runtime *RuntimeState
	queries sqlitedb.Querier
	policy  *scheduler.Policy
	db      *sql.DB
	metrics *Metrics
	wakeup  chan struct{}
}

func NewService(runtime *RuntimeState, queries sqlitedb.Querier, db *sql.DB, metrics *Metrics) *Service {
	wakeup := make(chan struct{}, 1)
	return &Service{
		runtime: runtime,
		queries: queries,
		db:      db,
		policy:  scheduler.NewFirstFitPolicy(),
		metrics: metrics,
		wakeup:  wakeup,
	}
}

func (s *Service) Initialize(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshMetricsLocked(ctx)
}

func (s *Service) Cancel(ctx context.Context, assignment domain.Assignment) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	job, err := withTx(ctx, s.db, func(queries *sqlitedb.Queries) (sqlitedb.Job, error) {
		stored, err := queries.GetJob(ctx, assignment.JobID)
		if err != nil {
			return sqlitedb.Job{}, err
		}
		if stored.Attempt != assignment.Attempt {
			return sqlitedb.Job{}, fmt.Errorf("job %q is not at attempt %d", assignment.JobID, assignment.Attempt)
		}
		status, err := jobmodel.ParseStatus(stored.Status)
		if err != nil {
			return sqlitedb.Job{}, err
		}
		if _, err := status.Next(jobmodel.EventAcknowledgeCancellation); err != nil {
			return sqlitedb.Job{}, fmt.Errorf("acknowledge cancellation for job %q: %w", assignment.JobID, err)
		}

		cancelled, err := queries.AcknowledgeCancelledJob(ctx, sqlitedb.AcknowledgeCancelledJobParams{
			JobID:             assignment.JobID,
			AssignmentAttempt: assignment.Attempt,
		})
		if err != nil {
			return sqlitedb.Job{}, err
		}
		if err := recordJobEvent(
			ctx,
			queries,
			stored,
			jobmodel.HistoryCancelled,
			"Job cancellation acknowledged by node "+stored.NodeID,
			now,
		); err != nil {
			return sqlitedb.Job{}, err
		}
		return cancelled, nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("job %q is not cancelling with attempt %d", assignment.JobID, assignment.Attempt)
	}
	if err != nil {
		return fmt.Errorf("acknowledge cancellation for job %q: %w", assignment.JobID, err)
	}
	s.metrics.RecordAttemptFinished(job.ID, job.Attempt, "cancelled", now)
	s.metrics.RecordJobFinished(job.ID, string(jobmodel.Cancelled), now)
	select {
	case s.wakeup <- struct{}{}:
		// Notification was queued.
	default:
		// Channel is already full; a notification is already pending.
	}
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
			released, releaseErr := s.releaseDueRetriesLocked(ctx, now)
			if releaseErr != nil {
				log.Printf("failed to release due retries: %v", releaseErr)
			}
			if released > 0 {
				log.Printf("released %d jobs for retry", released)
			}
			if err := s.scheduleLocked(ctx); err != nil {
				log.Printf("failed to schedule jobs: %v", err)
			}
			if err := s.refreshMetricsLocked(ctx); err != nil {
				log.Printf("failed to refresh metrics: %v", err)
			}
			s.mu.Unlock()
		case <-s.wakeup:

			batchTimer := time.NewTimer(5 * time.Millisecond)

			select {
			case <-ctx.Done():
				batchTimer.Stop()
				return

			case <-batchTimer.C:
				// Batching window finished.
			}
			s.mu.Lock()
			select {
			case <-s.wakeup:
				// Coalesced notification consumed.
			default:
			}

			if err := s.scheduleLocked(ctx); err != nil {
				log.Printf("failed to schedule jobs: %v", err)
			}
			s.mu.Unlock()
		}
	}
}

func (s *Service) CancelJob(ctx context.Context, jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	type cancellationResult struct {
		schedule bool
		terminal bool
	}

	now := time.Now()
	result, err := withTx(ctx, s.db, func(queries *sqlitedb.Queries) (cancellationResult, error) {
		job, err := queries.GetJob(ctx, jobID)
		if err != nil {
			return cancellationResult{}, err
		}

		status, err := jobmodel.ParseStatus(job.Status)
		if err != nil {
			return cancellationResult{}, err
		}
		next, err := status.Next(jobmodel.EventRequestCancellation)
		if err != nil {
			return cancellationResult{}, fmt.Errorf("cancel job %q: %w", jobID, err)
		}
		if next == status {
			return cancellationResult{}, nil
		}

		switch next {
		case jobmodel.Cancelled:
			updated, err := queries.CancelNotRunningJob(ctx, jobID)
			if err != nil {
				return cancellationResult{}, fmt.Errorf("cancel job %q: %w", jobID, err)
			}
			if updated == 0 {
				return cancellationResult{}, fmt.Errorf("job %q changed status before cancellation", jobID)
			}
			if err := recordJobEvent(
				ctx,
				queries,
				job,
				jobmodel.HistoryCancelled,
				"Job cancelled before execution",
				now,
			); err != nil {
				return cancellationResult{}, err
			}
			return cancellationResult{schedule: true, terminal: true}, nil

		case jobmodel.Cancelling:
			updated, err := queries.StartCancelJob(ctx, jobID)
			if err != nil {
				return cancellationResult{}, fmt.Errorf("cancel running job %q: %w", jobID, err)
			}
			if updated == 0 {
				return cancellationResult{}, fmt.Errorf("job %q changed status before cancellation", jobID)
			}
			if err := recordJobEvent(
				ctx,
				queries,
				job,
				jobmodel.HistoryCancellationRequested,
				"Job cancellation requested",
				now,
			); err != nil {
				return cancellationResult{}, err
			}
			return cancellationResult{}, nil

		default:
			return cancellationResult{}, fmt.Errorf("cancel job %q produced unsupported status %q", jobID, next)
		}
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("job %q not found", jobID)
	}
	if err != nil {
		return err
	}
	if result.terminal {
		s.metrics.RecordJobFinished(jobID, string(jobmodel.Cancelled), now)
	}
	if result.schedule {
		select {
		case s.wakeup <- struct{}{}:
			// Notification was queued.
		default:
			// Channel is already full; a notification is already pending.
		}
		return nil
	}
	return nil
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
	select {
	case s.wakeup <- struct{}{}:
		// Notification was queued.
	default:
		// Channel is already full; a notification is already pending.
	}
	return nil
}

func (s *Service) Heartbeat(nodeID string) error {
	return s.runtime.Heartbeat(nodeID, time.Now())
}

func (s *Service) Submit(ctx context.Context, spec jobmodel.Spec) (sqlitedb.Job, error) {
	if err := spec.Validate(); err != nil {
		return sqlitedb.Job{}, err
	}
	params, err := createJobParams(spec)
	if err != nil {
		return sqlitedb.Job{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	_, err = withTx(ctx, s.db, func(queries *sqlitedb.Queries) (sqlitedb.Job, error) {
		job, err := queries.CreateJob(ctx, params)
		if err != nil {
			return sqlitedb.Job{}, err
		}
		if err := recordJobEvent(
			ctx,
			queries,
			job,
			jobmodel.HistorySubmitted,
			"Job submitted",
			now,
		); err != nil {
			return sqlitedb.Job{}, err
		}
		return job, nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return sqlitedb.Job{}, fmt.Errorf("job %q already exists", params.ID)
	}
	if err != nil {
		return sqlitedb.Job{}, fmt.Errorf("create job %q: %w", params.ID, err)
	}

	s.metrics.RecordJobSubmitted(params.ID, now)
	select {
	case s.wakeup <- struct{}{}:
		// Notification was queued.
	default:
		// Channel is already full; a notification is already pending.
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
	now := time.Now()
	assignment, err := withTx(ctx, s.db, func(queries *sqlitedb.Queries) (domain.Assignment, error) {
		job, err := queries.ClaimJob(ctx, sqlitedb.ClaimJobParams{
			LeaseExpiresAt: now.Add(assignmentLeaseDuration).UnixMilli(),
			ClaimNodeID:    nodeID,
		})
		if err != nil {
			return domain.Assignment{}, err
		}

		var args []string
		if err := json.Unmarshal([]byte(job.Args), &args); err != nil {
			return domain.Assignment{}, fmt.Errorf(
				"decode arguments for job %q: %w",
				job.ID,
				err,
			)
		}
		status, err := jobmodel.ParseStatus(job.Status)
		if err != nil {
			return domain.Assignment{}, fmt.Errorf("read claimed job %q status: %w", job.ID, err)
		}
		if status != jobmodel.Running {
			return domain.Assignment{}, fmt.Errorf("claimed job %q has status %q", job.ID, status)
		}

		if err := recordJobEvent(
			ctx,
			queries,
			job,
			jobmodel.HistoryClaimed,
			"Job claimed by node "+nodeID,
			now,
		); err != nil {
			return domain.Assignment{}, err
		}

		return domain.Assignment{
			JobID:           job.ID,
			NodeID:          job.NodeID,
			Attempt:         job.Attempt,
			Command:         job.Command,
			Args:            args,
			Image:           job.Image,
			CPUs:            job.Cpus,
			MemoryGB:        job.MemoryGb,
			DurationSeconds: job.DurationSeconds,
		}, nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Assignment{}, false, nil
	}
	if err != nil {
		return domain.Assignment{}, false, fmt.Errorf("claim job for node %q: %w", nodeID, err)
	}
	s.metrics.RecordAttemptStarted(assignment.JobID, assignment.Attempt, now)

	return assignment, true, nil
}

func (s *Service) Complete(ctx context.Context, assignment domain.Assignment) error {
	return s.finish(ctx, assignment, jobmodel.EventComplete, "", "")
}

func (s *Service) Fail(
	ctx context.Context,
	assignment domain.Assignment,
	reason domain.FailureReason,
	lastError string,
) error {
	return s.finish(ctx, assignment, jobmodel.EventFail, reason, lastError)
}

func (s *Service) finish(
	ctx context.Context,
	assignment domain.Assignment,
	event jobmodel.Event,
	failureReason domain.FailureReason,
	lastError string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	job, err := withTx(ctx, s.db, func(queries *sqlitedb.Queries) (sqlitedb.Job, error) {
		stored, err := queries.GetJob(ctx, assignment.JobID)
		if err != nil {
			return sqlitedb.Job{}, err
		}
		if stored.Attempt != assignment.Attempt {
			return sqlitedb.Job{}, fmt.Errorf("job %q is not at attempt %d", assignment.JobID, assignment.Attempt)
		}
		current, err := jobmodel.ParseStatus(stored.Status)
		if err != nil {
			return sqlitedb.Job{}, err
		}
		if event == jobmodel.EventFail && stored.Attempt < stored.MaxAttempts {
			event = jobmodel.EventScheduleRetry
		}
		next, err := current.Next(event)
		if err != nil {
			return sqlitedb.Job{}, fmt.Errorf("finish job %q: %w", assignment.JobID, err)
		}

		var finished sqlitedb.Job
		switch next {
		case jobmodel.Succeeded:
			finished, err = queries.CompleteJob(ctx, sqlitedb.CompleteJobParams{
				JobID:             assignment.JobID,
				AssignmentAttempt: assignment.Attempt,
			})
			if err == nil {
				err = recordJobEvent(ctx, queries, finished, jobmodel.HistoryCompleted, "Job completed successfully", now)
			}
		case jobmodel.Failed:
			finished, err = queries.FailJob(ctx, sqlitedb.FailJobParams{
				JobID:             assignment.JobID,
				AssignmentAttempt: assignment.Attempt,
				LastError:         lastError,
			})
			if err == nil {
				err = recordJobEvent(ctx, queries, finished, jobmodel.HistoryFailed, failureHistoryMessage(lastError), now)
			}
		case jobmodel.Retrying:
			retryBackoff := time.Duration(stored.RetryBackoffSeconds) * time.Second
			finished, err = queries.RetryJob(ctx, sqlitedb.RetryJobParams{
				JobID:             assignment.JobID,
				AssignmentAttempt: assignment.Attempt,
				NextAttemptAt:     now.Add(retryBackoff).UnixMilli(),
				LastError:         lastError,
			})
			if err == nil {
				eventJob := finished
				eventJob.NodeID = stored.NodeID
				err = recordJobEvent(ctx, queries, eventJob, jobmodel.HistoryAttemptFailed, failureHistoryMessage(lastError), now)
				if err == nil {
					err = recordJobEvent(
						ctx,
						queries,
						eventJob,
						jobmodel.HistoryRetryScheduled,
						"Retry scheduled in "+retryBackoff.String(),
						now,
					)
				}
			}
		default:
			return sqlitedb.Job{}, fmt.Errorf("unsupported terminal job status %q", next)
		}
		if err != nil {
			return sqlitedb.Job{}, err
		}
		return finished, nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("job %q is not running with attempt %d", assignment.JobID, assignment.Attempt)
	}
	if err != nil {
		return fmt.Errorf("finish job %q: %w", assignment.JobID, err)
	}
	result := string(jobmodel.Succeeded)
	if event == jobmodel.EventScheduleRetry {
		result = string(failureReason)
		s.metrics.RecordRetryScheduled(result)
	} else if event == jobmodel.EventFail {
		result = string(failureReason)
	}
	s.metrics.RecordAttemptFinished(job.ID, assignment.Attempt, result, now)
	if jobmodel.Status(job.Status).Terminal() {
		s.metrics.RecordJobFinished(job.ID, job.Status, now)
	}

	select {
	case s.wakeup <- struct{}{}:
		// Notification was queued.
	default:
		// Channel is already full; a notification is already pending.
	}
	return nil
}

func (s *Service) RenewAssignment(ctx context.Context, assignment domain.Assignment) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.queries.GetJob(ctx, assignment.JobID)
	if err != nil {
		return false, fmt.Errorf("get job %q: %w", assignment.JobID, err)
	}
	if job.NodeID != assignment.NodeID {
		return false, fmt.Errorf("job %q is not assigned to node %q", assignment.JobID, assignment.NodeID)
	}
	if job.Attempt != assignment.Attempt {
		return false, fmt.Errorf("job %q is not at attempt %d", assignment.JobID, assignment.Attempt)
	}
	if job.LeaseExpiresAt < time.Now().UnixMilli() {
		return false, fmt.Errorf("job %q lease has expired", assignment.JobID)
	}

	status, err := jobmodel.ParseStatus(job.Status)
	if err != nil {
		return false, err
	}
	if status.ShouldStopExecution() {
		return true, nil
	}

	if status != jobmodel.Running {
		return false, fmt.Errorf("job %q is not running", assignment.JobID)
	}

	updated, err := s.queries.RenewAssignment(ctx, sqlitedb.RenewAssignmentParams{
		LeaseExpiresAt:    time.Now().Add(assignmentLeaseDuration).UnixMilli(),
		JobID:             assignment.JobID,
		AssignmentNodeID:  assignment.NodeID,
		AssignmentAttempt: assignment.Attempt,
	})
	if err != nil {
		return false, fmt.Errorf("renew assignment for job %q: %w", assignment.JobID, err)
	}
	if updated == 0 {
		return false, fmt.Errorf("assignment for job %q is stale", assignment.JobID)
	}
	return false, nil
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

func (s *Service) JobCount(ctx context.Context) (int64, error) {
	count, err := s.queries.CountJobs(ctx)
	if err != nil {
		return 0, fmt.Errorf("count jobs: %w", err)
	}
	return count, nil
}

func (s *Service) RecentJobs(ctx context.Context, limit int64) ([]sqlitedb.Job, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("job list limit must be greater than zero")
	}
	jobs, err := s.queries.ListRecentJobs(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	return jobs, nil
}

func (s *Service) JobLogs(ctx context.Context, jobID string, attempt int64) ([]sqlitedb.JobLog, error) {
	if strings.TrimSpace(jobID) == "" {
		return nil, fmt.Errorf("job ID is required")
	}
	if attempt <= 0 {
		return nil, fmt.Errorf("attempt must be greater than 0")
	}

	logs, err := s.queries.ListJobLogs(ctx, sqlitedb.ListJobLogsParams{
		JobID:   jobID,
		Attempt: attempt,
	})
	if err != nil {
		return nil, fmt.Errorf("list logs for job %q attempt %d: %w", jobID, attempt, err)
	}
	return logs, nil
}

func (s *Service) JobEvents(ctx context.Context, jobID string) ([]sqlitedb.JobEvent, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, fmt.Errorf("job ID is required")
	}

	events, err := s.queries.ListAllJobEvents(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("list events for job %q: %w", jobID, err)
	}
	return events, nil
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

func (s *Service) scheduleLocked(ctx context.Context) (err error) {
	startedAt := time.Now()
	defer func() { s.metrics.RecordSchedulerRun(startedAt, err) }()

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
		_, err := withTx(ctx, s.db, func(queries *sqlitedb.Queries) (sqlitedb.Job, error) {
			job, err := queries.AssignJob(ctx, sqlitedb.AssignJobParams{
				TargetNodeID:   assignment.NodeID,
				LeaseExpiresAt: now.Add(assignmentLeaseDuration).UnixMilli(),
				JobID:          assignment.JobID,
			})
			if err != nil {
				return sqlitedb.Job{}, err
			}
			if err := recordJobEvent(
				ctx,
				queries,
				job,
				jobmodel.HistoryAssigned,
				"Job assigned to node "+assignment.NodeID,
				now,
			); err != nil {
				return sqlitedb.Job{}, err
			}
			return job, nil
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
		status, err := jobmodel.ParseStatus(job.Status)
		if err != nil {
			return nil, nil, fmt.Errorf("read active job %q status: %w", job.ID, err)
		}
		if !status.HoldsResources() {
			return nil, nil, fmt.Errorf("job %q with status %q does not hold resources", job.ID, status)
		}
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
	type expirationResult struct {
		retrying  int
		failed    int
		cancelled int
	}
	type expiredJobs struct {
		retrying  []sqlitedb.Job
		failed    []sqlitedb.Job
		cancelled []sqlitedb.Job
	}

	jobsResult := expiredJobs{}
	result, err := withTx(ctx, s.db, func(queries *sqlitedb.Queries) (expirationResult, error) {
		active, err := queries.ListActiveJobs(ctx)
		if err != nil {
			return expirationResult{}, fmt.Errorf("list active jobs before lease expiration: %w", err)
		}
		nodeByJobID := make(map[string]string, len(active))
		for _, job := range active {
			if job.LeaseExpiresAt <= now.UnixMilli() {
				nodeByJobID[job.ID] = job.NodeID
			}
		}
		retrying, err := queries.RetryExpiredAssignments(ctx, now.UnixMilli())
		if err != nil {
			return expirationResult{}, fmt.Errorf("retry expired assignments: %w", err)
		}
		for _, job := range retrying {
			job.NodeID = nodeByJobID[job.ID]
			if err := recordJobEvent(
				ctx,
				queries,
				job,
				jobmodel.HistoryLeaseExpired,
				"Assignment lease expired",
				now,
			); err != nil {
				return expirationResult{}, err
			}
			if err := recordJobEvent(
				ctx,
				queries,
				job,
				jobmodel.HistoryRetryScheduled,
				"Retry scheduled in "+(time.Duration(job.RetryBackoffSeconds)*time.Second).String(),
				now,
			); err != nil {
				return expirationResult{}, err
			}
		}

		failed, err := queries.FailExpiredAssignments(ctx, now.UnixMilli())
		if err != nil {
			return expirationResult{}, fmt.Errorf("fail expired assignments: %w", err)
		}
		for _, job := range failed {
			job.NodeID = nodeByJobID[job.ID]
			if err := recordJobEvent(ctx, queries, job, jobmodel.HistoryLeaseExpired, "Assignment lease expired", now); err != nil {
				return expirationResult{}, err
			}
			if err := recordJobEvent(ctx, queries, job, jobmodel.HistoryFailed, "Job failed after its final assignment lease expired", now); err != nil {
				return expirationResult{}, err
			}
		}

		cancelled, err := queries.CancelExpiredAssignments(ctx, now.UnixMilli())
		if err != nil {
			return expirationResult{}, fmt.Errorf("cancel expired assignments: %w", err)
		}
		for _, job := range cancelled {
			job.NodeID = nodeByJobID[job.ID]
			if err := recordJobEvent(
				ctx,
				queries,
				job,
				jobmodel.HistoryLeaseExpired,
				"Cancellation finalized after assignment lease expired",
				now,
			); err != nil {
				return expirationResult{}, err
			}
		}

		jobsResult = expiredJobs{retrying: retrying, failed: failed, cancelled: cancelled}
		return expirationResult{
			retrying:  len(retrying),
			failed:    len(failed),
			cancelled: len(cancelled),
		}, nil
	})
	if err != nil {
		return 0, err
	}
	for _, job := range jobsResult.retrying {
		s.metrics.RecordLeaseExpiration()
		s.metrics.RecordAttemptFinished(job.ID, job.Attempt, "lease_expired", now)
		s.metrics.RecordRetryScheduled("lease_expired")
	}
	for _, job := range jobsResult.failed {
		s.metrics.RecordLeaseExpiration()
		s.metrics.RecordAttemptFinished(job.ID, job.Attempt, "lease_expired", now)
		s.metrics.RecordJobFinished(job.ID, string(jobmodel.Failed), now)
	}
	for _, job := range jobsResult.cancelled {
		s.metrics.RecordLeaseExpiration()
		s.metrics.RecordAttemptFinished(job.ID, job.Attempt, "cancelled", now)
		s.metrics.RecordJobFinished(job.ID, string(jobmodel.Cancelled), now)
	}
	return result.retrying + result.failed + result.cancelled, nil
}

func (s *Service) releaseDueRetriesLocked(ctx context.Context, now time.Time) (int, error) {
	released, err := withTx(ctx, s.db, func(queries *sqlitedb.Queries) ([]sqlitedb.Job, error) {
		jobs, err := queries.ReleaseDueRetries(ctx, now.UnixMilli())
		if err != nil {
			return nil, fmt.Errorf("release due retries: %w", err)
		}
		for _, job := range jobs {
			if err := recordJobEvent(
				ctx,
				queries,
				job,
				jobmodel.HistoryRetryReady,
				"Retry backoff elapsed; job returned to pending",
				now,
			); err != nil {
				return nil, err
			}
		}
		return jobs, nil
	})
	if err != nil {
		return 0, err
	}
	for _, job := range released {
		s.metrics.RecordRetryReady(job.ID, now)
	}
	return len(released), nil
}

func createJobParams(spec jobmodel.Spec) (sqlitedb.CreateJobParams, error) {
	args := spec.Args
	if args == nil {
		args = make([]string, 0)
	}
	encodedArgs, err := json.Marshal(args)
	if err != nil {
		return sqlitedb.CreateJobParams{}, fmt.Errorf("encode arguments for job %q: %w", spec.ID, err)
	}
	maxAttempts := spec.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = defaultMaxAttempts
	}
	retryBackoffSeconds := spec.RetryBackoffSeconds
	if retryBackoffSeconds == 0 {
		retryBackoffSeconds = defaultRetryBackoffSeconds
	}
	return sqlitedb.CreateJobParams{
		ID:                  spec.ID,
		Priority:            spec.Priority,
		GpuType:             spec.GPUType,
		Gpus:                spec.GPUs,
		Cpus:                spec.CPUs,
		MemoryGb:            spec.MemoryGB,
		DurationSeconds:     spec.DurationSeconds,
		Command:             spec.Command,
		Args:                string(encodedArgs),
		Image:               spec.Image,
		MaxAttempts:         maxAttempts,
		RetryBackoffSeconds: retryBackoffSeconds,
	}, nil
}

func failureHistoryMessage(lastError string) string {
	lastError = strings.TrimSpace(lastError)
	if lastError == "" {
		return "Job execution failed"
	}
	return "Job execution failed: " + lastError
}

func validateLogStream(logStream domain.LogStream) error {
	if strings.TrimSpace(logStream.JobID) == "" {
		return fmt.Errorf("log stream job ID is required")
	}
	if logStream.Attempt <= 0 {
		return fmt.Errorf("log stream attempt must be greater than 0")
	}
	if logStream.Sequence < 0 {
		return fmt.Errorf("log stream sequence cannot be negative")
	}
	if logStream.Source != agentv1.LogSource_LOG_SOURCE_STDOUT &&
		logStream.Source != agentv1.LogSource_LOG_SOURCE_STDERR &&
		logStream.Source != agentv1.LogSource_LOG_SOURCE_SYSTEM {
		return fmt.Errorf("log stream source must be stdout or stderr")
	}
	if len(logStream.Data) == 0 {
		return fmt.Errorf("log stream data cannot be empty")
	}
	if logStream.CreatedAt.IsZero() {
		return fmt.Errorf("log stream created at is required")
	}
	return nil
}

func (s *Service) CreateJobLog(ctx context.Context, logStream domain.LogStream) error {
	if err := validateLogStream(logStream); err != nil {
		return fmt.Errorf("validate log stream: %w", err)
	}
	source := "stderr"
	if logStream.Source == agentv1.LogSource_LOG_SOURCE_STDOUT {
		source = "stdout"
	}
	if logStream.Source == agentv1.LogSource_LOG_SOURCE_SYSTEM {
		source = "system"
	}
	_, err := s.queries.CreateJobLog(ctx, sqlitedb.CreateJobLogParams{
		ID:        uuid.NewString(),
		JobID:     logStream.JobID,
		Attempt:   logStream.Attempt,
		Sequence:  logStream.Sequence,
		Source:    source,
		Data:      string(logStream.Data),
		CreatedAt: logStream.CreatedAt.UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("create job log: %w", err)
	}
	s.metrics.RecordLogBytes(source, len(logStream.Data))
	return nil
}

func (s *Service) refreshMetricsLocked(ctx context.Context) error {
	jobs, err := s.queries.ListJobs(ctx)
	if err != nil {
		return fmt.Errorf("list jobs for metrics: %w", err)
	}
	nodes, _, err := s.capacityNodesLocked(ctx, time.Now())
	if err != nil {
		return fmt.Errorf("list nodes for metrics: %w", err)
	}
	s.metrics.Refresh(jobs, nodes, s.NodeHealthy)
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
