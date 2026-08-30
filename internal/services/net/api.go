package net

import (
	"context"
	"fmt"
	"strings"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/compute/v1"
)

type Client struct {
	service *compute.Service
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	s, err := compute.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create compute service: %w", err)
	}
	return &Client{service: s}, nil
}

func (c *Client) ListNetworks(projectID string) ([]Network, error) {
	if demo.Enabled {
		return []Network{}, nil
	}
	var networks []Network
	req := c.service.Networks.List(projectID)
	if err := req.Pages(context.Background(), func(page *compute.NetworkList) error {
		for _, n := range page.Items {
			mode := "CUSTOM"
			if n.AutoCreateSubnetworks {
				mode = "AUTO"
			} else if n.IPv4Range != "" {
				mode = "LEGACY"
			}

			networks = append(networks, Network{
				Name:        n.Name,
				ID:          n.Id,
				SelfLink:    n.SelfLink,
				IPv4Range:   n.IPv4Range,
				Mode:        mode,
				GatewayIPv4: n.GatewayIPv4,
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return networks, nil
}

func (c *Client) ListSubnets(projectID string, networkLink string) ([]Subnet, error) {
	if demo.Enabled {
		return []Subnet{}, nil
	}
	// Subnets are regional (AggregatedList)
	var subnets []Subnet
	req := c.service.Subnetworks.AggregatedList(projectID)
	// Filter by network? API filter string: "network eq link"
	req.Filter(fmt.Sprintf("network eq \"%s\"", networkLink))

	if err := req.Pages(context.Background(), func(page *compute.SubnetworkAggregatedList) error {
		for _, items := range page.Items {
			for _, s := range items.Subnetworks {
				subnets = append(subnets, Subnet{
					Name:        s.Name,
					Region:      extractRegion(s.Region),
					IPCidrRange: s.IpCidrRange,
					Gateway:     s.GatewayAddress,
					Network:     s.Network,
				})
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return subnets, nil
}

func (c *Client) ListFirewalls(projectID string, networkLink string) ([]Firewall, error) {
	if demo.Enabled {
		return []Firewall{}, nil
	}
	var firewalls []Firewall
	req := c.service.Firewalls.List(projectID)
	req.Filter(fmt.Sprintf("network eq \"%s\"", networkLink))

	if err := req.Pages(context.Background(), func(page *compute.FirewallList) error {
		for _, f := range page.Items {
			action := "ALLOW"
			if len(f.Denied) > 0 {
				action = "DENY"
			}

			direction := f.Direction

			// Source/Target formatting
			var source string
			if direction == "INGRESS" {
				if len(f.SourceRanges) > 0 {
					source = fmt.Sprintf("IPs: %v", truncateList(f.SourceRanges))
				} else if len(f.SourceTags) > 0 {
					source = fmt.Sprintf("Tags: %v", truncateList(f.SourceTags))
				} else {
					source = "All"
				}
			} else {
				if len(f.DestinationRanges) > 0 {
					source = fmt.Sprintf("Dest: %v", truncateList(f.DestinationRanges))
				} else {
					source = "All"
				}
			}

			var target string
			if len(f.TargetTags) > 0 {
				target = fmt.Sprintf("Tags: %v", truncateList(f.TargetTags))
			} else {
				target = "All Instances"
			}

			firewalls = append(firewalls, Firewall{
				Name:      f.Name,
				Network:   f.Network,
				Direction: direction,
				Priority:  f.Priority,
				Action:    action,
				Source:    source,
				Target:    target,
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return firewalls, nil
}

// CreateFirewallRule creates a new firewall rule in the given network.
// Only a single ALLOW/DENY rule with one protocol is supported here — this
// is a minimal-viable create form, not full parity with `gcloud compute
// firewall-rules create`.
func (c *Client) CreateFirewallRule(projectID string, opts FirewallCreateOpts) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}

	fw := &compute.Firewall{
		Name:         opts.Name,
		Network:      fmt.Sprintf("projects/%s/global/networks/%s", projectID, opts.Network),
		Direction:    opts.Direction,
		SourceRanges: splitAndTrim(opts.SourceRanges),
	}

	rule := &compute.FirewallAllowed{
		IPProtocol: opts.Protocol,
		Ports:      splitAndTrim(opts.Ports),
	}
	if strings.EqualFold(opts.Action, "DENY") {
		fw.Denied = []*compute.FirewallDenied{{IPProtocol: opts.Protocol, Ports: rule.Ports}}
	} else {
		fw.Allowed = []*compute.FirewallAllowed{rule}
	}

	_, err := c.service.Firewalls.Insert(projectID, fw).Do()
	return err
}

// UpdateFirewallPriority patches a firewall rule's priority, matching
// `gcloud compute firewall-rules update --priority`. Rule action, ports,
// source/target ranges, and expand-ip-range are out of scope for this
// minimal Update flow.
func (c *Client) UpdateFirewallPriority(projectID, name string, priority int64) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	_, err := c.service.Firewalls.Patch(projectID, name, &compute.Firewall{
		Priority: priority,
	}).Do()
	return err
}

// DeleteFirewallRule deletes a VPC firewall rule, matching
// `gcloud compute firewall-rules delete`.
func (c *Client) DeleteFirewallRule(projectID, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	_, err := c.service.Firewalls.Delete(projectID, name).Do()
	return err
}

// CreateNetwork creates a new VPC network, matching `gcloud compute
// networks create`. Only auto-mode vs. custom-mode subnet creation is
// configurable here -- routing mode, MTU, and other advanced options are
// out of scope for this minimal-viable create form.
func (c *Client) CreateNetwork(projectID, name string, autoCreateSubnetworks bool) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	_, err := c.service.Networks.Insert(projectID, &compute.Network{
		Name:                  name,
		AutoCreateSubnetworks: autoCreateSubnetworks,
	}).Do()
	return err
}

// DeleteNetwork deletes a VPC network, matching `gcloud compute networks
// delete`. GCP refuses this while any subnets, firewall rules, or other
// resources still reference the network, so no client-side guard is needed.
func (c *Client) DeleteNetwork(projectID, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	_, err := c.service.Networks.Delete(projectID, name).Do()
	return err
}

// CreateSubnet creates a new subnet in an existing custom-mode network,
// matching `gcloud compute networks subnets create`. Secondary ranges,
// private Google access, and flow logs are out of scope for this
// minimal-viable create form.
func (c *Client) CreateSubnet(projectID, region, name, networkLink, ipCidrRange string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	_, err := c.service.Subnetworks.Insert(projectID, region, &compute.Subnetwork{
		Name:        name,
		Network:     networkLink,
		IpCidrRange: ipCidrRange,
	}).Do()
	return err
}

// DeleteSubnet deletes a subnet, matching `gcloud compute networks subnets
// delete`.
func (c *Client) DeleteSubnet(projectID, region, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	_, err := c.service.Subnetworks.Delete(projectID, region, name).Do()
	return err
}

// AddSubnetIAMBinding grants a role to a member on a subnet, matching
// `gcloud compute networks subnets add-iam-policy-binding`. It fetches the
// current policy, merges the new binding into it, and writes the whole
// policy back -- this never drops any existing binding, unlike a raw
// set-iam-policy.
func (c *Client) AddSubnetIAMBinding(projectID, region, name, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	policy, err := c.service.Subnetworks.GetIamPolicy(projectID, region, name).Do()
	if err != nil {
		return fmt.Errorf("get subnet IAM policy: %w", err)
	}
	policy.Bindings = mergeComputeIAMBinding(policy.Bindings, role, member)
	_, err = c.service.Subnetworks.SetIamPolicy(projectID, region, name, &compute.RegionSetPolicyRequest{Policy: policy}).Do()
	return err
}

// mergeComputeIAMBinding appends member to the existing binding for role if
// one exists (skipping if already granted), or appends a brand-new role
// binding otherwise. It never removes or replaces any other binding.
func mergeComputeIAMBinding(bindings []*compute.Binding, role, member string) []*compute.Binding {
	for _, b := range bindings {
		if b.Role != role {
			continue
		}
		for _, m := range b.Members {
			if m == member {
				return bindings
			}
		}
		b.Members = append(b.Members, member)
		return bindings
	}
	return append(bindings, &compute.Binding{Role: role, Members: []string{member}})
}

func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Helpers

func extractRegion(url string) string {
	parts := strings.Split(url, "/")
	return parts[len(parts)-1]
}

func truncateList(list []string) string {
	if len(list) > 2 {
		return fmt.Sprintf("[%s, %s, +%d]", list[0], list[1], len(list)-2)
	}
	return fmt.Sprintf("%v", list)
}
