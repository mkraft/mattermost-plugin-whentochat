package main

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/mattermost/mattermost-server/v5/model"
	"github.com/mattermost/mattermost-server/v5/plugin"
	"github.com/pkg/errors"
)

// Plugin implements the interface expected by the Mattermost server to communicate between the server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex

	// configuration is the active plugin configuration. Consult getConfiguration and
	// setConfiguration for usage.
	configuration *configuration

	BotUserID string
}

func (p *Plugin) OnActivate() error {
	botId, err := p.Helpers.EnsureBot(&model.Bot{
		Username:    "whentochat",
		DisplayName: "When To Chat",
		Description: "Created by the WhenToChat plugin.",
	})
	if err != nil {
		return errors.Wrap(err, "failed to ensure whentochat bot")
	}
	p.BotUserID = botId

	command := &model.Command{
		Trigger:          "whentochat",
		AutoComplete:     true,
		AutoCompleteDesc: "Find a good time to in everyone's timezone.",
		DisplayName:      "When To Chat",
	}
	err = p.API.RegisterCommand(command)
	if err != nil {
		return err
	}

	return nil
}

func location(user *model.User) *time.Location {
	var useAutomatic bool
	b, err := strconv.ParseBool(user.Timezone["useAutomaticTimezone"])
	if err == nil {
		useAutomatic = b
	}

	var tz string
	if useAutomatic {
		tz = user.Timezone["automaticTimezone"]
	} else {
		tz = user.Timezone["manualTimezone"]
	}

	location, err := time.LoadLocation(tz)
	if err != nil {
		return nil
	}

	return location
}

func (p *Plugin) ExecuteCommand(c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	var allUsers []*model.User
	var page int
	const batchSize = 100
	for {
		usersBatch, err := p.API.GetUsersInChannel(args.ChannelId, "username", page, batchSize)
		if err != nil {
			return nil, err
		}
		allUsers = append(allUsers, usersBatch...)
		if len(usersBatch) < batchSize {
			break
		}
		page++
	}

	var earliestStart time.Time
	var latestEnd time.Time

	// strings to display for each user, keyed by user id
	bestTimes := map[string]string{}

	for i, user := range allUsers {
		location := location(user)
		if location == nil {
			bestTimes[user.Id] = "?"
			continue
		}

		now := time.Now()
		userEarliestStart := time.Date(now.Year(), now.Month(), now.Day(), 7, 0, 0, 0, location)
		userLatestEnd := time.Date(now.Year(), now.Month(), now.Day(), 22, 0, 0, 0, location)

		fmt.Printf("userEarliestStart: %+v\n", userEarliestStart)
		fmt.Printf("userLatestEnd: %+v\n", userLatestEnd)

		if i == 0 {
			earliestStart = userEarliestStart
			latestEnd = userLatestEnd
		}

		if userEarliestStart.After(earliestStart) {
			earliestStart = userEarliestStart
		}

		if userLatestEnd.Before(latestEnd) {
			latestEnd = userLatestEnd
		}

		fmt.Printf("earliestStart: %+v\n", earliestStart)
		fmt.Printf("latestEnd: %+v\n", latestEnd)
	}

	post := &model.Post{
		UserId:    p.BotUserID,
		ChannelId: args.ChannelId,
	}

	if earliestStart.After(latestEnd) {
		post.Message = "There's no time that is good for everyone."
		_ = p.API.SendEphemeralPost(args.UserId, post)
		return &model.CommandResponse{}, nil
	}

	message := "It looks like the best times to chat are:\n"

	for _, user := range allUsers {
		location := location(user)
		if location == nil {
			message = fmt.Sprintf("%s\n- %s %s: ?", message, user.FirstName, user.LastName)
			continue
		}
		walltimeStart := earliestStart.In(location)
		walltimeEnd := latestEnd.In(location)
		timeLayout := "3:04pm"
		message = fmt.Sprintf("%s\n- %s %s: %s - %s %s", message, user.FirstName, user.LastName,
			walltimeStart.Format(timeLayout),
			walltimeEnd.Format(timeLayout),
			walltimeEnd.Format("(MST)"))
	}

	post.Message = message
	_ = p.API.SendEphemeralPost(args.UserId, post)
	return &model.CommandResponse{}, nil
}

// See https://developers.mattermost.com/extend/plugins/server/reference/
