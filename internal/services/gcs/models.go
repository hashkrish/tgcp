package gcs

import "time"

type Bucket struct {
	Name                     string
	Location                 string
	LocationType             string
	StorageClass             string
	Created                  time.Time
	Updated                  time.Time
	VersioningEnabled        bool
	RequesterPays            bool
	UniformBucketLevelAccess bool
	PublicAccessPrevention   string
	AutoclassEnabled         bool
	Labels                   map[string]string
	LifecycleRuleCount       int
	CORSRuleCount            int
	RetentionPeriod          time.Duration
	DefaultKMSKeyName        string
	LoggingBucket            string
}

// IAMBinding is a single role -> members pair from a bucket's IAM policy,
// as returned by GetBucketIAMPolicy.
type IAMBinding struct {
	Role    string
	Members []string
}

type Object struct {
	Name            string
	Size            int64
	Updated         time.Time
	Created         time.Time
	Type            string // "Folder" or ContentType
	StorageClass    string
	Generation      int64
	Metageneration  int64
	CacheControl    string
	ContentEncoding string
	EventBasedHold  bool
	TemporaryHold   bool
	KMSKeyName      string
	MD5             string
	CRC32C          uint32
	Metadata        map[string]string
}
