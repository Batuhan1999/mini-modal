package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	sqlitedb "mini-modal/internal/store/sqlite/sqlc"
)

func (s *Server) submitJob(w http.ResponseWriter, r *http.Request) {
	var request sqlitedb.CreateJobParams
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

	record, err := s.service.Submit(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, record)
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.service.Jobs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if jobs == nil {
		jobs = make([]sqlitedb.Job, 0)
	}

	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	record, err := s.service.Job(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, record)
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
		writeJSON(w, http.StatusOK, make([]sqlitedb.JobLog, 0))
		return
	}

	logs, err := s.service.JobLogs(r.Context(), jobID, attempt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if logs == nil {
		logs = make([]sqlitedb.JobLog, 0)
	}

	writeJSON(w, http.StatusOK, logs)
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
