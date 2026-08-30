package monitoring

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	gmonitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	dashboard "cloud.google.com/go/monitoring/dashboard/apiv1"
	"cloud.google.com/go/monitoring/dashboard/apiv1/dashboardpb"
	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/genproto/googleapis/api/monitoredres"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Client wraps the Cloud Monitoring sub-clients this service needs. Uptime
// checks, Alert policies, Dashboards, and Snoozes all live under the Cloud
// Monitoring API surface, but are exposed via separate typed clients rather
// than one combined client — so we hold all of them here.
type Client struct {
	uptime    *gmonitoring.UptimeCheckClient
	alert     *gmonitoring.AlertPolicyClient
	dashboard *dashboard.DashboardsClient
	snooze    *gmonitoring.SnoozeClient
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	uptimeClient, err := gmonitoring.NewUptimeCheckClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("monitoring uptime check client: %w", err)
	}
	alertClient, err := gmonitoring.NewAlertPolicyClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("monitoring alert policy client: %w", err)
	}
	dashboardClient, err := dashboard.NewDashboardsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("monitoring dashboards client: %w", err)
	}
	snoozeClient, err := gmonitoring.NewSnoozeClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("monitoring snooze client: %w", err)
	}
	return &Client{uptime: uptimeClient, alert: alertClient, dashboard: dashboardClient, snooze: snoozeClient}, nil
}

// ListUptimeChecks lists all Uptime check configurations for the project.
// Uptime checks are a global (non-region-scoped) resource, so this is a
// single List call rather than the region fan-out pattern used elsewhere.
func (c *Client) ListUptimeChecks(projectID string) ([]UptimeCheck, error) {
	if demo.Enabled {
		return []UptimeCheck{}, nil
	}
	if c.uptime == nil {
		return nil, fmt.Errorf("client not init")
	}
	ctx := context.Background()
	parent := fmt.Sprintf("projects/%s", projectID)

	var checks []UptimeCheck
	it := c.uptime.ListUptimeCheckConfigs(ctx, &monitoringpb.ListUptimeCheckConfigsRequest{Parent: parent})
	for cfg, err := range it.All() {
		if err != nil {
			return nil, fmt.Errorf("list uptime check configs: %w", err)
		}
		checks = append(checks, toUptimeCheck(cfg))
	}
	return checks, nil
}

// ListAlertPolicies lists all alerting policies for the project.
func (c *Client) ListAlertPolicies(projectID string) ([]AlertPolicy, error) {
	if demo.Enabled {
		return []AlertPolicy{}, nil
	}
	if c.alert == nil {
		return nil, fmt.Errorf("client not init")
	}
	ctx := context.Background()
	parent := fmt.Sprintf("projects/%s", projectID)

	var policies []AlertPolicy
	it := c.alert.ListAlertPolicies(ctx, &monitoringpb.ListAlertPoliciesRequest{Name: parent})
	for p, err := range it.All() {
		if err != nil {
			return nil, fmt.Errorf("list alert policies: %w", err)
		}
		policies = append(policies, toAlertPolicy(p))
	}
	return policies, nil
}

// CreateUptimeCheck creates a new HTTP/HTTPS Uptime check config targeting
// the given host. This is a minimal-viable create form, not full parity with
// `gcloud monitoring uptime create` (no TCP checks, content matchers, or
// selected regions here).
func (c *Client) CreateUptimeCheck(projectID string, opts UptimeCheckCreateOpts) error {
	if demo.Enabled {
		return nil
	}
	if c.uptime == nil {
		return fmt.Errorf("client not init")
	}

	interval, err := strconv.ParseInt(opts.CheckIntervalSec, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid check interval %q: %w", opts.CheckIntervalSec, err)
	}

	cfg := &monitoringpb.UptimeCheckConfig{
		DisplayName: opts.DisplayName,
		Resource: &monitoringpb.UptimeCheckConfig_MonitoredResource{
			MonitoredResource: &monitoredres.MonitoredResource{
				Type:   "uptime_url",
				Labels: map[string]string{"host": opts.Host},
			},
		},
		CheckRequestType: &monitoringpb.UptimeCheckConfig_HttpCheck_{
			HttpCheck: &monitoringpb.UptimeCheckConfig_HttpCheck{
				UseSsl: strings.EqualFold(opts.Protocol, "HTTPS"),
				Path:   opts.Path,
			},
		},
		Period:  durationpb.New(time.Duration(interval) * time.Second),
		Timeout: durationpb.New(10 * time.Second),
	}

	ctx := context.Background()
	_, err = c.uptime.CreateUptimeCheckConfig(ctx, &monitoringpb.CreateUptimeCheckConfigRequest{
		Parent:            fmt.Sprintf("projects/%s", projectID),
		UptimeCheckConfig: cfg,
	})
	return err
}

// UpdateUptimeCheckPeriod patches an uptime check's check interval,
// matching `gcloud monitoring uptime update --period`. HTTP/path/host,
// content matchers, and alert-policy fields are out of scope for this
// minimal Update flow.
func (c *Client) UpdateUptimeCheckPeriod(fullName string, periodSec int64) error {
	if demo.Enabled {
		return nil
	}
	if c.uptime == nil {
		return fmt.Errorf("client not init")
	}
	cfg := &monitoringpb.UptimeCheckConfig{
		Name:   fullName,
		Period: durationpb.New(time.Duration(periodSec) * time.Second),
	}
	_, err := c.uptime.UpdateUptimeCheckConfig(context.Background(), &monitoringpb.UpdateUptimeCheckConfigRequest{
		UptimeCheckConfig: cfg,
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{"period"},
		},
	})
	return err
}

// DeleteUptimeCheck deletes an uptime check config, matching
// `gcloud monitoring uptime delete`.
func (c *Client) DeleteUptimeCheck(fullName string) error {
	if demo.Enabled {
		return nil
	}
	if c.uptime == nil {
		return fmt.Errorf("client not init")
	}
	return c.uptime.DeleteUptimeCheckConfig(context.Background(), &monitoringpb.DeleteUptimeCheckConfigRequest{Name: fullName})
}

// CreateAlertPolicy creates a single-condition metric-threshold alerting
// policy, matching the simplest shape of `gcloud alpha monitoring policies
// create`. Multi-condition policies, notification channels, and condition
// kinds other than a metric threshold (absence, log match, MQL, PromQL,
// SQL) are deliberately out of scope for this minimal-viable Create flow.
func (c *Client) CreateAlertPolicy(projectID string, opts AlertPolicyCreateOpts) error {
	if demo.Enabled {
		return nil
	}
	if c.alert == nil {
		return fmt.Errorf("client not init")
	}

	threshold, err := strconv.ParseFloat(opts.ThresholdValue, 64)
	if err != nil {
		return fmt.Errorf("invalid threshold value %q: %w", opts.ThresholdValue, err)
	}
	durationSec, err := strconv.ParseInt(opts.DurationSec, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", opts.DurationSec, err)
	}

	policy := &monitoringpb.AlertPolicy{
		DisplayName: opts.DisplayName,
		Combiner:    monitoringpb.AlertPolicy_OR,
		Conditions: []*monitoringpb.AlertPolicy_Condition{
			{
				DisplayName: opts.DisplayName,
				Condition: &monitoringpb.AlertPolicy_Condition_ConditionThreshold{
					ConditionThreshold: &monitoringpb.AlertPolicy_Condition_MetricThreshold{
						Filter:         opts.MetricFilter,
						Comparison:     comparisonFromSymbol(opts.Comparison),
						ThresholdValue: threshold,
						Duration:       durationpb.New(time.Duration(durationSec) * time.Second),
					},
				},
			},
		},
	}

	ctx := context.Background()
	_, err = c.alert.CreateAlertPolicy(ctx, &monitoringpb.CreateAlertPolicyRequest{
		Name:        fmt.Sprintf("projects/%s", projectID),
		AlertPolicy: policy,
	})
	return err
}

func comparisonFromSymbol(s string) monitoringpb.ComparisonType {
	switch s {
	case ">":
		return monitoringpb.ComparisonType_COMPARISON_GT
	case ">=":
		return monitoringpb.ComparisonType_COMPARISON_GE
	case "<":
		return monitoringpb.ComparisonType_COMPARISON_LT
	case "<=":
		return monitoringpb.ComparisonType_COMPARISON_LE
	case "==":
		return monitoringpb.ComparisonType_COMPARISON_EQ
	case "!=":
		return monitoringpb.ComparisonType_COMPARISON_NE
	default:
		return monitoringpb.ComparisonType_COMPARISON_GT
	}
}

// ListDashboards lists all custom dashboards for the project.
func (c *Client) ListDashboards(projectID string) ([]Dashboard, error) {
	if demo.Enabled {
		return []Dashboard{}, nil
	}
	if c.dashboard == nil {
		return nil, fmt.Errorf("client not init")
	}
	ctx := context.Background()
	var out []Dashboard
	it := c.dashboard.ListDashboards(ctx, &dashboardpb.ListDashboardsRequest{
		Parent: fmt.Sprintf("projects/%s", projectID),
	})
	for d, err := range it.All() {
		if err != nil {
			return nil, fmt.Errorf("list dashboards: %w", err)
		}
		out = append(out, Dashboard{Name: shortName(d.GetName()), FullName: d.GetName(), DisplayName: d.GetDisplayName()})
	}
	return out, nil
}

// DeleteDashboard deletes a custom dashboard, matching
// `gcloud monitoring dashboards delete`. Dashboard *creation* is
// deliberately not implemented — a dashboard's layout is an arbitrary,
// deeply nested JSON structure (grid/mosaic layout + widgets), which
// doesn't fit this codebase's simple-form Create pattern; use the Cloud
// Console or `gcloud monitoring dashboards create --config-from-file`
// instead.
func (c *Client) DeleteDashboard(fullName string) error {
	if demo.Enabled {
		return nil
	}
	if c.dashboard == nil {
		return fmt.Errorf("client not init")
	}
	return c.dashboard.DeleteDashboard(context.Background(), &dashboardpb.DeleteDashboardRequest{Name: fullName})
}

// ListSnoozes lists all Snoozes for the project (both active and expired;
// the API has no built-in "active only" filter parameter beyond a raw
// AIP-160 filter string, which this minimal wrapper doesn't expose).
func (c *Client) ListSnoozes(projectID string) ([]Snooze, error) {
	if demo.Enabled {
		return []Snooze{}, nil
	}
	if c.snooze == nil {
		return nil, fmt.Errorf("client not init")
	}
	ctx := context.Background()
	var out []Snooze
	it := c.snooze.ListSnoozes(ctx, &monitoringpb.ListSnoozesRequest{
		Parent: fmt.Sprintf("projects/%s", projectID),
	})
	for sn, err := range it.All() {
		if err != nil {
			return nil, fmt.Errorf("list snoozes: %w", err)
		}
		s := Snooze{Name: shortName(sn.GetName()), FullName: sn.GetName(), DisplayName: sn.GetDisplayName()}
		if c := sn.GetCriteria(); c != nil {
			s.Policies = c.GetPolicies()
		}
		if iv := sn.GetInterval(); iv != nil {
			s.StartTime = iv.GetStartTime().AsTime()
			s.EndTime = iv.GetEndTime().AsTime()
		}
		out = append(out, s)
	}
	return out, nil
}

// CreateSnooze creates a Snooze that suppresses alerts from the given alert
// policy for durationMinutes starting now, matching
// `gcloud alpha monitoring snoozes create`.
func (c *Client) CreateSnooze(projectID string, opts SnoozeCreateOpts) error {
	if demo.Enabled {
		return nil
	}
	if c.snooze == nil {
		return fmt.Errorf("client not init")
	}
	durationMin, err := strconv.ParseInt(opts.DurationMinutes, 10, 64)
	if err != nil || durationMin <= 0 {
		return fmt.Errorf("invalid duration minutes %q", opts.DurationMinutes)
	}

	start := time.Now()
	end := start.Add(time.Duration(durationMin) * time.Minute)

	snooze := &monitoringpb.Snooze{
		DisplayName: opts.DisplayName,
		Criteria:    &monitoringpb.Snooze_Criteria{Policies: []string{opts.AlertPolicyFullName}},
		Interval: &monitoringpb.TimeInterval{
			StartTime: timestamppb.New(start),
			EndTime:   timestamppb.New(end),
		},
	}

	ctx := context.Background()
	_, err = c.snooze.CreateSnooze(ctx, &monitoringpb.CreateSnoozeRequest{
		Parent: fmt.Sprintf("projects/%s", projectID),
		Snooze: snooze,
	})
	return err
}

// DeleteAlertPolicy deletes an alerting policy, matching
// `gcloud alpha monitoring policies delete`.
func (c *Client) DeleteAlertPolicy(fullName string) error {
	if demo.Enabled {
		return nil
	}
	if c.alert == nil {
		return fmt.Errorf("client not init")
	}
	return c.alert.DeleteAlertPolicy(context.Background(), &monitoringpb.DeleteAlertPolicyRequest{Name: fullName})
}

func toUptimeCheck(cfg *monitoringpb.UptimeCheckConfig) UptimeCheck {
	uc := UptimeCheck{
		Name:        shortName(cfg.GetName()),
		FullName:    cfg.GetName(),
		DisplayName: cfg.GetDisplayName(),
		Period:      cfg.GetPeriod().AsDuration().String(),
		Timeout:     cfg.GetTimeout().AsDuration().String(),
	}

	if mr := cfg.GetMonitoredResource(); mr != nil {
		uc.ResourceType = mr.GetType()
		labels := mr.GetLabels()
		if h, ok := labels["host"]; ok {
			uc.Host = h
		} else if h, ok := labels["hostname"]; ok {
			uc.Host = h
		}
	} else if rg := cfg.GetResourceGroup(); rg != nil {
		uc.ResourceType = "resource_group"
	} else if cfg.GetSyntheticMonitor() != nil {
		uc.ResourceType = "synthetic_monitor"
	}

	switch {
	case cfg.GetHttpCheck() != nil:
		hc := cfg.GetHttpCheck()
		if hc.GetUseSsl() {
			uc.CheckType = "HTTPS"
		} else {
			uc.CheckType = "HTTP"
		}
		uc.Path = hc.GetPath()
		uc.Port = hc.GetPort()
	case cfg.GetTcpCheck() != nil:
		uc.CheckType = "TCP"
		uc.Port = cfg.GetTcpCheck().GetPort()
	default:
		uc.CheckType = "UNKNOWN"
	}

	for _, cm := range cfg.GetContentMatchers() {
		uc.ContentMatchers = append(uc.ContentMatchers, ContentMatcher{
			Content: cm.GetContent(),
			Matcher: cm.GetMatcher().String(),
		})
	}

	return uc
}

func toAlertPolicy(p *monitoringpb.AlertPolicy) AlertPolicy {
	// Enabled defaults to true when unset, per the API's documented
	// interpretation of a missing `enabled` field.
	enabled := true
	if p.GetEnabled() != nil {
		enabled = p.GetEnabled().GetValue()
	}

	ap := AlertPolicy{
		Name:                     shortName(p.GetName()),
		FullName:                 p.GetName(),
		DisplayName:              p.GetDisplayName(),
		Enabled:                  enabled,
		Combiner:                 p.GetCombiner().String(),
		NotificationChannelCount: len(p.GetNotificationChannels()),
	}

	for _, cond := range p.GetConditions() {
		ap.Conditions = append(ap.Conditions, toAlertCondition(cond))
	}

	return ap
}

func toAlertCondition(cond *monitoringpb.AlertPolicy_Condition) AlertCondition {
	ac := AlertCondition{DisplayName: cond.GetDisplayName()}

	switch {
	case cond.GetConditionThreshold() != nil:
		ac.Kind = "Metric Threshold"
		mt := cond.GetConditionThreshold()
		ac.MetricFilter = mt.GetFilter()
		ac.Comparison = comparisonSymbol(mt.GetComparison())
		ac.Threshold = strconv.FormatFloat(mt.GetThresholdValue(), 'g', -1, 64)
		ac.Duration = mt.GetDuration().AsDuration().String()
	case cond.GetConditionAbsent() != nil:
		ac.Kind = "Metric Absence"
		ma := cond.GetConditionAbsent()
		ac.MetricFilter = ma.GetFilter()
		ac.Duration = ma.GetDuration().AsDuration().String()
	case cond.GetConditionMatchedLog() != nil:
		ac.Kind = "Log Match"
		ac.MetricFilter = cond.GetConditionMatchedLog().GetFilter()
	case cond.GetConditionMonitoringQueryLanguage() != nil:
		ac.Kind = "MQL"
		ac.Duration = cond.GetConditionMonitoringQueryLanguage().GetDuration().AsDuration().String()
	case cond.GetConditionPrometheusQueryLanguage() != nil:
		ac.Kind = "PromQL"
	case cond.GetConditionSql() != nil:
		ac.Kind = "SQL"
	default:
		ac.Kind = "Unknown"
	}

	return ac
}

func comparisonSymbol(c monitoringpb.ComparisonType) string {
	switch c {
	case monitoringpb.ComparisonType_COMPARISON_GT:
		return ">"
	case monitoringpb.ComparisonType_COMPARISON_GE:
		return ">="
	case monitoringpb.ComparisonType_COMPARISON_LT:
		return "<"
	case monitoringpb.ComparisonType_COMPARISON_LE:
		return "<="
	case monitoringpb.ComparisonType_COMPARISON_EQ:
		return "=="
	case monitoringpb.ComparisonType_COMPARISON_NE:
		return "!="
	default:
		return ""
	}
}

func shortName(longName string) string {
	parts := strings.Split(longName, "/")
	return parts[len(parts)-1]
}
