package kms

import (
	"context"
	"fmt"
	"strings"

	gkms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/yogirk/tgcp/internal/demo"
	locationpb "google.golang.org/genproto/googleapis/cloud/location"
)

// Client wraps the Cloud KMS Key Management API.
//
// SAFETY: this client only ever calls metadata-listing operations
// (ListLocations, ListKeyRings, ListCryptoKeys). It never calls Encrypt,
// Decrypt, AsymmetricSign, AsymmetricDecrypt, MacSign, MacVerify,
// GetPublicKey, RawEncrypt/RawDecrypt, or any other operation that touches
// or returns key material — those are all intentionally absent from this
// package, not just unused.
type Client struct {
	client *gkms.KeyManagementClient
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	c, err := gkms.NewKeyManagementClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("kms client: %w", err)
	}
	return &Client{client: c}, nil
}

// ListKeyRings lists Cloud KMS key rings across every location the project
// has key rings in. Key rings are location-scoped (ListKeyRings requires a
// parent of the form projects/{project}/locations/{location}), so — like
// Cloud Scheduler/Cloud Tasks — we first discover the project's available
// locations via the Cloud Locations API, then fan out one ListKeyRings call
// per location rather than hardcoding a region list.
func (c *Client) ListKeyRings(projectID string) ([]KeyRing, error) {
	if demo.Enabled {
		return []KeyRing{}, nil
	}
	if c.client == nil {
		return nil, fmt.Errorf("client not init")
	}
	ctx := context.Background()

	locations, err := c.listLocationIDs(ctx, projectID)
	if err != nil {
		return nil, err
	}

	var rings []KeyRing
	for _, loc := range locations {
		parent := fmt.Sprintf("projects/%s/locations/%s", projectID, loc)
		it := c.client.ListKeyRings(ctx, &kmspb.ListKeyRingsRequest{Parent: parent})
		for kr, err := range it.All() {
			if err != nil {
				return nil, fmt.Errorf("list key rings in %s: %w", loc, err)
			}
			rings = append(rings, toKeyRing(kr, loc))
		}
	}
	return rings, nil
}

// ListCryptoKeys lists all crypto keys within a key ring.
func (c *Client) ListCryptoKeys(keyRingName string) ([]CryptoKey, error) {
	if demo.Enabled {
		return []CryptoKey{}, nil
	}
	if c.client == nil {
		return nil, fmt.Errorf("client not init")
	}
	ctx := context.Background()

	var keys []CryptoKey
	it := c.client.ListCryptoKeys(ctx, &kmspb.ListCryptoKeysRequest{Parent: keyRingName})
	for k, err := range it.All() {
		if err != nil {
			return nil, fmt.Errorf("list crypto keys in %s: %w", keyRingName, err)
		}
		keys = append(keys, toCryptoKey(k))
	}
	return keys, nil
}

// listLocationIDs returns the canonical location IDs (e.g. "us-central1",
// "global") available to Cloud KMS for this project.
func (c *Client) listLocationIDs(ctx context.Context, projectID string) ([]string, error) {
	var ids []string
	req := &locationpb.ListLocationsRequest{
		Name: fmt.Sprintf("projects/%s", projectID),
	}
	it := c.client.ListLocations(ctx, req)
	for loc, err := range it.All() {
		if err != nil {
			return nil, fmt.Errorf("list kms locations: %w", err)
		}
		ids = append(ids, loc.LocationId)
	}
	return ids, nil
}

func toKeyRing(kr *kmspb.KeyRing, location string) KeyRing {
	createTime := ""
	if t := kr.GetCreateTime(); t != nil {
		createTime = t.AsTime().Local().Format("2006-01-02 15:04:05 MST")
	}
	return KeyRing{
		Name:       shortName(kr.GetName()),
		FullName:   kr.GetName(),
		Location:   location,
		CreateTime: createTime,
	}
}

func toCryptoKey(k *kmspb.CryptoKey) CryptoKey {
	algorithm := ""
	protectionLevel := ""
	state := ""
	if primary := k.GetPrimary(); primary != nil {
		algorithm = primary.GetAlgorithm().String()
		protectionLevel = primary.GetProtectionLevel().String()
		state = primary.GetState().String()
	}

	rotationPeriod := ""
	if rp := k.GetRotationPeriod(); rp != nil {
		rotationPeriod = rp.AsDuration().String()
	}

	nextRotation := ""
	if t := k.GetNextRotationTime(); t != nil {
		nextRotation = t.AsTime().Local().Format("2006-01-02 15:04:05 MST")
	}

	createTime := ""
	if t := k.GetCreateTime(); t != nil {
		createTime = t.AsTime().Local().Format("2006-01-02 15:04:05 MST")
	}

	return CryptoKey{
		Name:             shortName(k.GetName()),
		Purpose:          k.GetPurpose().String(),
		Algorithm:        algorithm,
		ProtectionLevel:  protectionLevel,
		State:            state,
		RotationPeriod:   rotationPeriod,
		NextRotationTime: nextRotation,
		CreateTime:       createTime,
	}
}

func shortName(longName string) string {
	parts := strings.Split(longName, "/")
	return parts[len(parts)-1]
}
