package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsTrue(t *testing.T) {
	cases := []struct {
		input    string
		expected bool
	}{
		{"true", true},
		{"TRUE", true},
		{"True", true},
		{"1", true},
		{"on", true},
		{"ON", true},
		{"enable", true},
		{"Enable", true},
		{"yes", true},
		{"YES", true},
		{" true", true},
		{"true ", true},
		{"false", false},
		{"0", false},
		{"off", false},
		{"disable", false},
		{"", false},
		{"no", false},
		{"foo", false},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			assert.Equal(t, c.expected, IsTrue(c.input))
		})
	}
}

func TestIsTruePtr(t *testing.T) {
	t.Run("empty string returns nil", func(t *testing.T) {
		assert.Nil(t, IsTruePtr(""))
	})

	t.Run("truthy values return pointer to true", func(t *testing.T) {
		truthyInputs := []string{"true", "TRUE", "1", "on", "enable"}
		for _, input := range truthyInputs {
			result := IsTruePtr(input)
			if assert.NotNil(t, result, "input %q", input) {
				assert.True(t, *result, "input %q", input)
			}
		}
	})

	t.Run("falsy values return pointer to false", func(t *testing.T) {
		falsyInputs := []string{"false", "0", "off", "disable", "foo"}
		for _, input := range falsyInputs {
			result := IsTruePtr(input)
			if assert.NotNil(t, result, "input %q", input) {
				assert.False(t, *result, "input %q", input)
			}
		}
	})
}
