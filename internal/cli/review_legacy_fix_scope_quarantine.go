package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

type ReviewLegacyFixScopeQuarantineResult struct {
	Operation string                                 `json:"operation"`
	Record    reviewtransaction.CompactReclaimRecord `json:"record"`
}

func RunReviewLegacyFixScopeQuarantine(args []string, stdout io.Writer) error {
	flags := newReviewFlagSet("review quarantine-legacy-fix-scope", stdout, "Quarantine one immutable legacy-v1 ordinary_4r lineage whose historical review/complete-fix event alone has a correction scope expansion outside its frozen genesis scope. The exact content-addressed history moves whole into audited quarantine and is never rewritten, accepted as valid, or handled by repair-legacy-alias. Unknown operations, aliases, malformed semantics, hashes, mixed authority, stale revisions, and active ownership are refused. Exact committed retries converge idempotently.")
	cwd := flags.String("cwd", ".", "repository path")
	lineage := flags.String("lineage", "", "legacy-v1 lineage to quarantine")
	expected := flags.String("expected-revision", "", "exact current legacy HEAD revision")
	diagnostic := flags.String("diagnostic", "", "exact inventory diagnostic")
	disposition := flags.String("disposition", "", "exact supported quarantine disposition")
	reason := flags.String("reason", "", "non-empty quarantine reason")
	actor := flags.String("actor", "", "quarantine actor")
	authorization := flags.String("maintainer-authorization", "", "exact eight-line LF-only binding: gentle-ai.review-legacy-fix-scope-quarantine-authorization/v1, repository, lineage, revision, diagnostic, disposition, actor, reason")
	if err := parseReviewFlags(flags, args); err != nil {
		return err
	}
	if reviewHelpRequested(args) {
		return nil
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected review quarantine-legacy-fix-scope argument %q", flags.Arg(0))
	}
	for _, required := range []string{*lineage, *expected, *diagnostic, *disposition, *reason, *actor, *authorization} {
		if strings.TrimSpace(required) == "" {
			return errors.New("review quarantine-legacy-fix-scope requires --lineage, --expected-revision, --diagnostic, --disposition, --reason, --actor, and --maintainer-authorization")
		}
	}
	root, err := (reviewtransaction.SnapshotBuilder{Repo: *cwd}).ResolveRepositoryRoot(context.Background())
	if err != nil {
		return fmt.Errorf("resolve review repository root: %w", err)
	}
	record, err := reviewtransaction.QuarantineHistoricalLegacyFixScope(context.Background(), root, reviewtransaction.LegacyFixScopeQuarantineRequest{
		LineageID: *lineage, ExpectedRevision: *expected, ExpectedDiagnostic: *diagnostic,
		Disposition: *disposition, Reason: *reason, Actor: *actor, MaintainerAuthorization: *authorization,
	})
	if err != nil {
		if record.QuarantinePath != "" {
			_ = encodeReviewJSON(stdout, ReviewLegacyFixScopeQuarantineResult{Operation: "review/quarantine-legacy-fix-scope", Record: record})
		}
		return err
	}
	return encodeReviewJSON(stdout, ReviewLegacyFixScopeQuarantineResult{Operation: "review/quarantine-legacy-fix-scope", Record: record})
}
