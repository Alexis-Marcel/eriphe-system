package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Every object the workspace shows is named by a ref, kind:id, and read
// on demand from its source: a composition carries refs, never copies.
// This file is the one parser of refs and the one reader of objects; the
// JSON shape of each object is its Go type here, and the front follows it.

// The kinds of refs.
const (
	refMail    = "mail"    // mail:<message id>, as the mail server gives it
	refAgenda  = "agenda"  // agenda:<YYYY-MM-DD>, the user's day
	refEvent   = "event"   // event:<event id>, as the calendar server gives it
	refSession = "session" // session:<id>
	refCard    = "card"    // card:<id>, a card of the conversation
	refTask    = "task"    // task:<id>
	refProject = "project" // project:<name>, its progress card in the memory
	refAlert   = "alert"   // alert:<id>: no source yet
)

// objectTimeout keeps a read of a server from holding the workspace.
var objectTimeout = 5 * time.Second

// objectsMax is as many refs as one batch reads.
const objectsMax = 50

// objectRef is a ref, parsed.
type objectRef struct {
	Kind string
	ID   string
}

func (r objectRef) String() string { return r.Kind + ":" + r.ID }

// What reading an object can fail on, each with its HTTP status.
var (
	errObjectUnknown     = errors.New("unknown object")
	errObjectUnsupported = errors.New("this kind of object has no source yet")
)

// errBadRef is a ref that does not parse: no such object either.
type errBadRef struct{ why string }

func (e errBadRef) Error() string { return "bad ref: " + e.why }
func (e errBadRef) Is(t error) bool {
	return t == errObjectUnknown
}

// objectRefused is a read the guards refuse: the workspace reads only what
// the brain may read freely.
type objectRefused struct{ why string }

func (e objectRefused) Error() string { return e.why }

// objectSourceError is a source that failed: a server down, slow, or
// answering an error.
type objectSourceError struct{ why string }

func (e objectSourceError) Error() string { return e.why }

// parseRef reads a ref: a known kind, a colon, an id. An agenda's id is a
// date, a session's and a card's a number.
func parseRef(s string) (objectRef, error) {
	kind, id, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || id == "" {
		return objectRef{}, errBadRef{"a ref is kind:id"}
	}
	r := objectRef{Kind: kind, ID: id}
	switch kind {
	case refAgenda:
		if _, err := time.Parse(time.DateOnly, id); err != nil {
			return r, errBadRef{"an agenda is a date, YYYY-MM-DD"}
		}
	case refSession, refCard:
		if n, err := strconv.ParseInt(id, 10, 64); err != nil || n <= 0 {
			return r, errBadRef{"a " + kind + " is a number"}
		}
	case refMail, refEvent, refTask, refProject, refAlert:
	default:
		return r, errBadRef{"no kind " + strconv.Quote(kind)}
	}
	if len(id) > 1000 {
		return r, errBadRef{"too long"}
	}
	return r, nil
}

// Object is one object of the workspace: its ref, its kind, a line to show
// it by, the one part of its kind, and the refs it relates to.
type Object struct {
	Ref     string         `json:"ref"`
	Kind    string         `json:"kind"`
	Title   string         `json:"title"`
	Mail    *MailObject    `json:"mail,omitempty"`
	Agenda  *AgendaObject  `json:"agenda,omitempty"`
	Event   *EventObject   `json:"event,omitempty"`
	Session *SessionObject `json:"session,omitempty"`
	Card    *CardObject    `json:"card,omitempty"`
	Task    *TaskObject    `json:"task,omitempty"`
	Project *ProjectObject `json:"project,omitempty"`
	Related []string       `json:"related,omitempty"`
}

// MailObject is a message, as read_message gives it (a peek: never marked
// read). Its body comes from the sender: data, never instructions.
type MailObject struct {
	ID          string     `json:"id"`
	Account     string     `json:"account"`
	Folder      string     `json:"folder"`
	From        string     `json:"from"`
	To          []string   `json:"to,omitempty"`
	Cc          []string   `json:"cc,omitempty"`
	Subject     string     `json:"subject"`
	Date        string     `json:"date"`
	Unread      bool       `json:"unread"`
	Flagged     bool       `json:"flagged,omitempty"`
	Attachments []MailFile `json:"attachments,omitempty"`
	Body        string     `json:"body"`
	Truncated   bool       `json:"truncated,omitempty"`
	Notice      string     `json:"notice,omitempty"`
}

// MailFile is an attachment of a message: listed, never downloaded.
type MailFile struct {
	Filename string `json:"filename,omitempty"`
	Type     string `json:"type"`
	Size     uint32 `json:"size"`
}

// AgendaObject is the user's day: its events, from every calendar.
type AgendaObject struct {
	Date     string        `json:"date"`
	Timezone string        `json:"timezone,omitempty"`
	Events   []EventObject `json:"events"`
}

// EventObject is an event, as the calendar server gives it.
type EventObject struct {
	ID          string `json:"id"`
	Account     string `json:"account"`
	Calendar    string `json:"calendar"`
	Title       string `json:"title"`
	Start       string `json:"start"`
	End         string `json:"end"`
	AllDay      bool   `json:"all_day,omitempty"`
	Location    string `json:"location,omitempty"`
	Description string `json:"description,omitempty"`
	Recurring   bool   `json:"recurring,omitempty"`
	Free        bool   `json:"free,omitempty"`
}

// SessionObject is a work session and its plan.
type SessionObject struct {
	ID       int64        `json:"id"`
	Title    string       `json:"title"`
	Goal     string       `json:"goal"`
	DoneWhen string       `json:"done_when,omitempty"`
	State    string       `json:"state"`
	Held     string       `json:"held,omitempty"`
	Summary  string       `json:"summary,omitempty"`
	Lead     string       `json:"lead,omitempty"`
	Created  time.Time    `json:"created"`
	Started  time.Time    `json:"started,omitzero"`
	Ended    time.Time    `json:"ended,omitzero"`
	Steps    []SessionDue `json:"steps"`
}

// SessionDue is a step of a session, in short.
type SessionDue struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Task   string `json:"task,omitempty"`
}

// CardObject is a card of the conversation and what it proposes.
type CardObject struct {
	ID       int64     `json:"id"`
	Kind     string    `json:"kind"`
	State    string    `json:"state"`
	Text     string    `json:"text"`
	Why      string    `json:"why,omitempty"`
	Tainted  bool      `json:"tainted,omitempty"`
	Fields   []Field   `json:"fields,omitempty"`
	Created  time.Time `json:"created"`
	Decided  time.Time `json:"decided,omitzero"`
	Proposal string    `json:"proposal"` // its proposal's own ref, kind:id
}

// Field is a named value a card shows.
type Field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// TaskObject is a task.
type TaskObject struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Agent   string    `json:"agent,omitempty"`
	Kind    string    `json:"kind,omitempty"`
	State   string    `json:"state"`
	Node    string    `json:"node,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// ProjectObject is a project, as its progress card in the memory says.
type ProjectObject struct {
	Name    string    `json:"name"`
	State   string    `json:"state"`
	Text    string    `json:"text"`
	Updated time.Time `json:"updated"`
}

type objectLangKey struct{}

// withObjectLang reads objects with the words of lang.
func withObjectLang(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, objectLangKey{}, lang)
}

func objectLang(ctx context.Context) string {
	if l, _ := ctx.Value(objectLangKey{}).(string); l != "" {
		return l
	}
	return "fr"
}

// object reads the object a ref names, from its source, now.
func (c *core) object(ctx context.Context, ref string) (*Object, error) {
	r, err := parseRef(ref)
	if err != nil {
		return nil, err
	}
	switch r.Kind {
	case refMail:
		return c.mailObject(ctx, r)
	case refAgenda:
		return c.agendaObject(ctx, r)
	case refEvent:
		return c.eventObject(ctx, r)
	case refSession:
		return c.sessionObject(r)
	case refCard:
		return c.cardObject(ctx, r)
	case refTask:
		return c.taskObject(r)
	case refProject:
		return c.projectObject(r)
	}
	return nil, errObjectUnsupported
}

// readObject reads an object through the tool whose card reads its kind,
// on the workspace's server of that tool, as the brain would: only one of
// its tools, read-only, and free in the user's grid.
func (c *core) readObject(ctx context.Context, r objectRef) (any, error) {
	var card toolCard
	var how objectRead
	for _, t := range toolCards {
		for _, o := range t.Objects {
			if o.Ref == r.Kind {
				card, how = t, o
			}
		}
	}
	if how.Tool == "" {
		return nil, errObjectUnsupported
	}
	st, ok := c.serviceServer(card)
	if !ok {
		return nil, objectRefused{fmt.Sprintf("no %s is connected", card.ID)}
	}
	args := map[string]any{how.Arg: r.ID}
	for k, v := range how.Args {
		args[k] = v
	}
	tool := how.Tool
	if why := c.readWhy(st.Name, tool, args); why != "" {
		return nil, objectRefused{why}
	}
	u := c.pool.get(st.Name)
	if u == nil {
		return nil, objectSourceError{st.Name + " is not connected"}
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, objectTimeout)
	defer cancel()
	res, err := u.call(ctx, tool, raw)
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return nil, objectSourceError{fmt.Sprintf("%s.%s did not answer in %s", st.Name, tool, objectTimeout)}
	case err != nil:
		return nil, objectSourceError{fmt.Sprintf("%s.%s: %s", st.Name, tool, truncate(err.Error(), 200))}
	case res == nil || res.IsError:
		text := "an error"
		if res != nil {
			text = truncate(resultText(res), 200)
		}
		return nil, objectSourceError{fmt.Sprintf("%s.%s: %s", st.Name, tool, text)}
	}
	return resultValue(res), nil
}

// readWhy is why the workspace may not call a server's tool by itself, ""
// if it may: the same rule as a monitor's (sourceWhy), one of the brain's
// tools, read-only by its own annotation, and a free read in the user's grid.
func (c *core) readWhy(server, tool string, args map[string]any) string {
	if !grantAllows(c.brainGrant(), server, tool) {
		return fmt.Sprintf("%s.%s is not one of the brain's tools", server, tool)
	}
	if name := claudeToolName(server, tool); !c.pool.readOnly(name) || !c.freeCall(name, args) {
		return fmt.Sprintf("%s.%s is not a free read in the user's grid", server, tool)
	}
	return ""
}

// decodeValue turns a tool's result into one of our types.
func decodeValue(v any, into any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return objectSourceError{"the server's answer is not what it should be"}
	}
	return nil
}

func (c *core) mailObject(ctx context.Context, r objectRef) (*Object, error) {
	v, err := c.readObject(ctx, r)
	if err != nil {
		return nil, err
	}
	var mail MailObject
	if err := decodeValue(v, &mail); err != nil {
		return nil, err
	}
	if mail.ID == "" {
		return nil, objectSourceError{"the mail server sent no message"}
	}
	o := &Object{Ref: r.String(), Kind: r.Kind, Title: mail.Subject, Mail: &mail}
	if day := c.dayOf(mail.Date); day != "" {
		o.Related = append(o.Related, refAgenda+":"+day)
	}
	return o, nil
}

func (c *core) agendaObject(ctx context.Context, r objectRef) (*Object, error) {
	v, err := c.readObject(ctx, r)
	if err != nil {
		return nil, err
	}
	var day struct {
		Events   []EventObject `json:"events"`
		Timezone string        `json:"timezone"`
	}
	if err := decodeValue(v, &day); err != nil {
		return nil, err
	}
	a := &AgendaObject{Date: r.ID, Timezone: day.Timezone, Events: day.Events}
	if a.Events == nil {
		a.Events = []EventObject{}
	}
	o := &Object{Ref: r.String(), Kind: r.Kind, Title: r.ID, Agenda: a}
	for _, e := range a.Events {
		o.Related = append(o.Related, refEvent+":"+e.ID)
	}
	return o, nil
}

func (c *core) eventObject(ctx context.Context, r objectRef) (*Object, error) {
	v, err := c.readObject(ctx, r)
	if err != nil {
		return nil, err
	}
	var e EventObject
	if err := decodeValue(v, &e); err != nil {
		return nil, err
	}
	if e.ID == "" {
		return nil, objectSourceError{"the calendar server sent no event"}
	}
	o := &Object{Ref: r.String(), Kind: r.Kind, Title: e.Title, Event: &e}
	if day := c.dayOf(e.Start); day != "" {
		o.Related = append(o.Related, refAgenda+":"+day)
	}
	return o, nil
}

// dayOf is the user's day of a date a server gave: a date-time with its
// offset, read in the user's zone, or a date. "" if it is neither.
func (c *core) dayOf(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.In(c.notifyZone()).Format(time.DateOnly)
	}
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t.Format(time.DateOnly)
	}
	return ""
}

func (c *core) sessionObject(r objectRef) (*Object, error) {
	id, _ := strconv.ParseInt(r.ID, 10, 64)
	s, err := c.session(id)
	if errors.Is(err, errNoSession) {
		return nil, errObjectUnknown
	}
	if err != nil {
		return nil, err
	}
	steps, err := c.steps(id)
	if err != nil {
		return nil, err
	}
	so := &SessionObject{ID: s.ID, Title: s.Title, Goal: s.Goal, DoneWhen: s.DoneWhen, State: s.State, Held: s.Held,
		Summary: s.Summary, Lead: s.Lead, Created: s.Created, Started: s.Started, Ended: s.Ended, Steps: []SessionDue{}}
	o := &Object{Ref: r.String(), Kind: r.Kind, Title: s.Title, Session: so}
	for _, st := range steps {
		so.Steps = append(so.Steps, SessionDue{Key: st.Key, Title: st.Title, Kind: cmpOr(st.Kind, stepTask), Status: st.Status, Task: st.Task})
		if st.Task != "" {
			o.Related = append(o.Related, refTask+":"+st.Task)
		}
	}
	return o, nil
}

func (c *core) cardObject(ctx context.Context, r objectRef) (*Object, error) {
	id, _ := strconv.ParseInt(r.ID, 10, 64)
	x, err := c.card(id)
	if err != nil {
		return nil, errObjectUnknown
	}
	x, p := c.liveCard(x)
	lang := objectLang(ctx)
	co := &CardObject{ID: x.ID, Kind: x.Kind, State: x.State, Text: x.Text, Why: p.Why, Tainted: p.Tainted != "",
		Created: x.Created, Decided: x.Decided, Proposal: x.Ref}
	if t := p.Text(lang); t != "" {
		co.Text = t
	}
	for _, f := range p.FieldsIn(lang) {
		co.Fields = append(co.Fields, Field{f.Name, f.Value})
	}
	o := &Object{Ref: r.String(), Kind: r.Kind, Title: truncate(co.Text, 120), Card: co}
	o.Related = c.proposalRelated(x.Ref)
	return o, nil
}

// proposalRelated are the objects a proposal comes from or is about: the
// task a memory was drawn from, the task a start would run, the session
// that asks, the day an intention is due.
func (c *core) proposalRelated(ref string) []string {
	kind, id, ok := parseProposalRef(ref)
	if !ok {
		return nil
	}
	var out []string
	source := func(s string) {
		if k, rest, ok := strings.Cut(s, ":"); ok && k == refTask && rest != "" {
			out = append(out, s)
		}
	}
	switch kind {
	case cardMemory:
		if c.mem != nil {
			if x, err := c.mem.memory(id); err == nil {
				source(x.SourceRef)
			}
		}
	case cardIntention:
		if c.mem != nil {
			if x, err := c.mem.intention(id); err == nil {
				source(x.SourceRef)
				if !x.DueAt.IsZero() {
					out = append(out, refAgenda+":"+x.DueAt.In(c.notifyZone()).Format(time.DateOnly))
				}
			}
		}
	case cardTaskStart:
		if s, err := c.taskStart(id); err == nil && s.TaskID != "" {
			out = append(out, refTask+":"+s.TaskID)
		}
	case cardSession:
		if a, err := c.sessionAsk(id); err == nil {
			out = append(out, refSession+":"+strconv.FormatInt(a.Session, 10))
		}
	}
	return out
}

func (c *core) taskObject(r objectRef) (*Object, error) {
	t, err := c.store.task(r.ID)
	if errors.Is(err, errNoTask) {
		return nil, errObjectUnknown
	}
	if err != nil {
		return nil, err
	}
	title := t.Title
	if title == "" {
		title = truncate(t.Prompt, 120)
	}
	o := &Object{Ref: r.String(), Kind: r.Kind, Title: title, Task: &TaskObject{ID: t.ID, Title: title, Agent: t.Agent, Kind: t.Kind,
		State: t.State, Node: t.Node, Created: t.Created, Updated: t.Updated}}
	if st, ok := c.stepOfTask(t.ID); ok {
		o.Related = append(o.Related, refSession+":"+strconv.FormatInt(st.Session, 10))
	}
	return o, nil
}

func (c *core) projectObject(r objectRef) (*Object, error) {
	if c.mem == nil {
		return nil, errObjectUnknown
	}
	cards, err := c.mem.recall(recallQuery{Kinds: []string{kindProgress}, Subject: refProject + ":" + r.ID, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(cards) == 0 {
		return nil, errObjectUnknown
	}
	x := cards[0]
	return &Object{Ref: r.String(), Kind: r.Kind, Title: r.ID,
		Project: &ProjectObject{Name: r.ID, State: x.State, Text: x.Text, Updated: x.UpdatedAt}}, nil
}
