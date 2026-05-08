package util

import "strings"

func IsTrue(value string) bool {
	switch strings.ToLower(value) {
	case "true", "1", "on", "enable":
		return true
	default:
		return false
	}
}
