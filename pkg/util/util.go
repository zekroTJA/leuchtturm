package util

import (
	"strings"
)

func IsTrue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "on", "enable", "yes":
		return true
	default:
		return false
	}
}

func IsTruePtr(value string) *bool {
	if value == "" {
		return nil
	}
	result := IsTrue(value)
	return &result

}
