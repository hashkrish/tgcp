package filestore

// FileShare represents a single NFS export on a Filestore instance.
type FileShare struct {
	Name       string
	CapacityGB int64
}

// InstanceCreateOpts holds the minimal set of fields needed to create a
// Filestore instance via the Create form.
type InstanceCreateOpts struct {
	InstanceID string
	Zone       string
	Tier       string // BASIC_HDD, BASIC_SSD, ZONAL, ...
	CapacityGB string // numeric string
	ShareName  string
	Network    string
}

// Instance represents a Filestore (managed NFS) instance (read-only view).
//
// Filestore instances are zone-scoped for the BASIC_HDD/BASIC_SSD tiers and
// region-scoped for the newer tiers (ZONAL/REGIONAL/ENTERPRISE/etc). Rather
// than branching on tier to decide whether Location holds a zone or a
// region, we just surface whatever location string the API returned the
// instance under (see api.go) — the field is labeled "Zone/Region" in the
// UI to make that ambiguity explicit to the user.
type Instance struct {
	Name          string // Short instance ID
	FullName      string // Full resource name (projects/*/locations/*/instances/*)
	Location      string // Zone (Basic tiers) or region (other tiers)
	Tier          string // BASIC_HDD, BASIC_SSD, ZONAL, REGIONAL, ENTERPRISE, ...
	State         string // READY, CREATING, DELETING, ERROR, ...
	StatusMessage string
	CapacityGB    int64 // Sum of all file share capacities
	Network       string
	IPAddresses   []string
	CreateTime    string // RFC3339-ish formatted, empty if unknown
	FileShares    []FileShare
}
