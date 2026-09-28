package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
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
	postgresdb "mini-modal/internal/store/postgres/sqlc"
	"mini-modal/internal/store/postgres/testdb"

	"github.com/prometheus/client_golang/prometheus"
)

func TestJobEndpoints(t *testing.T) {
	server := newTestServer(t)

	submitRequest := httptest.NewRequest(
		http.MethodPost,
		"/v1/jobs",
		strings.NewReader(`{
			"id":"job-1",
			"function_version_id":"test-function-version",
			"priority":5,
            "input":{"args":[],"kwargs":{}}
		}`),
	)
	submitResponse := httptest.NewRecorder()
	server.ServeHTTP(submitResponse, submitRequest)

	if submitResponse.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, want %d; body = %s", submitResponse.Code, http.StatusAccepted, submitResponse.Body.String())
	}

	var submitted postgresdb.Job
	if err := json.NewDecoder(submitResponse.Body).Decode(&submitted); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}
	if submitted.ID != "job-1" || jobmodel.Status(submitted.Status) != jobmodel.Pending || submitted.FunctionVersionID != testdb.FunctionVersionID {
		t.Fatalf("submitted job = %+v, want ID job-1 with pending status", submitted)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.service.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for {
		job, err := server.service.Job(ctx, submitted.ID)
		if err != nil {
			t.Fatal(err)
		}
		if jobmodel.Status(job.Status) == jobmodel.Assigned {
			break
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatalf("job %q stayed in status %q instead of becoming assigned", submitted.ID, job.Status)
		}
	}
	scheduled, err := server.service.Job(context.Background(), submitted.ID)
	if err != nil {
		t.Fatal(err)
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

	var listed []postgresdb.Job
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
	var events []postgresdb.JobEvent
	if err := json.NewDecoder(eventsResponse.Body).Decode(&events); err != nil {
		t.Fatalf("decode events response: %v", err)
	}
	if len(events) != 2 || events[0].Event != "submitted" || events[1].Event != "assigned" {
		t.Fatalf("events = %+v, want submitted then assigned", events)
	}

	if err := server.service.CreateJobLog(context.Background(), domain.LogStream{
		JobID:     "job-1",
		Attempt:   scheduled.Attempt,
		Sequence:  0,
		Source:    agentv1.LogSource_LOG_SOURCE_STDOUT,
		Data:      []byte("hello from the executor\n"),
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create job log: %v", err)
	}
	if err := server.service.CreateJobLog(context.Background(), domain.LogStream{
		JobID:     "job-1",
		Attempt:   scheduled.Attempt,
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

	var logs []postgresdb.JobLog
	if err := json.NewDecoder(logsResponse.Body).Decode(&logs); err != nil {
		t.Fatalf("decode logs response: %v", err)
	}
	if len(logs) != 2 || logs[0].Data != "hello from the executor\n" || logs[0].Source != "stdout" ||
		logs[1].Data != "pulling image\n" || logs[1].Source != "system" {
		t.Fatalf("logs = %+v, want stdout and system chunks", logs)
	}
}

func TestListJobsOnlyLimitsResponseWhenRequested(t *testing.T) {
	server := newTestServer(t)
	ctx := context.Background()

	for index := 0; index < 105; index++ {
		if _, err := server.service.Submit(ctx, jobmodel.Spec{FunctionVersionID: testdb.FunctionVersionID,
			ID: fmt.Sprintf("job-%03d", index),
		}); err != nil {
			t.Fatal(err)
		}
	}

	unboundedRequest := httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)
	unboundedResponse := httptest.NewRecorder()
	server.ServeHTTP(unboundedResponse, unboundedRequest)

	var allJobs []postgresdb.Job
	if err := json.NewDecoder(unboundedResponse.Body).Decode(&allJobs); err != nil {
		t.Fatal(err)
	}
	if len(allJobs) != 105 {
		t.Fatalf("unbounded jobs = %d, want 105", len(allJobs))
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/jobs?limit=100", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", response.Code, http.StatusOK)
	}
	var jobs []postgresdb.Job
	if err := json.NewDecoder(response.Body).Decode(&jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 100 {
		t.Fatalf("listed jobs = %d, want 100", len(jobs))
	}

	countRequest := httptest.NewRequest(http.MethodGet, "/v1/jobs/count", nil)
	countResponse := httptest.NewRecorder()
	server.ServeHTTP(countResponse, countRequest)

	var count jobCountResponse
	if err := json.NewDecoder(countResponse.Body).Decode(&count); err != nil {
		t.Fatal(err)
	}
	if count.Count != 105 {
		t.Fatalf("job count = %d, want 105", count.Count)
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
	if _, err := server.service.Submit(ctx, jobmodel.Spec{FunctionVersionID: testdb.FunctionVersionID,
		ID: "job-cancel",
	}); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/jobs/job-cancel/cancel", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusAccepted, response.Body.String())
	}

	var record postgresdb.Job
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

func TestSubmitIdempotentRetryAndConflict(t *testing.T) {
	server := newTestServer(t)
	submit := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(body))
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}

	body := `{"id":"job-1","function_version_id":"test-function-version","input":{"args":[],"kwargs":{}}}`
	first := submit(body)
	retry := submit(body)
	if first.Code != http.StatusAccepted || retry.Code != http.StatusAccepted {
		t.Fatalf("submit statuses = %d, %d; want %d", first.Code, retry.Code, http.StatusAccepted)
	}
	if first.Body.String() != retry.Body.String() {
		t.Fatalf("retry response = %s, want %s", retry.Body.String(), first.Body.String())
	}

	conflict := submit(`{"id":"job-1","function_version_id":"test-function-version","input":{"args":[2],"kwargs":{}}}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflicting submit status = %d, want %d; body = %s", conflict.Code, http.StatusConflict, conflict.Body.String())
	}
	if !strings.Contains(conflict.Body.String(), "different specification") {
		t.Fatalf("conflicting submit response = %s, want specification conflict", conflict.Body.String())
	}

	count, err := server.service.JobCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("job count = %d, want 1", count)
	}
}

func TestJobResultEndpoint(t *testing.T) {
	server := newTestServer(t)
	ctx := context.Background()
	if _, err := server.service.Submit(ctx, jobmodel.Spec{ID: "result-job", FunctionVersionID: testdb.FunctionVersionID}); err != nil {
		t.Fatal(err)
	}
	getResult := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/jobs/result-job/result", nil))
		return response
	}
	if response := getResult(); response.Code != http.StatusAccepted {
		t.Fatalf("result before completion status=%d body=%s", response.Code, response.Body.String())
	}
	done := make(chan struct{})
	runCtx, cancel := context.WithCancel(ctx)
	go func() { server.service.Run(runCtx); close(done) }()
	defer func() { cancel(); <-done }()
	var assignment domain.Assignment
	var found bool
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		assignment, found, err = server.service.Claim(ctx, "node-a")
		if err != nil {
			t.Fatal(err)
		}
		if found {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !found {
		t.Fatal("job was not assigned")
	}
	if err := server.service.Complete(ctx, assignment, json.RawMessage(`{"answer":42}`)); err != nil {
		t.Fatal(err)
	}
	response := getResult()
	if response.Code != http.StatusOK {
		t.Fatalf("result status=%d body=%s", response.Code, response.Body.String())
	}
	var result postgresdb.JobResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.JobID != "result-job" || string(result.OutputJson) != `{"answer": 42}` && string(result.OutputJson) != `{"answer":42}` {
		t.Fatalf("result=%+v", result)
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
	for _, id := range []string{"function-upload-form", "function-deploy-form", "function-invoke-form", "function-version-list"} {
		if !strings.Contains(dashboardResponse.Body.String(), `id="`+id+`"`) {
			t.Fatalf("dashboard response does not contain %s", id)
		}
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
	return newTestServerWithArtifacts(t, nil)
}

func newTestServerWithArtifacts(t *testing.T, artifacts controlplane.ArtifactStore) *Server {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	testdb.SeedFunctionVersion(t, db)

	registry := prometheus.NewRegistry()
	metrics := controlplane.NewMetrics(registry)
	service := controlplane.NewService(controlplane.NewRuntimeState(), postgresdb.New(db), db, metrics, artifacts)
	if err := service.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterNode(ctx, domain.Node{
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
