package agent

import (
	"fmt"
	"strings"

	"github.com/p-blackswan/platform-agent/internal/k8s"
)

// AlertTriageResult contains the structured summary of an alert investigation.
type AlertTriageResult struct {
	PodName   string         `json:"pod_name"`
	Namespace string         `json:"namespace"`
	Status    string         `json:"status"`
	Restarts  int            `json:"restarts"`
	LastLog   string         `json:"last_log"`
	Events    []k8s.EventInfo `json:"events"`
	Summary   string         `json:"summary"`
}

func buildTriageSummary(r *AlertTriageResult) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Alert Triage: %s/%s\n\n", r.Namespace, r.PodName))

	if r.Status != "" {
		sb.WriteString(fmt.Sprintf("Status: %s\n", r.Status))
	}
	if r.Restarts > 0 {
		sb.WriteString(fmt.Sprintf("Restarts: %d\n", r.Restarts))
	}

	if len(r.Events) > 0 {
		sb.WriteString("\nRecent Events:\n")
		limit := 5
		if len(r.Events) < limit {
			limit = len(r.Events)
		}
		for _, e := range r.Events[:limit] {
			sb.WriteString(fmt.Sprintf("• %s — %s (%s)\n", e.Reason, e.Message, e.Age))
		}
	}

	if r.LastLog != "" {
		sb.WriteString(fmt.Sprintf("\nLast Logs:\n%s\n", r.LastLog))
	}

	return sb.String()
}

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
