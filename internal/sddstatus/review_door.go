package sddstatus

// review_door.go is internal/sddstatus's one door into reviewtransaction's
// offer/ReviewCore symbols (design.md decision 4). It exists so the Wave 4
// S3 absence guard (review_offer_absence_guard_test.go) can name exactly one
// exempt file rather than allowing offer/ReviewCore references anywhere in
// the package.
//
// reviewEntryHook is not yet called by any production code in this slice:
// internal/sddstatus does not touch offer/ReviewCore symbols today (that is
// the guard's own proof — see TestReviewOfferAbsenceGuardHoldsForProduction
// Files). It is declared now, ahead of its first caller, following the same
// "ship the shape, baseline the gap honestly" precedent Wave 3 used for
// review_offer.go itself (design.md's Wave-0 trace citation): a future
// slice (S5's ValidateReceiptRef) is expected to wire this door's first real
// call. Modelled on internal/reviewtransaction/gate.go's
// finalGateAuthorizationHook/artifactPreimagesReadHook trip-wire pattern —
// tests and bench journeys override it to prove call presence or absence
// without needing production code to call it yet.
var reviewEntryHook = func() {}
