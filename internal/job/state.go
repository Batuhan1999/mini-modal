package job

import "fmt"

type Status string

const (
	Pending       Status = "pending"
	Assigned      Status = "assigned"
	Running       Status = "running"
	Retrying      Status = "retrying"
	Cancelling    Status = "cancelling"
	Cancelled     Status = "cancelled"
	Succeeded     Status = "succeeded"
	Failed        Status = "failed"
	Unschedulable Status = "unschedulable"
)

type Event string

const (
	EventSchedule                Event = "schedule"
	EventMarkUnschedulable       Event = "mark_unschedulable"
	EventCapacityAvailable       Event = "capacity_available"
	EventClaim                   Event = "claim"
	EventComplete                Event = "complete"
	EventFail                    Event = "fail"
	EventScheduleRetry           Event = "schedule_retry"
	EventRetryReady              Event = "retry_ready"
	EventRequestCancellation     Event = "request_cancellation"
	EventAcknowledgeCancellation Event = "acknowledge_cancellation"
	EventLeaseExpired            Event = "lease_expired"
)

var transitions = map[Status]map[Event]Status{
	Pending: {
		EventSchedule:            Assigned,
		EventMarkUnschedulable:   Unschedulable,
		EventRequestCancellation: Cancelled,
	},
	Assigned: {
		EventClaim:               Running,
		EventFail:                Failed,
		EventScheduleRetry:       Retrying,
		EventRequestCancellation: Cancelled,
		EventLeaseExpired:        Retrying,
	},
	Running: {
		EventComplete:            Succeeded,
		EventFail:                Failed,
		EventScheduleRetry:       Retrying,
		EventRequestCancellation: Cancelling,
		EventLeaseExpired:        Retrying,
	},
	Retrying: {
		EventRetryReady:          Pending,
		EventRequestCancellation: Cancelled,
	},
	Cancelling: {
		EventRequestCancellation:     Cancelling,
		EventAcknowledgeCancellation: Cancelled,
		EventLeaseExpired:            Cancelled,
	},
	Cancelled: {
		EventRequestCancellation: Cancelled,
	},
	Unschedulable: {
		EventCapacityAvailable:   Pending,
		EventRequestCancellation: Cancelled,
	},
}

func ParseStatus(value string) (Status, error) {
	status := Status(value)
	if !status.Valid() {
		return "", fmt.Errorf("unknown job status %q", value)
	}
	return status, nil
}

func (s Status) Next(event Event) (Status, error) {
	events, exists := transitions[s]
	if !exists {
		return "", fmt.Errorf("unknown job status %q", s)
	}
	next, exists := events[event]
	if !exists {
		return "", fmt.Errorf("cannot apply job event %q while status is %q", event, s)
	}
	return next, nil
}

func (s Status) Valid() bool {
	_, exists := transitions[s]
	if exists {
		return true
	}
	return s == Succeeded || s == Failed
}

func (s Status) Terminal() bool {
	return s == Cancelled || s == Succeeded || s == Failed
}

func (s Status) HoldsResources() bool {
	return s == Assigned || s == Running || s == Cancelling
}

func (s Status) AcceptsLogs() bool {
	return s == Running || s == Cancelling || s == Cancelled
}

func (s Status) ShouldStopExecution() bool {
	return s == Cancelling || s == Cancelled
}
