package redis

type Instance struct {
	Name               string // Short ID
	DisplayName        string
	ProjectID          string
	Location           string
	LocationID         string // Zone the primary node is provisioned in
	CurrentLocationID  string // Zone the primary node currently resides in
	Tier               string // BASIC, STANDARD_HA
	MemorySizeGb       int
	RedisVersion       string // REDIS_6_X
	Host               string
	Port               int
	ReadEndpoint       string
	ReadEndpointPort   int
	State              string // READY, CREATING
	StatusMessage      string
	CreateTime         string
	AuthorizedNetwork  string
	ConnectMode        string
	ReservedIPRange    string
	TransitEncryption  string
	AuthEnabled        bool
	ReplicaCount       int
	ReadReplicasMode   string
	PersistenceMode    string
	CustomerManagedKey string
	Labels             map[string]string
}
