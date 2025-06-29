package queue

import "time"

type QueueStatus string

const (
	StatusPending    QueueStatus = "pending"
	StatusGenerating QueueStatus = "generating"
	StatusError      QueueStatus = "error"
	StatusAvailable  QueueStatus = "available"
	StatusShutdown   QueueStatus = "shutdown"
	StatusTerminated QueueStatus = "terminated"
)

type QueueItem struct {
	Owner           string      `json:"owner"`
	K8sVersion      string      `json:"k8s_version"`
	Status          QueueStatus `json:"status"`
	ErrorMessage    string      `json:"error_message,omitempty"`
	StatusUpdatedAt time.Time   `json:"status_updated_at"`
	PodID           string      `json:"pod_id,omitempty"` // This will hold the StatefulSet or Deployment name
	ExpiresAt       time.Time   `json:"expires_at"`
	ID              string      `json:"id"`
	DisplayName     string      `json:"display_name,omitempty"`
	// ★ ワークロードのタイプ ("statefulset" or "deployment") を追加
	WorkloadType string `json:"workload_type,omitempty"`
}

func (q *QueueItem) IsExpired() bool {
	return time.Now().After(q.ExpiresAt)
}

func (q *QueueItem) ShouldBeCollected() bool {
	terminalStates := []QueueStatus{StatusShutdown, StatusTerminated, StatusError}
	for _, state := range terminalStates {
		if q.Status == state {
			return false // Already in a terminal state or being processed for shutdown
		}
	}
	return q.IsExpired()
}
