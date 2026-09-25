package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"mini-modal/internal/domain"
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

const dockerCleanupTimeout = 10 * time.Second

type DockerExecutor struct {
	Binary string
}

func (e DockerExecutor) Run(
	ctx context.Context,
	assignment domain.Assignment,
	stdout io.Writer,
	stderr io.Writer,
) error {
	containerName := dockerContainerName(assignment)
	args := []string{
		"run",
		"--name", containerName,
		"--cpus", strconv.FormatInt(assignment.CPUs, 10),
		"--memory", fmt.Sprintf("%dg", assignment.MemoryGB),
		assignment.Image,
		assignment.Command,
	}
	args = append(args, assignment.Args...)

	cmd := exec.CommandContext(ctx, e.binary(), args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	runErr := cmd.Run()
	cleanupErr := e.removeContainer(containerName)

	if runErr != nil {
		runErr = fmt.Errorf("run Docker container for job %q: %w", assignment.JobID, runErr)
	}
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("clean up Docker container for job %q: %w", assignment.JobID, cleanupErr)
	}
	return errors.Join(runErr, cleanupErr)
}

func (e DockerExecutor) removeContainer(containerName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dockerCleanupTimeout)
	defer cancel()

	output, err := exec.CommandContext(ctx, e.binary(), "rm", "-f", containerName).CombinedOutput()
	if err == nil || strings.Contains(string(output), "No such container") {
		return nil
	}
	return fmt.Errorf("docker rm -f: %w: %s", err, strings.TrimSpace(string(output)))
}

func (e DockerExecutor) binary() string {
	if strings.TrimSpace(e.Binary) == "" {
		return "docker"
	}
	return e.Binary
}

func dockerContainerName(assignment domain.Assignment) string {
	var name strings.Builder
	for _, character := range strings.ToLower(assignment.JobID) {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '-' || character == '_' || character == '.' {
			name.WriteRune(character)
			continue
		}
		name.WriteByte('-')
	}

	jobID := strings.Trim(name.String(), "-_.")
	if jobID == "" {
		jobID = "job"
	}
	if len(jobID) > 80 {
		jobID = jobID[:80]
	}
	return fmt.Sprintf("mini-modal-%s-%d", jobID, assignment.Attempt)
}
