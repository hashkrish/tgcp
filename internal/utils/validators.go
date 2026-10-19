package utils

import "strconv"

// ValidateOptionalInt allows an empty value (meaning "leave unchanged") or
// a valid base-10 integer.
func ValidateOptionalInt(v string) string {
	if v == "" {
		return ""
	}
	if _, err := strconv.ParseInt(v, 10, 64); err != nil {
		return "must be a whole number"
	}
	return ""
}
