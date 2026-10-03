package videosched

import "fmt"

const (
	PolicyWeightedV1      = "weighted_v1"
	PolicyStabilityCostV2 = "stability_cost_v2"

	HealthUnverified = "unverified"
	HealthNormal     = "normal"
	HealthBlocked    = "blocked"
	HealthRecovering = "recovering"

	ReasonManualRecoveryRequested = "manual_recovery_requested"

	ReliabilityVersion = 1
)

// ReliabilityEvidence counts outcomes from one fixed cohort of real submits.
// All times are Unix seconds. User/cancelled outcomes remain visible but never
// contribute to either reliability denominator. Unknowns prevent qualification.
type ReliabilityEvidence struct {
	Version          int    `json:"version"`
	Source           string `json:"source"` // window | cold_start | revalidation | recovery
	BatchStart       int64  `json:"batch_start"`
	BatchEnd         int64  `json:"batch_end"`
	WindowSeconds    int    `json:"window_seconds"`
	AsOf             int64  `json:"as_of"`
	ValidatedAt      int64  `json:"validated_at"`
	ExpiresAt        int64  `json:"expires_at"`
	Submitted        int64  `json:"submitted"`
	Accepted         int64  `json:"accepted"`
	Succeeded        int64  `json:"succeeded"`
	Rejected         int64  `json:"rejected"`
	GenerationFailed int64  `json:"generation_failed"`
	User             int64  `json:"user"`
	Cancelled        int64  `json:"cancelled"`
	Pending          int64  `json:"pending"`
	Unknown          int64  `json:"unknown"`
	Missing          int64  `json:"missing"`
}

func (e ReliabilityEvidence) GenerationSamples() int64 { return e.Succeeded + e.GenerationFailed }
func (e ReliabilityEvidence) OverallSamples() int64    { return e.GenerationSamples() + e.Rejected }

func (e ReliabilityEvidence) GenerationRate() float64 {
	if n := e.GenerationSamples(); n > 0 {
		return float64(e.Succeeded) / float64(n)
	}
	return 0 // callers must check the denominator; zero samples means unknown
}

func (e ReliabilityEvidence) OverallRate() float64 {
	if n := e.OverallSamples(); n > 0 {
		return float64(e.Succeeded) / float64(n)
	}
	return 0
}

func (e ReliabilityEvidence) Validate() error {
	if e.Version != ReliabilityVersion || (e.Source != "window" && e.Source != "recovery" && e.Source != "cold_start" && e.Source != "revalidation") ||
		e.BatchStart < 0 || e.BatchEnd < e.BatchStart || e.WindowSeconds <= 0 || e.AsOf < 0 ||
		e.ValidatedAt < 0 || e.ExpiresAt < 0 {
		return fmt.Errorf("invalid reliability cohort")
	}
	for _, n := range []int64{e.Submitted, e.Accepted, e.Succeeded, e.Rejected, e.GenerationFailed, e.User, e.Cancelled, e.Pending, e.Unknown, e.Missing} {
		if n < 0 || n > 1_000_000_000 {
			return fmt.Errorf("invalid reliability count")
		}
	}
	if e.Accepted > e.Submitted || e.GenerationSamples() > e.Accepted ||
		e.OverallSamples()+e.User+e.Cancelled+e.Pending+e.Unknown != e.Submitted || e.Missing > e.Submitted {
		return fmt.Errorf("inconsistent reliability counts")
	}
	return nil
}

func (e ReliabilityEvidence) Mature(now int64) bool {
	return e.Validate() == nil && e.BatchEnd <= now && e.Submitted > 0 && e.Pending == 0 && e.Unknown == 0 && e.Missing == 0
}

// QualificationReason deliberately evaluates the independent denominators.
// A 60/75 generation cohort passes 80%, but only 60/100 passes overall 60%.
func (e ReliabilityEvidence) QualificationReason(minSamples int, minGen, minOverall float64, now int64) string {
	if !e.Mature(now) {
		return "health evidence incomplete"
	}
	if e.GenerationSamples() < int64(max(1, minSamples)) || e.OverallSamples() < int64(max(1, minSamples)) {
		return "health samples insufficient"
	}
	if e.GenerationRate() < minGen {
		return "generation rate below minimum"
	}
	if e.OverallRate() < minOverall {
		return "overall completion below minimum"
	}
	return ""
}

// ReliabilitySnapshot is the independently persisted channel x model state.
// Missing snapshots mean unavailable history, never a new healthy channel.
type ReliabilitySnapshot struct {
	Version           int                  `json:"version"`
	Model             string               `json:"model"`
	ConfigIdentity    string               `json:"config_identity,omitempty"`
	State             string               `json:"state"`
	StateVersion      int64                `json:"state_version"`
	StateRevision     int64                `json:"state_revision"`
	ValidationRound   int64                `json:"validation_round"`
	ProbeFailures     int                  `json:"probe_failures"`
	Reason            string               `json:"reason"`
	Integrity         string               `json:"integrity"` // complete | unavailable | uncertain
	BlockedAt         int64                `json:"blocked_at"`
	RecoveryStarted   int64                `json:"recovery_started"`
	RecoveryExpires   int64                `json:"recovery_expires"`
	ValidationStarted int64                `json:"validation_started"`
	ValidationExpires int64                `json:"validation_expires"`
	LastValidationAt  int64                `json:"last_validation_at"`
	Qualification     *ReliabilityEvidence `json:"qualification"`
	Current           *ReliabilityEvidence `json:"current"`
	Recovery          *ReliabilityEvidence `json:"recovery"`
}

func (s ReliabilitySnapshot) NormalReason(p Policy, now int64) string {
	if s.Validate() != nil || s.Integrity != "complete" {
		return "health evidence incomplete"
	}
	if s.State == HealthBlocked || s.State == HealthRecovering {
		return "waiting for recovery verification"
	}
	if s.State != HealthNormal || s.Qualification == nil {
		return "health samples insufficient"
	}
	q := s.Qualification
	if q.ExpiresAt <= now || q.ValidatedAt <= 0 || q.ValidatedAt > now {
		return "health qualification expired"
	}
	return q.QualificationReason(p.MinSamples, p.MinGenRate, p.MinOverallRate, now)
}

func (s ReliabilitySnapshot) Validate() error {
	if s.ProbeFailures < 0 || s.ProbeFailures > 32 {
		return fmt.Errorf("invalid probe failure count")
	}
	if s.Version != ReliabilityVersion || s.Model == "" || s.StateVersion < 1 || (s.Integrity != "complete" && s.Integrity != "uncertain" && s.Integrity != "unavailable") {
		return fmt.Errorf("invalid reliability state")
	}
	switch s.State {
	case HealthNormal, HealthUnverified, HealthBlocked, HealthRecovering:
	default:
		return fmt.Errorf("invalid reliability state")
	}
	if s.State == HealthNormal && s.Qualification == nil {
		return fmt.Errorf("normal health state requires a qualification")
	}
	for _, n := range []int64{s.BlockedAt, s.RecoveryStarted, s.RecoveryExpires, s.ValidationStarted, s.ValidationExpires, s.LastValidationAt} {
		if n < 0 {
			return fmt.Errorf("invalid reliability time")
		}
	}
	for _, e := range []*ReliabilityEvidence{s.Qualification, s.Current, s.Recovery} {
		if e != nil {
			if err := e.Validate(); err != nil {
				return err
			}
		}
	}
	if q := s.Qualification; q != nil && (q.ValidatedAt < q.BatchEnd || q.ExpiresAt <= q.ValidatedAt || q.ExpiresAt-q.ValidatedAt > 7*86400) {
		return fmt.Errorf("invalid reliability qualification lifetime")
	}
	return nil
}
