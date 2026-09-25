package job

import "testing"

func TestStateTransitions(t *testing.T) {
	tests := []struct {
		current Status
		event   Event
		next    Status
	}{
		{Pending, EventSchedule, Assigned},
		{Pending, EventMarkUnschedulable, Unschedulable},
		{Pending, EventRequestCancellation, Cancelled},
		{Assigned, EventClaim, Running},
		{Assigned, EventRequestCancellation, Cancelled},
		{Assigned, EventLeaseExpired, Retrying},
		{Running, EventComplete, Succeeded},
		{Running, EventFail, Failed},
		{Running, EventScheduleRetry, Retrying},
		{Running, EventRequestCancellation, Cancelling},
		{Running, EventLeaseExpired, Retrying},
		{Retrying, EventRetryReady, Pending},
		{Retrying, EventRequestCancellation, Cancelled},
		{Cancelling, EventRequestCancellation, Cancelling},
		{Cancelling, EventAcknowledgeCancellation, Cancelled},
		{Cancelling, EventLeaseExpired, Cancelled},
		{Cancelled, EventRequestCancellation, Cancelled},
		{Unschedulable, EventCapacityAvailable, Pending},
		{Unschedulable, EventRequestCancellation, Cancelled},
	}

	for _, test := range tests {
		t.Run(string(test.current)+"_"+string(test.event), func(t *testing.T) {
			next, err := test.current.Next(test.event)
			if err != nil {
				t.Fatal(err)
			}
			if next != test.next {
				t.Fatalf("next status = %q, want %q", next, test.next)
			}
		})
	}
}

func TestStateRejectsInvalidTransitions(t *testing.T) {
	tests := []struct {
		current Status
		event   Event
	}{
		{Pending, EventComplete},
		{Assigned, EventComplete},
		{Running, EventClaim},
		{Cancelling, EventComplete},
		{Succeeded, EventRequestCancellation},
		{Failed, EventRequestCancellation},
	}

	for _, test := range tests {
		if _, err := test.current.Next(test.event); err == nil {
			t.Fatalf("transition %q + %q unexpectedly succeeded", test.current, test.event)
		}
	}
}

func TestStatusBehavior(t *testing.T) {
	if !Succeeded.Terminal() || !Failed.Terminal() || !Cancelled.Terminal() {
		t.Fatal("terminal status was not recognized")
	}
	if Running.Terminal() {
		t.Fatal("running status was considered terminal")
	}
	if Retrying.Terminal() {
		t.Fatal("retrying status was considered terminal")
	}
	if !Assigned.HoldsResources() || !Running.HoldsResources() || !Cancelling.HoldsResources() {
		t.Fatal("active status did not hold resources")
	}
	if !Running.AcceptsLogs() || !Cancelling.AcceptsLogs() || !Cancelled.AcceptsLogs() {
		t.Fatal("execution status did not accept logs")
	}
	if !Cancelling.ShouldStopExecution() || !Cancelled.ShouldStopExecution() {
		t.Fatal("cancellation status did not stop execution")
	}
}

func TestParseStatusRejectsUnknownValue(t *testing.T) {
	if _, err := ParseStatus("mystery"); err == nil {
		t.Fatal("unknown status was accepted")
	}
}
