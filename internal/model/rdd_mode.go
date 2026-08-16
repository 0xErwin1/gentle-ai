package model

// RDDMode is the receipt-driven-development kill-switch value as the
// declarative contract names it. The domain lives here rather than in
// reviewtransaction because the contract package cannot import it, and
// InstallState deliberately persists the value as a plain string so
// reviewtransaction stays the authority that fails closed on anything it does
// not recognise.
type RDDMode string

const (
	RDDModeOn  RDDMode = "on"
	RDDModeOff RDDMode = "off"
)

func (m RDDMode) Valid() bool {
	return m == RDDModeOn || m == RDDModeOff
}
