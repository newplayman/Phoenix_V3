package riskcontrol

import (
	"math"
	"time"

	"phoenix-v3/internal/contracts"
)

func ApplyDegradation(intent contracts.Intent, d IntentDegradation) contracts.Intent {
	out := intent

	if d.SetUrgencyLower != nil {
		if out.Urgency == 0 || *d.SetUrgencyLower < out.Urgency {
			out.Urgency = *d.SetUrgencyLower
		}
	}

	if d.SetDeadlineEarlier != nil && !d.SetDeadlineEarlier.IsZero() {
		if out.Deadline.IsZero() || d.SetDeadlineEarlier.Before(out.Deadline) {
			out.Deadline = *d.SetDeadlineEarlier
		}
	}

	if len(d.MetadataDelete) > 0 && out.Metadata != nil {
		for _, k := range d.MetadataDelete {
			delete(out.Metadata, k)
		}
	}

	if len(d.MetadataOverride) > 0 {
		if out.Metadata == nil {
			out.Metadata = make(map[string]string, len(d.MetadataOverride))
		}
		for k, v := range d.MetadataOverride {
			out.Metadata[k] = v
		}
	}

	return out
}

func urgencyOrMax(v *int) int {
	if v == nil {
		return math.MaxInt
	}
	return *v
}

func deadlineOrMax(t *time.Time) time.Time {
	if t == nil || t.IsZero() {
		return time.Unix(1<<62, 0).UTC()
	}
	return t.UTC()
}
