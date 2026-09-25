package controlplane

import "fmt"

type JobStatus string

const (
	JobPending       JobStatus = "pending"
	JobAssigned      JobStatus = "assigned"
	JobRunning       JobStatus = "running"
	JobCancelling    JobStatus = "cancelling"
	JobCancelled     JobStatus = "cancelled"
	JobSucceeded     JobStatus = "succeeded"
	JobFailed        JobStatus = "failed"
	JobUnschedulable JobStatus = "unschedulable"
)

type JobEvent string

const (
	JobEventSchedule                JobEvent = "schedule"
	JobEventMarkUnschedulable       JobEvent = "mark_unschedulable"
	JobEventCapacityAvailable       JobEvent = "capacity_available"
	JobEventClaim                   JobEvent = "claim"
	JobEventComplete                JobEvent = "complete"
	JobEventFail                    JobEvent = "fail"
	JobEventRequestCancellation     JobEvent = "request_cancellation"
	JobEventAcknowledgeCancellation JobEvent = "acknowledge_cancellation"
	JobEventLeaseExpired            JobEvent = "lease_expired"
)

var jobTransitions = map[JobStatus]map[JobEvent]JobStatus{
	JobPending: {
		JobEventSchedule:            JobAssigned,
		JobEventMarkUnschedulable:   JobUnschedulable,
		JobEventRequestCancellation: JobCancelled,
	},
	JobAssigned: {
		JobEventClaim:               JobRunning,
		JobEventRequestCancellation: JobCancelled,
		JobEventLeaseExpired:        JobPending,
	},
	JobRunning: {
		JobEventComplete:            JobSucceeded,
		JobEventFail:                JobFailed,
		JobEventRequestCancellation: JobCancelling,
		JobEventLeaseExpired:        JobPending,
	},
	JobCancelling: {
		JobEventRequestCancellation:     JobCancelling,
		JobEventAcknowledgeCancellation: JobCancelled,
		JobEventLeaseExpired:            JobCancelled,
	},
	JobCancelled: {
		JobEventRequestCancellation: JobCancelled,
	},
	JobUnschedulable: {
		JobEventCapacityAvailable:   JobPending,
		JobEventRequestCancellation: JobCancelled,
	},
}

func ParseJobStatus(value string) (JobStatus, error) {
	status := JobStatus(value)
	if !status.Valid() {
		return "", fmt.Errorf("unknown job status %q", value)
	}
	return status, nil
}

func NextJobStatus(current JobStatus, event JobEvent) (JobStatus, error) {
	events, exists := jobTransitions[current]
	if !exists {
		return "", fmt.Errorf("unknown job status %q", current)
	}
	next, exists := events[event]
	if !exists {
		return "", fmt.Errorf("cannot apply job event %q while status is %q", event, current)
	}
	return next, nil
}

func (s JobStatus) Valid() bool {
	_, exists := jobTransitions[s]
	if exists {
		return true
	}
	return s == JobSucceeded || s == JobFailed
}

func (s JobStatus) Terminal() bool {
	return s == JobCancelled || s == JobSucceeded || s == JobFailed
}

func (s JobStatus) HoldsResources() bool {
	return s == JobAssigned || s == JobRunning || s == JobCancelling
}

func (s JobStatus) AcceptsLogs() bool {
	return s == JobRunning || s == JobCancelling || s == JobCancelled
}

func (s JobStatus) ShouldStopExecution() bool {
	return s == JobCancelling || s == JobCancelled
}
