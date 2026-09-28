package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	artifactpkg "mini-modal/internal/artifact"
	"mini-modal/internal/domain"
)

const pythonRunner = `import importlib, json, os, sys
module_name, function_name = os.environ["MINI_MODAL_ENTRYPOINT"].split(":", 1)
sys.path.insert(0, os.getcwd())
input_value = json.load(sys.stdin)
function = getattr(importlib.import_module(module_name), function_name)
result = function(*input_value.get("args", []), **input_value.get("kwargs", {}))
with open(os.environ["MINI_MODAL_RESULT_PATH"], "w", encoding="utf-8") as output:
    json.dump(result, output)
`

const maxResultBytes = 1 << 20

func artifactDirectory(ctx context.Context, store ArtifactStore, assignment domain.Assignment) (string, func(), error) {
	if store == nil {
		return "", nil, fmt.Errorf("artifact storage is not configured")
	}
	archive, err := store.Download(ctx, assignment.ArtifactKey)
	if err != nil {
		return "", nil, fmt.Errorf("download artifact for job %q: %w", assignment.JobID, err)
	}
	directory, err := os.MkdirTemp("", "mini-modal-artifact-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	if err := artifactpkg.ExtractArchive(archive, directory); err != nil {
		cleanup()
		return "", nil, err
	}
	return directory, cleanup, nil
}

func resultDirectory() (string, func(), error) {
	directory, err := os.MkdirTemp("", "mini-modal-result-")
	if err != nil {
		return "", nil, err
	}
	return directory, func() { _ = os.RemoveAll(directory) }, nil
}

func readResult(directory string) (json.RawMessage, error) {
	file, err := os.Open(filepath.Join(directory, "result.json"))
	if err != nil {
		return nil, fmt.Errorf("function did not write a result: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxResultBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResultBytes {
		return nil, fmt.Errorf("function result exceeds %d bytes", maxResultBytes)
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("function result is not valid JSON")
	}
	return data, nil
}

func validateEntrypoint(entrypoint string) error {
	parts := strings.Split(entrypoint, ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("entrypoint must be module:function")
	}
	return nil
}
