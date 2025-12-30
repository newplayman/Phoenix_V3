package advisory

import "fmt"

// CalculateSeverityScore computes a 0-100 severity score based on evidence
func CalculateSeverityScore(instantaneousMode string, evidence AdvisoryEvidence) (int, string) {
base := 0
components := []string{}

// Base score for instantaneous HALT condition
if instantaneousMode == SuggestionHalt {
60
ents = append(components, "halt_condition=60")
}

// Divergence contribution
divScore := int(evidence.DivergenceRejectRate * 50)
if evidence.MaxDeviationBps > 0 {
int(float64(evidence.MaxDeviationBps) / 25)
}
if divScore > 40 {
40
}
if divScore > 0 {
divScore
ents = append(components, fmt.Sprintf("divergence=%d", divScore))
}

// Time mismatch contribution
tmScore := int(evidence.TimeMismatchSkipRate * 20)
if tmScore > 20 {
20
}
if tmScore > 0 {
tmScore
ents = append(components, fmt.Sprintf("time_mismatch=%d", tmScore))
}

// Clamp to 0-100
if base > 100 {
100
}
if base < 0 {
0
}

reason := fmt.Sprintf("severity_score=%d (components: %v)", base, components)
return base, reason
}
