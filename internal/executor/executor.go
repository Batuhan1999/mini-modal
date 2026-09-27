package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	artifactpkg "mini-modal/internal/artifact"
	"mini-modal/internal/domain"
)

type Executor interface {
	Run(context context.Context, assignment domain.Assignment, stdout io.Writer, stderr io.Writer, systemOut io.Writer) error
}

type Fake struct{}

func (Fake) Run(ctx context.Context, assignment domain.Assignment, stdout io.Writer, stderr io.Writer, systemOut io.Writer) error {
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

func (ProcessExecutor) Run(ctx context.Context, assignment domain.Assignment, stdout io.Writer, stderr io.Writer, systemOut io.Writer) error {
	cmd := exec.CommandContext(ctx, assignment.Command, assignment.Args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run job %q: %w", assignment.JobID, err)
	}
	return nil
}

const dockerCleanupTimeout = 10 * time.Second

type ArtifactStore interface {
	Download(context.Context, string) ([]byte, error)
}

type DockerExecutor struct {
	Binary    string
	Artifacts ArtifactStore
}

func (e DockerExecutor) Run(
	ctx context.Context,
	assignment domain.Assignment,
	stdout io.Writer,
	stderr io.Writer,
	systemOut io.Writer,
) (result error) {

	containerName := dockerContainerName(assignment)

	if !e.checkImageExixts(ctx, e.binary(), assignment.Image) {
		pullErr := e.pullImage(ctx, e.binary(), assignment.Image, systemOut)
		if pullErr != nil {
			return fmt.Errorf("pull image %q: %w", assignment.Image, pullErr)
		}
	}

	var artifactDirectory string
	if assignment.ArtifactKey != "" {
		if e.Artifacts == nil {
			return fmt.Errorf("download artifact for job %q: artifact storage is not configured", assignment.JobID)
		}
		archive, err := e.Artifacts.Download(ctx, assignment.ArtifactKey)
		if err != nil {
			return fmt.Errorf("download artifact for job %q: %w", assignment.JobID, err)
		}
		artifactDirectory, err = os.MkdirTemp("", "mini-modal-artifact-")
		if err != nil {
			return fmt.Errorf("create artifact directory for job %q: %w", assignment.JobID, err)
		}
		defer func() {
			if err := os.RemoveAll(artifactDirectory); err != nil {
				result = errors.Join(result, fmt.Errorf("remove artifact directory for job %q: %w", assignment.JobID, err))
			}
		}()
		if err := artifactpkg.ExtractArchive(archive, artifactDirectory); err != nil {
			return fmt.Errorf("extract artifact for job %q: %w", assignment.JobID, err)
		}
	}

	args := []string{
		"run",
		"--name", containerName,
		"--cpus", strconv.FormatInt(assignment.CPUs, 10),
		"--memory", fmt.Sprintf("%dg", assignment.MemoryGB),
	}
	if artifactDirectory != "" {
		args = append(args,
			"--mount", fmt.Sprintf("type=bind,source=%s,target=/workspace,readonly", artifactDirectory),
			"--workdir", "/workspace",
		)
	}
	args = append(args, assignment.Image, assignment.Command)
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

func (e DockerExecutor) checkImageExixts(ctx context.Context, dockerBinary string, image string) bool {
	cmd := exec.CommandContext(ctx, dockerBinary, "image", "inspect", image)
	err := cmd.Run()
	if err != nil {
		return false
	}
	return true
}

func (e DockerExecutor) pullImage(ctx context.Context, dockerBinary string, image string, systemOut io.Writer) error {
	cmd := exec.CommandContext(ctx, dockerBinary, "pull", image)
	cmd.Stdout = systemOut
	cmd.Stderr = systemOut
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("pull image %q: %w", image, err)
	}
	return nil
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
