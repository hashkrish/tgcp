package gce

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/demo"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

// Client wraps the GCE API service
type Client struct {
	service *compute.Service
}

// NewClient initializes a new GCE API client.
// In demo mode, returns a stub client; List* methods short-circuit to fixtures.
func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}

	httpClient, err := core.NewHTTPClient(ctx, compute.ComputeScope)
	if err != nil {
		return nil, fmt.Errorf("failed to create http client: %w", err)
	}

	svc, err := compute.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("failed to create compute service: %w", err)
	}
	return &Client{service: svc}, nil
}

// ListInstances fetches all instances across all zones (AggregatedList)
func (c *Client) ListInstances(projectID string) ([]Instance, error) {
	if demo.Enabled {
		return loadDemoInstances(), nil
	}
	req := c.service.Instances.AggregatedList(projectID)
	var instances []Instance

	if err := req.Pages(context.Background(), func(page *compute.InstanceAggregatedList) error {
		for zoneKey, items := range page.Items {
			// zoneKey format: "zones/us-central1-a"
			zone := strings.TrimPrefix(zoneKey, "zones/")

			// Skip scopes that might be regions or warnings
			if len(items.Instances) == 0 {
				continue
			}

			for _, inst := range items.Instances {
				// Parse Network Interfaces
				var internalIP, externalIP string
				if len(inst.NetworkInterfaces) > 0 {
					internalIP = inst.NetworkInterfaces[0].NetworkIP
					if len(inst.NetworkInterfaces[0].AccessConfigs) > 0 {
						externalIP = inst.NetworkInterfaces[0].AccessConfigs[0].NatIP
					}
				}

				// Parse Machine Type
				// Format: "https://www.googleapis.com/compute/v1/projects/proj/zones/zone/machineTypes/n1-standard-1"
				parts := strings.Split(inst.MachineType, "/")
				machineType := parts[len(parts)-1]

				// Parse Disks
				var disks []Disk
				for _, d := range inst.Disks {
					// d.Type is not always populated or is a URL
					// If boot disk, it might be in InitializeParams but AttachedDisk also has DiskSizeGb
					diskType := "pd-standard" // Default
					// Try to guess from InitializeParams if exists
					if d.InitializeParams != nil && d.InitializeParams.DiskType != "" {
						// Format: zones/.../diskTypes/pd-ssd
						dtParts := strings.Split(d.InitializeParams.DiskType, "/")
						diskType = dtParts[len(dtParts)-1]
					}

					disks = append(disks, Disk{
						Name:   d.DeviceName,
						SizeGB: d.DiskSizeGb,
						Type:   diskType,
					})
				}

				// Parse Creation Time
				creationTime, _ := time.Parse(time.RFC3339, inst.CreationTimestamp)

				// Determine OS Image
				osImage := "Unknown"
				for _, d := range inst.Disks {
					if d.Boot {
						// Try InitializeParams first
						if d.InitializeParams != nil && d.InitializeParams.SourceImage != "" {
							parts := strings.Split(d.InitializeParams.SourceImage, "/")
							osImage = parts[len(parts)-1]
						} else if len(d.Licenses) > 0 {
							// Fallback to licenses
							parts := strings.Split(d.Licenses[0], "/")
							osImage = parts[len(parts)-1]
						}
						break
					}
				}

				instances = append(instances, Instance{
					ID:           fmt.Sprintf("%d", inst.Id),
					Name:         inst.Name,
					Zone:         zone,
					State:        InstanceState(inst.Status), // Simplified cast
					MachineType:  machineType,
					InternalIP:   internalIP,
					ExternalIP:   externalIP,
					CreationTime: creationTime,
					Tags:         inst.Tags.Items,
					Disks:        disks,
					OSImage:      osImage,
				})
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return instances, nil
}

// ListInstanceGroups fetches all Managed Instance Groups across all zones
// (AggregatedList also returns regional MIGs under "regions/..." keys).
func (c *Client) ListInstanceGroups(projectID string) ([]InstanceGroup, error) {
	if demo.Enabled {
		return nil, nil
	}
	req := c.service.InstanceGroupManagers.AggregatedList(projectID)
	var groups []InstanceGroup

	if err := req.Pages(context.Background(), func(page *compute.InstanceGroupManagerAggregatedList) error {
		for scopeKey, items := range page.Items {
			if len(items.InstanceGroupManagers) == 0 {
				continue
			}

			regional := strings.HasPrefix(scopeKey, "regions/")
			location := strings.TrimPrefix(strings.TrimPrefix(scopeKey, "zones/"), "regions/")

			for _, mig := range items.InstanceGroupManagers {
				templateParts := strings.Split(mig.InstanceTemplate, "/")
				template := templateParts[len(templateParts)-1]

				status := "Stable"
				autoscaling := false
				if mig.Status != nil {
					if !mig.Status.IsStable {
						status = "Updating"
					}
					autoscaling = mig.Status.Autoscaler != ""
				}

				groups = append(groups, InstanceGroup{
					Name:             mig.Name,
					Location:         location,
					Regional:         regional,
					TargetSize:       mig.TargetSize,
					InstanceTemplate: template,
					AutoscalingOn:    autoscaling,
					Status:           status,
				})
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return groups, nil
}

// ResizeInstanceGroup changes a Managed Instance Group's target size,
// matching `gcloud compute instance-groups managed resize`. This is the
// minimal viable Update for MIGs — set-autoscaling and update-instances
// (rolling replace/restart) are separate, higher-risk operations and are
// intentionally out of scope.
func (c *Client) ResizeInstanceGroup(projectID, location, name string, size int64, regional bool) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	if regional {
		_, err := c.service.RegionInstanceGroupManagers.Resize(projectID, location, name, size).Do()
		return err
	}
	_, err := c.service.InstanceGroupManagers.Resize(projectID, location, name, size).Do()
	return err
}

// DeleteInstance deletes a VM instance, matching `gcloud compute instances delete`.
func (c *Client) DeleteInstance(projectID, zone, instanceName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	_, err := c.service.Instances.Delete(projectID, zone, instanceName).Do()
	return err
}

// DeleteInstanceGroup deletes a Managed Instance Group (and all instances it
// manages), matching `gcloud compute instance-groups managed delete`.
func (c *Client) DeleteInstanceGroup(projectID, location, name string, regional bool) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	if regional {
		_, err := c.service.RegionInstanceGroupManagers.Delete(projectID, location, name).Do()
		return err
	}
	_, err := c.service.InstanceGroupManagers.Delete(projectID, location, name).Do()
	return err
}

// StartInstance starts a stopped instance
func (c *Client) StartInstance(projectID, zone, instanceName string) error {
	if demo.Enabled {
		return nil
	}
	_, err := c.service.Instances.Start(projectID, zone, instanceName).Do()
	return err
}

// StopInstance stops a running instance
func (c *Client) StopInstance(projectID, zone, instanceName string) error {
	if demo.Enabled {
		return nil
	}
	_, err := c.service.Instances.Stop(projectID, zone, instanceName).Do()
	return err
}

// ResetInstance performs a hard reset of a running instance, matching
// `gcloud compute instances reset`.
func (c *Client) ResetInstance(projectID, zone, instanceName string) error {
	if demo.Enabled {
		return nil
	}
	_, err := c.service.Instances.Reset(projectID, zone, instanceName).Do()
	return err
}

// SuspendInstance suspends a running instance to disk, matching
// `gcloud compute instances suspend`.
func (c *Client) SuspendInstance(projectID, zone, instanceName string) error {
	if demo.Enabled {
		return nil
	}
	_, err := c.service.Instances.Suspend(projectID, zone, instanceName).Do()
	return err
}

// ResumeInstance resumes a previously-suspended instance, matching
// `gcloud compute instances resume`.
func (c *Client) ResumeInstance(projectID, zone, instanceName string) error {
	if demo.Enabled {
		return nil
	}
	_, err := c.service.Instances.Resume(projectID, zone, instanceName).Do()
	return err
}

// CreateInstance creates a new VM instance with a single boot disk and a
// single network interface with an ephemeral external IP.
func (c *Client) CreateInstance(projectID, zone, name, machineType, sourceImage, network string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}

	inst := &compute.Instance{
		Name:        name,
		MachineType: fmt.Sprintf("zones/%s/machineTypes/%s", zone, machineType),
		Disks: []*compute.AttachedDisk{
			{
				Boot:       true,
				AutoDelete: true,
				InitializeParams: &compute.AttachedDiskInitializeParams{
					SourceImage: sourceImage,
				},
			},
		},
		NetworkInterfaces: []*compute.NetworkInterface{
			{
				Network: fmt.Sprintf("global/networks/%s", network),
				AccessConfigs: []*compute.AccessConfig{
					{Type: "ONE_TO_ONE_NAT", Name: "External NAT"},
				},
			},
		},
	}

	_, err := c.service.Instances.Insert(projectID, zone, inst).Do()
	return err
}

// PerformMaintenanceInstance triggers a manual live migration for the given
// instance, matching `gcloud compute instances perform-maintenance`. Only
// applicable to sole-tenant-node-hosted instances; on any other instance the
// API call fails with a clear error surfaced to the caller as-is.
func (c *Client) PerformMaintenanceInstance(projectID, zone, instanceName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	_, err := c.service.Instances.PerformMaintenance(projectID, zone, instanceName).Do()
	return err
}

// GetInstanceIAMPolicy reads a VM instance's current IAM policy, matching
// `gcloud compute instances get-iam-policy`. Used as the "look before you
// grant" read step before AddInstanceIAMBinding.
func (c *Client) GetInstanceIAMPolicy(projectID, zone, instanceName string) ([]IAMBinding, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("compute client not initialized")
	}
	policy, err := c.service.Instances.GetIamPolicy(projectID, zone, instanceName).Do()
	if err != nil {
		return nil, fmt.Errorf("get instance IAM policy: %w", err)
	}
	var out []IAMBinding
	for _, b := range policy.Bindings {
		out = append(out, IAMBinding{Role: b.Role, Members: b.Members})
	}
	return out, nil
}

// AddInstanceIAMBinding grants role to member on a VM instance, matching
// `gcloud compute instances add-iam-policy-binding`. Fetches the current
// policy, merges the binding in, and writes the whole policy back — this
// never drops any existing binding, unlike a raw set-iam-policy.
func (c *Client) AddInstanceIAMBinding(projectID, zone, instanceName, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	policy, err := c.service.Instances.GetIamPolicy(projectID, zone, instanceName).Do()
	if err != nil {
		return fmt.Errorf("get instance IAM policy: %w", err)
	}
	policy.Bindings = mergeComputeIAMBinding(policy.Bindings, role, member)
	_, err = c.service.Instances.SetIamPolicy(projectID, zone, instanceName, &compute.ZoneSetPolicyRequest{Policy: policy}).Do()
	return err
}

// mergeComputeIAMBinding appends member to the existing binding for role if
// one exists (skipping if already granted), or appends a brand-new role
// binding otherwise. Shared by instance- and MIG-level IAM grants; never
// removes or replaces any other binding in the slice.
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

// CreateInstanceGroup creates a new zonal Managed Instance Group from an
// existing instance template, matching `gcloud compute instance-groups
// managed create`. Regional MIGs are intentionally out of scope for this
// minimal Create flow.
func (c *Client) CreateInstanceGroup(projectID, zone, name, baseInstanceName, instanceTemplate string, targetSize int64) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	mig := &compute.InstanceGroupManager{
		Name:             name,
		BaseInstanceName: baseInstanceName,
		InstanceTemplate: instanceTemplate,
		TargetSize:       targetSize,
	}
	_, err := c.service.InstanceGroupManagers.Insert(projectID, zone, mig).Do()
	return err
}

// instanceURLsInGroup lists the member instances of a zonal MIG, returning
// their URLs for use with StartInstances/StopInstances/ApplyUpdatesToInstances,
// which (aside from ApplyUpdatesToInstances' AllInstances flag) require
// explicit instance URLs rather than an "every instance" shortcut.
func (c *Client) instanceURLsInGroup(projectID, zone, name string) ([]string, error) {
	var urls []string
	req := c.service.InstanceGroupManagers.ListManagedInstances(projectID, zone, name)
	err := req.Pages(context.Background(), func(page *compute.InstanceGroupManagersListManagedInstancesResponse) error {
		for _, mi := range page.ManagedInstances {
			urls = append(urls, mi.Instance)
		}
		return nil
	})
	return urls, err
}

// StartInstancesInGroup starts every instance currently in the MIG,
// matching `gcloud compute instance-groups managed start-instances` without
// an --instances filter.
func (c *Client) StartInstancesInGroup(projectID, zone, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	urls, err := c.instanceURLsInGroup(projectID, zone, name)
	if err != nil {
		return err
	}
	if len(urls) == 0 {
		return nil
	}
	_, err = c.service.InstanceGroupManagers.StartInstances(projectID, zone, name, &compute.InstanceGroupManagersStartInstancesRequest{Instances: urls}).Do()
	return err
}

// StopInstancesInGroup stops every instance currently in the MIG, matching
// `gcloud compute instance-groups managed stop-instances` without an
// --instances filter.
func (c *Client) StopInstancesInGroup(projectID, zone, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	urls, err := c.instanceURLsInGroup(projectID, zone, name)
	if err != nil {
		return err
	}
	if len(urls) == 0 {
		return nil
	}
	_, err = c.service.InstanceGroupManagers.StopInstances(projectID, zone, name, &compute.InstanceGroupManagersStopInstancesRequest{Instances: urls}).Do()
	return err
}

// RollingActionReplaceGroup recreates every instance in the MIG, matching
// `gcloud compute instance-groups managed rolling-action replace`.
func (c *Client) RollingActionReplaceGroup(projectID, zone, name string) error {
	return c.applyUpdatesToGroup(projectID, zone, name, "REPLACE")
}

// RollingActionRestartGroup restarts every instance in the MIG in place,
// matching `gcloud compute instance-groups managed rolling-action restart`.
func (c *Client) RollingActionRestartGroup(projectID, zone, name string) error {
	return c.applyUpdatesToGroup(projectID, zone, name, "RESTART")
}

func (c *Client) applyUpdatesToGroup(projectID, zone, name, minimalAction string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	req := &compute.InstanceGroupManagersApplyUpdatesRequest{
		AllInstances:                true,
		MinimalAction:               minimalAction,
		MostDisruptiveAllowedAction: minimalAction,
	}
	_, err := c.service.InstanceGroupManagers.ApplyUpdatesToInstances(projectID, zone, name, req).Do()
	return err
}

// UpdateInstanceTags replaces the network tags on an existing VM instance.
// This is the minimal viable Update flow for VM Instances: machine-type
// changes require the instance to be stopped first and label/metadata
// updates each need their own fingerprinted Set* call, so they're
// intentionally left out in favor of the single most common
// `gcloud compute instances add-tags`-equivalent operation. The Compute API
// requires re-reading the current tag fingerprint immediately before the
// SetTags call to avoid a conflicting-concurrent-modification error.
func (c *Client) UpdateInstanceTags(projectID, zone, instanceName string, tags []string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	inst, err := c.service.Instances.Get(projectID, zone, instanceName).Do()
	if err != nil {
		return err
	}
	fingerprint := ""
	if inst.Tags != nil {
		fingerprint = inst.Tags.Fingerprint
	}
	_, err = c.service.Instances.SetTags(projectID, zone, instanceName, &compute.Tags{
		Items:       tags,
		Fingerprint: fingerprint,
	}).Do()
	return err
}
