package kms

// KeyRing represents a Cloud KMS key ring (read-only view).
type KeyRing struct {
	Name       string // Short key ring ID
	FullName   string // Full resource name: projects/*/locations/*/keyRings/*
	Location   string // Region, e.g. us-central1, or "global"
	CreateTime string
}

// IAMBinding is a single role -> members pair from a key ring's IAM policy.
type IAMBinding struct {
	Role    string
	Members []string
}

// CryptoKey represents a Cloud KMS crypto key within a key ring.
//
// This is metadata only, sourced from ListCryptoKeys/GetCryptoKey. No
// operation that could return or operate on private/symmetric key material
// (encrypt, decrypt, sign, verify, etc.) is ever called by this package —
// see api.go. The one exception is GetPublicKey, which only ever returns the
// public half of an asymmetric key.
type CryptoKey struct {
	Name             string // Short key ID
	Purpose          string // ENCRYPT_DECRYPT, ASYMMETRIC_SIGN, ASYMMETRIC_DECRYPT, MAC, RAW_ENCRYPT_DECRYPT
	Algorithm        string // Primary version's algorithm, e.g. GOOGLE_SYMMETRIC_ENCRYPTION
	ProtectionLevel  string // SOFTWARE, HSM, EXTERNAL, EXTERNAL_VPC
	State            string // Primary version's state, e.g. ENABLED
	RotationPeriod   string // Human-readable duration, empty if rotation is not configured
	NextRotationTime string // RFC3339-ish local timestamp, empty if unset
	CreateTime       string
}
