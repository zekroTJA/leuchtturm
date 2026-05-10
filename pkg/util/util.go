package util

import (
	"strings"
)

// IsTrue returns true when the given string is one of the given values (lowercased and space trimmed):
//   - "true"
//   - "1"
//   - "on"
//   - "enabled"
//   - "yes"
//
// Otherwise, false is returned.
func IsTrue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "on", "enable", "yes":
		return true
	default:
		return false
	}
}

// IsTruePtr behaves same as IsTrue but returns a reference which is nil when the given value is an empty string.
func IsTruePtr(value string) *bool {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	result := IsTrue(value)
	return &result
}
