package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentv1 "mini-modal/gen/agent/v1"
	"mini-modal/internal/controlplane"
	"mini-modal/internal/devcluster"
	"mini-modal/internal/domain"
	jobmodel "mini-modal/internal/job"
	"mini-modal/internal/store/sqlite"
	sqlitedb "mini-modal/internal/store/sqlite/sqlc"

	"github.com/prometheus/client_golang/prometheus"
)

func TestJobEndpoints(t *testing.T) {
	server := newTestServer(t)

	submitRequest := httptest.NewRequest(
		http.MethodPost,
		"/v1/jobs",
		strings.NewReader(`{
			"id":"job-1",
			"priority":5,
			"gpus":0,
			"cpus":2,
			"memory_gb":4,
			"duration_seconds":1,
			"image":"alpine:3.20",
			"command":"echo",
			"args":[]
		}`),
	)
	submitResponse := httptest.NewRecorder()
	server.ServeHTTP(submitResponse, submitRequest)

	if submitResponse.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, want %d; body = %s", submitResponse.Code, http.StatusAccepted, submitResponse.Body.String())
	}

	var submitted sqlitedb.Job
	if err := json.NewDecoder(submitResponse.Body).Decode(&submitted); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}
	if submitted.ID != "job-1" || jobmodel.Status(submitted.Status) != jobmodel.Assigned || submitted.Image != "alpine:3.20" {
		t.Fatalf("submitted job = %+v, want ID job-1 with assigned status", submitted)
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/v1/jobs/job-1", nil)
	getResponse := httptest.NewRecorder()
	server.ServeHTTP(getResponse, getRequest)

	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d; body = %s", getResponse.Code, http.StatusOK, getResponse.Body.String())
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)

	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d; body = %s", listResponse.Code, http.StatusOK, listResponse.Body.String())
	}

	var listed []sqlitedb.Job
	if err := json.NewDecoder(listResponse.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != "job-1" {
		t.Fatalf("listed jobs = %+v, want only job-1", listed)
	}

	eventsRequest := httptest.NewRequest(http.MethodGet, "/v1/jobs/job-1/events", nil)
	eventsResponse := httptest.NewRecorder()
	server.ServeHTTP(eventsResponse, eventsRequest)
	if eventsResponse.Code != http.StatusOK {
		t.Fatalf("events status = %d, want %d; body = %s", eventsResponse.Code, http.StatusOK, eventsResponse.Body.String())
	}
	var events []sqlitedb.JobEvent
	if err := json.NewDecoder(eventsResponse.Body).Decode(&events); err != nil {
		t.Fatalf("decode events response: %v", err)
	}
	if len(events) != 2 || events[0].Event != "submitted" || events[1].Event != "assigned" {
		t.Fatalf("events = %+v, want submitted then assigned", events)
	}

	if err := server.service.CreateJobLog(context.Background(), domain.LogStream{
		JobID:     "job-1",
		Attempt:   submitted.Attempt,
		Sequence:  0,
		Source:    agentv1.LogSource_LOG_SOURCE_STDOUT,
		Data:      []byte("hello from the executor\n"),
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create job log: %v", err)
	}
	if err := server.service.CreateJobLog(context.Background(), domain.LogStream{
		JobID:     "job-1",
		Attempt:   submitted.Attempt,
		Sequence:  1,
		Source:    agentv1.LogSource_LOG_SOURCE_SYSTEM,
		Data:      []byte("pulling image\n"),
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create system job log: %v", err)
	}

	logsRequest := httptest.NewRequest(http.MethodGet, "/v1/jobs/job-1/logs?attempt=1", nil)
	logsResponse := httptest.NewRecorder()
	server.ServeHTTP(logsResponse, logsRequest)
	if logsResponse.Code != http.StatusOK {
		t.Fatalf("logs status = %d, want %d; body = %s", logsResponse.Code, http.StatusOK, logsResponse.Body.String())
	}

	var logs []sqlitedb.JobLog
	if err := json.NewDecoder(logsResponse.Body).Decode(&logs); err != nil {
		t.Fatalf("decode logs response: %v", err)
	}
	if len(logs) != 2 || logs[0].Data != "hello from the executor\n" || logs[0].Source != "stdout" ||
		logs[1].Data != "pulling image\n" || logs[1].Source != "system" {
		t.Fatalf("logs = %+v, want stdout and system chunks", logs)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); !strings.Contains(body, "mini_modal_jobs_submitted_total") {
		t.Fatalf("metrics response does not contain mini-modal metrics: %s", body)
	}
}

func TestGetJobReturnsNotFound(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/jobs/missing", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNotFound, response.Body.String())
	}
}

func TestGetMissingJobEventsReturnsNotFound(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/jobs/missing/events", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNotFound, response.Body.String())
	}
}

func TestCancelJobEndpoint(t *testing.T) {
	server := newTestServer(t)
	ctx := context.Background()
	if _, err := server.service.Submit(ctx, jobmodel.Spec{
		ID: "job-cancel", CPUs: 1, MemoryGB: 1,
		Image: "alpine:3.20", Command: "sleep", Args: []string{"30"},
	}); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/jobs/job-cancel/cancel", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusAccepted, response.Body.String())
	}

	var record sqlitedb.Job
	if err := json.NewDecoder(response.Body).Decode(&record); err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(record.Status) != jobmodel.Cancelled {
		t.Fatalf("status = %q, want %q", record.Status, jobmodel.Cancelled)
	}
}

func TestCancelMissingJobReturnsNotFound(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/jobs/missing/cancel", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNotFound, response.Body.String())
	}
}

func TestSubmitRejectsInvalidJSON(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(`{"id":`))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestSubmitRejectsMultipleJSONValues(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/jobs",
		strings.NewReader(`{"id":"job-1","cpus":1,"memory_gb":1} {}`),
	)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestDashboardAndNodesEndpoints(t *testing.T) {
	server := newTestServer(t)

	dashboardRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	dashboardResponse := httptest.NewRecorder()
	server.ServeHTTP(dashboardResponse, dashboardRequest)
	if dashboardResponse.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d, want %d", dashboardResponse.Code, http.StatusOK)
	}
	if !strings.Contains(dashboardResponse.Body.String(), "Mini Modal") {
		t.Fatal("dashboard response does not contain the application title")
	}
	if !strings.Contains(dashboardResponse.Body.String(), `id="jobs-body"`) {
		t.Fatal("dashboard response does not contain the jobs table")
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/jobs/job-1", nil)
	detailResponse := httptest.NewRecorder()
	server.ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("job detail status = %d, want %d", detailResponse.Code, http.StatusOK)
	}
	if !strings.Contains(detailResponse.Body.String(), `id="job-detail-view"`) {
		t.Fatal("job detail response does not contain the job detail view")
	}

	nodesRequest := httptest.NewRequest(http.MethodGet, "/v1/nodes", nil)
	nodesResponse := httptest.NewRecorder()
	server.ServeHTTP(nodesResponse, nodesRequest)
	if nodesResponse.Code != http.StatusOK {
		t.Fatalf("nodes status = %d, want %d; body = %s", nodesResponse.Code, http.StatusOK, nodesResponse.Body.String())
	}

	var nodes []nodeResponse
	if err := json.NewDecoder(nodesResponse.Body).Decode(&nodes); err != nil {
		t.Fatalf("decode nodes response: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != "node-a" || !nodes[0].Healthy {
		t.Fatalf("nodes = %+v, want one healthy node-a", nodes)
	}
}

func TestDevNodeEndpoints(t *testing.T) {
	server := newTestServer(t)
	startRequest := httptest.NewRequest(
		http.MethodPut,
		"/v1/dev/nodes",
		strings.NewReader(`{
			"node_count":2,
			"cpus_per_node":4,
			"memory_gb_per_node":8
		}`),
	)
	startResponse := httptest.NewRecorder()
	server.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusOK {
		t.Fatalf("start dev nodes status = %d, want %d; body = %s", startResponse.Code, http.StatusOK, startResponse.Body.String())
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/v1/dev/nodes", nil)
	getResponse := httptest.NewRecorder()
	server.ServeHTTP(getResponse, getRequest)
	var status devcluster.Status
	if err := json.NewDecoder(getResponse.Body).Decode(&status); err != nil {
		t.Fatalf("decode dev node status: %v", err)
	}
	if !status.Running || status.Config.NodeCount != 2 {
		t.Fatalf("dev node status = %+v, want running with two nodes", status)
	}

	stopRequest := httptest.NewRequest(http.MethodDelete, "/v1/dev/nodes", nil)
	stopResponse := httptest.NewRecorder()
	server.ServeHTTP(stopResponse, stopRequest)
	if stopResponse.Code != http.StatusOK {
		t.Fatalf("stop dev nodes status = %d, want %d", stopResponse.Code, http.StatusOK)
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	registry := prometheus.NewRegistry()
	metrics := controlplane.NewMetrics(registry)
	service := controlplane.NewService(controlplane.NewRuntimeState(), sqlitedb.New(db), db, metrics)
	if err := service.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterNode(ctx, domain.Node{
		ID: "node-a", TotalCPUs: 8, TotalMemoryGB: 16,
	}); err != nil {
		t.Fatal(err)
	}

	return NewServer(service, &fakeDevNodeManager{}, registry)
}

type fakeDevNodeManager struct {
	status devcluster.Status
}

func (m *fakeDevNodeManager) Start(config devcluster.Config) (devcluster.Status, error) {
	m.status = devcluster.Status{
		Running: true,
		Config:  config,
		Processes: []devcluster.ProcessStatus{
			{NodeID: "dev-node-01", PID: 1001, Running: true},
		},
	}
	return m.status, nil
}

func (m *fakeDevNodeManager) Stop() devcluster.Status {
	m.status.Running = false
	m.status.Processes = nil
	return m.status
}

func (m *fakeDevNodeManager) Status() devcluster.Status {
	if m.status.Config.NodeCount == 0 {
		m.status.Config = devcluster.Config{NodeCount: 3, CPUsPerNode: 8, MemoryGBPerNode: 16}
	}
	return m.status
}
