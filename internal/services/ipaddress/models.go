package ipaddress

// Address represents a reserved (static) IP address -- either regional
// (Region set) or global (Region empty, used for global external
// load-balancer/CDN-style addresses via GlobalAddresses). This deliberately
// does not cover ephemeral IPs (those aren't a reservable resource of their
// own -- they belong to whatever instance/forwarding-rule holds them).
type Address struct {
	Name        string
	Address     string // the actual reserved IP
	Region      string // empty for a global address
	AddressType string // EXTERNAL or INTERNAL
	IPVersion   string // IPV4 or IPV6
	Status      string // RESERVED or IN_USE
	NetworkTier string // PREMIUM or STANDARD
	Purpose     string // e.g. GCE_ENDPOINT, VPC_PEERING, PRIVATE_SERVICE_CONNECT
	Description string
	Users       []string // resource URLs currently using this address, if any
	Created     string   // RFC3339
}

// IsGlobal returns true for a global address (created via GlobalAddresses,
// used by global external load balancers/Cloud CDN), as opposed to a
// regional one.
func (a Address) IsGlobal() bool {
	return a.Region == ""
}

// Scope returns a's region name, or "global" for a global address --
// intended for display (table/detail views) and to disambiguate two
// addresses of the same name across a regional/global pair.
func (a Address) Scope() string {
	if a.IsGlobal() {
		return "global"
	}
	return a.Region
}
