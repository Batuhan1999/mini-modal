package simulator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunnerSubmitsAndWaitsForJobs(t *testing.T) {
	var mu sync.Mutex
	jobs := make([]jobResponse, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/jobs":
			var spec submitRequest
			if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			jobs = append(jobs, jobResponse{ID: spec.ID, Status: "succeeded"})
			mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs":
			mu.Lock()
			defer mu.Unlock()
			_ = json.NewEncoder(w).Encode(jobs)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	summary, err := NewRunner(server.Client()).Run(context.Background(), Config{
		BaseURL:      server.URL,
		Jobs:         3,
		Seed:         42,
		MinRuntime:   time.Second,
		MaxRuntime:   time.Second,
		PollInterval: time.Millisecond,
		WaitTimeout:  time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Submitted != 3 || summary.Statuses["succeeded"] != 3 {
		t.Fatalf("summary = %+v, want three succeeded jobs", summary)
	}
	if summary.SubmissionThroughput() <= 0 || summary.CompletionThroughput() <= 0 {
		t.Fatalf("throughput was not recorded: %+v", summary)
	}
}

func TestRunnerSupportsConcurrentSubmitOnlyLoad(t *testing.T) {
	var inFlight atomic.Int64
	var maxInFlight atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/jobs" {
			http.Error(w, "unexpected request", http.StatusInternalServerError)
			return
		}
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			maximum := maxInFlight.Load()
			if current <= maximum || maxInFlight.CompareAndSwap(maximum, current) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	summary, err := NewRunner(server.Client()).Run(context.Background(), Config{
		BaseURL:         server.URL,
		Jobs:            20,
		Concurrency:     4,
		SubmitOnly:      true,
		Seed:            42,
		MinRuntime:      time.Second,
		MaxRuntime:      time.Second,
		TimeoutHeadroom: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Submitted != 20 || summary.SubmissionErrors != 0 {
		t.Fatalf("summary = %+v, want 20 successful submissions", summary)
	}
	if summary.TerminalJobs() != 0 {
		t.Fatalf("submit-only run waited for terminal jobs: %+v", summary)
	}
	if maxInFlight.Load() < 2 {
		t.Fatalf("maximum concurrent requests = %d, want at least 2", maxInFlight.Load())
	}
	if summary.SubmissionLatencyP95 <= 0 {
		t.Fatalf("submission latency was not recorded: %+v", summary)
	}
}
