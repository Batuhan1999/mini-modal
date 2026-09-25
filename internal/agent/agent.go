package agent

import (
	"context"
	"log"
	"time"

	"mini-modal/internal/domain"
	"mini-modal/internal/executor"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ControlPlane interface {
	Claim(ctx context.Context, nodeID string) (domain.Assignment, bool, error)
	Complete(ctx context.Context, assignment domain.Assignment) error
	Fail(ctx context.Context, assignment domain.Assignment) error
	RenewAssignment(ctx context.Context, assignment domain.Assignment) error
}

type Agent struct {
	nodeID       string
	controlPlane ControlPlane
	executor     executor.Executor
	pollInterval time.Duration
}

func New(nodeID string, controlPlane ControlPlane, executor executor.Executor, pollInterval time.Duration) *Agent {
	if pollInterval <= 0 {
		pollInterval = time.Second
	}

	return &Agent{
		nodeID:       nodeID,
		controlPlane: controlPlane,
		executor:     executor,
		pollInterval: pollInterval,
	}
}

func (a *Agent) Run(ctx context.Context) {
	ticker := time.NewTicker(a.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			assignment, found, err := a.controlPlane.Claim(ctx, a.nodeID)
			if err != nil {
				log.Printf("agent %q failed to claim work: %v", a.nodeID, err)
				continue
			}
			if !found {
				continue
			}
			go a.runAssignment(ctx, assignment)
		}
	}
}

func (a *Agent) runAssignment(ctx context.Context, assignment domain.Assignment) {
	renewCtx, stopRenewing := context.WithCancel(ctx)
	defer stopRenewing()
	go a.renewAssignment(renewCtx, assignment)

	executionErr := a.executor.Run(ctx, assignment)

	if executionErr != nil {
		a.reportResult(ctx, assignment, "failure", a.controlPlane.Fail)
		return
	}

	a.reportResult(ctx, assignment, "completion", a.controlPlane.Complete)
}

func (a *Agent) reportResult(
	ctx context.Context,
	assignment domain.Assignment,
	result string,
	report func(context.Context, domain.Assignment) error,
) {
	for {
		err := report(ctx, assignment)
		if err == nil {
			return
		}
		if status.Code(err) == codes.FailedPrecondition {
			log.Printf(
				"agent %q discarded stale %s for job %q attempt %d: %v",
				a.nodeID,
				result,
				assignment.JobID,
				assignment.Attempt,
				err,
			)
			return
		}

		log.Printf(
			"agent %q failed to report job %q %s; retrying: %v",
			a.nodeID,
			assignment.JobID,
			result,
			err,
		)
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (a *Agent) renewAssignment(
	ctx context.Context,
	assignment domain.Assignment,
) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			if err := a.controlPlane.RenewAssignment(ctx, assignment); err != nil {
				if ctx.Err() != nil {
					return
				}

				log.Printf(
					"agent %q failed to renew job %q attempt %d: %v",
					a.nodeID,
					assignment.JobID,
					assignment.Attempt,
					err,
				)
			}
		}
	}
}
