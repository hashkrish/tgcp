package firestore

type Database struct {
	Name                          string // Short ID: (default)
	ProjectID                     string
	Location                      string
	Type                          string // FIRESTORE_NATIVE / DATASTORE_MODE
	CreateTime                    string
	UpdateTime                    string
	Uid                           string
	DatabaseEdition               string // STANDARD / ENTERPRISE
	ConcurrencyMode               string
	DeleteProtectionState         string
	PointInTimeRecoveryEnablement string
	FreeTier                      bool
}

// IsDatastoreMode returns true if this database is in Datastore mode
func (d Database) IsDatastoreMode() bool {
	return d.Type == "DATASTORE_MODE"
}

// Kind represents a Datastore entity kind (for DATASTORE_MODE databases)
type Kind struct {
	Name      string
	Namespace string
}

// Namespace represents a Datastore namespace (for DATASTORE_MODE databases)
type Namespace struct {
	Name string
}
