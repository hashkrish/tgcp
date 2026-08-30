package gcs

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"cloud.google.com/go/iam"
	"cloud.google.com/go/storage"
	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/iamcredentials/v1"
	"google.golang.org/api/iterator"
)

type Client struct {
	client   *storage.Client
	iamCreds *iamcredentials.Service
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	c, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	iamCreds, err := iamcredentials.NewService(ctx)
	if err != nil {
		return nil, err
	}
	return &Client{client: c, iamCreds: iamCreds}, nil
}

func (c *Client) ListBuckets(projectID string) ([]Bucket, error) {
	if demo.Enabled {
		return loadDemoBuckets(), nil
	}
	var buckets []Bucket
	it := c.client.Buckets(context.Background(), projectID)
	for {
		battrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var defaultKMSKeyName string
		if battrs.Encryption != nil {
			defaultKMSKeyName = battrs.Encryption.DefaultKMSKeyName
		}
		var loggingBucket string
		if battrs.Logging != nil {
			loggingBucket = battrs.Logging.LogBucket
		}
		var autoclassEnabled bool
		if battrs.Autoclass != nil {
			autoclassEnabled = battrs.Autoclass.Enabled
		}
		var retentionPeriod time.Duration
		if battrs.RetentionPolicy != nil {
			retentionPeriod = battrs.RetentionPolicy.RetentionPeriod
		}
		buckets = append(buckets, Bucket{
			Name:                     battrs.Name,
			Location:                 battrs.Location,
			LocationType:             battrs.LocationType,
			StorageClass:             battrs.StorageClass,
			Created:                  battrs.Created,
			Updated:                  battrs.Updated,
			VersioningEnabled:        battrs.VersioningEnabled,
			RequesterPays:            battrs.RequesterPays,
			UniformBucketLevelAccess: battrs.UniformBucketLevelAccess.Enabled,
			PublicAccessPrevention:   battrs.PublicAccessPrevention.String(),
			AutoclassEnabled:         autoclassEnabled,
			Labels:                   battrs.Labels,
			LifecycleRuleCount:       len(battrs.Lifecycle.Rules),
			CORSRuleCount:            len(battrs.CORS),
			RetentionPeriod:          retentionPeriod,
			DefaultKMSKeyName:        defaultKMSKeyName,
			LoggingBucket:            loggingBucket,
		})
	}
	return buckets, nil
}

// CreateBucket creates a new GCS bucket with the given name, location, and
// storage class.
func (c *Client) CreateBucket(projectID, name, location, storageClass string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("storage client not initialized")
	}
	attrs := &storage.BucketAttrs{
		Location:     location,
		StorageClass: storageClass,
	}
	return c.client.Bucket(name).Create(context.Background(), projectID, attrs)
}

// UpdateBucketStorageClass patches a bucket's default storage class,
// matching `gcloud storage buckets update --default-storage-class`.
// Other update flags (lifecycle, CORS, versioning, retention, etc.) are out
// of scope for this minimal Update flow.
func (c *Client) UpdateBucketStorageClass(name, storageClass string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("storage client not initialized")
	}
	_, err := c.client.Bucket(name).Update(context.Background(), storage.BucketAttrsToUpdate{
		StorageClass: storageClass,
	})
	return err
}

// UpdateBucketSettings patches a bucket's versioning, retention period,
// CORS, and lifecycle-delete-age in a single call, matching
// `gcloud storage buckets update --versioning --retention-period
// --cors-file --lifecycle-file`. Each parameter only touches its own
// setting: versioningEnabled/retentionDays/lifecycleDeleteAgeDays==0 means
// "disable/clear that setting" (matching the GCS API's own semantics for a
// zero retention period or an empty lifecycle rule set), while a nil
// corsOrigins/corsMethods leaves CORS untouched. Bucket "relocate" (moving
// a bucket to a new location, a distinct long-running Storage Control API
// operation) and full multi-rule lifecycle/CORS configs are out of scope
// for this minimal Update flow.
func (c *Client) UpdateBucketSettings(name string, versioningEnabled bool, retentionDays int64, corsOrigins, corsMethods []string, lifecycleDeleteAgeDays int64) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("storage client not initialized")
	}

	update := storage.BucketAttrsToUpdate{
		VersioningEnabled: versioningEnabled,
		RetentionPolicy:   &storage.RetentionPolicy{RetentionPeriod: time.Duration(retentionDays) * 24 * time.Hour},
	}
	if len(corsOrigins) > 0 || len(corsMethods) > 0 {
		update.CORS = []storage.CORS{{Origins: corsOrigins, Methods: corsMethods}}
	}
	if lifecycleDeleteAgeDays > 0 {
		update.Lifecycle = &storage.Lifecycle{Rules: []storage.LifecycleRule{
			{
				Action:    storage.LifecycleAction{Type: storage.DeleteAction},
				Condition: storage.LifecycleCondition{AgeInDays: lifecycleDeleteAgeDays},
			},
		}}
	} else {
		update.Lifecycle = &storage.Lifecycle{Rules: nil}
	}

	_, err := c.client.Bucket(name).Update(context.Background(), update)
	return err
}

// IsBucketEmpty reports whether a bucket has zero objects (any generation).
// Used to gate DeleteBucket, since the GCS API itself refuses to delete a
// non-empty bucket but this lets the UI show a clear error before even
// attempting the call.
func (c *Client) IsBucketEmpty(bucket string) (bool, error) {
	if demo.Enabled {
		return true, nil
	}
	if c.client == nil {
		return false, fmt.Errorf("storage client not initialized")
	}
	it := c.client.Bucket(bucket).Objects(context.Background(), &storage.Query{})
	_, err := it.Next()
	if err == iterator.Done {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

// DeleteBucket deletes an empty GCS bucket, matching
// `gcloud storage buckets delete`. Object-level delete (emptying a
// non-empty bucket) is out of scope — this app doesn't implement GCS
// data-plane operations at all, so callers must ensure the bucket is
// already empty (see IsBucketEmpty).
func (c *Client) DeleteBucket(name string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("storage client not initialized")
	}
	return c.client.Bucket(name).Delete(context.Background())
}

// GetBucketIAMPolicy reads a bucket's current IAM policy, matching
// `gcloud storage buckets get-iam-policy`. Used as the "look before you
// grant" read step before AddBucketIAMBinding.
func (c *Client) GetBucketIAMPolicy(name string) ([]IAMBinding, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.client == nil {
		return nil, fmt.Errorf("storage client not initialized")
	}
	policy, err := c.client.Bucket(name).IAM().Policy(context.Background())
	if err != nil {
		return nil, fmt.Errorf("get bucket IAM policy: %w", err)
	}
	var out []IAMBinding
	for _, role := range policy.Roles() {
		out = append(out, IAMBinding{Role: string(role), Members: policy.Members(role)})
	}
	return out, nil
}

// AddBucketIAMBinding grants a role to a member on a bucket, matching
// `gcloud storage buckets add-iam-policy-binding`. It fetches the current
// policy, merges the new binding in via the client library's Policy.Add
// (which appends rather than replaces), and writes the merged result back —
// this never drops any existing binding, unlike a raw set-iam-policy.
func (c *Client) AddBucketIAMBinding(name, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("storage client not initialized")
	}
	ctx := context.Background()
	h := c.client.Bucket(name).IAM()
	policy, err := h.Policy(ctx)
	if err != nil {
		return fmt.Errorf("get bucket IAM policy: %w", err)
	}
	policy.Add(member, iam.RoleName(role))
	return h.SetPolicy(ctx, policy)
}

// DeleteObject deletes a single object from a bucket, matching
// `gcloud storage rm gs://bucket/name`. This is a data-plane operation
// distinct from DeleteBucket (which only removes the bucket itself).
func (c *Client) DeleteObject(bucket, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("storage client not initialized")
	}
	return c.client.Bucket(bucket).Object(name).Delete(context.Background())
}

// MoveObject renames/moves a single object within (or across) buckets,
// matching `gcloud storage mv gs://bucket/src gs://bucket/dst` for a single
// object. GCS has no native rename, so this copies then deletes the
// source, mirroring what the gcloud CLI does under the hood. Bulk/recursive
// sync (`rsync`) across many objects is out of scope for this minimal
// data-plane flow.
func (c *Client) MoveObject(srcBucket, srcName, dstBucket, dstName string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("storage client not initialized")
	}
	ctx := context.Background()
	src := c.client.Bucket(srcBucket).Object(srcName)
	dst := c.client.Bucket(dstBucket).Object(dstName)
	if _, err := dst.CopierFrom(src).Run(ctx); err != nil {
		return fmt.Errorf("copy object: %w", err)
	}
	if err := src.Delete(ctx); err != nil {
		return fmt.Errorf("delete source object after copy: %w", err)
	}
	return nil
}

// SignURL generates a signed GET URL for an object valid for the given
// duration, matching `gcloud storage sign-url`. serviceAccountEmail is the
// identity whose key signs the URL; rather than requiring a private key
// file (gcloud's default, but not something this app has access to under
// ADC), signing is delegated to the IAM Credentials API's SignBlob RPC --
// the same approach `gcloud storage sign-url --impersonate-service-account`
// uses. The caller's own credentials need
// `roles/iam.serviceAccountTokenCreator` on serviceAccountEmail for this to
// succeed.
func (c *Client) SignURL(bucket, object, serviceAccountEmail string, expiry time.Duration) (string, error) {
	if demo.Enabled {
		return "https://storage.googleapis.com/demo-bucket/demo-object?X-Goog-Signature=demo", nil
	}
	if c.client == nil || c.iamCreds == nil {
		return "", fmt.Errorf("storage client not initialized")
	}
	name := fmt.Sprintf("projects/-/serviceAccounts/%s", serviceAccountEmail)
	signBytes := func(b []byte) ([]byte, error) {
		resp, err := c.iamCreds.Projects.ServiceAccounts.SignBlob(name, &iamcredentials.SignBlobRequest{
			Payload: base64.StdEncoding.EncodeToString(b),
		}).Do()
		if err != nil {
			return nil, fmt.Errorf("sign blob via IAM Credentials API: %w", err)
		}
		return base64.StdEncoding.DecodeString(resp.SignedBlob)
	}
	return storage.SignedURL(bucket, object, &storage.SignedURLOptions{
		GoogleAccessID: serviceAccountEmail,
		SignBytes:      signBytes,
		Method:         "GET",
		Expires:        time.Now().Add(expiry),
	})
}

// DownloadObject streams an object's contents to a local file, matching a
// simple `gcloud storage cp gs://bucket/name .`. It writes to a fixed,
// predictable path — ~/Downloads/<basename> when the home directory can be
// resolved (created if missing), falling back to the current working
// directory otherwise — rather than prompting for a destination. Returns
// the local path written.
func (c *Client) DownloadObject(bucket, name string) (string, error) {
	base := filepath.Base(name)
	if demo.Enabled {
		return filepath.Join("demo-downloads", base), nil
	}
	if c.client == nil {
		return "", fmt.Errorf("storage client not initialized")
	}

	dir := "."
	if home, err := os.UserHomeDir(); err == nil {
		downloads := filepath.Join(home, "Downloads")
		if err := os.MkdirAll(downloads, 0o755); err == nil {
			dir = downloads
		}
	}
	dest := filepath.Join(dir, base)

	ctx := context.Background()
	r, err := c.client.Bucket(bucket).Object(name).NewReader(ctx)
	if err != nil {
		return "", fmt.Errorf("open object reader: %w", err)
	}
	defer func() { _ = r.Close() }()

	f, err := os.Create(dest)
	if err != nil {
		return "", fmt.Errorf("create local file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(f, r); err != nil {
		return "", fmt.Errorf("download object: %w", err)
	}
	return dest, nil
}

func (c *Client) ListObjects(bucket, prefix string) ([]Object, error) {
	if demo.Enabled {
		return loadDemoObjects(), nil
	}
	var objects []Object
	it := c.client.Bucket(bucket).Objects(context.Background(), &storage.Query{
		Prefix:    prefix,
		Delimiter: "/",
	})

	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		if attrs.Prefix != "" {
			// It's a folder
			objects = append(objects, Object{
				Name: attrs.Prefix,
				Type: "Folder",
			})
		} else {
			// It's a file
			objects = append(objects, Object{
				Name:            attrs.Name,
				Size:            attrs.Size,
				Updated:         attrs.Updated,
				Created:         attrs.Created,
				Type:            attrs.ContentType,
				StorageClass:    attrs.StorageClass,
				Generation:      attrs.Generation,
				Metageneration:  attrs.Metageneration,
				CacheControl:    attrs.CacheControl,
				ContentEncoding: attrs.ContentEncoding,
				EventBasedHold:  attrs.EventBasedHold,
				TemporaryHold:   attrs.TemporaryHold,
				KMSKeyName:      attrs.KMSKeyName,
				MD5:             hex.EncodeToString(attrs.MD5),
				CRC32C:          attrs.CRC32C,
				Metadata:        attrs.Metadata,
			})
		}
	}
	return objects, nil
}
