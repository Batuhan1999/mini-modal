package httpapi

import (
	"net/http"

	"mini-modal/internal/controlplane"
)

type Server struct {
	service *controlplane.Service
	mux     *http.ServeMux
}

func NewServer(service *controlplane.Service) *Server {
	server := &Server{
		service: service,
		mux:     http.NewServeMux(),
	}
	server.registerRoutes()

	return server
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("POST /v1/jobs", s.submitJob)
	s.mux.HandleFunc("GET /v1/jobs", s.listJobs)
	s.mux.HandleFunc("GET /v1/jobs/{id}", s.getJob)
}
