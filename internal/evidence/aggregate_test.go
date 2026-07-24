package evidence

import (
	"math/rand"
	"testing"
	"testing/quick"
)

func TestAggregateObligationsConservativeRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		outcomes []ObligationOutcome
		want     AggregateOutcome
	}{
		{name: "empty is unavailable", want: AggregateUnavailable},
		{
			name: "all owner-proven not applicable is not required",
			outcomes: []ObligationOutcome{
				ObligationNotApplicable,
				ObligationNotApplicable,
			},
			want: AggregateNotRequired,
		},
		{
			name: "passed obligations are complete",
			outcomes: []ObligationOutcome{
				ObligationPassed,
				ObligationNotApplicable,
			},
			want: AggregateComplete,
		},
		{
			name: "missing evidence makes success partial",
			outcomes: []ObligationOutcome{
				ObligationPassed,
				ObligationMissing,
			},
			want: AggregatePartial,
		},
		{
			name: "only unavailable evidence is unavailable",
			outcomes: []ObligationOutcome{
				ObligationUnavailable,
				ObligationCancelled,
			},
			want: AggregateUnavailable,
		},
		{
			name: "failure dominates every weaker result",
			outcomes: []ObligationOutcome{
				ObligationPassed,
				ObligationUnavailable,
				ObligationFailed,
			},
			want: AggregateFailed,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := AggregateObligations(test.outcomes); got != test.want {
				t.Fatalf("AggregateObligations(%v) = %q, want %q", test.outcomes, got, test.want)
			}
		})
	}
}

func TestAggregateAddingMissingOrWeakerEvidenceNeverImproves(t *testing.T) {
	t.Parallel()

	all := []ObligationOutcome{
		ObligationPassed,
		ObligationFailed,
		ObligationNotApplicable,
		ObligationSkippedByUser,
		ObligationSkippedByPolicy,
		ObligationUnavailable,
		ObligationCancelled,
		ObligationMissing,
	}
	weaker := []ObligationOutcome{
		ObligationSkippedByUser,
		ObligationSkippedByPolicy,
		ObligationUnavailable,
		ObligationCancelled,
		ObligationMissing,
	}
	property := func(seed uint64, count uint8, weakIndex uint8) bool {
		random := rand.New(rand.NewSource(int64(seed))) // #nosec G404 -- deterministic property generation
		base := make([]ObligationOutcome, int(count%32))
		for index := range base {
			base[index] = all[random.Intn(len(all))]
		}
		before := AggregateObligations(base)
		afterInput := append(append([]ObligationOutcome{}, base...),
			weaker[int(weakIndex)%len(weaker)])
		after := AggregateObligations(afterInput)
		return aggregateStrength(after) <= aggregateStrength(before)
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 5_000}); err != nil {
		t.Fatal(err)
	}
}
