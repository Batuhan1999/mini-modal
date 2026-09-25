package httpapi

import (
	"encoding/json"
	"net/http"

	"mini-modal/internal/devcluster"
)

func (s *Server) getDevNodes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.devCluster.Status())
}

func (s *Server) startDevNodes(w http.ResponseWriter, r *http.Request) {
	var config devcluster.Config
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&config); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}

	status, err := s.devCluster.Start(config)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) stopDevNodes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.devCluster.Stop())
}
