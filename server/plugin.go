package main

import (
	"fmt"
	"strconv"
	"strings"
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

// OnActivate initializes the plugin.
func (p *Plugin) OnActivate() error {
	botID, err := p.Helpers.EnsureBot(&model.Bot{
		Username:    "whentochat",
		DisplayName: "When To Chat",
		Description: "Created by the WhenToChat plugin.",
	})
	if err != nil {
		return errors.Wrap(err, "failed to ensure whentochat bot")
	}
	p.BotUserID = botID

	command := &model.Command{
		Trigger:          "whentochat",
		AutoComplete:     true,
		AutoCompleteDesc: "Find a time to chat!",
		DisplayName:      "When To Chat",
	}
	err = p.API.RegisterCommand(command)
	if err != nil {
		return err
	}

	return nil
}

func (p *Plugin) ExecuteCommand(c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	split := strings.Fields(args.Command)
	command := split[0]
	if command != "/whentochat" {
		return &model.CommandResponse{}, nil
	}

	allUsers, err := p.allUsers(args.ChannelId)
	if err != nil {
		return nil, err
	}

	earliestStart, latestEnd, ok := window(allUsers)

	post := &model.Post{
		UserId:    p.BotUserID,
		ChannelId: args.ChannelId,
	}

	if !ok {
		post.Message = "There is no window that suits everyone."
		_ = p.API.SendEphemeralPost(args.UserId, post)
		return &model.CommandResponse{}, nil
	}

	message := "It looks like the best times to chat are:\n"

	allUsers = arrangeUserFirst(args.UserId, allUsers)

	for _, user := range allUsers {
		location := location(user)
		if location == nil {
			message = fmt.Sprintf("%s\n- %s %s: ?", message, user.FirstName, user.LastName)
			continue
		}
		walltimeStart := earliestStart.In(location)
		walltimeEnd := latestEnd.In(location)
		timeLayout := "3:04pm"
		message = fmt.Sprintf("%s\n- %s: %s - %s %s", message, user.GetDisplayName("full_name"),
			walltimeStart.Format(timeLayout),
			walltimeEnd.Format(timeLayout),
			walltimeEnd.Format("(MST)"))
	}

	post.Message = message
	_ = p.API.SendEphemeralPost(args.UserId, post)
	return &model.CommandResponse{}, nil
}

func (p *Plugin) allUsers(channelID string) ([]*model.User, *model.AppError) {
	var allUsers []*model.User
	var page int
	const batchSize = 100
	for {
		usersBatch, err := p.API.GetUsersInChannel(channelID, "username", page, batchSize)
		if err != nil {
			return nil, err
		}
		for _, user := range usersBatch {
			if user.IsBot {
				continue
			}
			allUsers = append(allUsers, user)
		}
		if len(usersBatch) < batchSize {
			break
		}
		page++
	}
	return allUsers, nil
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

func window(users []*model.User) (start, end time.Time, ok bool) {
	for i, user := range users {
		loc := location(user)
		if loc == nil {
			continue
		}

		now := time.Now()
		userEarliestStart := time.Date(now.Year(), now.Month(), now.Day(), 7, 0, 0, 0, loc)
		userLatestEnd := time.Date(now.Year(), now.Month(), now.Day(), 22, 0, 0, 0, loc)

		if i == 0 {
			start = userEarliestStart
			end = userLatestEnd
		}

		if userEarliestStart.After(start) {
			start = userEarliestStart
		}

		if userLatestEnd.Before(end) {
			end = userLatestEnd
		}

		if start.After(end) || start.Equal(end) {
			return
		}
	}
	ok = true
	return
}

func arrangeUserFirst(userID string, users []*model.User) []*model.User {
	var indexOfUser int
	for i, user := range users {
		if user.Id == userID {
			indexOfUser = i
			break
		}
	}
	sorted := []*model.User{users[indexOfUser]}
	sorted = append(sorted, users[:indexOfUser]...)
	sorted = append(sorted, users[indexOfUser+1:]...)
	return sorted
}

// See https://developers.mattermost.com/extend/plugins/server/reference/
