package docker

import (
	"github.com/zekrotja/leuchtturm/pkg/util"
)

const (
	labelKeyKeepOldImage  = "leuchtturm.keep-old-image"
	labelKeySchedule      = "leuchtturm.schedule"
	labelKeyRemoveVolumes = "leuchtturm.remove-volumes"
)

type Container struct {
	ID            string
	Schedule      string
	KeepOldImage  *bool
	RemoveVolumes *bool
}

func newContainer(id string, labels map[string]string) *Container {
	return &Container{
		ID:            id,
		Schedule:      labels[labelKeySchedule],
		KeepOldImage:  util.IsTruePtr(labels[labelKeyKeepOldImage]),
		RemoveVolumes: util.IsTruePtr(labels[labelKeyRemoveVolumes]),
	}
}

func (t *Container) String() string {
	return t.ID
}
