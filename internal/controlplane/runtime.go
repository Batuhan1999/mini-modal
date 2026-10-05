package controlplane

import (
	"fmt"
	"sync"
	"time"
)

const nodeHeartbeatTimeout = 5 * time.Second

// RuntimeState contains only process-local node health information. Jobs,
// nodes, and assignment leases live in PostgreSQL.
type RuntimeState struct {
	mu           sync.Mutex
	nodeLastSeen map[string]time.Time
}

func NewRuntimeState() *RuntimeState {
	return &RuntimeState{
		nodeLastSeen: make(map[string]time.Time),
	}
}

func (s *RuntimeState) RegisterNode(nodeID string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodeLastSeen[nodeID] = now
}

func (s *RuntimeState) ForgetNode(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.nodeLastSeen, nodeID)
}

func (s *RuntimeState) Heartbeat(nodeID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.nodeLastSeen[nodeID]; !exists {
		return fmt.Errorf("node %q has not registered", nodeID)
	}
	s.nodeLastSeen[nodeID] = now
	return nil
}

func (s *RuntimeState) NodeStatus(nodeID string, now time.Time) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	lastSeen, exists := s.nodeLastSeen[nodeID]
	return exists, exists && now.Sub(lastSeen) <= nodeHeartbeatTimeout
}

func (s *RuntimeState) NodeHealthy(nodeID string, now time.Time) bool {
	_, healthy := s.NodeStatus(nodeID, now)
	return healthy
}
