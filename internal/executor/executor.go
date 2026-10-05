package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mini-modal/internal/domain"
)

type Executor interface {
	Run(context.Context, domain.Assignment, io.Writer, io.Writer, io.Writer) (json.RawMessage, error)
}

type Fake struct{}

func (Fake) Run(ctx context.Context, assignment domain.Assignment, stdout io.Writer, stderr io.Writer, systemOut io.Writer) (json.RawMessage, error) {
	fmt.Printf("running job %s on node %s\n",
		assignment.JobID,
		assignment.NodeID,
	)

	select {
	case <-time.After(2 * time.Second):
		fmt.Printf("finished job %s\n", assignment.JobID)
		return json.RawMessage("null"), nil

	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type ProcessExecutor struct {
	Artifacts ArtifactStore
}

func (e ProcessExecutor) Run(ctx context.Context, assignment domain.Assignment, stdout io.Writer, stderr io.Writer, systemOut io.Writer) (json.RawMessage, error) {
	if err := validateEntrypoint(assignment.Entrypoint); err != nil {
		return nil, err
	}
	directory, cleanup, err := artifactDirectory(ctx, e.Artifacts, assignment)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	outputDirectory, cleanupOutput, err := resultDirectory()
	if err != nil {
		return nil, err
	}
	defer cleanupOutput()
	cmd := exec.CommandContext(ctx, "python3", "-c", pythonRunner)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), "MINI_MODAL_ENTRYPOINT="+assignment.Entrypoint, "MINI_MODAL_RESULT_PATH="+filepath.Join(outputDirectory, "result.json"))
	cmd.Stdin = bytes.NewReader(assignment.InputJSON)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run job %q: %w", assignment.JobID, err)
	}
	return readResult(outputDirectory)
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
) (output json.RawMessage, result error) {
	if err := validateEntrypoint(assignment.Entrypoint); err != nil {
		return nil, err
	}

	containerName := dockerContainerName(assignment)

	if !e.imageExists(ctx, e.binary(), assignment.Image) {
		pullErr := e.pullImage(ctx, e.binary(), assignment.Image, systemOut)
		if pullErr != nil {
			return nil, fmt.Errorf("pull image %q: %w", assignment.Image, pullErr)
		}
	}

	artifactDir, cleanup, err := artifactDirectory(ctx, e.Artifacts, assignment)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	outputDirectory, cleanupOutput, err := resultDirectory()
	if err != nil {
		return nil, err
	}
	defer cleanupOutput()

	args := []string{
		"run",
		"--name", containerName,
		"--cpus", strconv.FormatInt(assignment.CPUs, 10),
		"--memory", fmt.Sprintf("%dg", assignment.MemoryGB),
	}
	args = append(args,
		"--mount", fmt.Sprintf("type=bind,source=%s,target=/workspace,readonly", artifactDir),
		"--mount", fmt.Sprintf("type=bind,source=%s,target=/mini-modal-output", outputDirectory),
		"--workdir", "/workspace", "-i",
		"-e", "MINI_MODAL_ENTRYPOINT="+assignment.Entrypoint,
		"-e", "MINI_MODAL_RESULT_PATH=/mini-modal-output/result.json",
		assignment.Image, "python", "-c", pythonRunner,
	)

	cmd := exec.CommandContext(ctx, e.binary(), args...)
	cmd.Stdin = bytes.NewReader(assignment.InputJSON)

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
	if err := errors.Join(runErr, cleanupErr); err != nil {
		return nil, err
	}
	return readResult(outputDirectory)
}

func (e DockerExecutor) imageExists(ctx context.Context, dockerBinary string, image string) bool {
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
