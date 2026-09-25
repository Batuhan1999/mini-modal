package executor

import (
	"context"
	"fmt"
	"mini-modal/internal/domain"
	"os/exec"
	"time"
)

type Executor interface {
	Run(context.Context, domain.Assignment) error
}

type Fake struct{}

func (Fake) Run(ctx context.Context, assignment domain.Assignment) error {
	fmt.Printf("running job %s on node %s\n",
		assignment.JobID,
		assignment.NodeID,
	)

	select {
	case <-time.After(2 * time.Second):
		fmt.Printf("finished job %s\n", assignment.JobID)
		return nil

	case <-ctx.Done():
		return ctx.Err()
	}
}

type ProcessExecutor struct {
}

func (ProcessExecutor) Run(ctx context.Context, assignment domain.Assignment) error {
	cmd := exec.CommandContext(ctx, assignment.Command, assignment.Args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run job %q: %w", assignment.JobID, err)
	}
	return nil
}
