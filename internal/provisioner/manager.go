package provisioner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	postgresdb "mini-modal/internal/store/postgres/sqlc"

	"github.com/google/uuid"
)

const (
	stopTimeout           = 2 * time.Second
	maxNewProcessesPerRun = 2
	maxLocalProcesses     = 32
)

type Drainer interface {
	SetNodeDrain(context.Context, string, bool) error
	UnregisterNode(context.Context, string) error
}

type managedProcess struct {
	nodeID   string
	poolID   string
	cmd      *exec.Cmd
	done     chan struct{}
	running  bool
	draining bool
}

type Manager struct {
	operationMu         sync.Mutex
	mu                  sync.Mutex
	parent              context.Context
	agentBinary         string
	controlPlaneAddress string
	queries             *postgresdb.Queries
	drainer             Drainer
	stdout              io.Writer
	stderr              io.Writer
	processes           map[string]*managedProcess
}

func New(parent context.Context, agentBinary, controlPlaneAddress string, queries *postgresdb.Queries, drainer Drainer) *Manager {
	if absolutePath, err := filepath.Abs(agentBinary); err == nil {
		agentBinary = absolutePath
	}
	return &Manager{
		parent:              parent,
		agentBinary:         agentBinary,
		controlPlaneAddress: controlPlaneAddress,
		queries:             queries,
		drainer:             drainer,
		stdout:              os.Stdout,
		stderr:              os.Stderr,
		processes:           make(map[string]*managedProcess),
	}
}

func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer m.Stop()
	for {
		if err := m.Reconcile(ctx); err != nil && ctx.Err() == nil {
			log.Printf("provisioner reconciliation failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Manager) Reconcile(ctx context.Context) error {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()

	pools, err := m.queries.ListAutoscalingNodePools(ctx)
	if err != nil {
		return fmt.Errorf("list node pools: %w", err)
	}
	activeJobs, err := m.queries.ListActiveJobs(ctx)
	if err != nil {
		return fmt.Errorf("list active jobs: %w", err)
	}
	busy := make(map[string]bool, len(activeJobs))
	for _, job := range activeJobs {
		busy[job.NodeID] = true
	}

	poolByID := make(map[string]postgresdb.NodePool, len(pools))
	for _, pool := range pools {
		poolByID[pool.ID] = pool
	}
	m.mu.Lock()
	for id, process := range m.processes {
		if !process.running {
			delete(m.processes, id)
		}
	}
	byPool := make(map[string][]*managedProcess)
	for _, process := range m.processes {
		if !process.draining {
			byPool[process.poolID] = append(byPool[process.poolID], process)
		}
	}
	m.mu.Unlock()

	for poolID, processes := range byPool {
		sort.Slice(processes, func(i, j int) bool { return processes[i].nodeID < processes[j].nodeID })
		desired := int64(0)
		if pool, ok := poolByID[poolID]; ok {
			desired = pool.DesiredNodes
		}
		excess := int64(len(processes)) - desired
		for _, process := range processes {
			if excess <= 0 {
				break
			}
			if busy[process.nodeID] {
				continue
			}
			if err := m.drainer.SetNodeDrain(ctx, process.nodeID, true); err != nil {
				if !errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("drain node %q: %w", process.nodeID, err)
				}
				_ = process.cmd.Process.Signal(os.Interrupt)
			}
			m.mu.Lock()
			process.draining = true
			m.mu.Unlock()
			excess--
		}
	}

	newProcesses := 0
	for _, pool := range pools {
		running := int64(0)
		m.mu.Lock()
		for _, process := range m.processes {
			if process.poolID == pool.ID && process.running && !process.draining {
				running++
			}
		}
		m.mu.Unlock()
		for running < pool.DesiredNodes && newProcesses < maxNewProcessesPerRun {
			m.mu.Lock()
			atLimit := len(m.processes) >= maxLocalProcesses
			m.mu.Unlock()
			if atLimit {
				return fmt.Errorf("local agent process limit of %d reached", maxLocalProcesses)
			}
			if err := m.startProcess(pool); err != nil {
				return err
			}
			running++
			newProcesses++
		}
	}
	return nil
}

func (m *Manager) startProcess(pool postgresdb.NodePool) error {
	if err := validateBinary(m.agentBinary); err != nil {
		return err
	}
	nodeID := "node-" + uuid.NewString()
	cmd := exec.CommandContext(m.parent, m.agentBinary,
		"-node-id", nodeID,
		"-pool-id", pool.ID,
		"-control-plane-address", m.controlPlaneAddress,
		"-gpu-type", pool.GpuType,
		"-gpus", strconv.FormatInt(pool.GpusPerNode, 10),
		"-cpus", strconv.FormatInt(pool.CpusPerNode, 10),
		"-memory-gb", strconv.FormatInt(pool.MemoryGbPerNode, 10),
		"-executor", "docker",
	)
	cmd.Stdout = m.stdout
	cmd.Stderr = m.stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start agent for pool %q: %w", pool.ID, err)
	}
	process := &managedProcess{
		nodeID: nodeID, poolID: pool.ID, cmd: cmd, done: make(chan struct{}), running: true,
	}
	m.mu.Lock()
	m.processes[nodeID] = process
	m.mu.Unlock()
	go m.wait(process)
	return nil
}

func (m *Manager) Stop() {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	m.mu.Lock()
	processes := m.processes
	m.processes = make(map[string]*managedProcess)
	m.mu.Unlock()
	for _, process := range processes {
		_ = process.cmd.Process.Signal(os.Interrupt)
	}
	timer := time.NewTimer(stopTimeout)
	defer timer.Stop()
	for _, process := range processes {
		select {
		case <-process.done:
		case <-timer.C:
			for _, remaining := range processes {
				_ = remaining.cmd.Process.Kill()
			}
			return
		}
	}
}

func (m *Manager) wait(process *managedProcess) {
	err := process.cmd.Wait()
	m.mu.Lock()
	process.running = false
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	if drainErr := m.drainer.SetNodeDrain(ctx, process.nodeID, true); drainErr != nil && !errors.Is(drainErr, sql.ErrNoRows) {
		log.Printf("mark exited node %q draining: %v", process.nodeID, drainErr)
	}
	if removeErr := m.drainer.UnregisterNode(ctx, process.nodeID); removeErr != nil {
		log.Printf("unregister exited node %q: %v", process.nodeID, removeErr)
	}
	if err != nil && m.parent.Err() == nil {
		log.Printf("agent %q in pool %q exited: %v", process.nodeID, process.poolID, err)
	}
	close(process.done)
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
