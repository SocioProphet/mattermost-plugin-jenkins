package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-server/model"
	"github.com/mattermost/mattermost-server/plugin"
)

const helpText = `* |/jenkins connect username API Token| - Connect your Mattermost account to Jenkins
* |/jenkins build jobname - Trigger a job build
* |/jenkins build "jobname with space" - Trigger a job which has space in the job name. Note the double quotes
* |/jenkins build folder/jobname - Trigger a job inside a folder. Note the character '/'
* |/jenkins build "folder name/job name with space" - Trigger a job inside a folder with space in job name or folder name. Note double quotes and the character '/'`

const jobNotSpecifiedResponse = "Please specify a job name to build."
const jenkinsConnectedResponse = "Jenkins has been connected."

func getCommand() *model.Command {
	return &model.Command{
		Trigger:          "jenkins",
		Description:      "A Mattermost plugin to interact with Jenkins",
		DisplayName:      "Jenkins",
		AutoComplete:     true,
		AutoCompleteDesc: "Available commands: connect, build, help",
		AutoCompleteHint: "[command]",
	}
}

func (p *Plugin) getCommandResponse(responseType, text string) *model.CommandResponse {
	return &model.CommandResponse{
		ResponseType: responseType,
		Username:     jenkinsUsername,
		IconURL:      p.getConfiguration().ProfileImageURL,
		Text:         text,
		Type:         model.POST_DEFAULT,
	}
}

func (p *Plugin) ExecuteCommand(c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	split := strings.Fields(args.Command)
	command := split[0]
	parameters := []string{}
	action := ""
	if len(split) > 1 {
		action = split[1]
	}
	if len(split) > 2 {
		parameters = split[2:]
	}

	if command != "/jenkins" {
		return &model.CommandResponse{}, nil
	}
	switch action {
	case "connect":
		if len(parameters) == 0 || len(parameters) == 1 {
			return p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, "Please specify both username and API token."), nil
		} else if len(parameters) == 2 {
			p.createEphemeralPost(args.UserId, args.ChannelId, "Validating Jenkins credentials...")
			verify, verifyErr := p.verifyJenkinsCredentials(parameters[0], parameters[1])
			if verifyErr != nil {
				p.API.LogError("Error verifying Jenkins credentials", "user_id", args.UserId, "Err", verifyErr.Error())
				return p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, "Error connecting to Jenkins."), nil
			}

			if !verify {
				return p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, "Incorrect username or token"), nil
			}

			jenkinsUserInfo := &JenkinsUserInfo{
				UserID:   args.UserId,
				Username: parameters[0],
				Token:    parameters[1],
			}

			err := p.storeJenkinsUserInfo(jenkinsUserInfo)
			if err != nil {
				p.API.LogError("Error saving Jenkins user information to KV store", "Err", err.Error())
				return &model.CommandResponse{}, nil
			}

			return p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, jenkinsConnectedResponse), nil
		}
	case "build":
		if len(parameters) == 0 {
			return p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, jobNotSpecifiedResponse), nil
		} else if len(parameters) == 1 {
			jobName := parameters[0]
			buildInfo, buildErr := p.triggerJenkinsJob(args.UserId, args.ChannelId, jobName)
			if buildErr != nil {
				p.API.LogError("Error triggering build", "job_name", jobName, "err", buildErr.Error())
				return p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, fmt.Sprintf("Error triggering build for the job '%s'.", jobName)), nil
			}

			commandResponse := p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, fmt.Sprintf("Build for the job '%s' has been started.\nHere's the build URL : %s. ", jobName, buildInfo.GetUrl()))
			return commandResponse, nil
		} else if len(parameters) > 1 {
			jobName, ok := parseJobName(parameters)
			if !ok {
				return p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, "Please check `/jenkins help` to find information on how to trigger a job."), nil
			}
			buildInfo, buildErr := p.triggerJenkinsJob(args.UserId, args.ChannelId, jobName)
			if buildErr != nil {
				p.API.LogError("Error triggering build", "job_name", jobName, "err", buildErr.Error())
				return p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, fmt.Sprintf("Error triggering build for the job '%s'.", jobName)), nil
			}

			commandResponse := p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, fmt.Sprintf("Build for the job '%s' has been started.\nHere's the build URL : %s.", jobName, buildInfo.GetUrl()))
			return commandResponse, nil
		}
	case "get-artifacts":
		if len(parameters) == 0 {
			return p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, jobNotSpecifiedResponse), nil
		} else if len(parameters) == 1 {
			p.createEphemeralPost(args.UserId, args.ChannelId, fmt.Sprintf("Fetching build artifacts of '%s'...", parameters[0]))
			err := p.fetchAndUploadArtifactsOfABuild(args.UserId, args.ChannelId, parameters[0])
			if err != nil {
				p.API.LogError("Error fetching artifacts", "job_name", parameters[0], "err", err.Error())
				return p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, "Error fetching artifacts."), nil
			}
		} else if len(parameters) > 1 {
			jobName, ok := parseJobName(parameters)
			if !ok {
				return p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, "Please check `/jenkins help` to find information on how to get build artifacts."), nil
			}
			p.createEphemeralPost(args.UserId, args.ChannelId, fmt.Sprintf("Fetching build artifacts of '%s'...", jobName))
			err := p.fetchAndUploadArtifactsOfABuild(args.UserId, args.ChannelId, jobName)
			if err != nil {
				p.API.LogError("Error fetching artifacts", "job_name", jobName, "err", err.Error())
				return p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, "Error fetching artifacts."), nil
			}
		}
	case "test-results":
		if len(parameters) == 0 {
			return p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, jobNotSpecifiedResponse), nil
		} else if len(parameters) == 1 {
			p.createEphemeralPost(args.UserId, args.ChannelId, fmt.Sprintf("Fetching test results of '%s'...", parameters[0]))
			testReportMsg, err := p.fetchTestReportsLinkOfABuild(args.UserId, args.ChannelId, parameters[0])
			if err != nil {
				p.API.LogError("Error fetching test results", "job_name", parameters[0], "err", err.Error())
				return p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, "Error fetching test results."), nil
			}
			p.createEphemeralPost(args.UserId, args.ChannelId, testReportMsg)
		} else if len(parameters) > 1 {
			jobName, ok := parseJobName(parameters)
			if !ok {
				return p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, "Please check `/jenkins help` to find information on how to get test results of a build."), nil
			}
			p.createEphemeralPost(args.UserId, args.ChannelId, fmt.Sprintf("Fetching test results of '%s'...", jobName))
			testReportMsg, err := p.fetchTestReportsLinkOfABuild(args.UserId, args.ChannelId, jobName)
			if err != nil {
				p.API.LogError("Error fetching test results", "job_name", jobName, "err", err.Error())
				return p.getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, "Error fetching test results."), nil
			}
			p.createEphemeralPost(args.UserId, args.ChannelId, testReportMsg)
		}
	}
	return &model.CommandResponse{}, nil
}
