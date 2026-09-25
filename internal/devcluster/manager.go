package devcluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

const stopTimeout = 2 * time.Second

type Config struct {
	NodeCount       int   `json:"node_count"`
	CPUsPerNode     int64 `json:"cpus_per_node"`
	MemoryGBPerNode int64 `json:"memory_gb_per_node"`
}

type ProcessStatus struct {
	NodeID    string `json:"node_id"`
	PID       int    `json:"pid"`
	Running   bool   `json:"running"`
	StartedAt int64  `json:"started_at"`
	Error     string `json:"error,omitempty"`
}

type Status struct {
	Running   bool            `json:"running"`
	Config    Config          `json:"config"`
	Processes []ProcessStatus `json:"processes"`
	StartedAt int64           `json:"started_at"`
}

type managedProcess struct {
	nodeID    string
	cmd       *exec.Cmd
	done      chan struct{}
	pid       int
	running   bool
	startedAt int64
	err       string
}

type Manager struct {
	operationMu         sync.Mutex
	mu                  sync.Mutex
	parent              context.Context
	agentBinary         string
	controlPlaneAddress string
	stdout              io.Writer
	stderr              io.Writer
	config              Config
	startedAt           int64
	processes           map[string]*managedProcess
}

func New(parent context.Context, agentBinary, controlPlaneAddress string) *Manager {
	if absolutePath, err := filepath.Abs(agentBinary); err == nil {
		agentBinary = absolutePath
	}

	return &Manager{
		parent:              parent,
		agentBinary:         agentBinary,
		controlPlaneAddress: controlPlaneAddress,
		stdout:              os.Stdout,
		stderr:              os.Stderr,
		config: Config{
			NodeCount:       3,
			CPUsPerNode:     8,
			MemoryGBPerNode: 16,
		},
		processes: make(map[string]*managedProcess),
	}
}

func (m *Manager) Start(config Config) (Status, error) {
	if err := validateConfig(config); err != nil {
		return Status{}, err
	}
	if err := validateBinary(m.agentBinary); err != nil {
		return Status{}, err
	}

	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	m.stopCurrent()

	processes := make(map[string]*managedProcess, config.NodeCount)
	for index := 1; index <= config.NodeCount; index++ {
		nodeID := fmt.Sprintf("dev-node-%02d", index)
		cmd := exec.CommandContext(
			m.parent,
			m.agentBinary,
			"-node-id", nodeID,
			"-control-plane-address", m.controlPlaneAddress,
			"-gpus", "0",
			"-cpus", strconv.FormatInt(config.CPUsPerNode, 10),
			"-memory-gb", strconv.FormatInt(config.MemoryGBPerNode, 10),
		)
		cmd.Stdout = m.stdout
		cmd.Stderr = m.stderr

		process := &managedProcess{
			nodeID:    nodeID,
			cmd:       cmd,
			done:      make(chan struct{}),
			startedAt: time.Now().UnixMilli(),
		}
		if err := cmd.Start(); err != nil {
			m.stopProcesses(processes)
			return Status{}, fmt.Errorf("start agent %q: %w", nodeID, err)
		}

		process.pid = cmd.Process.Pid
		process.running = true
		processes[nodeID] = process
		go m.wait(process)
	}

	m.mu.Lock()
	m.config = config
	m.startedAt = time.Now().UnixMilli()
	m.processes = processes
	m.mu.Unlock()

	return m.Status(), nil
}

func (m *Manager) Stop() Status {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	m.stopCurrent()
	return m.Status()
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	processes := make([]ProcessStatus, 0, len(m.processes))
	running := false
	for _, process := range m.processes {
		processes = append(processes, ProcessStatus{
			NodeID:    process.nodeID,
			PID:       process.pid,
			Running:   process.running,
			StartedAt: process.startedAt,
			Error:     process.err,
		})
		if process.running {
			running = true
		}
	}
	sort.Slice(processes, func(i, j int) bool {
		return processes[i].NodeID < processes[j].NodeID
	})

	return Status{
		Running:   running,
		Config:    m.config,
		Processes: processes,
		StartedAt: m.startedAt,
	}
}

func (m *Manager) stopCurrent() {
	m.mu.Lock()
	processes := m.processes
	m.processes = make(map[string]*managedProcess)
	m.startedAt = 0
	m.mu.Unlock()

	m.stopProcesses(processes)
}

func (m *Manager) stopProcesses(processes map[string]*managedProcess) {
	for _, process := range processes {
		if process.cmd.Process != nil {
			_ = process.cmd.Process.Signal(os.Interrupt)
		}
	}

	deadline := time.NewTimer(stopTimeout)
	defer deadline.Stop()
	for _, process := range processes {
		select {
		case <-process.done:
		case <-deadline.C:
			for _, remaining := range processes {
				if remaining.cmd.Process != nil {
					_ = remaining.cmd.Process.Kill()
				}
			}
			return
		}
	}
}

func (m *Manager) wait(process *managedProcess) {
	err := process.cmd.Wait()

	m.mu.Lock()
	process.running = false
	if err != nil && !errors.Is(err, context.Canceled) {
		process.err = err.Error()
	}
	close(process.done)
	m.mu.Unlock()
}

func validateConfig(config Config) error {
	if config.NodeCount < 1 || config.NodeCount > 32 {
		return errors.New("node count must be between 1 and 32")
	}
	if config.CPUsPerNode < 1 || config.CPUsPerNode > 1024 {
		return errors.New("CPUs per node must be between 1 and 1024")
	}
	if config.MemoryGBPerNode < 1 || config.MemoryGBPerNode > 8192 {
		return errors.New("memory per node must be between 1 and 8192 GB")
	}
	return nil
}

func validateBinary(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("agent binary %q is unavailable; run go build -o ./tmp/agent ./cmd/agent: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("agent binary %q is not executable", path)
	}
	return nil
}
