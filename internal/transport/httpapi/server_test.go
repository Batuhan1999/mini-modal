package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mini-modal/internal/controlplane"
	"mini-modal/internal/domain"
	"mini-modal/internal/store/sqlite"
	sqlitedb "mini-modal/internal/store/sqlite/sqlc"
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
			"duration_seconds":1
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
	if submitted.ID != "job-1" || submitted.Status != controlplane.JobAssigned {
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

func newTestServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	service := controlplane.NewService(controlplane.NewRuntimeState(), sqlitedb.New(db))
	if err := service.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterNode(ctx, domain.Node{
		ID: "node-a", TotalCPUs: 8, TotalMemoryGB: 16,
	}); err != nil {
		t.Fatal(err)
	}

	return NewServer(service)
}
