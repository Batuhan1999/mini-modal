package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"mini-modal/internal/simulator"
)

func main() {
	config := simulator.Config{}
	flag.StringVar(&config.Workload, "workload", "sleep", "function workload: sleep or hello")
	flag.StringVar(&config.BaseURL, "base-url", "http://localhost:8080", "control-plane HTTP base URL")
	flag.IntVar(&config.Jobs, "jobs", 100, "number of jobs to submit")
	flag.Float64Var(&config.Rate, "rate", 10, "submissions per second; 0 submits a burst")
	flag.IntVar(&config.Concurrency, "concurrency", 1, "number of concurrent HTTP submitters")
	flag.BoolVar(&config.SubmitOnly, "submit-only", false, "return after admission without polling job completion")
	flag.Int64Var(&config.Seed, "seed", 1, "deterministic workload seed")
	flag.DurationVar(&config.MinRuntime, "min-runtime", time.Second, "minimum simulated job runtime")
	flag.DurationVar(&config.MaxRuntime, "max-runtime", 5*time.Second, "maximum simulated job runtime")
	flag.DurationVar(&config.TimeoutHeadroom, "timeout-headroom", 5*time.Second, "extra execution time allowed for non-timeout jobs")
	flag.Float64Var(&config.TimeoutFraction, "timeout-fraction", 0.1, "fraction of jobs designed to time out")
	flag.Float64Var(&config.FailureFraction, "failure-fraction", 0.1, "fraction of jobs designed to fail")
	flag.Int64Var(&config.CPUs, "cpus", 1, "CPUs requested per job")
	flag.Int64Var(&config.MemoryGB, "memory-gb", 1, "memory requested per job")
	flag.Int64Var(&config.MaxAttempts, "max-attempts", 2, "maximum attempts per job")
	flag.Int64Var(&config.RetryBackoffSeconds, "retry-backoff-seconds", 1, "retry delay in seconds")
	flag.DurationVar(&config.PollInterval, "poll-interval", time.Second, "interval for observing job status")
	flag.DurationVar(&config.WaitTimeout, "wait-timeout", 5*time.Minute, "maximum time to wait for terminal jobs")
	outputPath := flag.String("output", "", "optional path for the JSON summary")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("Submitting %d jobs to %s (rate %.1f/s, seed %d)\n", config.Jobs, config.BaseURL, config.Rate, config.Seed)
	summary, err := simulator.NewRunner(nil).Run(ctx, config)
	printSummary(summary)
	if strings.TrimSpace(*outputPath) != "" {
		if writeErr := writeSummary(*outputPath, summary); writeErr != nil {
			log.Printf("write summary: %v", writeErr)
		}
	}
	if err != nil {
		log.Fatal(err)
	}
}

func printSummary(summary simulator.Summary) {
	fmt.Printf(
		"Submitted: %d, submission errors: %d, submission time: %s (%.1f jobs/s)\n",
		summary.Submitted,
		summary.SubmissionErrors,
		summary.SubmissionElapsed.Round(time.Millisecond),
		summary.SubmissionThroughput(),
	)
	fmt.Printf(
		"Submission latency: p50=%s p95=%s p99=%s\n",
		summary.SubmissionLatencyP50.Round(time.Microsecond),
		summary.SubmissionLatencyP95.Round(time.Microsecond),
		summary.SubmissionLatencyP99.Round(time.Microsecond),
	)
	fmt.Printf(
		"Terminal: %d, end-to-end time: %s (%.1f jobs/s)\n",
		summary.TerminalJobs(),
		summary.Elapsed.Round(time.Millisecond),
		summary.CompletionThroughput(),
	)
	statuses := make([]string, 0, len(summary.Statuses))
	for status := range summary.Statuses {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	for _, status := range statuses {
		fmt.Printf("  %-14s %d\n", status, summary.Statuses[status])
	}
}

func writeSummary(path string, summary simulator.Summary) error {
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
