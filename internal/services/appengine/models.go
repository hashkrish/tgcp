package appengine

import "time"

// Application is the single per-project App Engine application resource.
// There is at most one per project, addressed directly by project ID
// (Apps.Get) -- it can't be listed.
type Application struct {
	Id              string
	DefaultHostname string
	LocationId      string
	ServingStatus   string
}

// AppEngineService is one App Engine service (e.g. "default"). Named
// AppEngineService rather than Service to avoid clashing with
// services.Service (this package's own TUI service interface) and with the
// App Engine API's own Service type, both of which are otherwise in scope
// wherever this type is used.
type AppEngineService struct {
	Id string
	// Split maps version ID to the fraction (0-1) of traffic it receives.
	// Fractions across a service's Split sum to 1.
	Split map[string]float64
}

// Version is one deployed version of an AppEngineService.
type Version struct {
	Id            string
	ServingStatus string // SERVING | STOPPED
	Runtime       string
	Env           string // "standard" | "flexible"
	InstanceClass string
	CreateTime    time.Time
	VersionUrl    string
	Threadsafe    bool
	// Traffic is this version's fraction (0-1) of its service's traffic
	// split, looked up from the owning AppEngineService.Split by ID.
	Traffic float64
}

// Instance is one running instance of a Version.
type Instance struct {
	Id           string
	VmStatus     string // flex only; empty for standard-environment instances
	Availability string
	StartTime    time.Time
	Requests     int64 // since instance start
	Errors       int64 // since instance start
	Qps          float64
}
