package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"mini-modal/internal/controlplane"
	jobmodel "mini-modal/internal/job"
	postgresdb "mini-modal/internal/store/postgres/sqlc"
)

type submitJobRequest struct {
	ID                string          `json:"id"`
	FunctionVersionID string          `json:"function_version_id"`
	Priority          int64           `json:"priority"`
	Input             json.RawMessage `json:"input"`
}

type jobCountResponse struct {
	Count int64 `json:"count"`
}

func (r submitJobRequest) spec() jobmodel.Spec {
	return jobmodel.Spec{
		ID: r.ID, FunctionVersionID: r.FunctionVersionID, Priority: r.Priority, Input: r.Input,
	}
}

func (s *Server) submitJob(w http.ResponseWriter, r *http.Request) {
	var request submitJobRequest
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

	record, err := s.service.Submit(r.Context(), request.spec())
	if err != nil {
		if errors.Is(err, controlplane.ErrSubmissionConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, record)
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	var (
		jobs []postgresdb.Job
		err  error
	)
	limitValue := r.URL.Query().Get("limit")
	if limitValue == "" {
		jobs, err = s.service.Jobs(r.Context())
	} else {
		limit, parseErr := strconv.ParseInt(limitValue, 10, 64)
		if parseErr != nil || limit <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		jobs, err = s.service.RecentJobs(r.Context(), limit)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if jobs == nil {
		jobs = make([]postgresdb.Job, 0)
	}

	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) countJobs(w http.ResponseWriter, r *http.Request) {
	count, err := s.service.JobCount(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, jobCountResponse{Count: count})
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	record, err := s.service.Job(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, record)
}

func (s *Server) getJobResult(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if _, err := s.service.Job(r.Context(), jobID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	result, err := s.service.JobResult(r.Context(), jobID)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(r.PathValue("id"))
	if _, err := s.service.Job(r.Context(), jobID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := s.service.CancelJob(r.Context(), jobID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	record, err := s.service.Job(r.Context(), jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, record)
}

func (s *Server) listJobLogs(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(r.PathValue("id"))
	record, err := s.service.Job(r.Context(), jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	attempt := record.Attempt
	if value := strings.TrimSpace(r.URL.Query().Get("attempt")); value != "" {
		attempt, err = strconv.ParseInt(value, 10, 64)
		if err != nil || attempt <= 0 {
			writeError(w, http.StatusBadRequest, "attempt must be a positive integer")
			return
		}
	}

	if attempt <= 0 {
		writeJSON(w, http.StatusOK, make([]postgresdb.JobLog, 0))
		return
	}

	logs, err := s.service.JobLogs(r.Context(), jobID, attempt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if logs == nil {
		logs = make([]postgresdb.JobLog, 0)
	}

	writeJSON(w, http.StatusOK, logs)
}

func (s *Server) listJobEvents(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(r.PathValue("id"))
	if _, err := s.service.Job(r.Context(), jobID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	events, err := s.service.JobEvents(r.Context(), jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if events == nil {
		events = make([]postgresdb.JobEvent, 0)
	}

	writeJSON(w, http.StatusOK, events)
}

func ensureSingleJSONValue(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}
