package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewContainer(t *testing.T) {
	t.Run("populates ID", func(t *testing.T) {
		c := newContainer("abc123", nil)
		assert.Equal(t, "abc123", c.ID)
	})

	t.Run("nil labels yields nil KeepOldImage and empty Schedule", func(t *testing.T) {
		c := newContainer("id", nil)
		assert.Nil(t, c.KeepOldImage)
		assert.Equal(t, "", c.Schedule)
	})

	t.Run("missing keep-old-image label yields nil", func(t *testing.T) {
		c := newContainer("id", map[string]string{
			labelKeyEnable: "true",
		})
		assert.Nil(t, c.KeepOldImage)
	})

	t.Run("keep-old-image truthy label yields pointer to true", func(t *testing.T) {
		c := newContainer("id", map[string]string{
			labelKeyKeepOldImage: "true",
		})
		if assert.NotNil(t, c.KeepOldImage) {
			assert.True(t, *c.KeepOldImage)
		}
	})

	t.Run("keep-old-image falsy label yields pointer to false", func(t *testing.T) {
		c := newContainer("id", map[string]string{
			labelKeyKeepOldImage: "false",
		})
		if assert.NotNil(t, c.KeepOldImage) {
			assert.False(t, *c.KeepOldImage)
		}
	})

	t.Run("populates schedule from label", func(t *testing.T) {
		c := newContainer("id", map[string]string{
			labelKeySchedule: "@hourly",
		})
		assert.Equal(t, "@hourly", c.Schedule)
	})

	t.Run("populates all fields together", func(t *testing.T) {
		c := newContainer("xyz", map[string]string{
			labelKeyEnable:       "true",
			labelKeyKeepOldImage: "1",
			labelKeySchedule:     "0 0 * * *",
		})
		assert.Equal(t, "xyz", c.ID)
		assert.Equal(t, "0 0 * * *", c.Schedule)
		if assert.NotNil(t, c.KeepOldImage) {
			assert.True(t, *c.KeepOldImage)
		}
	})
}

func TestContainerString(t *testing.T) {
	c := &Container{ID: "container-id-123"}
	assert.Equal(t, "container-id-123", c.String())
}

func TestNewContainerRemoveVolumes(t *testing.T) {
	t.Run("missing label yields nil", func(t *testing.T) {
		c := newContainer("id", map[string]string{})
		assert.Nil(t, c.RemoveVolumes)
	})

	t.Run("truthy label yields pointer to true", func(t *testing.T) {
		c := newContainer("id", map[string]string{labelKeyRemoveVolumes: "on"})
		if assert.NotNil(t, c.RemoveVolumes) {
			assert.True(t, *c.RemoveVolumes)
		}
	})

	t.Run("falsy label yields pointer to false", func(t *testing.T) {
		c := newContainer("id", map[string]string{labelKeyRemoveVolumes: "0"})
		if assert.NotNil(t, c.RemoveVolumes) {
			assert.False(t, *c.RemoveVolumes)
		}
	})

	t.Run("whitespace only label yields nil", func(t *testing.T) {
		c := newContainer("id", map[string]string{labelKeyRemoveVolumes: "   "})
		assert.Nil(t, c.RemoveVolumes)
	})

	t.Run("unknown value yields pointer to false", func(t *testing.T) {
		c := newContainer("id", map[string]string{labelKeyRemoveVolumes: "maybe"})
		if assert.NotNil(t, c.RemoveVolumes) {
			assert.False(t, *c.RemoveVolumes)
		}
	})
}

func TestNewContainerEdgeCases(t *testing.T) {
	t.Run("empty ID is kept", func(t *testing.T) {
		c := newContainer("", nil)
		assert.Equal(t, "", c.ID)
		assert.Equal(t, "", c.String())
	})

	t.Run("schedule label is not trimmed or validated", func(t *testing.T) {
		c := newContainer("id", map[string]string{labelKeySchedule: " not a schedule "})
		assert.Equal(t, " not a schedule ", c.Schedule)
	})

	t.Run("label keys are case sensitive", func(t *testing.T) {
		c := newContainer("id", map[string]string{
			"Leuchtturm.Schedule":       "@hourly",
			"LEUCHTTURM.KEEP-OLD-IMAGE": "true",
		})
		assert.Empty(t, c.Schedule)
		assert.Nil(t, c.KeepOldImage)
	})

	t.Run("unrelated labels are ignored", func(t *testing.T) {
		c := newContainer("id", map[string]string{
			"com.docker.compose.project": "test",
			"leuchtturm.unknown":         "true",
		})
		assert.Equal(t, &Container{ID: "id"}, c)
	})

	t.Run("pointers are independent between containers", func(t *testing.T) {
		labels := map[string]string{labelKeyKeepOldImage: "true"}
		first := newContainer("a", labels)
		second := newContainer("b", labels)

		*first.KeepOldImage = false

		assert.True(t, *second.KeepOldImage)
	})
}
