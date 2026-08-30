package kms

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/iam"
	gkms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/yogirk/tgcp/internal/demo"
	locationpb "google.golang.org/genproto/googleapis/cloud/location"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Client wraps the Cloud KMS Key Management API.
//
// SAFETY: this client only ever calls metadata-listing operations
// (ListLocations, ListKeyRings, ListCryptoKeys) plus the one read-only
// data-plane call below (GetPublicKey, for asymmetric keys only — the public
// half of an asymmetric key is not secret material). It never calls Encrypt,
// Decrypt, AsymmetricSign, AsymmetricDecrypt, MacSign, MacVerify,
// RawEncrypt/RawDecrypt, or any other operation that touches or returns
// private/symmetric key material — those are all intentionally absent from
// this package, not just unused.
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
	primaryVersion := ""
	if primary := k.GetPrimary(); primary != nil {
		algorithm = primary.GetAlgorithm().String()
		protectionLevel = primary.GetProtectionLevel().String()
		state = primary.GetState().String()
		primaryVersion = shortName(primary.GetName())
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
		PrimaryVersion:   primaryVersion,
	}
}

func shortName(longName string) string {
	parts := strings.Split(longName, "/")
	return parts[len(parts)-1]
}

// GetKeyRingIAMPolicy reads a key ring's current IAM policy, matching
// `gcloud kms keyrings get-iam-policy`. Used as the "look before you grant"
// read step before AddKeyRingIAMBinding.
func (c *Client) GetKeyRingIAMPolicy(keyRingFullName string) ([]IAMBinding, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.client == nil {
		return nil, fmt.Errorf("kms client not initialized")
	}
	h := c.client.ResourceIAM(keyRingFullName)
	policy, err := h.Policy(context.Background())
	if err != nil {
		return nil, fmt.Errorf("get key ring IAM policy: %w", err)
	}
	var out []IAMBinding
	for _, role := range policy.Roles() {
		out = append(out, IAMBinding{Role: string(role), Members: policy.Members(role)})
	}
	return out, nil
}

// AddKeyRingIAMBinding grants a role to a member on a key ring, matching
// `gcloud kms keyrings add-iam-policy-binding`. It fetches the current
// policy, merges the new binding in via the client library's Policy.Add
// (which appends rather than replaces), and writes the merged result back —
// this never drops any existing binding, unlike a raw set-iam-policy.
func (c *Client) AddKeyRingIAMBinding(keyRingFullName, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("kms client not initialized")
	}
	ctx := context.Background()
	h := c.client.ResourceIAM(keyRingFullName)
	policy, err := h.Policy(ctx)
	if err != nil {
		return fmt.Errorf("get key ring IAM policy: %w", err)
	}
	policy.Add(member, iam.RoleName(role))
	return h.SetPolicy(ctx, policy)
}

// GetCryptoKeyIAMPolicy reads a crypto key's current IAM policy, matching
// `gcloud kms keys get-iam-policy`.
func (c *Client) GetCryptoKeyIAMPolicy(cryptoKeyFullName string) ([]IAMBinding, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.client == nil {
		return nil, fmt.Errorf("kms client not initialized")
	}
	h := c.client.ResourceIAM(cryptoKeyFullName)
	policy, err := h.Policy(context.Background())
	if err != nil {
		return nil, fmt.Errorf("get crypto key IAM policy: %w", err)
	}
	var out []IAMBinding
	for _, role := range policy.Roles() {
		out = append(out, IAMBinding{Role: string(role), Members: policy.Members(role)})
	}
	return out, nil
}

// AddCryptoKeyIAMBinding grants a role to a member on a crypto key, matching
// `gcloud kms keys add-iam-policy-binding`. Same merge-not-replace semantics
// as AddKeyRingIAMBinding.
func (c *Client) AddCryptoKeyIAMBinding(cryptoKeyFullName, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("kms client not initialized")
	}
	ctx := context.Background()
	h := c.client.ResourceIAM(cryptoKeyFullName)
	policy, err := h.Policy(ctx)
	if err != nil {
		return fmt.Errorf("get crypto key IAM policy: %w", err)
	}
	policy.Add(member, iam.RoleName(role))
	return h.SetPolicy(ctx, policy)
}

// ListCryptoKeyVersions lists the versions of a crypto key, matching
// `gcloud kms keys versions list`.
func (c *Client) ListCryptoKeyVersions(cryptoKeyFullName string) ([]CryptoKeyVersion, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.client == nil {
		return nil, fmt.Errorf("kms client not initialized")
	}
	ctx := context.Background()

	key, err := c.client.GetCryptoKey(ctx, &kmspb.GetCryptoKeyRequest{Name: cryptoKeyFullName})
	if err != nil {
		return nil, fmt.Errorf("get crypto key: %w", err)
	}
	primaryName := ""
	if key.GetPrimary() != nil {
		primaryName = key.GetPrimary().GetName()
	}

	var versions []CryptoKeyVersion
	it := c.client.ListCryptoKeyVersions(ctx, &kmspb.ListCryptoKeyVersionsRequest{Parent: cryptoKeyFullName})
	for v, err := range it.All() {
		if err != nil {
			return nil, fmt.Errorf("list crypto key versions: %w", err)
		}
		createTime := ""
		if t := v.GetCreateTime(); t != nil {
			createTime = t.AsTime().Local().Format("2006-01-02 15:04:05 MST")
		}
		versions = append(versions, CryptoKeyVersion{
			Name:       v.GetName(),
			VersionID:  shortName(v.GetName()),
			State:      v.GetState().String(),
			Primary:    v.GetName() == primaryName,
			CreateTime: createTime,
		})
	}
	return versions, nil
}

// SetPrimaryVersion sets the given version as the crypto key's primary
// version, matching `gcloud kms keys set-primary-version`.
func (c *Client) SetPrimaryVersion(cryptoKeyFullName, versionID string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("kms client not initialized")
	}
	_, err := c.client.UpdateCryptoKeyPrimaryVersion(context.Background(), &kmspb.UpdateCryptoKeyPrimaryVersionRequest{
		Name:               cryptoKeyFullName,
		CryptoKeyVersionId: versionID,
	})
	return err
}

// setVersionState patches a crypto key version's state, backing
// EnableCryptoKeyVersion/DisableCryptoKeyVersion below.
func (c *Client) setVersionState(versionFullName string, state kmspb.CryptoKeyVersion_CryptoKeyVersionState) error {
	if c.client == nil {
		return fmt.Errorf("kms client not initialized")
	}
	_, err := c.client.UpdateCryptoKeyVersion(context.Background(), &kmspb.UpdateCryptoKeyVersionRequest{
		CryptoKeyVersion: &kmspb.CryptoKeyVersion{
			Name:  versionFullName,
			State: state,
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"state"}},
	})
	return err
}

// EnableCryptoKeyVersion re-enables a disabled crypto key version, matching
// `gcloud kms keys versions enable`.
func (c *Client) EnableCryptoKeyVersion(versionFullName string) error {
	if demo.Enabled {
		return nil
	}
	return c.setVersionState(versionFullName, kmspb.CryptoKeyVersion_ENABLED)
}

// DisableCryptoKeyVersion disables a crypto key version so it can no longer
// be used for crypto operations (but can still be re-enabled), matching
// `gcloud kms keys versions disable`.
func (c *Client) DisableCryptoKeyVersion(versionFullName string) error {
	if demo.Enabled {
		return nil
	}
	return c.setVersionState(versionFullName, kmspb.CryptoKeyVersion_DISABLED)
}

// DestroyCryptoKeyVersion schedules a crypto key version's key material for
// destruction (a ~24h grace period during which RestoreCryptoKeyVersion can
// still undo it, after which the material is unrecoverably gone), matching
// `gcloud kms keys versions destroy`.
func (c *Client) DestroyCryptoKeyVersion(versionFullName string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("kms client not initialized")
	}
	_, err := c.client.DestroyCryptoKeyVersion(context.Background(), &kmspb.DestroyCryptoKeyVersionRequest{Name: versionFullName})
	return err
}

// RestoreCryptoKeyVersion undoes a pending DestroyCryptoKeyVersion, matching
// `gcloud kms keys versions restore`. Only works while the version is still
// in the DESTROY_SCHEDULED grace period.
func (c *Client) RestoreCryptoKeyVersion(versionFullName string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("kms client not initialized")
	}
	_, err := c.client.RestoreCryptoKeyVersion(context.Background(), &kmspb.RestoreCryptoKeyVersionRequest{Name: versionFullName})
	return err
}

// CreateKeyRing creates a new Cloud KMS key ring in the given location.
func (c *Client) CreateKeyRing(projectID, location, keyRingID string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("kms client not initialized")
	}
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, location)
	_, err := c.client.CreateKeyRing(context.Background(), &kmspb.CreateKeyRingRequest{
		Parent:    parent,
		KeyRingId: keyRingID,
		KeyRing:   &kmspb.KeyRing{},
	})
	return err
}

// CreateCryptoKey creates a new crypto key within an existing key ring.
// keyRingFullName is the full resource name of the key ring
// (projects/*/locations/*/keyRings/*).
func (c *Client) CreateCryptoKey(keyRingFullName, keyID, purpose, algorithm string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("kms client not initialized")
	}

	purposeEnum := kmspb.CryptoKey_ENCRYPT_DECRYPT
	if p, ok := kmspb.CryptoKey_CryptoKeyPurpose_value[purpose]; ok {
		purposeEnum = kmspb.CryptoKey_CryptoKeyPurpose(p)
	}

	algorithmEnum := kmspb.CryptoKeyVersion_GOOGLE_SYMMETRIC_ENCRYPTION
	if a, ok := kmspb.CryptoKeyVersion_CryptoKeyVersionAlgorithm_value[algorithm]; ok {
		algorithmEnum = kmspb.CryptoKeyVersion_CryptoKeyVersionAlgorithm(a)
	}

	key := &kmspb.CryptoKey{
		Purpose: purposeEnum,
		VersionTemplate: &kmspb.CryptoKeyVersionTemplate{
			Algorithm: algorithmEnum,
		},
	}
	_, err := c.client.CreateCryptoKey(context.Background(), &kmspb.CreateCryptoKeyRequest{
		Parent:      keyRingFullName,
		CryptoKeyId: keyID,
		CryptoKey:   key,
	})
	return err
}

// DeleteCryptoKey permanently deletes a CryptoKey, matching
// `gcloud kms keys delete` (only true as of the API supporting outright
// CryptoKey deletion — this was historically impossible in KMS). The API
// requires every CryptoKeyVersion under the key to have already been
// destroyed via DestroyCryptoKeyVersion first; since this app doesn't
// implement key-version operations (out of scope, see Update above), this
// will fail for any key with active versions, which is nearly all of them.
// Key rings genuinely cannot be deleted at all in GCP — no keyring-delete
// is implemented.
func (c *Client) DeleteCryptoKey(keyRingFullName, keyID string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("kms client not initialized")
	}
	name := fmt.Sprintf("%s/cryptoKeys/%s", keyRingFullName, keyID)
	_, err := c.client.DeleteCryptoKey(context.Background(), &kmspb.DeleteCryptoKeyRequest{Name: name})
	return err
}

// UpdateCryptoKeyRotationSchedule sets a crypto key's automatic rotation
// period, matching `gcloud kms keys update --rotation-period` (which also
// requires --next-rotation-time under the hood — this defaults it to
// rotationPeriod from now, same as gcloud's own default). Key-version
// operations (set-primary-version, enable/disable/destroy/restore) are out
// of scope for this minimal Update flow.
func (c *Client) UpdateCryptoKeyRotationSchedule(keyRingFullName, keyID string, rotationPeriod time.Duration) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("kms client not initialized")
	}
	name := fmt.Sprintf("%s/cryptoKeys/%s", keyRingFullName, keyID)
	key := &kmspb.CryptoKey{
		Name:             name,
		RotationSchedule: &kmspb.CryptoKey_RotationPeriod{RotationPeriod: durationpb.New(rotationPeriod)},
		NextRotationTime: timestamppb.New(time.Now().Add(rotationPeriod)),
	}
	_, err := c.client.UpdateCryptoKey(context.Background(), &kmspb.UpdateCryptoKeyRequest{
		CryptoKey: key,
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{"rotation_period", "next_rotation_time"},
		},
	})
	return err
}

// GetPublicKey fetches the PEM-encoded public key for an asymmetric crypto
// key (purpose ASYMMETRIC_SIGN or ASYMMETRIC_DECRYPT), matching
// `gcloud kms keys versions get-public-key`.
//
// SIMPLIFICATION: GetPublicKey operates on a specific CryptoKeyVersion, but
// this app has never fetched or listed individual CryptoKeyVersions (only
// key-level metadata via ListCryptoKeys/GetCryptoKey — see models.go). Rather
// than add full version-listing just for this, we hardcode version "1",
// which is the primary/only version for the common case of an asymmetric key
// that hasn't been manually rotated to a later version. Keys with a primary
// version other than 1 will get a "not found"-style error from the API here.
func (c *Client) GetPublicKey(keyRingFullName, keyID string) (string, error) {
	if demo.Enabled {
		return "-----BEGIN PUBLIC KEY-----\n(demo public key)\n-----END PUBLIC KEY-----\n", nil
	}
	if c.client == nil {
		return "", fmt.Errorf("kms client not initialized")
	}
	versionName := fmt.Sprintf("%s/cryptoKeys/%s/cryptoKeyVersions/1", keyRingFullName, keyID)
	resp, err := c.client.GetPublicKey(context.Background(), &kmspb.GetPublicKeyRequest{Name: versionName})
	if err != nil {
		return "", fmt.Errorf("get public key: %w", err)
	}
	return resp.GetPem(), nil
}
