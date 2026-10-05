package shellinstaller

import "strings"

// These are fixed product actions, not shell commands or caller-defined steps.
type LinuxJournalAction string

const (
	LinuxJournalHome       LinuxJournalAction = "dedicated-home"
	LinuxJournalPackage    LinuxJournalAction = "package-declaration"
	LinuxJournalExecutable LinuxJournalAction = "executable-object"
	LinuxJournalLink       LinuxJournalAction = "executable-link"
	LinuxJournalSettings   LinuxJournalAction = "settings"
)

// LinuxJournalObject is reported DATA. The future protected host connector
// must independently bind each physical ID and digest to its actual object.
type LinuxJournalObject struct {
	Dev     uint64
	Ino     uint64
	MountID uint64
	SHA256  string
	Exists  bool
}

func linuxJournalValidObject(o LinuxJournalObject) bool {
	if !o.Exists || o.Dev == 0 || o.Ino == 0 || o.MountID == 0 || len(o.SHA256) != 64 {
		return false
	}
	for _, c := range o.SHA256 {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func linuxJournalSame(a, b LinuxJournalObject) bool {
	return linuxJournalValidObject(a) && linuxJournalValidObject(b) && a == b
}

// The arrays are fixed internally, so no caller can replace or reorder actions.
func linuxJournalActions(mode TerminalEntryPoint) ([4]LinuxJournalAction, bool) {
	switch mode {
	case TerminalEntryPointGentleShell:
		return [4]LinuxJournalAction{
			LinuxJournalHome, LinuxJournalPackage, LinuxJournalExecutable, LinuxJournalSettings,
		}, true
	case TerminalEntryPointPi:
		return [4]LinuxJournalAction{
			LinuxJournalPackage, LinuxJournalExecutable, LinuxJournalLink, LinuxJournalSettings,
		}, true
	default:
		return [4]LinuxJournalAction{}, false // zero never means takeover
	}
}

type LinuxJournalPhase string

const (
	LinuxJournalNeedsPersistedReceipt LinuxJournalPhase = "needs-persisted-journal-receipt"
	LinuxJournalClaimedReceipt        LinuxJournalPhase = "unverified-journal-receipt-data"
	LinuxJournalPostimageData         LinuxJournalPhase = "unverified-postimage-data"
	LinuxJournalQuarantined           LinuxJournalPhase = "quarantine"
)

const LinuxJournalNotAuthorized = "not-authorized"

// LinuxTransactionData cannot represent Approved, Apply, restored or Ready.
// Every receipt and InstanceID string is unverified caller DATA.
type LinuxTransactionData struct {
	Kind               string
	Mode               TerminalEntryPoint
	Phase              LinuxJournalPhase
	Actions            [4]LinuxJournalAction
	Preimage           [4]LinuxJournalObject
	AppliedPostimage   [4]LinuxJournalObject
	CreatedClaimSHA256 [4]string // unverified create receipt, separate mode only
	ExistingInstanceID string
	JournalClaimSHA256 string
	AppliedCount       uint8
}

// LinuxJournalDemand describes what a future host must validate/persist; it
// does not grant permission to perform an effect, or attest that one occurred.
type LinuxJournalDemand struct {
	Kind                     string
	Action                   LinuxJournalAction
	RequiresProtectedHost    bool
	RequiresPersistedJournal bool
}

// BeginLinuxTransaction records fixed order and demands a durable journal
// BEFORE any mutation request can be selected. Preimages are not authenticated.
func BeginLinuxTransaction(mode TerminalEntryPoint, instanceID string,
	preimage [4]LinuxJournalObject) (LinuxTransactionData, LinuxJournalDemand) {
	actions, valid := linuxJournalActions(mode)
	s := LinuxTransactionData{Kind: LinuxJournalNotAuthorized, Mode: mode,
		Phase: LinuxJournalQuarantined, Actions: actions}
	if !valid {
		return s, LinuxJournalDemand{Kind: "quarantine"}
	}
	if mode == TerminalEntryPointPi {
		if instanceID == "" {
			return s, LinuxJournalDemand{Kind: "quarantine"}
		}
		for _, object := range preimage {
			if !linuxJournalValidObject(object) {
				return s, LinuxJournalDemand{Kind: "quarantine"}
			}
		}
	} else {
		if instanceID != "" || preimage != ([4]LinuxJournalObject{}) {
			return s, LinuxJournalDemand{Kind: "quarantine"}
		}
	}
	s.Preimage, s.ExistingInstanceID = preimage, instanceID
	s.Phase = LinuxJournalNeedsPersistedReceipt
	return s, LinuxJournalDemand{Kind: "persist-preimage-journal", RequiresProtectedHost: true}
}

func linuxJournalShape(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	for _, c := range digest {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// ReportLinuxJournal stores a SHAPE-ONLY claim that a protected preimage
// journal was persisted. Only a later trusted connector may authenticate it.
func ReportLinuxJournal(s LinuxTransactionData, claimedReceiptSHA256 string) LinuxTransactionData {
	if s.Kind != LinuxJournalNotAuthorized || s.Phase != LinuxJournalNeedsPersistedReceipt ||
		!linuxJournalShape(claimedReceiptSHA256) {
		s.Phase = LinuxJournalQuarantined
		return s
	}
	s.JournalClaimSHA256 = claimedReceiptSHA256
	s.Phase = LinuxJournalClaimedReceipt
	return s
}

// NextLinuxJournalDemand is selection DATA, never an executable operation.
// All claimed receipts require independent validation before a host effect.
func NextLinuxJournalDemand(s LinuxTransactionData) LinuxJournalDemand {
	fixed, ok := linuxJournalActions(s.Mode)
	if !ok || fixed != s.Actions || s.Kind != LinuxJournalNotAuthorized ||
		!linuxJournalShape(s.JournalClaimSHA256) ||
		(s.Phase != LinuxJournalClaimedReceipt && s.Phase != LinuxJournalPostimageData) ||
		s.AppliedCount >= uint8(len(s.Actions)) {
		return LinuxJournalDemand{Kind: "quarantine"}
	}
	return LinuxJournalDemand{Kind: "request-mode-scoped-effect-data",
		Action: s.Actions[s.AppliedCount], RequiresProtectedHost: true,
		RequiresPersistedJournal: true}
}

// ReportLinuxPostimage accepts only the NEXT fixed action and an object-shaped
// postimage claim. It neither mutates nor validates the host's physical object.
func ReportLinuxPostimage(s LinuxTransactionData, action LinuxJournalAction,
	post LinuxJournalObject, createdClaimSHA256 string) LinuxTransactionData {
	demand := NextLinuxJournalDemand(s)
	if demand.Kind != "request-mode-scoped-effect-data" || action != demand.Action ||
		!linuxJournalValidObject(post) ||
		(s.Mode == TerminalEntryPointGentleShell && !linuxJournalShape(createdClaimSHA256)) ||
		(s.Mode == TerminalEntryPointPi && createdClaimSHA256 != "") {
		s.Phase = LinuxJournalQuarantined
		return s
	}
	for i := uint8(0); i < s.AppliedCount; i++ {
		if s.AppliedPostimage[i].Dev == post.Dev && s.AppliedPostimage[i].Ino == post.Ino {
			s.Phase = LinuxJournalQuarantined // no ambiguous reused object IDs
			return s
		}
	}
	s.AppliedPostimage[s.AppliedCount] = post
	s.CreatedClaimSHA256[s.AppliedCount] = createdClaimSHA256
	s.AppliedCount++
	s.Phase = LinuxJournalPostimageData
	return s
}

type LinuxInverseAction string

const (
	LinuxInverseDeleteCreated LinuxInverseAction = "select-created-object-removal"
	LinuxInverseRestorePi     LinuxInverseAction = "select-existing-pi-preimage-restore"
)

// An inverse is a conditional selection, not deletion or restoration.
type LinuxInverseSelection struct {
	Action             LinuxInverseAction
	JournalAction      LinuxJournalAction
	ExpectedPostimage  LinuxJournalObject // exact CAS against live host object
	RestorePreimage    LinuxJournalObject // empty for separate create-only mode
	CreatedClaimSHA256 string             // unverified, separately authenticated by host
	ExistingInstanceID string
}

type LinuxInversePlan struct {
	Kind           string // always not-authorized
	Quarantined    bool
	RequiresHost   bool
	Count          uint8
	ReverseActions [4]LinuxInverseSelection
}

// SelectLinuxInverse handles ONLY the fully reported four-step postimage set.
// Partial/zero counts quarantine: pure DATA cannot resolve a post-effect,
// pre-receipt crash. A later protected host recovery must inspect that case.
// All reported inputs still need independent connector authentication.
func SelectLinuxInverse(s LinuxTransactionData, live [4]LinuxJournalObject,
	restoreFailed bool) LinuxInversePlan {
	p := LinuxInversePlan{Kind: LinuxJournalNotAuthorized, Quarantined: true,
		RequiresHost: true}
	if restoreFailed || s.Kind != LinuxJournalNotAuthorized ||
		s.Phase != LinuxJournalPostimageData ||
		!linuxJournalShape(s.JournalClaimSHA256) || s.AppliedCount != 4 {
		return p
	}
	if s.Mode != TerminalEntryPointPi && s.Mode != TerminalEntryPointGentleShell {
		return p
	}
	fixed, ok := linuxJournalActions(s.Mode)
	if !ok || fixed != s.Actions || int(s.AppliedCount) != len(fixed) {
		return p
	}
	if s.Mode == TerminalEntryPointPi {
		if s.ExistingInstanceID == "" {
			return p
		}
		for _, object := range s.Preimage {
			if !linuxJournalValidObject(object) {
				return p
			}
		}
	} else if s.ExistingInstanceID != "" || s.Preimage != ([4]LinuxJournalObject{}) {
		return p
	}
	for i := uint8(0); i < s.AppliedCount; i++ {
		if !linuxJournalSame(s.AppliedPostimage[i], live[i]) ||
			(s.Mode == TerminalEntryPointGentleShell && !linuxJournalShape(s.CreatedClaimSHA256[i])) ||
			(s.Mode == TerminalEntryPointPi && s.CreatedClaimSHA256[i] != "") {
			return p // missing create receipt, CAS mismatch or unknown: preserve all
		}
		for j := uint8(0); j < i; j++ {
			if s.AppliedPostimage[i].Dev == s.AppliedPostimage[j].Dev &&
				s.AppliedPostimage[i].Ino == s.AppliedPostimage[j].Ino {
				return p // forged duplicate physical object: no inverse selected
			}
		}
	}
	for i := int(s.AppliedCount) - 1; i >= 0; i-- {
		inverse := LinuxInverseSelection{JournalAction: fixed[i],
			ExpectedPostimage: s.AppliedPostimage[i]}
		if s.Mode == TerminalEntryPointGentleShell {
			inverse.Action = LinuxInverseDeleteCreated
			inverse.CreatedClaimSHA256 = s.CreatedClaimSHA256[i]
		} else {
			inverse.Action = LinuxInverseRestorePi
			inverse.RestorePreimage = s.Preimage[i]
			inverse.ExistingInstanceID = s.ExistingInstanceID
		}
		p.ReverseActions[p.Count] = inverse
		p.Count++
	}
	p.Quarantined = false // only a selection over untrusted DATA, NOT restored
	return p
}
