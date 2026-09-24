package controlplane

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"mini-modal/internal/domain"
)

const nodeHeartbeatTimeout = 5 * time.Second
const assignmentLeaseDuration = 15 * time.Second

type assignmentLease struct {
	assignment domain.Assignment
	expiresAt  time.Time
}

// RuntimeState contains only process-local timing information. Jobs and node
// configuration live in SQLite.
type RuntimeState struct {
	mu           sync.Mutex
	nodeLastSeen map[string]time.Time
	leases       map[string]assignmentLease
}

func NewRuntimeState() *RuntimeState {
	return &RuntimeState{
		nodeLastSeen: make(map[string]time.Time),
		leases:       make(map[string]assignmentLease),
	}
}

func (s *RuntimeState) RegisterNode(nodeID string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodeLastSeen[nodeID] = now
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

func (s *RuntimeState) SetLease(assignment domain.Assignment, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.leases[assignment.JobID] = assignmentLease{
		assignment: assignment,
		expiresAt:  now.Add(assignmentLeaseDuration),
	}
}

func (s *RuntimeState) RenewLease(assignment domain.Assignment, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	lease, exists := s.leases[assignment.JobID]
	if !exists || lease.assignment != assignment {
		return fmt.Errorf("assignment for job %q is no longer active", assignment.JobID)
	}
	lease.expiresAt = now.Add(assignmentLeaseDuration)
	s.leases[assignment.JobID] = lease
	return nil
}

func (s *RuntimeState) ClearLease(assignment domain.Assignment) {
	s.mu.Lock()
	defer s.mu.Unlock()

	lease, exists := s.leases[assignment.JobID]
	if exists && lease.assignment == assignment {
		delete(s.leases, assignment.JobID)
	}
}

func (s *RuntimeState) ExpiredAssignments(now time.Time) []domain.Assignment {
	s.mu.Lock()
	defer s.mu.Unlock()

	expired := make([]domain.Assignment, 0)
	for _, lease := range s.leases {
		if !lease.expiresAt.After(now) {
			expired = append(expired, lease.assignment)
		}
	}
	sort.Slice(expired, func(i, j int) bool {
		return expired[i].JobID < expired[j].JobID
	})
	return expired
}
