package main

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

// Channels are how the user and Eriphe talk: one notion for everything the
// conversation goes through, apart from the tools (the catalog), which act.
// The workspace and push are eriphe-core's own; a service eriphe-core talks
// to for the user (a chat app's bot) registers itself as one more
// (registerChannel, from its own file; a bot channel: bot_channel.go); the user adds others,
// any app that talks with text through a token (channels_added.go). The
// code here and the router (route.go) know none of them by name. Each
// channel says:
//
//   - its direction: it receives (the user talks to Eriphe), it notifies
//     (Eriphe tells the user), or both;
//   - who it guarantees speaks, inbound: the user (their session or token),
//     their verified account; for an added channel, what the user declared
//     (trusted, or anyone). Two levels only: trusted (the user, their
//     account, an app the user trusts), whose words are the user's as in the
//     workspace and may answer every card, a task's permission too; or not;
//   - what it may carry, outbound: a full text, or only a sentence the code
//     writes;
//   - its own connection and secret, never another's.
//
// Notifications reach the user through the outbound ones. The brain still
// calls notify and the code still decides (notify.go); the channels only
// say where what is sent goes. By default, every outbound channel the user
// set up; the user may narrow that default (Settings → Notifications), and a
// topic rule may name its own ("project:getout → now, push and a chat app").
// That is how notifications go in observe mode; in route mode the router
// picks one device at a time (route.go), and a rule's channels keep only
// those.

const (
	chanWorkspace = "workspace"
	chanPush      = "push"

	notifyChannelsKey = "notify_channels" // the default channels; all when empty
)

// channelDir is which way a channel goes.
type channelDir int

const (
	dirIn   channelDir = 1 << iota // the user talks to Eriphe
	dirOut                         // Eriphe tells the user
	dirBoth = dirIn | dirOut
)

// Who an inbound channel guarantees is speaking.
const (
	speakerUser    = "user"    // the user's own session or token
	speakerAccount = "account" // the user's account, verified on the channel
)

// What an outbound channel may carry.
const (
	carryText     = "text"     // the notification's words and buttons
	carrySentence = "sentence" // only a sentence the code writes
)

// channelKind is a channel as the code knows it.
type channelKind struct {
	ID      string
	Label   string // its name, as the user knows it: what the user writes there is marked with it (messages.via)
	Dir     channelDir
	Speaker string // inbound: who it guarantees speaks; "" when it only notifies
	Carries string // outbound: what it may carry; "" when it only receives
	// Line: the router's line of its own (route.go), after the phone; its
	// id is the channel's.
	Line bool
	Page string // where it is set up (Settings → Access)
	// Outbound channels a notification is sent on as such (broadcast, a
	// rule's channels): whether the user can be reached on it now, and
	// sending a note there, with what it reached.
	ready   func(c *core) bool
	deliver func(c *core, n pushNote) []routeTarget
	// Every outbound channel: sending a note to one target the router
	// picked (route.go) on one of its lines.
	reach func(c *core, n pushNote, logID int64, t routeTarget)
	// A channel the user writes from and Eriphe answers on with text and
	// buttons (channel_relay.go): the brain's message that answers what
	// the user wrote there, and a card with Oui / Non that goes with one.
	reply func(c *core, m Message)
	card  func(c *core, x convoCard, p proposal)
	// Its own part of the workspace: its routes, what it starts, and its
	// row in Settings → Access once set up.
	routes func(w *web, mux *webMux)
	start  func(w *web)
	entry  func(w *web) (entryRow, bool)
}

// registerChannel adds a channel of eriphe-core's (from an init): after
// the built-in ones, in the order registered.
func registerChannel(k channelKind) { channelKinds = append(channelKinds, k) }

// lineChannels are the channels with a router line of their own, in order.
func lineChannels() []channelKind {
	var out []channelKind
	for _, k := range channelKinds {
		if k.Line {
			out = append(out, k)
		}
	}
	return out
}

// channelByVia is the channel the user wrote a message from, by its mark.
func channelByVia(via string) (channelKind, bool) {
	for _, k := range channelKinds {
		if k.Label != "" && k.Label == via && k.reply != nil {
			return k, true
		}
	}
	return channelKind{}, false
}

func (k channelKind) in() bool  { return k.Dir&dirIn != 0 }
func (k channelKind) out() bool { return k.Dir&dirOut != 0 }

// sends: a notification may name it, and goes there by broadcast.
func (k channelKind) sends() bool { return k.out() && k.deliver != nil }

// channelKinds are every channel, in their order on the page.
var channelKinds = []channelKind{
	// The workspace and the CLI: the user's session or token.
	{ID: chanWorkspace, Dir: dirIn, Speaker: speakerUser},
	// Web push on the user's devices (push.go).
	{ID: chanPush, Dir: dirOut, Carries: carryText,
		ready: func(c *core) bool { return c.push != nil && c.push.any() },
		deliver: func(c *core, n pushNote) []routeTarget {
			if c.push == nil {
				return nil
			}
			c.push.deliver(n)
			var sent []routeTarget
			subs, _ := c.push.subscriptions()
			for _, s := range subs {
				if n.OnlyFor == "" || s.ID == n.OnlyFor {
					sent = append(sent, routeTarget{Step: stepBroadcast, Device: s.ID, Label: s.Label})
				}
			}
			return sent
		},
		reach: func(c *core, n pushNote, logID int64, t routeTarget) {
			if c.push == nil {
				return
			}
			if t.Step == stepInApp {
				c.push.queueInApp(t.Tabs, inApp{ID: logID, Body: n.Body, URL: n.URL})
				return
			}
			one := n
			one.OnlyFor = t.Device
			c.push.deliver(one)
		}},
}

// channelKindOf is the channel of that id.
func channelKindOf(id string) (channelKind, bool) {
	i := slices.IndexFunc(channelKinds, func(k channelKind) bool { return k.ID == id })
	if i < 0 {
		return channelKind{}, false
	}
	return channelKinds[i], true
}

// sendChannels are the ids of the channels a notification may name, in
// their fixed order: push, then those registered.
func sendChannels() []string {
	var out []string
	for _, k := range channelKinds {
		if k.sends() {
			out = append(out, k.ID)
		}
	}
	return out
}

// checkChannels normalises a list of channels, "push,<a channel>": known ones,
// each once, in a fixed order; "" stays "" (the default).
func checkChannels(s string) (string, error) {
	all := sendChannels()
	var out []string
	for _, ch := range strings.Split(strings.ToLower(s), ",") {
		ch = strings.TrimSpace(ch)
		switch {
		case ch == "":
		case !slices.Contains(all, ch):
			return "", errors.New("channels are " + strings.Join(all, ", ") + " or several of them")
		case !slices.Contains(out, ch):
			out = append(out, ch)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return slices.Index(all, a) - slices.Index(all, b) })
	return strings.Join(out, ","), nil
}

// defaultChannels are the channels a note without its own goes by.
func (c *core) defaultChannels() string {
	if ch, err := checkChannels(c.store.get(notifyChannelsKey)); err == nil && ch != "" {
		return ch
	}
	return strings.Join(sendChannels(), ",")
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
	return slices.ContainsFunc(channelKinds, func(k channelKind) bool { return k.sends() && k.ready(c) })
}

// channelNames words a list of channels, "push,<id>": push by the page's
// words, the others by their names.
func channelNames(lang, ids string) string {
	var names []string
	for _, id := range splitList(ids) {
		if k, ok := channelKindOf(id); ok && k.Label != "" {
			names = append(names, k.Label)
			continue
		}
		names = append(names, tr(lang, "rule.ch."+id))
	}
	if len(names) == 0 {
		return tr(lang, "rule.ch.")
	}
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + tr(lang, "rule.ch.and") + names[len(names)-1]
}

// channelWords are the channels' names as alternatives of a regular
// expression, "|<a channel>" ("" without any), for the wording that speaks of
// being told.
func channelWords() string {
	var out string
	for _, k := range channelKinds {
		if k.Label != "" {
			out += "|" + regexp.QuoteMeta(strings.ToLower(k.Label))
		}
	}
	return out
}
