package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type Config struct {
	BaseURL             string
	Jobs                int
	Rate                float64
	Concurrency         int
	SubmitOnly          bool
	Seed                int64
	MinRuntime          time.Duration
	MaxRuntime          time.Duration
	TimeoutHeadroom     time.Duration
	TimeoutFraction     float64
	FailureFraction     float64
	CPUs                int64
	MemoryGB            int64
	MaxAttempts         int64
	RetryBackoffSeconds int64
	PollInterval        time.Duration
	WaitTimeout         time.Duration
}

type Summary struct {
	Submitted            int            `json:"submitted"`
	SubmissionErrors     int            `json:"submission_errors"`
	Statuses             map[string]int `json:"statuses"`
	SubmissionElapsed    time.Duration  `json:"submission_elapsed_ns"`
	Elapsed              time.Duration  `json:"elapsed_ns"`
	SubmissionLatencyP50 time.Duration  `json:"submission_latency_p50_ns"`
	SubmissionLatencyP95 time.Duration  `json:"submission_latency_p95_ns"`
	SubmissionLatencyP99 time.Duration  `json:"submission_latency_p99_ns"`
}

func (s Summary) TerminalJobs() int {
	return s.Statuses["succeeded"] + s.Statuses["failed"] +
		s.Statuses["cancelled"] + s.Statuses["unschedulable"]
}

type Runner struct {
	client *http.Client
}

func NewRunner(client *http.Client) *Runner {
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        512,
				MaxIdleConnsPerHost: 256,
			},
		}
	}
	return &Runner{client: client}
}

func (r *Runner) Run(ctx context.Context, config Config) (Summary, error) {
	config = withDefaults(config)
	if err := validateConfig(config); err != nil {
		return Summary{}, err
	}

	startedAt := time.Now()
	prefix := fmt.Sprintf("sim-%d-%d", startedAt.UnixMilli(), config.Seed)
	jobIDs := make(map[string]struct{}, retainedJobCapacity(config))
	summary := Summary{Statuses: make(map[string]int)}

	type submissionResult struct {
		jobID   string
		latency time.Duration
		err     error
	}
	work := make(chan int, config.Concurrency*2)
	results := make(chan submissionResult, config.Concurrency*2)
	var workers sync.WaitGroup
	for worker := 0; worker < config.Concurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range work {
				random := rand.New(rand.NewSource(config.Seed + int64(index)*7919))
				spec := randomJob(random, config, fmt.Sprintf("%s-%06d", prefix, index+1))
				requestStartedAt := time.Now()
				err := r.submit(ctx, config.BaseURL, spec)
				results <- submissionResult{
					jobID:   spec.ID,
					latency: time.Since(requestStartedAt),
					err:     err,
				}
			}
		}()
	}

	latencies := make([]time.Duration, 0, config.Jobs)
	aggregated := make(chan struct{})
	go func() {
		defer close(aggregated)
		for result := range results {
			latencies = append(latencies, result.latency)
			if result.err != nil {
				summary.SubmissionErrors++
				continue
			}
			summary.Submitted++
			if !config.SubmitOnly {
				jobIDs[result.jobID] = struct{}{}
			}
		}
	}()

	dispatchErr := dispatch(ctx, config, work)
	close(work)
	workers.Wait()
	close(results)
	<-aggregated
	summary.SubmissionElapsed = time.Since(startedAt)
	setLatencyPercentiles(&summary, latencies)
	if dispatchErr != nil {
		summary.Elapsed = time.Since(startedAt)
		return summary, dispatchErr
	}
	if config.SubmitOnly {
		summary.Elapsed = summary.SubmissionElapsed
		return summary, nil
	}

	if len(jobIDs) == 0 {
		summary.Elapsed = time.Since(startedAt)
		if summary.SubmissionErrors > 0 {
			return summary, fmt.Errorf("all job submissions failed")
		}
		return summary, nil
	}

	waitCtx := ctx
	cancel := func() {}
	if config.WaitTimeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, config.WaitTimeout)
	}
	defer cancel()

	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()
	for {
		statuses, err := r.statuses(waitCtx, config.BaseURL, jobIDs)
		if err != nil {
			summary.Elapsed = time.Since(startedAt)
			return summary, err
		}
		summary.Statuses = statuses
		if summary.TerminalJobs() == summary.Submitted {
			summary.Elapsed = time.Since(startedAt)
			return summary, nil
		}

		select {
		case <-waitCtx.Done():
			summary.Elapsed = time.Since(startedAt)
			return summary, fmt.Errorf(
				"wait for jobs: %w (%d of %d terminal)",
				waitCtx.Err(),
				summary.TerminalJobs(),
				summary.Submitted,
			)
		case <-ticker.C:
		}
	}
}

func dispatch(ctx context.Context, config Config, work chan<- int) error {
	var ticker *time.Ticker
	if config.Rate > 0 {
		ticker = time.NewTicker(time.Duration(float64(time.Second) / config.Rate))
		defer ticker.Stop()
	}
	for index := 0; index < config.Jobs; index++ {
		if ticker != nil && index > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case work <- index:
		}
	}
	return nil
}

func setLatencyPercentiles(summary *Summary, latencies []time.Duration) {
	if len(latencies) == 0 {
		return
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	summary.SubmissionLatencyP50 = percentile(latencies, 0.50)
	summary.SubmissionLatencyP95 = percentile(latencies, 0.95)
	summary.SubmissionLatencyP99 = percentile(latencies, 0.99)
}

func percentile(values []time.Duration, quantile float64) time.Duration {
	index := int(float64(len(values)-1) * quantile)
	return values[index]
}

func retainedJobCapacity(config Config) int {
	if config.SubmitOnly {
		return 0
	}
	return config.Jobs
}

func (s Summary) SubmissionThroughput() float64 {
	if s.SubmissionElapsed <= 0 {
		return 0
	}
	return float64(s.Submitted) / s.SubmissionElapsed.Seconds()
}

func (s Summary) CompletionThroughput() float64 {
	if s.Elapsed <= 0 {
		return 0
	}
	return float64(s.TerminalJobs()) / s.Elapsed.Seconds()
}

type submitRequest struct {
	ID                  string   `json:"id"`
	Priority            int64    `json:"priority"`
	CPUs                int64    `json:"cpus"`
	MemoryGB            int64    `json:"memory_gb"`
	DurationSeconds     int64    `json:"duration_seconds"`
	Image               string   `json:"image"`
	Command             string   `json:"command"`
	Args                []string `json:"args"`
	MaxAttempts         int64    `json:"max_attempts"`
	RetryBackoffSeconds int64    `json:"retry_backoff_seconds"`
}

type jobResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (r *Runner) submit(ctx context.Context, baseURL string, spec submitRequest) error {
	body, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/jobs", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return fmt.Errorf("submit %q: %w", spec.ID, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("submit %q: HTTP %d: %s", spec.ID, response.StatusCode, strings.TrimSpace(string(message)))
	}
	return nil
}

func (r *Runner) statuses(ctx context.Context, baseURL string, wanted map[string]struct{}) (map[string]int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/jobs", nil)
	if err != nil {
		return nil, err
	}
	response, err := r.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return nil, fmt.Errorf("list jobs: HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}

	var jobs []jobResponse
	if err := json.NewDecoder(response.Body).Decode(&jobs); err != nil {
		return nil, fmt.Errorf("decode jobs: %w", err)
	}
	statuses := make(map[string]int)
	for _, job := range jobs {
		if _, ok := wanted[job.ID]; ok {
			statuses[job.Status]++
		}
	}
	return statuses, nil
}

func randomJob(random *rand.Rand, config Config, id string) submitRequest {
	minSeconds := int(config.MinRuntime / time.Second)
	maxSeconds := int(config.MaxRuntime / time.Second)
	runtimeSeconds := minSeconds
	if spread := maxSeconds - minSeconds; spread > 0 {
		runtimeSeconds += random.Intn(spread + 1)
	}

	spec := submitRequest{
		ID:                  id,
		Priority:            int64(random.Intn(10)),
		CPUs:                config.CPUs,
		MemoryGB:            config.MemoryGB,
		DurationSeconds:     int64((time.Duration(runtimeSeconds)*time.Second + config.TimeoutHeadroom) / time.Second),
		Image:               "alpine:3.20",
		Command:             "sh",
		MaxAttempts:         config.MaxAttempts,
		RetryBackoffSeconds: config.RetryBackoffSeconds,
	}

	roll := random.Float64()
	switch {
	case roll < config.TimeoutFraction:
		timeoutSeconds := runtimeSeconds / 2
		if timeoutSeconds < 1 {
			timeoutSeconds = 1
		}
		if runtimeSeconds <= timeoutSeconds {
			runtimeSeconds = timeoutSeconds + 1
		}
		spec.DurationSeconds = int64(timeoutSeconds)
		spec.Args = []string{"-c", fmt.Sprintf("echo %s started; sleep %d; echo should-not-finish", id, runtimeSeconds)}
	case roll < config.TimeoutFraction+config.FailureFraction:
		spec.Args = []string{"-c", fmt.Sprintf("echo %s started; sleep %d; echo intentional-failure >&2; exit 1", id, runtimeSeconds)}
	default:
		spec.Args = []string{"-c", fmt.Sprintf("echo %s started; sleep %d; echo %s finished", id, runtimeSeconds, id)}
	}
	return spec
}

func withDefaults(config Config) Config {
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	if config.BaseURL == "" {
		config.BaseURL = "http://localhost:8080"
	}
	if config.Jobs == 0 {
		config.Jobs = 100
	}
	if config.Concurrency == 0 {
		config.Concurrency = 1
	}
	if config.Seed == 0 {
		config.Seed = 1
	}
	if config.MinRuntime == 0 {
		config.MinRuntime = time.Second
	}
	if config.MaxRuntime == 0 {
		config.MaxRuntime = 5 * time.Second
	}
	if config.TimeoutHeadroom == 0 {
		config.TimeoutHeadroom = 5 * time.Second
	}
	if config.CPUs == 0 {
		config.CPUs = 1
	}
	if config.MemoryGB == 0 {
		config.MemoryGB = 1
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = 2
	}
	if config.RetryBackoffSeconds == 0 {
		config.RetryBackoffSeconds = 1
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Second
	}
	if config.WaitTimeout == 0 {
		config.WaitTimeout = 5 * time.Minute
	}
	return config
}

func validateConfig(config Config) error {
	parsed, err := url.ParseRequestURI(config.BaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("base URL must be an absolute HTTP URL")
	}
	if config.Jobs < 1 {
		return fmt.Errorf("jobs must be greater than zero")
	}
	if config.Rate < 0 {
		return fmt.Errorf("rate cannot be negative")
	}
	if config.Concurrency < 1 {
		return fmt.Errorf("concurrency must be greater than zero")
	}
	if config.MinRuntime < time.Second || config.MaxRuntime < config.MinRuntime {
		return fmt.Errorf("runtime range must be at least one second and ordered")
	}
	if config.TimeoutHeadroom < time.Second {
		return fmt.Errorf("timeout headroom must be at least one second")
	}
	if config.TimeoutFraction < 0 || config.FailureFraction < 0 ||
		config.TimeoutFraction+config.FailureFraction > 1 {
		return fmt.Errorf("timeout and failure fractions must be non-negative and total at most 1")
	}
	if config.CPUs < 1 || config.MemoryGB < 1 || config.MaxAttempts < 1 {
		return fmt.Errorf("CPU, memory, and max attempts must be positive")
	}
	return nil
}
