package advisory

import "fmt"

// CalculateSeverityScore computes a 0-100 severity score based on evidence.
func CalculateSeverityScore(instantaneousMode string, evidence AdvisoryEvidence) (int, string) {
	score := 0
	components := []string{}

	if instantaneousMode == SuggestionHalt {
		score += 60
		components = append(components, "halt_condition=60")
	}

	divScore := int(evidence.DivergenceRejectRate * 50)
	if evidence.MaxDeviationBps > 0 {
		divScore += int(float64(evidence.MaxDeviationBps) / 25)
	}
	if divScore > 40 {
		divScore = 40
	}
	if divScore > 0 {
		score += divScore
		components = append(components, fmt.Sprintf("divergence=%d", divScore))
	}

	tmScore := int(evidence.TimeMismatchSkipRate * 20)
	if tmScore > 20 {
		tmScore = 20
	}
	if tmScore > 0 {
		score += tmScore
		components = append(components, fmt.Sprintf("time_mismatch=%d", tmScore))
	}

	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	return score, fmt.Sprintf("severity_score=%d (components: %v)", score, components)
}
