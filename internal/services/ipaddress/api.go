package ipaddress

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
	svc, err := compute.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("compute client: %w", err)
	}
	return &Client{service: svc}, nil
}

// lastPathSegment extracts the trailing segment of a resource URL, e.g.
// ".../regions/us-central1" -> "us-central1".
func lastPathSegment(url string) string {
	parts := strings.Split(url, "/")
	return parts[len(parts)-1]
}

func toAddress(a *compute.Address, region string) Address {
	return Address{
		Name:        a.Name,
		Address:     a.Address,
		Region:      region,
		AddressType: a.AddressType,
		IPVersion:   a.IpVersion,
		Status:      a.Status,
		NetworkTier: a.NetworkTier,
		Purpose:     a.Purpose,
		Description: a.Description,
		Users:       a.Users,
		Created:     a.CreationTimestamp,
	}
}

// ListAddresses returns every reserved IP address in the project -- both
// regional (via an aggregated list across all regions) and global (used by
// global external load balancers/Cloud CDN).
func (c *Client) ListAddresses(projectID string) ([]Address, error) {
	if demo.Enabled {
		return []Address{}, nil
	}

	var addrs []Address

	// Regional addresses, all regions in one call.
	req := c.service.Addresses.AggregatedList(projectID)
	if err := req.Pages(context.Background(), func(page *compute.AddressAggregatedList) error {
		for scope, scopedList := range page.Items {
			for _, a := range scopedList.Addresses {
				region := a.Region
				if region != "" {
					region = lastPathSegment(region)
				} else {
					// Some responses only set the scope key (e.g.
					// "regions/us-central1"), not the Address's own Region
					// field -- fall back to it.
					region = lastPathSegment(scope)
				}
				addrs = append(addrs, toAddress(a, region))
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("list regional addresses: %w", err)
	}

	// Global addresses.
	greq := c.service.GlobalAddresses.List(projectID)
	if err := greq.Pages(context.Background(), func(page *compute.AddressList) error {
		for _, a := range page.Items {
			addrs = append(addrs, toAddress(a, ""))
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("list global addresses: %w", err)
	}

	return addrs, nil
}

// CreateAddress reserves a new static IP address, matching `gcloud compute
// addresses create`. An empty region reserves a global address (used by
// global external load balancers/Cloud CDN); anything else reserves a
// regional one. addressType is "EXTERNAL" or "INTERNAL" (defaults to
// EXTERNAL if empty, matching the API's own default).
func (c *Client) CreateAddress(projectID, region, name, addressType string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}

	addr := &compute.Address{
		Name:        name,
		AddressType: addressType,
	}

	if region == "" {
		_, err := c.service.GlobalAddresses.Insert(projectID, addr).Do()
		return err
	}
	_, err := c.service.Addresses.Insert(projectID, region, addr).Do()
	return err
}

// DeleteAddress releases a reserved static IP address, matching `gcloud
// compute addresses delete`. The Compute API refuses to release an address
// that's still IN_USE.
func (c *Client) DeleteAddress(projectID, region, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}

	if region == "" {
		_, err := c.service.GlobalAddresses.Delete(projectID, name).Do()
		return err
	}
	_, err := c.service.Addresses.Delete(projectID, region, name).Do()
	return err
}
