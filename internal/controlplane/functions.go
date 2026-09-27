package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"mini-modal/internal/artifact"
	jobmodel "mini-modal/internal/job"
	postgresdb "mini-modal/internal/store/postgres/sqlc"

	"github.com/google/uuid"
)

var ErrFunctionVersionNotFound = errors.New("function version not found")

type FunctionVersionSpec struct {
	ArtifactKey         string
	Image               string
	Command             string
	Args                []string
	GPUType             string
	GPUs                int64
	CPUs                int64
	MemoryGB            int64
	DurationSeconds     int64
	MaxAttempts         int64
	RetryBackoffSeconds int64
}

func (s *Service) Functions(ctx context.Context) ([]postgresdb.ListFunctionsRow, error) {
	return s.queries.ListFunctions(ctx)
}

func (s *Service) FunctionVersions(ctx context.Context, functionID string) ([]postgresdb.FunctionVersion, error) {
	functionID = strings.TrimSpace(functionID)
	if functionID == "" {
		return nil, fmt.Errorf("function ID is required")
	}
	return s.queries.ListFunctionVersions(ctx, functionID)
}

func (s *Service) UploadFunction(ctx context.Context, functionID string, archive []byte) (artifact.UploadResult, error) {
	functionID = strings.TrimSpace(functionID)
	if functionID == "" {
		return artifact.UploadResult{}, fmt.Errorf("function ID is required")
	}
	if err := artifact.ValidateArchive(archive); err != nil {
		return artifact.UploadResult{}, err
	}
	if s.artifacts == nil {
		return artifact.UploadResult{}, fmt.Errorf("%w: not configured", artifact.ErrStorage)
	}
	return s.artifacts.Upload(ctx, functionID, archive)
}

func (s *Service) DeployFunction(ctx context.Context, functionID string, spec FunctionVersionSpec) (postgresdb.FunctionVersion, error) {
	functionID = strings.TrimSpace(functionID)
	if functionID == "" {
		return postgresdb.FunctionVersion{}, fmt.Errorf("function ID is required")
	}
	if strings.TrimSpace(spec.ArtifactKey) == "" {
		return postgresdb.FunctionVersion{}, fmt.Errorf("artifact key is required")
	}
	if err := (jobmodel.Spec{
		ID: functionID, Image: spec.Image, Command: spec.Command, Args: spec.Args,
		GPUType: spec.GPUType, GPUs: spec.GPUs, CPUs: spec.CPUs,
		MemoryGB: spec.MemoryGB, DurationSeconds: spec.DurationSeconds,
		MaxAttempts: spec.MaxAttempts, RetryBackoffSeconds: spec.RetryBackoffSeconds,
	}).Validate(); err != nil {
		return postgresdb.FunctionVersion{}, err
	}
	if s.artifacts == nil {
		return postgresdb.FunctionVersion{}, fmt.Errorf("%w: not configured", artifact.ErrStorage)
	}
	if err := s.artifacts.Verify(ctx, functionID, spec.ArtifactKey); err != nil {
		return postgresdb.FunctionVersion{}, fmt.Errorf("verify artifact for function %q: %w", functionID, err)
	}

	args := spec.Args
	if args == nil {
		args = []string{}
	}
	encodedArgs, err := json.Marshal(args)
	if err != nil {
		return postgresdb.FunctionVersion{}, fmt.Errorf("encode function arguments: %w", err)
	}
	maxAttempts := spec.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = defaultMaxAttempts
	}
	retryBackoffSeconds := spec.RetryBackoffSeconds
	if retryBackoffSeconds == 0 {
		retryBackoffSeconds = defaultRetryBackoffSeconds
	}

	version, err := withTx(ctx, s.db, func(queries *postgresdb.Queries) (postgresdb.FunctionVersion, error) {
		if err := queries.CreateFunction(ctx, postgresdb.CreateFunctionParams{
			ID: functionID, CreatedAt: time.Now().UnixMilli(),
		}); err != nil {
			return postgresdb.FunctionVersion{}, err
		}
		return queries.CreateFunctionVersion(ctx, postgresdb.CreateFunctionVersionParams{
			ID: uuid.NewString(), FunctionID: functionID,
			ArtifactKey: spec.ArtifactKey,
			Image:       spec.Image, Command: spec.Command, Args: string(encodedArgs),
			GpuType: spec.GPUType, Gpus: spec.GPUs, Cpus: spec.CPUs,
			MemoryGb: spec.MemoryGB, DurationSeconds: spec.DurationSeconds,
			MaxAttempts: maxAttempts, RetryBackoffSeconds: retryBackoffSeconds,
			CreatedAt: time.Now().UnixMilli(),
		})
	})
	if err != nil {
		return postgresdb.FunctionVersion{}, fmt.Errorf("deploy function %q: %w", functionID, err)
	}
	return version, nil
}

func (s *Service) InvokeFunction(ctx context.Context, functionID, versionID, jobID string, priority int64) (postgresdb.Job, error) {
	functionID = strings.TrimSpace(functionID)
	versionID = strings.TrimSpace(versionID)
	if functionID == "" || versionID == "" {
		return postgresdb.Job{}, fmt.Errorf("function ID and version ID are required")
	}
	version, err := s.queries.GetFunctionVersion(ctx, versionID)
	if errors.Is(err, sql.ErrNoRows) {
		return postgresdb.Job{}, fmt.Errorf("%w: %q", ErrFunctionVersionNotFound, versionID)
	}
	if err != nil {
		return postgresdb.Job{}, fmt.Errorf("get function version %q: %w", versionID, err)
	}
	if version.FunctionID != functionID {
		return postgresdb.Job{}, fmt.Errorf("%w: version %q does not belong to function %q", ErrFunctionVersionNotFound, versionID, functionID)
	}

	var args []string
	if err := json.Unmarshal([]byte(version.Args), &args); err != nil {
		return postgresdb.Job{}, fmt.Errorf("decode function version arguments: %w", err)
	}
	return s.Submit(ctx, jobmodel.Spec{
		ID: jobID, FunctionVersionID: version.ID, Priority: priority,
		Image: version.Image, Command: version.Command, Args: args,
		GPUType: version.GpuType, GPUs: version.Gpus, CPUs: version.Cpus,
		MemoryGB: version.MemoryGb, DurationSeconds: version.DurationSeconds,
		MaxAttempts: version.MaxAttempts, RetryBackoffSeconds: version.RetryBackoffSeconds,
	})
}
