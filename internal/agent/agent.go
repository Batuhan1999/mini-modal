package agent

import (
	"context"
	"errors"
	"io"
	"log"
	"time"

	agentv1 "mini-modal/gen/agent/v1"
	"mini-modal/internal/domain"
	"mini-modal/internal/executor"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ControlPlane interface {
	Claim(ctx context.Context, nodeID string) (domain.Assignment, bool, error)
	Complete(ctx context.Context, assignment domain.Assignment) error
	Fail(ctx context.Context, assignment domain.Assignment, lastError string) error
	RenewAssignment(ctx context.Context, assignment domain.Assignment) (bool, error)
	Cancel(ctx context.Context, assignment domain.Assignment) error
	StreamLogs(ctx context.Context, chunks <-chan domain.LogStream) error
}

type Agent struct {
	nodeID        string
	controlPlane  ControlPlane
	executor      executor.Executor
	pollInterval  time.Duration
	renewInterval time.Duration
}

func New(nodeID string, controlPlane ControlPlane, executor executor.Executor, pollInterval time.Duration) *Agent {
	if pollInterval <= 0 {
		pollInterval = time.Second
	}

	return &Agent{
		nodeID:        nodeID,
		controlPlane:  controlPlane,
		executor:      executor,
		pollInterval:  pollInterval,
		renewInterval: 5 * time.Second,
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

	var executionCtx context.Context
	var cancelExecution context.CancelFunc

	if assignment.DurationSeconds > 0 {
		executionCtx, cancelExecution = context.WithTimeout(
			ctx,
			time.Duration(assignment.DurationSeconds)*time.Second,
		)
	} else {
		executionCtx, cancelExecution = context.WithCancel(ctx)
	}
	defer cancelExecution()
	chunks := make(chan domain.LogStream, 128)
	uploadResult := make(chan error, 1)
	uploadDone := make(chan struct{})

	go func() {
		defer close(uploadDone)
		uploadResult <- a.controlPlane.StreamLogs(ctx, chunks)
	}()
	sequence := &logSequence{}

	stdout := newLogWriter(
		ctx,
		assignment,
		agentv1.LogSource_LOG_SOURCE_STDOUT,
		chunks,
		sequence,
		uploadDone,
	)
	stderr := newLogWriter(
		ctx,
		assignment,
		agentv1.LogSource_LOG_SOURCE_STDERR,
		chunks,
		sequence,
		uploadDone,
	)

	systemOut := newLogWriter(
		ctx,
		assignment,
		agentv1.LogSource_LOG_SOURCE_SYSTEM,
		chunks,
		sequence,
		uploadDone,
	)

	cancellationRequested := make(chan struct{})
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		a.renewAssignment(
			renewCtx,
			assignment,
			cancelExecution,
			systemOut,
			cancellationRequested,
		)
	}()

	executionErr := a.executor.Run(executionCtx, assignment, stdout, stderr, systemOut)
	stopRenewing()
	<-renewDone

	wasCancelled := false
	select {
	case <-cancellationRequested:
		wasCancelled = true
	default:
	}
	timedOut := !wasCancelled &&
		errors.Is(executionCtx.Err(), context.DeadlineExceeded)

	if wasCancelled {
		_, _ = io.WriteString(systemOut, "Execution stopped after cancellation\n")
	} else if timedOut {
		_, _ = io.WriteString(systemOut, "Execution timed out\n")
	}

	close(chunks)
	uploadErr := <-uploadResult
	if uploadErr != nil {
		log.Printf("agent %q failed to upload logs: %v", a.nodeID, uploadErr)
	}

	if ctx.Err() != nil {
		return
	}
	if wasCancelled {
		a.reportResult(ctx, assignment, "cancelled", a.controlPlane.Cancel)
		return
	}
	if timedOut {
		failureMessage := "execution timed out"

		a.reportResult(
			ctx,
			assignment,
			"timeout",
			func(ctx context.Context, assignment domain.Assignment) error {
				return a.controlPlane.Fail(ctx, assignment, failureMessage)
			},
		)
		return
	}

	if executionErr != nil {
		failureMessage := executionErr.Error()

		a.reportResult(
			ctx,
			assignment,
			"failure",
			func(ctx context.Context, assignment domain.Assignment) error {
				return a.controlPlane.Fail(ctx, assignment, failureMessage)
			},
		)
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
	cancelExecution context.CancelFunc,
	systemOut io.Writer,
	cancellationRequested chan<- struct{},
) {
	ticker := time.NewTicker(a.renewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:

			shouldCancel, err := a.controlPlane.RenewAssignment(ctx, assignment)

			if err != nil {
				log.Printf("agent %q failed to renew job %q attempt %d: %v", a.nodeID, assignment.JobID, assignment.Attempt, err)
				continue
			}

			if shouldCancel {
				_, _ = io.WriteString(systemOut, "Cancellation requested\n")
				close(cancellationRequested)
				cancelExecution()
				return
			}
		}
	}
}
