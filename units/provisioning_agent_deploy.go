package units

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/evergreen-ci/evergreen"
	"github.com/evergreen-ci/evergreen/cloud"
	"github.com/evergreen-ci/evergreen/model/distro"
	"github.com/evergreen-ci/evergreen/model/event"
	"github.com/evergreen-ci/evergreen/model/host"
	"github.com/evergreen-ci/evergreen/subprocess"
	"github.com/evergreen-ci/evergreen/util"
	"github.com/mongodb/amboy"
	"github.com/mongodb/amboy/dependency"
	"github.com/mongodb/amboy/job"
	"github.com/mongodb/amboy/registry"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/pkg/errors"
)

const (
	agentDeployJobName = "agent-deploy"
	agentPutRetries    = 10
)

func init() {
	registry.AddJobType(agentDeployJobName, func() amboy.Job {
		return makeAgentDeployJob()
	})
}

type agentDeployJob struct {
	HostID   string `bson:"host_id" json:"host_id" yaml:"host_id"`
	job.Base `bson:"metadata" json:"metadata" yaml:"metadata"`

	host *host.Host
	env  evergreen.Environment
}

func makeAgentDeployJob() *agentDeployJob {
	j := &agentDeployJob{
		Base: job.Base{
			JobType: amboy.JobType{
				Name:    agentDeployJobName,
				Version: 0,
			},
		},
	}

	j.SetDependency(dependency.NewAlways())
	return j
}

func NewAgentDeployJob(env evergreen.Environment, h host.Host, id string) amboy.Job {
	j := makeAgentDeployJob()
	j.host = &h
	j.HostID = h.Id
	j.env = env
	j.SetPriority(1)
	j.SetID(fmt.Sprintf("%s.%s.%s", agentDeployJobName, j.HostID, id))

	return j
}

func (j *agentDeployJob) Run(ctx context.Context) {
	var err error
	defer j.MarkComplete()

	if j.host == nil {
		j.host, err = host.FindOneId(j.HostID)
		if err != nil {
			j.AddError(err)
			return
		}
		if j.host == nil {
			j.AddError(fmt.Errorf("could not find host %s for job %s", j.HostID, j.TaskID))
			return
		}
	}

	if j.env == nil {
		j.env = evergreen.GetEnvironment()
	}

	settings := j.env.Settings()

	err = j.startAgentOnHost(ctx, settings, *j.host)
	j.AddError(err)
	if err != nil {
		stat, err := event.GetRecentAgentDeployStatuses(j.HostID, agentPutRetries)
		j.AddError(err)
		if err != nil {
			return
		}

		if stat.LastAttemptFailed() && stat.AllAttemptsFailed() && stat.Count == agentPutRetries {
			msg := "error putting agent on host"
			job := NewDecoHostNotifyJob(j.env, j.host, nil, msg)
			grip.Critical(message.WrapError(j.env.RemoteQueue().Put(job),
				message.Fields{
					"message": fmt.Sprintf("tried %d times to put agent on host", agentPutRetries),
					"host_id": j.host.Id,
					"distro":  j.host.Distro,
				}))
		}

	}
}

// SSHTimeout defines the timeout for the SSH commands in this package.
const sshTimeout = 25 * time.Second

func (j *agentDeployJob) getHostMessage(h host.Host) message.Fields {
	m := message.Fields{
		"message":  "starting agent on host",
		"runner":   "taskrunner",
		"host":     h.Host,
		"distro":   h.Distro.Id,
		"provider": h.Distro.Provider,
	}

	if h.InstanceType != "" {
		m["instance"] = h.InstanceType
	}

	sinceLCT := time.Since(h.LastCommunicationTime)
	if h.NeedsNewAgent {
		m["reason"] = "flagged for new agent"
	} else if h.LastCommunicationTime.IsZero() {
		m["reason"] = "new host"
	} else if sinceLCT > host.MaxLCTInterval {
		m["reason"] = "host has exceeded last communication threshold"
		m["threshold"] = host.MaxLCTInterval
		m["threshold_span"] = host.MaxLCTInterval.String()
		m["last_communication_at"] = sinceLCT
		m["last_communication_at_time"] = sinceLCT.String()
	}

	return m
}

// Start an agent on the host specified.  First runs any necessary
// preparation on the remote machine, then kicks off the agent process on the
// machine. Returns an error if any step along the way fails.
func (j *agentDeployJob) startAgentOnHost(ctx context.Context, settings *evergreen.Settings, hostObj host.Host) error {

	// get the host's SSH options
	cloudHost, err := cloud.GetCloudHost(ctx, &hostObj, settings)
	if err != nil {
		return errors.Wrapf(err, "Failed to get cloud host for %s", hostObj.Id)
	}
	sshOptions, err := cloudHost.GetSSHOptions()
	if err != nil {
		return errors.Wrapf(err, "Error getting ssh options for host %s", hostObj.Id)
	}

	d, err := distro.FindOne(distro.ById(hostObj.Distro.Id))
	if err != nil {
		return errors.Wrapf(err, "error finding distro %s", hostObj.Distro.Id)
	}
	hostObj.Distro = d

	// prep the remote host
	grip.Info(message.Fields{
		"runner":  "taskrunner",
		"message": "prepping host for agent",
		"host":    hostObj.Id})
	if err = j.prepRemoteHost(ctx, hostObj, sshOptions, settings); err != nil {
		return errors.Wrapf(err, "error prepping remote host %s", hostObj.Id)
	}

	grip.Info(message.Fields{"runner": "taskrunner", "message": "prepping host finished successfully", "host": hostObj.Id})

	// generate the host secret if none exists
	if hostObj.Secret == "" {
		if err = hostObj.CreateSecret(); err != nil {
			return errors.Wrapf(err, "creating secret for %s", hostObj.Id)
		}
	}

	// Start agent to listen for tasks
	grip.Info(j.getHostMessage(hostObj))
	if err = j.startAgentOnRemote(ctx, settings, &hostObj, sshOptions); err != nil {
		// mark the host's provisioning as failed
		if err = hostObj.SetUnprovisioned(); err != nil {
			grip.Error(message.WrapError(err, message.Fields{
				"runner":  "taskrunner",
				"host_id": hostObj.Id,
				"message": "unprovisioning host failed",
			}))
		}

		event.LogHostAgentDeployFailed(hostObj.Id, err)

		return errors.WithStack(err)
	}
	grip.Info(message.Fields{"runner": "taskrunner", "message": "agent successfully started for host", "host": hostObj.Id})

	if err = hostObj.SetAgentRevision(evergreen.BuildRevision); err != nil {
		return errors.Wrapf(err, "error setting agent revision on host %s", hostObj.Id)
	}
	if err = hostObj.UpdateLastCommunicated(); err != nil {
		return errors.Wrapf(err, "error setting LCT on host %s", hostObj.Id)
	}
	if err = hostObj.SetNeedsNewAgent(false); err != nil {
		return errors.Wrapf(err, "error setting needs agent flag on host %s", hostObj.Id)
	}
	return nil
}

// Prepare the remote machine to run a task.
func (j *agentDeployJob) prepRemoteHost(ctx context.Context, hostObj host.Host, sshOptions []string, settings *evergreen.Settings) error {
	// copy over the correct agent binary to the remote host
	if logs, err := hostObj.RunSSHCommand(ctx, hostObj.CurlCommand(settings.Ui.Url), sshOptions); err != nil {
		return errors.Wrapf(err, "error downloading agent binary on remote host: %s", logs)
	}

	// run the setup script with the agent
	if hostObj.Distro.Setup == "" {
		return nil
	}
	if logs, err := hostObj.RunSSHCommand(ctx, hostObj.SetupCommand(), sshOptions); err != nil {
		event.LogProvisionFailed(hostObj.Id, logs)

		grip.Error(message.WrapError(err, message.Fields{
			"message": "error running setup script",
			"host_id": hostObj.Id,
			"distro":  hostObj.Distro.Id,
			"runner":  "taskrunner",
			"logs":    logs,
		}))

		// there is no guarantee setup scripts are idempotent, so we terminate the host if the setup script fails
		if disableErr := hostObj.DisablePoisonedHost(err.Error()); disableErr != nil {
			return errors.Wrapf(disableErr, "error terminating host %s", hostObj.Id)
		}

		return errors.Wrapf(err, "error running setup script on remote host: %s", logs)
	}

	return nil
}

// Start the agent process on the specified remote host.
func (j *agentDeployJob) startAgentOnRemote(ctx context.Context, settings *evergreen.Settings, hostObj *host.Host, sshOptions []string) error {
	// the path to the agent binary on the remote machine
	pathToExecutable := filepath.Join("~", "evergreen")
	if hostObj.Distro.IsWindows() {
		pathToExecutable += ".exe"
	}

	agentCmdParts := []string{
		pathToExecutable,
		"agent",
		fmt.Sprintf("--api_server='%s'", settings.ApiUrl),
		fmt.Sprintf("--host_id='%s'", hostObj.Id),
		fmt.Sprintf("--host_secret='%s'", hostObj.Secret),
		fmt.Sprintf("--log_prefix='%s'", filepath.Join(hostObj.Distro.WorkDir, "agent")),
		fmt.Sprintf("--working_directory='%s'", hostObj.Distro.WorkDir),
		"--cleanup",
	}

	// build the command to run on the remote machine
	remoteCmd := strings.Join(agentCmdParts, " ")
	grip.Info(message.Fields{
		"message": "starting agent on host",
		"host":    hostObj.Id,
		"command": remoteCmd,
		"runner":  "taskrunner",
	})

	// compute any info necessary to ssh into the host
	hostInfo, err := util.ParseSSHInfo(hostObj.Host)
	if err != nil {
		return errors.Wrapf(err, "error parsing ssh info %v", hostObj.Host)
	}

	// run the command to kick off the agent remotely
	env := map[string]string{}
	if sumoEndpoint, ok := settings.Credentials["sumologic"]; ok {
		env["GRIP_SUMO_ENDPOINT"] = sumoEndpoint
	}

	if settings.Splunk.Populated() {
		env["GRIP_SPLUNK_SERVER_URL"] = settings.Splunk.ServerURL
		env["GRIP_SPLUNK_CLIENT_TOKEN"] = settings.Splunk.Token

		if settings.Splunk.Channel != "" {
			env["GRIP_SPLUNK_CHANNEL"] = settings.Splunk.Channel
		}
	}

	startAgentCmd := subprocess.NewRemoteCommand(
		remoteCmd,
		hostInfo.Hostname,
		hostObj.User,
		env,
		true, // background
		append([]string{"-p", hostInfo.Port}, sshOptions...),
		false, // loggingDisabled
	)
	cmdOutBuff := &bytes.Buffer{}
	output := subprocess.OutputOptions{Output: cmdOutBuff, SendErrorToOutput: true}
	if err = startAgentCmd.SetOutput(output); err != nil {
		grip.Alert(message.WrapError(err, message.Fields{
			"runner":    "taskrunner",
			"operation": "setting up copy cli config command",
			"distro":    hostObj.Distro.Id,
			"host":      hostObj.Host,
			"output":    output,
			"cause":     "programmer error",
		}))

		return errors.Wrap(err, "problem configuring command output")
	}

	ctx, cancel := context.WithTimeout(ctx, sshTimeout)
	defer cancel()
	err = startAgentCmd.Run(ctx)

	// run cleanup regardless of what happens.
	grip.Notice(message.WrapError(startAgentCmd.Stop(), message.Fields{
		"runner":  "taskrunner",
		"message": "cleaning command failed",
	}))

	if err != nil {
		return errors.Wrapf(err, "error starting agent (%v): %v", hostObj.Id, cmdOutBuff.String())
	}

	event.LogHostAgentDeployed(hostObj.Id)

	return nil
}
