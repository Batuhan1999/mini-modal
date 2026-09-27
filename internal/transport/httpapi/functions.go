package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"mini-modal/internal/artifact"
	"mini-modal/internal/controlplane"
	postgresdb "mini-modal/internal/store/postgres/sqlc"
)

type deployFunctionRequest struct {
	ArtifactKey         string   `json:"artifact_key"`
	Image               string   `json:"image"`
	Command             string   `json:"command"`
	Args                []string `json:"args"`
	GPUType             string   `json:"gpu_type"`
	GPUs                int64    `json:"gpus"`
	CPUs                int64    `json:"cpus"`
	MemoryGB            int64    `json:"memory_gb"`
	DurationSeconds     int64    `json:"duration_seconds"`
	MaxAttempts         int64    `json:"max_attempts"`
	RetryBackoffSeconds int64    `json:"retry_backoff_seconds"`
}

func (s *Server) uploadFunction(w http.ResponseWriter, r *http.Request) {
	functionID := strings.TrimSpace(r.PathValue("id"))
	if functionID == "" {
		writeError(w, http.StatusBadRequest, "function ID is required")
		return
	}
	if r.ContentLength > artifact.MaxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "artifact exceeds 32 MiB")
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (contentType != "application/gzip" && contentType != "application/octet-stream" && contentType != "text/x-python") {
		writeError(w, http.StatusUnsupportedMediaType, "artifact must be a .tar.gz archive or Python source file")
		return
	}
	archive, err := io.ReadAll(http.MaxBytesReader(w, r.Body, artifact.MaxUploadBytes))
	if err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			writeError(w, http.StatusRequestEntityTooLarge, "artifact exceeds 32 MiB")
		} else {
			writeError(w, http.StatusBadRequest, "could not read artifact")
		}
		return
	}
	if len(archive) == 0 {
		writeError(w, http.StatusBadRequest, "artifact is empty")
		return
	}
	if contentType == "text/x-python" {
		archive, err = artifact.PackagePythonFile(r.URL.Query().Get("filename"), archive)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	result, err := s.service.UploadFunction(r.Context(), functionID, archive)
	if err != nil {
		if errors.Is(err, artifact.ErrInvalidArchive) {
			writeError(w, http.StatusBadRequest, err.Error())
		} else {
			writeError(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

type invokeFunctionRequest struct {
	ID        string `json:"id"`
	VersionID string `json:"version_id"`
	Priority  int64  `json:"priority"`
}

func (s *Server) listFunctions(w http.ResponseWriter, r *http.Request) {
	functions, err := s.service.Functions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if functions == nil {
		functions = make([]postgresdb.ListFunctionsRow, 0)
	}
	writeJSON(w, http.StatusOK, functions)
}

func (s *Server) listFunctionVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := s.service.FunctionVersions(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if versions == nil {
		versions = make([]postgresdb.FunctionVersion, 0)
	}
	writeJSON(w, http.StatusOK, versions)
}

func (s *Server) deployFunction(w http.ResponseWriter, r *http.Request) {
	var request deployFunctionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}

	version, err := s.service.DeployFunction(r.Context(), r.PathValue("id"), controlplane.FunctionVersionSpec{
		ArtifactKey: request.ArtifactKey,
		Image:       request.Image, Command: request.Command, Args: request.Args,
		GPUType: request.GPUType, GPUs: request.GPUs, CPUs: request.CPUs,
		MemoryGB: request.MemoryGB, DurationSeconds: request.DurationSeconds,
		MaxAttempts: request.MaxAttempts, RetryBackoffSeconds: request.RetryBackoffSeconds,
	})
	if err != nil {
		if errors.Is(err, artifact.ErrStorage) {
			writeError(w, http.StatusBadGateway, err.Error())
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, version)
}

func (s *Server) invokeFunction(w http.ResponseWriter, r *http.Request) {
	var request invokeFunctionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}

	job, err := s.service.InvokeFunction(r.Context(), r.PathValue("id"), request.VersionID, request.ID, request.Priority)
	if err != nil {
		switch {
		case errors.Is(err, controlplane.ErrFunctionVersionNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, controlplane.ErrSubmissionConflict):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}
