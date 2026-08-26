package parametermanager

import "time"

// Parameter represents a Parameter Manager parameter
type Parameter struct {
	Name       string // Short name (e.g., "app-config")
	FullName   string // Full resource name (projects/xxx/locations/yyy/parameters/zzz)
	Format     string // UNFORMATTED, JSON, or YAML
	Labels     map[string]string
	CreateTime time.Time
}

// ParameterVersion represents a version of a parameter
type ParameterVersion struct {
	Name       string // Version ID (e.g., "1", "v1")
	FullName   string // Full resource name
	Disabled   bool
	CreateTime time.Time
}
