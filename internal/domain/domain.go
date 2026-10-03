package domain

import (
	agentv1 "mini-modal/gen/agent/v1"
	"time"
)

type Node struct {
	ID                string
	PoolID            string
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
	JobID          string
	Image          string
	NodeID         string
	Attempt        int64
	Entrypoint     string
	InputJSON      []byte
	CPUs           int64
	MemoryGB       int64
	TimeoutSeconds int64
	ArtifactKey    string
}

type Resources struct {
	GPUType  string
	GPUs     int64
	CPUs     int64
	MemoryGB int64
}

type FailureReason string

const (
	FailureExecutionError FailureReason = "execution_error"
	FailureTimeout        FailureReason = "timeout"
)

func (n Node) CanRun(job Resources) bool {
	return (job.GPUType == "" || job.GPUType == n.GPUType) &&
		job.GPUs <= n.AvailableGPUs &&
		job.CPUs <= n.AvailableCPUs &&
		job.MemoryGB <= n.AvailableMemoryGB
}

func (n Node) CanPossiblyRun(job Resources) bool {
	return (job.GPUType == "" || job.GPUType == n.GPUType) &&
		job.GPUs <= n.TotalGPUs &&
		job.CPUs <= n.TotalCPUs &&
		job.MemoryGB <= n.TotalMemoryGB
}

func (n *Node) Reserve(job Resources) {
	n.AvailableGPUs -= job.GPUs
	n.AvailableCPUs -= job.CPUs
	n.AvailableMemoryGB -= job.MemoryGB
}

func (n *Node) Release(job Resources) {
	n.AvailableGPUs += job.GPUs
	n.AvailableCPUs += job.CPUs
	n.AvailableMemoryGB += job.MemoryGB
}
