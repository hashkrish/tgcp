package dns

// Zone represents a Cloud DNS managed zone (read-only view).
type Zone struct {
	Name        string // User-assigned zone name
	DNSName     string // e.g. "example.com."
	Visibility  string // PUBLIC or PRIVATE
	Description string
	CreateTime  string // RFC3339 text, as returned by the API
}

// RecordSet represents a DNS resource record set within a managed zone.
type RecordSet struct {
	Name    string
	Type    string // A, AAAA, CNAME, MX, TXT, NS, SOA, ...
	TTL     int64
	Rrdatas []string
}
