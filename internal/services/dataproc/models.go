package dataproc

type Cluster struct {
	Name          string
	ProjectID     string
	Status        string // RUNNING, ERROR
	MasterMachine string // n1-standard-4
	WorkerCount   int
	WorkerMachine string
	Zone          string

	ClusterUUID    string
	StatusDetail   string
	StateStartTime string
	ConfigBucket   string
	Labels         map[string]string
}

// JobInfo represents one Dataproc job as returned by jobs.list.
type JobInfo struct {
	ID          string
	ClusterName string
	Type        string // Spark, Hadoop, Hive, Pig, PySpark, Unknown
	State       string
}

// IAMBinding is one role -> members grant from a cluster's IAM policy.
type IAMBinding struct {
	Role    string
	Members []string
}
