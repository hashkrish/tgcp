package apis

// API is one Google API/service as seen by a project via the Service Usage
// API (e.g. compute.googleapis.com), together with whether it's enabled.
type API struct {
	Name    string // Service name, e.g. compute.googleapis.com
	Title   string // Human-readable title, e.g. "Compute Engine API"
	State   string // ENABLED or DISABLED
	Summary string // One-paragraph description from the service config
}
