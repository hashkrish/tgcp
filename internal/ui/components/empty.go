package components

import (
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
)

// emptyMessages are playful one-liners shown when a resource list returns
// zero rows. Keyed by resource type; the "default" entry is used as a
// fallback. Messages are picked deterministically by hour-of-day so the
// text stays stable during a session but varies day-to-day.
var emptyMessages = map[string][]string{
	"instances": {
		"No instances running. Quiet day in the cloud.",
		"Zero instances. Savings incoming.",
		"No machines here — fresh start.",
	},
	"buckets": {
		"Zero buckets. A clean canvas.",
		"No storage yet — empty shelves.",
	},
	"clusters": {
		"No clusters here. Ready when you are.",
		"Zero clusters — ship's waiting in dock.",
	},
	"databases": {
		"No databases. A fresh slate.",
		"Empty data tier — pristine.",
	},
	"disks": {
		"No disks attached. Light packing.",
	},
	"logs": {
		"The logs are silent. Good news, usually.",
		"Nothing to report — quiet is golden.",
	},
	"recommendations": {
		"No recommendations. You're running a tight ship.",
		"Nothing to optimize — nice.",
	},
	"budgets": {
		"No budgets configured. Set one up to track spend.",
		"Budget radar is clear.",
	},
	"jobs": {
		"No jobs running. Batch queue is empty.",
	},
	"topics": {
		"No topics yet. Ready to publish.",
	},
	"subscriptions": {
		"No subscriptions. Nobody's listening.",
	},
	"secrets": {
		"No secrets stored. Or they're really well hidden.",
	},
	"services": {
		"No services deployed. The stage is yours.",
	},
	"images": {
		"No images in this registry.",
	},
	"builds": {
		"No builds running. All quiet.",
	},
	"results": {
		"No rows returned.",
		"Query ran clean — nothing to show.",
	},
	"filestore instances": {
		"No filestore instances. Nothing to mount yet.",
	},
	"networks": {
		"No networks here. A blank map.",
	},
	"subnets": {
		"No subnets carved out yet.",
	},
	"firewalls": {
		"No firewall rules. Wide open, or not set up yet.",
	},
	"instance groups": {
		"No instance groups. Nothing managed here.",
	},
	"messages": {
		"No messages waiting. Quiet queue.",
	},
	"queues": {
		"No queues configured.",
	},
	"repositories": {
		"No repositories yet. A fresh registry.",
	},
	"functions": {
		"No functions deployed. Nothing to call.",
	},
	"IAM bindings": {
		"No IAM bindings on this resource.",
	},
	"tables": {
		"No tables in this instance.",
	},
	"uptime checks": {
		"No uptime checks configured.",
	},
	"alert policies": {
		"No alert policies configured.",
	},
	"default": {
		"Nothing here yet.",
		"Quiet in this corner.",
		"Empty — peaceful, huh?",
	},
}

// EmptyState renders a subtle, italicised one-liner for zero-result views.
// resourceType selects the message pool ("instances", "buckets", ...); an
// unknown key falls back to the "default" pool.
func EmptyState(resourceType string) string {
	pool, ok := emptyMessages[resourceType]
	if !ok || len(pool) == 0 {
		pool = emptyMessages["default"]
	}
	msg := pool[time.Now().Hour()%len(pool)]

	return lipgloss.NewStyle().
		Foreground(styles.ColorTextMuted).
		Italic(true).
		Padding(styles.SpaceS, styles.SpaceM).
		Render("‹ " + msg + " ›")
}
