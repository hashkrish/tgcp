package cloudrun

import "time"

type ServiceStatus string

const (
	StatusReady   ServiceStatus = "Ready"
	StatusFailed  ServiceStatus = "Failed"
	StatusUnknown ServiceStatus = "Unknown"
)

type RunService struct {
	Name         string
	Region       string
	URL          string
	Status       ServiceStatus
	LastModified time.Time
	// Image is the container image of the first container in the service's
	// revision template. Populated by ListServices; used to seed the Update
	// form's default value. Empty in demo mode (fixtures don't carry it).
	Image string
}
