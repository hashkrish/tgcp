package overview

// -----------------------------------------------------------------------------
// Data Models
// -----------------------------------------------------------------------------

type BillingInfo struct {
	Enabled            bool
	BillingAccountName string
	BillingAccountID   string
}

type Recommendation struct {
	ID                     string
	Description            string
	RecommenderSubtype     string
	Priority               string
	State                  string
	EstimatedSavingsAmount float64
	CurrencyCode           string
}

type ResourceInventory struct {
	InstanceCount int
	DiskCount     int
	DiskGB        int
	IPCount       int
	SQLCount      int
	BucketCount   int
	DatasetCount  int
}

type SpendLimit struct {
	Name            string
	BudgetAmount    string
	CurrencyCode    string
	AlertThresholds []float64
}

type DashboardData struct {
	Info            BillingInfo
	Recommendations []Recommendation
	Inventory       ResourceInventory
	Budgets         []SpendLimit

	// Granular Loading States
	InfoLoading      bool
	RecsLoading      bool
	InventoryLoading bool
	BudgetsLoading   bool

	// Granular Errors -- one per independently-fetched section, so a
	// failure in one (e.g. budgets, which requires Billing Account IAM
	// access many viewers don't have) only replaces that section's own card
	// with an inline error instead of hiding the whole dashboard, including
	// sections that loaded fine.
	InfoError      error
	RecsError      error
	InventoryError error
	BudgetsError   error
}

// Separate Messages for Granular Updates
type InfoMsg BillingInfo
type RecsMsg []Recommendation
type InventoryMsg ResourceInventory
type BudgetsMsg []SpendLimit

// Separate error messages per section -- mirroring the per-section data
// messages above -- so Update() knows which single section failed instead
// of only "something failed" (the old shared ErrMsg gave no way to tell
// which fetch produced it).
type InfoErrMsg struct{ Err error }
type RecsErrMsg struct{ Err error }
type InventoryErrMsg struct{ Err error }
type BudgetsErrMsg struct{ Err error }
