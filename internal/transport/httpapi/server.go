package httpapi

import (
	"net/http"

	"mini-modal/internal/controlplane"
	"mini-modal/internal/devcluster"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	metrics    http.Handler
}

func NewServer(
	service *controlplane.Service,
	devCluster devNodeManager,
	gatherer prometheus.Gatherer,
) *Server {
	server := &Server{
		service:    service,
		devCluster: devCluster,
		mux:        http.NewServeMux(),
		metrics:    promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}),
	}
	server.registerRoutes()

	return server
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	s.registerUIRoutes()
	s.mux.Handle("GET /metrics", s.metrics)
	s.mux.HandleFunc("POST /v1/jobs", s.submitJob)
	s.mux.HandleFunc("GET /v1/jobs", s.listJobs)
	s.mux.HandleFunc("GET /v1/jobs/count", s.countJobs)
	s.mux.HandleFunc("GET /v1/jobs/{id}", s.getJob)
	s.mux.HandleFunc("POST /v1/jobs/{id}/cancel", s.cancelJob)
	s.mux.HandleFunc("GET /v1/jobs/{id}/logs", s.listJobLogs)
	s.mux.HandleFunc("GET /v1/jobs/{id}/events", s.listJobEvents)
	s.mux.HandleFunc("GET /v1/nodes", s.listNodes)
	s.mux.HandleFunc("GET /v1/dev/nodes", s.getDevNodes)
	s.mux.HandleFunc("PUT /v1/dev/nodes", s.startDevNodes)
	s.mux.HandleFunc("DELETE /v1/dev/nodes", s.stopDevNodes)
}
