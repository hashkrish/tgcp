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
