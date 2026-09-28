package httpapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mini-modal/internal/artifact"
	postgresdb "mini-modal/internal/store/postgres/sqlc"
)

type fakeArtifactStore struct {
	functionID string
	key        string
	data       []byte
}

func (s *fakeArtifactStore) Upload(_ context.Context, functionID string, archive []byte) (artifact.UploadResult, error) {
	s.functionID = functionID
	s.data = append([]byte(nil), archive...)
	s.key = "artifact-for-" + functionID
	return artifact.UploadResult{Key: s.key, SizeBytes: int64(len(archive))}, nil
}

func (s *fakeArtifactStore) Verify(_ context.Context, functionID, key string) error {
	if functionID != s.functionID || key != s.key {
		return artifact.ErrNotFound
	}
	return nil
}

func TestUploadAndDeployFunctionArtifact(t *testing.T) {
	store := &fakeArtifactStore{}
	server := newTestServerWithArtifacts(t, store)
	var body bytes.Buffer
	gz := gzip.NewWriter(&body)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "main.py", Mode: 0644, Size: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	upload := func(functionID string, archive []byte) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/v1/functions/"+functionID+"/artifacts", bytes.NewReader(archive))
		request.Header.Set("Content-Type", "application/gzip")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}
	if response := upload("greet", []byte("bad archive")); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid archive status = %d, body = %s", response.Code, response.Body.String())
	}
	if response := upload("greet", nil); response.Code != http.StatusBadRequest {
		t.Fatalf("empty archive status = %d, body = %s", response.Code, response.Body.String())
	}
	tooLarge := httptest.NewRequest(http.MethodPost, "/v1/functions/greet/artifacts", bytes.NewReader(nil))
	tooLarge.Header.Set("Content-Type", "application/gzip")
	tooLarge.ContentLength = artifact.MaxUploadBytes + 1
	tooLargeResponse := httptest.NewRecorder()
	server.ServeHTTP(tooLargeResponse, tooLarge)
	if tooLargeResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized archive status = %d, body = %s", tooLargeResponse.Code, tooLargeResponse.Body.String())
	}
	response := upload("greet", body.Bytes())
	if response.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", response.Code, response.Body.String())
	}
	var uploaded artifact.UploadResult
	if err := json.NewDecoder(response.Body).Decode(&uploaded); err != nil {
		t.Fatal(err)
	}
	if uploaded.Key != store.key || uploaded.SizeBytes != int64(body.Len()) || !bytes.Equal(store.data, body.Bytes()) {
		t.Fatalf("unexpected upload: %+v", uploaded)
	}
	deploy := func(functionID, key string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/v1/functions/"+functionID+"/versions", strings.NewReader(fmt.Sprintf(`{"artifact_key":%q,"image":"python:3.13","entrypoint":"main:greet","cpus":1,"memory_gb":1}`, key)))
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}
	if bad := deploy("other", uploaded.Key); bad.Code != http.StatusBadRequest {
		t.Fatalf("wrong function artifact status = %d, body = %s", bad.Code, bad.Body.String())
	}
	deployed := deploy("greet", uploaded.Key)
	if deployed.Code != http.StatusCreated {
		t.Fatalf("deploy status = %d, body = %s", deployed.Code, deployed.Body.String())
	}
	var version postgresdb.FunctionVersion
	if err := json.NewDecoder(deployed.Body).Decode(&version); err != nil {
		t.Fatal(err)
	}
	if version.ArtifactKey != uploaded.Key {
		t.Fatalf("version artifact key = %q, want %q", version.ArtifactKey, uploaded.Key)
	}
	functionsResponse := httptest.NewRecorder()
	server.ServeHTTP(functionsResponse, httptest.NewRequest(http.MethodGet, "/v1/functions", nil))
	if functionsResponse.Code != http.StatusOK {
		t.Fatalf("list functions status = %d, body = %s", functionsResponse.Code, functionsResponse.Body.String())
	}
	var functions []postgresdb.ListFunctionsRow
	if err := json.NewDecoder(functionsResponse.Body).Decode(&functions); err != nil {
		t.Fatal(err)
	}
	if len(functions) != 2 || functions[0].ID != "greet" || functions[0].VersionCount != 1 {
		t.Fatalf("unexpected functions: %+v", functions)
	}
	versionsResponse := httptest.NewRecorder()
	server.ServeHTTP(versionsResponse, httptest.NewRequest(http.MethodGet, "/v1/functions/greet/versions", nil))
	if versionsResponse.Code != http.StatusOK {
		t.Fatalf("list versions status = %d, body = %s", versionsResponse.Code, versionsResponse.Body.String())
	}
	var versions []postgresdb.FunctionVersion
	if err := json.NewDecoder(versionsResponse.Body).Decode(&versions); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].ID != version.ID || versions[0].ArtifactKey != uploaded.Key {
		t.Fatalf("unexpected versions: %+v", versions)
	}
	invokeRequest := httptest.NewRequest(http.MethodPost, "/v1/functions/greet/jobs", strings.NewReader(fmt.Sprintf(`{"id":"uploaded-job","version_id":%q}`, version.ID)))
	invokeResponse := httptest.NewRecorder()
	server.ServeHTTP(invokeResponse, invokeRequest)
	if invokeResponse.Code != http.StatusAccepted {
		t.Fatalf("invoke status = %d, body = %s", invokeResponse.Code, invokeResponse.Body.String())
	}
	var job postgresdb.Job
	if err := json.NewDecoder(invokeResponse.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	if job.FunctionVersionID != version.ID {
		t.Fatalf("job did not refer to deployed function version: %+v", job)
	}

	pythonRequest := httptest.NewRequest(http.MethodPost, "/v1/functions/greet/artifacts?filename=hello_function.py", strings.NewReader("print('hello')\n"))
	pythonRequest.Header.Set("Content-Type", "text/x-python")
	pythonResponse := httptest.NewRecorder()
	server.ServeHTTP(pythonResponse, pythonRequest)
	if pythonResponse.Code != http.StatusCreated {
		t.Fatalf("Python upload status = %d, body = %s", pythonResponse.Code, pythonResponse.Body.String())
	}
	if err := artifact.ValidateArchive(store.data); err != nil {
		t.Fatalf("Python source was not packaged as an archive: %v", err)
	}
}

func TestDeployAndInvokeFunctionVersions(t *testing.T) {
	server := newTestServerWithArtifacts(t, &fakeArtifactStore{functionID: "greet", key: "artifact-for-greet"})
	post := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return response
	}

	deploy := func(image string) postgresdb.FunctionVersion {
		t.Helper()
		body := fmt.Sprintf(`{"artifact_key":"artifact-for-greet","image":%q,"entrypoint":"main:greet","cpus":2,"memory_gb":4}`, image)
		response := post("/v1/functions/greet/versions", body)
		if response.Code != http.StatusCreated {
			t.Fatalf("deploy status = %d, body = %s", response.Code, response.Body.String())
		}
		var version postgresdb.FunctionVersion
		if err := json.NewDecoder(response.Body).Decode(&version); err != nil {
			t.Fatal(err)
		}
		return version
	}
	first := deploy("alpine:3.20")
	second := deploy("alpine:3.21")
	if first.ID == second.ID || first.FunctionID != "greet" || second.FunctionID != "greet" {
		t.Fatalf("unexpected versions: first=%+v second=%+v", first, second)
	}

	invoke := func(jobID, versionID string) *httptest.ResponseRecorder {
		t.Helper()
		return post("/v1/functions/greet/jobs", fmt.Sprintf(`{"id":%q,"version_id":%q,"priority":7}`, jobID, versionID))
	}
	response := invoke("job-1", first.ID)
	if response.Code != http.StatusAccepted {
		t.Fatalf("invoke status = %d, body = %s", response.Code, response.Body.String())
	}
	var job postgresdb.Job
	if err := json.NewDecoder(response.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	if job.FunctionVersionID != first.ID || first.Cpus != 2 || first.MemoryGb != 4 {
		t.Fatalf("job did not refer to first version: %+v", job)
	}
	if retry := invoke("job-1", first.ID); retry.Code != http.StatusAccepted {
		t.Fatalf("idempotent retry status = %d, body = %s", retry.Code, retry.Body.String())
	}
	if conflict := invoke("job-1", second.ID); conflict.Code != http.StatusConflict {
		t.Fatalf("different version status = %d, want 409; body = %s", conflict.Code, conflict.Body.String())
	}
	if missing := post("/v1/functions/other/jobs", fmt.Sprintf(`{"id":"job-2","version_id":%q}`, first.ID)); missing.Code != http.StatusNotFound {
		t.Fatalf("wrong function status = %d, want 404; body = %s", missing.Code, missing.Body.String())
	}
	if next := invoke("job-2", second.ID); next.Code != http.StatusAccepted {
		t.Fatalf("invoke next version status = %d, body = %s", next.Code, next.Body.String())
	}
}

func TestGetFunctionVersion(t *testing.T) {
	server := newTestServer(t)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/function-versions/test-function-version", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("version status = %d; body = %s", response.Code, response.Body.String())
	}
	var version postgresdb.FunctionVersion
	if err := json.Unmarshal(response.Body.Bytes(), &version); err != nil {
		t.Fatal(err)
	}
	if version.Entrypoint != "hello_function:greet" || version.Cpus != 1 {
		t.Fatalf("unexpected version: %+v", version)
	}
	missing := httptest.NewRecorder()
	server.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/v1/function-versions/missing", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing version status = %d, want 404", missing.Code)
	}
}
