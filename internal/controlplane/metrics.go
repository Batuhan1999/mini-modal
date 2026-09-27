package controlplane

import (
	"strconv"
	"sync"
	"time"

	"mini-modal/internal/domain"
	jobmodel "mini-modal/internal/job"
	postgresdb "mini-modal/internal/store/postgres/sqlc"

	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	jobsSubmitted     prometheus.Counter
	attemptsStarted   prometheus.Counter
	attemptsFinished  *prometheus.CounterVec
	jobsFinished      *prometheus.CounterVec
	retriesScheduled  *prometheus.CounterVec
	cancellations     prometheus.Counter
	leaseExpirations  prometheus.Counter
	logBytesReceived  *prometheus.CounterVec
	schedulerRuns     *prometheus.CounterVec
	schedulerDuration prometheus.Histogram
	queueWait         prometheus.Histogram
	attemptDuration   *prometheus.HistogramVec
	jobDuration       *prometheus.HistogramVec
	jobsCurrent       *prometheus.GaugeVec
	nodesCurrent      *prometheus.GaugeVec
	resourcesTotal    *prometheus.GaugeVec
	resourcesFree     *prometheus.GaugeVec

	mu             sync.Mutex
	submittedAt    map[string]time.Time
	queuedAt       map[string]time.Time
	attemptStarted map[string]time.Time
}

func NewMetrics(registerer prometheus.Registerer) *Metrics {
	metrics := &Metrics{
		jobsSubmitted: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "mini_modal",
			Name:      "jobs_submitted_total",
			Help:      "Total number of jobs successfully submitted.",
		}),
		attemptsStarted: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "mini_modal",
			Name:      "job_attempts_started_total",
			Help:      "Total number of job attempts claimed by agents.",
		}),
		attemptsFinished: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "mini_modal",
			Name:      "job_attempts_finished_total",
			Help:      "Total number of finished job attempts by result.",
		}, []string{"result"}),
		jobsFinished: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "mini_modal",
			Name:      "jobs_finished_total",
			Help:      "Total number of jobs entering a terminal state.",
		}, []string{"result"}),
		retriesScheduled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "mini_modal",
			Name:      "job_retries_scheduled_total",
			Help:      "Total number of retries scheduled by reason.",
		}, []string{"reason"}),
		cancellations: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "mini_modal",
			Name:      "job_cancellations_total",
			Help:      "Total number of jobs successfully cancelled.",
		}),
		leaseExpirations: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "mini_modal",
			Name:      "assignment_lease_expirations_total",
			Help:      "Total number of expired assignment leases.",
		}),
		logBytesReceived: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "mini_modal",
			Name:      "log_bytes_received_total",
			Help:      "Total number of log bytes stored by source.",
		}, []string{"source"}),
		schedulerRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "mini_modal",
			Name:      "scheduler_runs_total",
			Help:      "Total number of scheduler runs by result.",
		}, []string{"result"}),
		schedulerDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "mini_modal",
			Name:      "scheduler_duration_seconds",
			Help:      "Duration of scheduler runs.",
			Buckets:   prometheus.ExponentialBuckets(0.0001, 2, 15),
		}),
		queueWait: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "mini_modal",
			Name:      "job_queue_wait_seconds",
			Help:      "Time from entering the pending queue until an agent claims the attempt.",
			Buckets:   prometheus.ExponentialBuckets(0.01, 2, 16),
		}),
		attemptDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "mini_modal",
			Name:      "job_attempt_duration_seconds",
			Help:      "Duration of job attempts by result.",
			Buckets:   prometheus.ExponentialBuckets(0.05, 2, 16),
		}, []string{"result"}),
		jobDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "mini_modal",
			Name:      "job_duration_seconds",
			Help:      "End-to-end duration of terminal jobs by result.",
			Buckets:   prometheus.ExponentialBuckets(0.05, 2, 18),
		}, []string{"result"}),
		jobsCurrent: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "mini_modal",
			Name:      "jobs_current",
			Help:      "Current number of jobs by status.",
		}, []string{"status"}),
		nodesCurrent: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "mini_modal",
			Name:      "nodes_current",
			Help:      "Current number of registered nodes by health.",
		}, []string{"health"}),
		resourcesTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "mini_modal",
			Name:      "node_resources_total",
			Help:      "Total registered node capacity by resource.",
		}, []string{"resource"}),
		resourcesFree: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "mini_modal",
			Name:      "node_resources_free",
			Help:      "Currently free capacity on healthy nodes by resource.",
		}, []string{"resource"}),
		submittedAt:    make(map[string]time.Time),
		queuedAt:       make(map[string]time.Time),
		attemptStarted: make(map[string]time.Time),
	}

	registerer.MustRegister(
		metrics.jobsSubmitted,
		metrics.attemptsStarted,
		metrics.attemptsFinished,
		metrics.jobsFinished,
		metrics.retriesScheduled,
		metrics.cancellations,
		metrics.leaseExpirations,
		metrics.logBytesReceived,
		metrics.schedulerRuns,
		metrics.schedulerDuration,
		metrics.queueWait,
		metrics.attemptDuration,
		metrics.jobDuration,
		metrics.jobsCurrent,
		metrics.nodesCurrent,
		metrics.resourcesTotal,
		metrics.resourcesFree,
	)
	return metrics
}

func (m *Metrics) RecordJobSubmitted(jobID string, now time.Time) {
	m.jobsSubmitted.Inc()
	m.mu.Lock()
	m.submittedAt[jobID] = now
	m.queuedAt[jobID] = now
	m.mu.Unlock()
}

func (m *Metrics) RecordAttemptStarted(jobID string, attempt int64, now time.Time) {
	m.attemptsStarted.Inc()
	m.mu.Lock()
	if queuedAt, ok := m.queuedAt[jobID]; ok {
		m.queueWait.Observe(now.Sub(queuedAt).Seconds())
		delete(m.queuedAt, jobID)
	}
	m.attemptStarted[attemptKey(jobID, attempt)] = now
	m.mu.Unlock()
}

func (m *Metrics) RecordAttemptFinished(jobID string, attempt int64, result string, now time.Time) {
	m.attemptsFinished.WithLabelValues(result).Inc()
	m.mu.Lock()
	key := attemptKey(jobID, attempt)
	if startedAt, ok := m.attemptStarted[key]; ok {
		m.attemptDuration.WithLabelValues(result).Observe(now.Sub(startedAt).Seconds())
		delete(m.attemptStarted, key)
	}
	m.mu.Unlock()
}

func (m *Metrics) RecordRetryScheduled(reason string) {
	m.retriesScheduled.WithLabelValues(reason).Inc()
}

func (m *Metrics) RecordRetryReady(jobID string, now time.Time) {
	m.mu.Lock()
	m.queuedAt[jobID] = now
	m.mu.Unlock()
}

func (m *Metrics) RecordJobFinished(jobID, result string, now time.Time) {
	m.jobsFinished.WithLabelValues(result).Inc()
	if result == string(jobmodel.Cancelled) {
		m.cancellations.Inc()
	}
	m.mu.Lock()
	if submittedAt, ok := m.submittedAt[jobID]; ok {
		m.jobDuration.WithLabelValues(result).Observe(now.Sub(submittedAt).Seconds())
	}
	delete(m.submittedAt, jobID)
	delete(m.queuedAt, jobID)
	m.mu.Unlock()
}

func (m *Metrics) RecordLeaseExpiration() {
	m.leaseExpirations.Inc()
}

func (m *Metrics) RecordLogBytes(source string, size int) {
	m.logBytesReceived.WithLabelValues(source).Add(float64(size))
}

func (m *Metrics) RecordSchedulerRun(startedAt time.Time, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	m.schedulerRuns.WithLabelValues(result).Inc()
	m.schedulerDuration.Observe(time.Since(startedAt).Seconds())
}

func (m *Metrics) Refresh(jobs []postgresdb.Job, nodes []domain.Node, healthy func(string) bool) {
	statuses := []jobmodel.Status{
		jobmodel.Pending,
		jobmodel.Assigned,
		jobmodel.Running,
		jobmodel.Retrying,
		jobmodel.Cancelling,
		jobmodel.Cancelled,
		jobmodel.Succeeded,
		jobmodel.Failed,
		jobmodel.Unschedulable,
	}
	counts := make(map[string]float64, len(statuses))
	for _, job := range jobs {
		counts[job.Status]++
	}
	for _, status := range statuses {
		m.jobsCurrent.WithLabelValues(string(status)).Set(counts[string(status)])
	}

	var healthyNodes, unhealthyNodes float64
	var totalCPU, totalMemory, totalGPU float64
	var freeCPU, freeMemory, freeGPU float64
	for _, node := range nodes {
		totalCPU += float64(node.TotalCPUs)
		totalMemory += float64(node.TotalMemoryGB)
		totalGPU += float64(node.TotalGPUs)
		if healthy(node.ID) {
			healthyNodes++
			freeCPU += float64(node.AvailableCPUs)
			freeMemory += float64(node.AvailableMemoryGB)
			freeGPU += float64(node.AvailableGPUs)
		} else {
			unhealthyNodes++
		}
	}
	m.nodesCurrent.WithLabelValues("healthy").Set(healthyNodes)
	m.nodesCurrent.WithLabelValues("unhealthy").Set(unhealthyNodes)
	m.resourcesTotal.WithLabelValues("cpu").Set(totalCPU)
	m.resourcesTotal.WithLabelValues("memory_gb").Set(totalMemory)
	m.resourcesTotal.WithLabelValues("gpu").Set(totalGPU)
	m.resourcesFree.WithLabelValues("cpu").Set(freeCPU)
	m.resourcesFree.WithLabelValues("memory_gb").Set(freeMemory)
	m.resourcesFree.WithLabelValues("gpu").Set(freeGPU)
}

func attemptKey(jobID string, attempt int64) string {
	return jobID + "/" + strconv.FormatInt(attempt, 10)
}
