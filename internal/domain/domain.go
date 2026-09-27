package domain

import (
	agentv1 "mini-modal/gen/agent/v1"
	postgresdb "mini-modal/internal/store/postgres/sqlc"
	"time"
)

type Node struct {
	ID                string
	GPUType           string
	TotalGPUs         int64
	AvailableGPUs     int64
	TotalCPUs         int64
	AvailableCPUs     int64
	TotalMemoryGB     int64
	AvailableMemoryGB int64
}

type LogStream struct {
	JobID     string
	Attempt   int64
	Sequence  int64
	Source    agentv1.LogSource
	Data      []byte
	CreatedAt time.Time
}

type Assignment struct {
	JobID           string
	Image           string
	NodeID          string
	Attempt         int64
	Command         string
	Args            []string
	CPUs            int64
	MemoryGB        int64
	DurationSeconds int64
}

type FailureReason string

const (
	FailureExecutionError FailureReason = "execution_error"
	FailureTimeout        FailureReason = "timeout"
)

func (n Node) CanRun(job postgresdb.Job) bool {
	return (job.GpuType == "" || job.GpuType == n.GPUType) &&
		job.Gpus <= n.AvailableGPUs &&
		job.Cpus <= n.AvailableCPUs &&
		job.MemoryGb <= n.AvailableMemoryGB
}

func (n Node) CanPossiblyRun(job postgresdb.Job) bool {
	return (job.GpuType == "" || job.GpuType == n.GPUType) &&
		job.Gpus <= n.TotalGPUs &&
		job.Cpus <= n.TotalCPUs &&
		job.MemoryGb <= n.TotalMemoryGB
}

func (n *Node) Reserve(job postgresdb.Job) {
	n.AvailableGPUs -= job.Gpus
	n.AvailableCPUs -= job.Cpus
	n.AvailableMemoryGB -= job.MemoryGb
}

func (n *Node) Release(job postgresdb.Job) {
	n.AvailableGPUs += job.Gpus
	n.AvailableCPUs += job.Cpus
	n.AvailableMemoryGB += job.MemoryGb
}
