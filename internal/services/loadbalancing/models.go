package loadbalancing

// BackendService is a Compute Engine backend service (the target that a
// load balancer's URL map / forwarding rule ultimately routes to).
type BackendService struct {
	Name                string
	Region              string // "global" for global backend services
	Protocol            string
	LoadBalancingScheme string
	HealthCheckName     string // short name of the first attached health check, if any
	BackendCount        int
}

// HealthCheck is a Compute Engine health check resource.
type HealthCheck struct {
	Name               string
	Region             string // "global" for global health checks
	Type               string // HTTP, HTTPS, HTTP2, TCP, SSL, GRPC
	Port               int64
	CheckIntervalSec   int64
	TimeoutSec         int64
	HealthyThreshold   int64
	UnhealthyThreshold int64
}
