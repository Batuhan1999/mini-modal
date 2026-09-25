package executor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mini-modal/internal/domain"
)

func TestDockerExecutorRunsWithLimitsStreamsOutputAndCleansUp(t *testing.T) {
	binary, capturePath := fakeDockerBinary(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	assignment := domain.Assignment{
		JobID:    "Job/One",
		Image:    "alpine:3.20",
		NodeID:   "node-a",
		Attempt:  3,
		Command:  "sh",
		Args:     []string{"-c", "echo hello"},
		CPUs:     2,
		MemoryGB: 4,
	}

	if err := (DockerExecutor{Binary: binary}).Run(
		context.Background(),
		assignment,
		&stdout,
		&stderr,
	); err != nil {
		t.Fatal(err)
	}

	if stdout.String() != "container stdout\n" {
		t.Fatalf("stdout = %q, want container output", stdout.String())
	}
	if stderr.String() != "container stderr\n" {
		t.Fatalf("stderr = %q, want container error output", stderr.String())
	}

	want := []string{
		"run",
		"--name", "mini-modal-job-one-3",
		"--cpus", "2",
		"--memory", "4g",
		"alpine:3.20",
		"sh",
		"-c",
		"echo hello",
		"rm", "-f", "mini-modal-job-one-3",
	}
	if got := capturedArguments(t, capturePath); !reflect.DeepEqual(got, want) {
		t.Fatalf("docker arguments = %#v, want %#v", got, want)
	}
}

func TestDockerExecutorCleansUpAfterContainerFailure(t *testing.T) {
	binary, capturePath := fakeDockerBinary(t)
	t.Setenv("MINI_MODAL_DOCKER_FAIL", "1")
	assignment := domain.Assignment{
		JobID: "failed-job", Image: "alpine:3.20", Attempt: 1,
		Command: "false", CPUs: 1, MemoryGB: 1,
	}

	err := (DockerExecutor{Binary: binary}).Run(
		context.Background(),
		assignment,
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "run Docker container") {
		t.Fatalf("error = %v, want Docker execution failure", err)
	}

	got := capturedArguments(t, capturePath)
	wantCleanup := []string{"rm", "-f", "mini-modal-failed-job-1"}
	if len(got) < len(wantCleanup) || !reflect.DeepEqual(got[len(got)-len(wantCleanup):], wantCleanup) {
		t.Fatalf("last Docker arguments = %#v, want cleanup %#v", got, wantCleanup)
	}
}

func fakeDockerBinary(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	binary := filepath.Join(directory, "docker")
	capturePath := filepath.Join(directory, "arguments")
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$MINI_MODAL_DOCKER_CAPTURE"
if [ "$1" = "run" ]; then
  printf 'container stdout\n'
  printf 'container stderr\n' >&2
  if [ "$MINI_MODAL_DOCKER_FAIL" = "1" ]; then
    exit 17
  fi
fi
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MINI_MODAL_DOCKER_CAPTURE", capturePath)
	return binary, capturePath
}

func capturedArguments(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}
