package units

import (
	"context"
	"fmt"

	"github.com/evergreen-ci/evergreen/model/host"
	"github.com/mongodb/amboy"
	"github.com/mongodb/amboy/dependency"
	"github.com/mongodb/amboy/job"
	"github.com/mongodb/amboy/registry"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/logging"
	"github.com/mongodb/grip/message"
	"github.com/pkg/errors"
)

const hostStatsName = "host-status-alerting"

func init() {
	registry.AddJobType(hostStatsName, func() amboy.Job {
		return makeHostStats()
	})
}

type hostStatsJob struct {
	job.Base `bson:"base" json:"base" yaml:"base"`
	logger   grip.Journaler
}

func makeHostStats() *hostStatsJob {
	j := &hostStatsJob{
		Base: job.Base{
			JobType: amboy.JobType{
				Name:    hostStatsName,
				Version: 0,
			},
		},
	}

	j.SetDependency(dependency.NewAlways())
	return j
}

func NewHostStatsJob(ts string) amboy.Job {
	job := makeHostStats()
	job.SetID(fmt.Sprintf("%s.%s", hostAlertingName, ts))
	return job
}

func (j *hostStatsJob) Run(_ context.Context) {
	defer j.MarkComplete()

	counts, err := host.CountInactiveHostsByProvider()
	if err != nil {
		j.AddError(errors.Wrap(err, "error aggregating inactive hosts"))
		return
	}

	if j.logger == nil {
		j.logger = logging.MakeGrip(grip.GetSender())
	}

	j.logger.Info(message.Fields{
		"message": "count of decommissioned/quarantined hosts",
		"counts":  counts,
	})
}
