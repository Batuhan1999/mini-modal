package executor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mini-modal/internal/artifact"
	"mini-modal/internal/domain"
)

type fakeArtifactStore struct {
	data []byte
	err  error
	keys chan string
}

func (f fakeArtifactStore) Download(_ context.Context, key string) ([]byte, error) {
	if f.keys != nil {
		f.keys <- key
	}
	return f.data, f.err
}

func TestDockerExecutorRunsWithLimitsStreamsOutputAndCleansUp(t *testing.T) {
	binary, capturePath := fakeDockerBinary(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var systemOut bytes.Buffer
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
		&systemOut,
	); err != nil {
		t.Fatal(err)
	}

	if stdout.String() != "container stdout\n" {
		t.Fatalf("stdout = %q, want container output", stdout.String())
	}
	if stderr.String() != "container stderr\n" {
		t.Fatalf("stderr = %q, want container error output", stderr.String())
	}
	if systemOut.Len() != 0 {
		t.Fatalf("system output = %q, want no pull output for a local image", systemOut.String())
	}

	want := []string{
		"image", "inspect", "alpine:3.20",
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

func TestDockerExecutorPullsMissingImageIntoSystemOutput(t *testing.T) {
	binary, capturePath := fakeDockerBinary(t)
	t.Setenv("MINI_MODAL_IMAGE_MISSING", "1")
	var systemOut bytes.Buffer
	assignment := domain.Assignment{
		JobID: "pull-job", Image: "alpine:3.20", Attempt: 1,
		Command: "true", CPUs: 1, MemoryGB: 1,
	}

	if err := (DockerExecutor{Binary: binary}).Run(
		context.Background(),
		assignment,
		&bytes.Buffer{},
		&bytes.Buffer{},
		&systemOut,
	); err != nil {
		t.Fatal(err)
	}

	if systemOut.String() != "pull stdout\npull stderr\n" {
		t.Fatalf("system output = %q, want image pull output", systemOut.String())
	}

	want := []string{
		"image", "inspect", "alpine:3.20",
		"pull", "alpine:3.20",
		"run",
		"--name", "mini-modal-pull-job-1",
		"--cpus", "1",
		"--memory", "1g",
		"alpine:3.20",
		"true",
		"rm", "-f", "mini-modal-pull-job-1",
	}
	if got := capturedArguments(t, capturePath); !reflect.DeepEqual(got, want) {
		t.Fatalf("docker arguments = %#v, want %#v", got, want)
	}
}

func TestDockerExecutorMountsArtifactAndCleansUp(t *testing.T) {
	archive, err := artifact.PackagePythonFile("hello_function.py", []byte("print('hello')\n"))
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name    string
		failRun bool
	}{
		{name: "success"},
		{name: "container failure", failRun: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary, capturePath := fakeDockerBinary(t)
			capturedFile := filepath.Join(t.TempDir(), "artifact.py")
			t.Setenv("MINI_MODAL_DOCKER_READ_ARTIFACT", capturedFile)
			if test.failRun {
				t.Setenv("MINI_MODAL_DOCKER_FAIL", "1")
			}
			assignment := domain.Assignment{
				JobID: "artifact-job", ArtifactKey: "functions/test/artifact.tar.gz",
				Image: "python:3.13-slim", Attempt: 1,
				Command: "python", Args: []string{"hello_function.py"}, CPUs: 1, MemoryGB: 1,
			}
			requestedKeys := make(chan string, 1)
			err := (DockerExecutor{Binary: binary, Artifacts: fakeArtifactStore{data: archive, keys: requestedKeys}}).Run(
				context.Background(), assignment,
				&bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
			)
			if (err != nil) != test.failRun {
				t.Fatalf("run error = %v, want failure %t", err, test.failRun)
			}
			if key := <-requestedKeys; key != assignment.ArtifactKey {
				t.Fatalf("downloaded key = %q, want %q", key, assignment.ArtifactKey)
			}

			got := capturedArguments(t, capturePath)
			mountIndex := -1
			for i, argument := range got {
				if argument == "--mount" {
					mountIndex = i
					break
				}
			}
			if mountIndex < 0 || mountIndex+3 >= len(got) {
				t.Fatalf("missing mount and working directory in Docker arguments: %#v", got)
			}
			const prefix = "type=bind,source="
			const suffix = ",target=/workspace,readonly"
			mount := got[mountIndex+1]
			if !strings.HasPrefix(mount, prefix) || !strings.HasSuffix(mount, suffix) {
				t.Fatalf("mount = %q, want read-only artifact bind mount", mount)
			}
			if got[mountIndex+2] != "--workdir" || got[mountIndex+3] != "/workspace" {
				t.Fatalf("working directory arguments = %#v", got[mountIndex+2:mountIndex+4])
			}
			mountSource := strings.TrimSuffix(strings.TrimPrefix(mount, prefix), suffix)
			if _, err := os.Stat(mountSource); !os.IsNotExist(err) {
				t.Fatalf("artifact directory %q still exists after run: %v", mountSource, err)
			}
			contents, err := os.ReadFile(capturedFile)
			if err != nil || string(contents) != "print('hello')\n" {
				t.Fatalf("mounted artifact = %q, error = %v", contents, err)
			}
		})
	}
}

func TestDockerExecutorReportsArtifactDownloadFailure(t *testing.T) {
	binary, capturePath := fakeDockerBinary(t)
	assignment := domain.Assignment{
		JobID: "download-failure", ArtifactKey: "missing.tar.gz",
		Image: "python:3.13-slim", Command: "python", CPUs: 1, MemoryGB: 1,
	}
	err := (DockerExecutor{Binary: binary, Artifacts: fakeArtifactStore{err: errors.New("storage unavailable")}}).Run(
		context.Background(), assignment, &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "download artifact") || !strings.Contains(err.Error(), "storage unavailable") {
		t.Fatalf("error = %v, want artifact download failure", err)
	}
	if got := capturedArguments(t, capturePath); !reflect.DeepEqual(got, []string{"image", "inspect", "python:3.13-slim"}) {
		t.Fatalf("Docker arguments after failed download = %#v, want image inspect only", got)
	}
}

func TestDockerExecutorRealDocker(t *testing.T) {
	if os.Getenv("MINI_MODAL_TEST_DOCKER") != "1" {
		t.Skip("set MINI_MODAL_TEST_DOCKER=1 to run a real Docker container")
	}
	archive, err := artifact.PackagePythonFile("hello_function.py", []byte("print('hello from artifact')\n"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var systemOut bytes.Buffer
	assignment := domain.Assignment{
		JobID: "artifact-smoke", ArtifactKey: "smoke.tar.gz", Attempt: 1,
		Image: "python:3.13-slim", Command: "python", Args: []string{"hello_function.py"},
		CPUs: 1, MemoryGB: 1,
	}
	if err := (DockerExecutor{Artifacts: fakeArtifactStore{data: archive}}).Run(ctx, assignment, &stdout, &stderr, &systemOut); err != nil {
		t.Fatalf("run real Docker container: %v; stderr=%q; system=%q", err, stderr.String(), systemOut.String())
	}
	if stdout.String() != "hello from artifact\n" {
		t.Fatalf("Docker stdout = %q, want artifact output; stderr=%q", stdout.String(), stderr.String())
	}
}

func fakeDockerBinary(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	binary := filepath.Join(directory, "docker")
	capturePath := filepath.Join(directory, "arguments")
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$MINI_MODAL_DOCKER_CAPTURE"
if [ "$1" = "image" ] && [ "$2" = "inspect" ] && [ "$MINI_MODAL_IMAGE_MISSING" = "1" ]; then
  exit 1
fi
if [ "$1" = "pull" ]; then
  printf 'pull stdout\n'
  printf 'pull stderr\n' >&2
  exit 0
fi
if [ "$1" = "run" ]; then
  if [ -n "$MINI_MODAL_DOCKER_READ_ARTIFACT" ]; then
    for argument do
      case "$argument" in
        type=bind,source=*,target=/workspace,readonly)
          source=${argument#type=bind,source=}
          source=${source%,target=/workspace,readonly}
          cp "$source/hello_function.py" "$MINI_MODAL_DOCKER_READ_ARTIFACT" || exit 19
          ;;
      esac
    done
  fi
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
