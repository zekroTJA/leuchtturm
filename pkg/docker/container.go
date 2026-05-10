package docker

import (
	"github.com/zekrotja/leuchtturm/pkg/util"
)

type Container struct {
	ID           string
	KeepOldImage *bool
	Schedule     string
}

func newContainer(id string, labels map[string]string) *Container {
	return &Container{
		ID:           id,
		KeepOldImage: util.IsTruePtr(labels[labelKeyKeepOldImage]),
		Schedule:     labels[labelKeySchedule],
	}
}

func (t *Container) String() string {
	return t.ID
}
