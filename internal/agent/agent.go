package agent

import (
	"context"
	"log"
	"time"

	"mini-modal/internal/domain"
	"mini-modal/internal/executor"
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
	stopRenewing()

	if executionErr != nil {
		if reportErr := a.controlPlane.Fail(ctx, assignment); reportErr != nil {
			log.Printf("agent %q failed to report job %q failure: %v", a.nodeID, assignment.JobID, reportErr)
		}
		return
	}

	if err := a.controlPlane.Complete(ctx, assignment); err != nil {
		log.Printf("agent %q failed to report job %q completion: %v", a.nodeID, assignment.JobID, err)
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
