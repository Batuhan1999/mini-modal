package executor

import (
	"context"
	"fmt"
	"io"
	"mini-modal/internal/domain"
	"os/exec"
	"time"
)

type Executor interface {
	Run(context context.Context, assignment domain.Assignment, stdout io.Writer, stderr io.Writer) error
}

type Fake struct{}

func (Fake) Run(ctx context.Context, assignment domain.Assignment, stdout io.Writer, stderr io.Writer) error {
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

func (ProcessExecutor) Run(ctx context.Context, assignment domain.Assignment, stdout io.Writer,
	stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, assignment.Command, assignment.Args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run job %q: %w", assignment.JobID, err)
	}
	return nil
}
