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
	Description         string
	Port                int64
	PortName            string
	TimeoutSec          int64
	SessionAffinity     string
	EnableCDN           bool
	SecurityPolicy      string
	CreationTimestamp   string
}

// HealthCheckCreateOpts holds the minimal set of fields needed to create a
// global HTTP/HTTPS/TCP health check via the Create form.
type HealthCheckCreateOpts struct {
	Name             string
	Protocol         string // HTTP, HTTPS, TCP
	Port             string // numeric string
	CheckIntervalSec string // numeric string
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
	Description        string
	LogEnabled         bool
}

// UrlMap is a Compute Engine URL map, routing incoming requests to backend
// services/buckets based on host/path rules.
type UrlMap struct {
	Name           string
	Region         string // "global" for global URL maps
	DefaultService string // short name of the default backend service/bucket
	Description    string
}

// ForwardingRule is a Compute Engine forwarding rule, mapping an IP+port
// range to a target (backend service, target proxy, etc).
type ForwardingRule struct {
	Name                string
	Region              string // "global" for global forwarding rules
	IPAddress           string
	IPProtocol          string
	PortRange           string
	Target              string // short name of the target resource
	LoadBalancingScheme string
}

// SslCertificate is a Compute Engine SSL certificate resource. The
// certificate PEM data and private key are never fetched or rendered here.
type SslCertificate struct {
	Name       string
	Region     string // "global" for global SSL certificates
	Type       string // MANAGED or SELF_MANAGED
	Domains    []string
	ExpireTime string
}
