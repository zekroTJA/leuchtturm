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
		assert.Nil(t, IsTruePtr("  "))
		assert.Nil(t, IsTruePtr("\r\t"))
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

func TestIsTrueEdgeCases(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected bool
	}{
		{"enabled", "enabled", true},
		{"mixed case enabled", "EnAbLeD", true},
		{"tab and newline padding", "\ttrue\n", true},
		{"inner whitespace", "t rue", false},
		{"y shorthand", "y", false},
		{"t shorthand", "t", false},
		{"numeric two", "2", false},
		{"negative one", "-1", false},
		{"leading zero", "01", false},
		{"quoted true", `"true"`, false},
		{"truthy prefix", "truely", false},
		{"truthy suffix", "yesno", false},
		{"unicode lookalike", "trυe", false},
		{"whitespace only", " \t\n ", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.expected, IsTrue(c.input))
		})
	}
}

func TestIsTruePtrEdgeCases(t *testing.T) {
	t.Run("padded truthy value", func(t *testing.T) {
		result := IsTruePtr("  yes\n")
		if assert.NotNil(t, result) {
			assert.True(t, *result)
		}
	})

	t.Run("returns distinct pointers", func(t *testing.T) {
		first := IsTruePtr("true")
		second := IsTruePtr("true")
		assert.NotSame(t, first, second)
	})
}

func TestOrPtr(t *testing.T) {
	t.Run("nil returns default", func(t *testing.T) {
		assert.True(t, OrPtr(nil, true))
		assert.False(t, OrPtr(nil, false))
	})

	t.Run("non-nil value takes precedence over default", func(t *testing.T) {
		valueFalse := false
		valueTrue := true
		assert.False(t, OrPtr(&valueFalse, true))
		assert.True(t, OrPtr(&valueTrue, false))
	})

	t.Run("zero value is not treated as unset", func(t *testing.T) {
		zero := 0
		assert.Equal(t, 0, OrPtr(&zero, 42))

		empty := ""
		assert.Equal(t, "", OrPtr(&empty, "default"))
	})

	t.Run("works with struct types", func(t *testing.T) {
		type point struct {
			X int
			Y int
		}
		value := point{X: 1, Y: 2}
		assert.Equal(t, value, OrPtr(&value, point{X: 9, Y: 9}))
		assert.Equal(t, point{X: 9, Y: 9}, OrPtr[point](nil, point{X: 9, Y: 9}))
	})

	t.Run("combined with IsTruePtr", func(t *testing.T) {
		assert.True(t, OrPtr(IsTruePtr(""), true))
		assert.False(t, OrPtr(IsTruePtr("false"), true))
		assert.True(t, OrPtr(IsTruePtr("on"), false))
	})
}
