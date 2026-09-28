package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mini-modal/internal/artifact"
	"mini-modal/internal/domain"
)

type fakeArtifactStore struct {
	data []byte
	err  error
}

func (f fakeArtifactStore) Download(_ context.Context, _ string) ([]byte, error) {
	return f.data, f.err
}

func testAssignment(t *testing.T) (domain.Assignment, fakeArtifactStore) {
	t.Helper()
	archive, err := artifact.PackagePythonFile("hello_function.py", []byte("def greet(name):\n    print('hello log', flush=True)\n    return {'greeting': 'Hello, ' + name}\n"))
	if err != nil {
		t.Fatal(err)
	}
	return domain.Assignment{JobID: "job-1", NodeID: "node-a", Attempt: 1, Image: "python:3.13-slim", CPUs: 1, MemoryGB: 1, ArtifactKey: "artifact-1", Entrypoint: "hello_function:greet", InputJSON: []byte(`{"args":["Ada"],"kwargs":{}}`)}, fakeArtifactStore{data: archive}
}

func TestProcessExecutorRunsFunctionAndSeparatesResultFromLogs(t *testing.T) {
	assignment, store := testAssignment(t)
	var stdout, stderr bytes.Buffer
	result, err := (ProcessExecutor{Artifacts: store}).Run(context.Background(), assignment, &stdout, &stderr, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "hello log\n" || stderr.Len() != 0 {
		t.Fatalf("logs stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	var decoded map[string]string
	if err := json.Unmarshal(result, &decoded); err != nil || decoded["greeting"] != "Hello, Ada" {
		t.Fatalf("result=%s err=%v", result, err)
	}
}

func TestProcessExecutorReportsUserFailure(t *testing.T) {
	assignment, store := testAssignment(t)
	assignment.Entrypoint = "hello_function:missing"
	_, err := (ProcessExecutor{Artifacts: store}).Run(context.Background(), assignment, &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("missing function unexpectedly succeeded")
	}
}

func TestDockerExecutorMountsArtifactAndResultAndCleansUp(t *testing.T) {
	assignment, store := testAssignment(t)
	binary, capture := fakeDockerBinary(t)
	var stdout, stderr bytes.Buffer
	result, err := (DockerExecutor{Binary: binary, Artifacts: store}).Run(context.Background(), assignment, &stdout, &stderr, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"greeting":"Hello, Ada"}` {
		t.Fatalf("result=%s", result)
	}
	if stdout.String() != "container stdout\n" || stderr.String() != "container stderr\n" {
		t.Fatalf("logs stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	captured, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	args := string(captured)
	for _, value := range []string{"--cpus\n1", "--memory\n1g", "target=/workspace,readonly", "target=/mini-modal-output", "MINI_MODAL_ENTRYPOINT=hello_function:greet", "rm\n-f\nmini-modal-job-1-1"} {
		if !strings.Contains(args, value) {
			t.Fatalf("missing %q in Docker args %q", value, args)
		}
	}
}

func TestDockerExecutorCleansUpAfterFailure(t *testing.T) {
	assignment, store := testAssignment(t)
	binary, capture := fakeDockerBinary(t)
	t.Setenv("MINI_MODAL_DOCKER_FAIL", "1")
	_, err := (DockerExecutor{Binary: binary, Artifacts: store}).Run(context.Background(), assignment, &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "run Docker container") {
		t.Fatalf("error=%v", err)
	}
	captured, _ := os.ReadFile(capture)
	if !strings.Contains(string(captured), "rm\n-f\nmini-modal-job-1-1") {
		t.Fatalf("container not cleaned up: %q", captured)
	}
}

func TestDockerExecutorReportsArtifactDownloadFailure(t *testing.T) {
	assignment, _ := testAssignment(t)
	binary, _ := fakeDockerBinary(t)
	_, err := (DockerExecutor{Binary: binary, Artifacts: fakeArtifactStore{err: errors.New("storage unavailable")}}).Run(context.Background(), assignment, &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "storage unavailable") {
		t.Fatalf("error=%v", err)
	}
}

func fakeDockerBinary(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	binary := filepath.Join(directory, "docker")
	capture := filepath.Join(directory, "arguments")
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$MINI_MODAL_DOCKER_CAPTURE"
if [ "$1" = "run" ]; then
  for argument do
    case "$argument" in
      type=bind,source=*,target=/mini-modal-output)
        source=${argument#type=bind,source=}
        source=${source%,target=/mini-modal-output}
        printf '%s' '{"greeting":"Hello, Ada"}' > "$source/result.json"
        ;;
    esac
  done
  printf 'container stdout\n'
  printf 'container stderr\n' >&2
  if [ "$MINI_MODAL_DOCKER_FAIL" = "1" ]; then exit 17; fi
fi
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MINI_MODAL_DOCKER_CAPTURE", capture)
	return binary, capture
}
