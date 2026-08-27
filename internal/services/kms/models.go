package kms

// KeyRing represents a Cloud KMS key ring (read-only view).
type KeyRing struct {
	Name       string // Short key ring ID
	FullName   string // Full resource name: projects/*/locations/*/keyRings/*
	Location   string // Region, e.g. us-central1, or "global"
	CreateTime string
}

// CryptoKey represents a Cloud KMS crypto key within a key ring.
//
// This is metadata only, sourced from ListCryptoKeys/GetCryptoKey. No
// operation that could return or operate on actual key material (encrypt,
// decrypt, sign, verify, GetPublicKey, etc.) is ever called by this
// package — see api.go.
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
