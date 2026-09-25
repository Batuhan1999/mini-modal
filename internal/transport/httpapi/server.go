package httpapi

import (
	"net/http"

	"mini-modal/internal/controlplane"
	"mini-modal/internal/devcluster"
)

type devNodeManager interface {
	Start(devcluster.Config) (devcluster.Status, error)
	Stop() devcluster.Status
	Status() devcluster.Status
}

type Server struct {
	service    *controlplane.Service
	devCluster devNodeManager
	mux        *http.ServeMux
}

func NewServer(service *controlplane.Service, devCluster devNodeManager) *Server {
	server := &Server{
		service:    service,
		devCluster: devCluster,
		mux:        http.NewServeMux(),
	}
	server.registerRoutes()

	return server
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	s.registerUIRoutes()
	s.mux.HandleFunc("POST /v1/jobs", s.submitJob)
	s.mux.HandleFunc("GET /v1/jobs", s.listJobs)
	s.mux.HandleFunc("GET /v1/jobs/{id}", s.getJob)
	s.mux.HandleFunc("GET /v1/nodes", s.listNodes)
	s.mux.HandleFunc("GET /v1/dev/nodes", s.getDevNodes)
	s.mux.HandleFunc("PUT /v1/dev/nodes", s.startDevNodes)
	s.mux.HandleFunc("DELETE /v1/dev/nodes", s.stopDevNodes)
}
