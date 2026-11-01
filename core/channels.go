package main

import (
	"errors"
	"slices"
	"strings"
)

// The channels a notification reaches the user by: web push on their
// devices (push.go) and Telegram (telegram.go). The brain still calls
// notify and the code still decides (notify.go); the channels only say
// where what is sent goes. By default, every channel the user set up; the
// user may narrow that default (Settings → Channels), and a topic rule may
// name its own ("project:getout → now, Telegram and push"). That is how
// notifications go in observe mode; in route mode the router picks one
// device at a time (route.go), and a rule's channels keep only those.

const (
	chanPush     = "push"
	chanTelegram = "telegram"

	notifyChannelsKey = "notify_channels" // the default channels; all when empty
)

var allChannels = []string{chanPush, chanTelegram}

// checkChannels normalises a list of channels, "push,telegram": known ones,
// each once, in a fixed order; "" stays "" (the default).
func checkChannels(s string) (string, error) {
	var out []string
	for _, ch := range strings.Split(strings.ToLower(s), ",") {
		ch = strings.TrimSpace(ch)
		switch {
		case ch == "":
		case !slices.Contains(allChannels, ch):
			return "", errors.New("channels are push, telegram or both")
		case !slices.Contains(out, ch):
			out = append(out, ch)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return slices.Index(allChannels, a) - slices.Index(allChannels, b) })
	return strings.Join(out, ","), nil
}

// defaultChannels are the channels a note without its own goes by.
func (c *core) defaultChannels() string {
	if ch, err := checkChannels(c.store.get(notifyChannelsKey)); err == nil && ch != "" {
		return ch
	}
	return strings.Join(allChannels, ",")
}

// channelsOf is where a note goes: its own channels, else the default.
func (c *core) channelsOf(n pushNote) []string {
	if n.OnlyFor != "" {
		return []string{chanPush} // a test of one device
	}
	ch := n.Channels
	if ch == "" {
		ch = c.defaultChannels()
	}
	return strings.Split(ch, ",")
}

// anyChannel tells whether the user can be reached at all.
func (c *core) anyChannel() bool {
	return (c.push != nil && c.push.any()) || (c.tg != nil && c.tg.linked())
}
