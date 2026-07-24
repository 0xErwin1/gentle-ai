package evidence

import "github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"

// ObligationOutcome is the evidence-provider view of one planned semantic
// obligation. Missing is explicit because absence of a required record is
// weaker evidence, never success.
type ObligationOutcome string

const (
	ObligationPassed          ObligationOutcome = "passed"
	ObligationFailed          ObligationOutcome = "failed"
	ObligationNotApplicable   ObligationOutcome = "not_applicable"
	ObligationSkippedByUser   ObligationOutcome = "skipped_by_user"
	ObligationSkippedByPolicy ObligationOutcome = "skipped_by_policy"
	ObligationUnavailable     ObligationOutcome = "unavailable"
	ObligationCancelled       ObligationOutcome = "cancelled"
	ObligationMissing         ObligationOutcome = "missing"
)

// AggregateOutcome is the canonical RAR aggregate vocabulary, not an
// evidence-local copy. EPD calculates from admitted facts; RAR remains the
// authority that validates and publishes VerificationResultRef.
type AggregateOutcome = reviewtransaction.VerificationAggregate

const (
	AggregateNotRequired = reviewtransaction.VerificationAggregateNotRequired
	AggregateComplete    = reviewtransaction.VerificationAggregateComplete
	AggregateFailed      = reviewtransaction.VerificationAggregateFailed
	AggregatePartial     = reviewtransaction.VerificationAggregatePartial
	AggregateUnavailable = reviewtransaction.VerificationAggregateUnavailable
)

// AggregateObligations is deliberately monotonic: a failed obligation is
// terminal; missing/unavailable/skipped evidence can only retain or weaken the
// aggregate; and success requires every planned obligation to have an explicit
// passed or owner-proven not-applicable record.
func AggregateObligations(outcomes []ObligationOutcome) AggregateOutcome {
	if len(outcomes) == 0 {
		return AggregateUnavailable
	}
	var passed, notApplicable, weaker int
	for _, outcome := range outcomes {
		switch outcome {
		case ObligationFailed:
			return AggregateFailed
		case ObligationPassed:
			passed++
		case ObligationNotApplicable:
			notApplicable++
		case ObligationSkippedByUser, ObligationSkippedByPolicy,
			ObligationUnavailable, ObligationCancelled, ObligationMissing:
			weaker++
		default:
			// Unknown evidence is indistinguishable from missing evidence.
			weaker++
		}
	}
	if weaker > 0 {
		if passed > 0 || notApplicable > 0 {
			return AggregatePartial
		}
		return AggregateUnavailable
	}
	if passed > 0 {
		return AggregateComplete
	}
	return AggregateNotRequired
}

// aggregateStrength is test-visible within the package and intentionally
// orders only delivery readiness, not diagnostic severity.
func aggregateStrength(outcome AggregateOutcome) int {
	switch outcome {
	case AggregateComplete, AggregateNotRequired:
		return 3
	case AggregatePartial:
		return 2
	case AggregateUnavailable:
		return 1
	case AggregateFailed:
		return 0
	default:
		return -1
	}
}
