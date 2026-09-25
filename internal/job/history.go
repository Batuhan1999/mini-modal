package job

type HistoryEvent string

const (
	HistorySubmitted             HistoryEvent = "submitted"
	HistoryAssigned              HistoryEvent = "assigned"
	HistoryClaimed               HistoryEvent = "claimed"
	HistoryCompleted             HistoryEvent = "completed"
	HistoryFailed                HistoryEvent = "failed"
	HistoryRetryScheduled        HistoryEvent = "retry_scheduled"
	HistoryCancellationRequested HistoryEvent = "cancellation_requested"
	HistoryCancelled             HistoryEvent = "cancelled"
	HistoryLeaseExpired          HistoryEvent = "lease_expired"
)
