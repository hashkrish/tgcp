package pubsub

type Topic struct {
	Name           string // Short name
	ProjectID      string
	Labels         map[string]string
	KmsKeyName     string
	MessageStorage string // Config info
}

// IAMBinding is a single role -> members pair from a topic's IAM policy.
type IAMBinding struct {
	Role    string
	Members []string
}

type Subscription struct {
	Name              string
	Topic             string
	PushEndpoint      string // Empty if pull
	AckDeadline       int
	RetainAcked       bool
	RetentionDuration string
	DeadLetterTopic   string // Alerting
	State             string // Active/ResourceError/etc
}
