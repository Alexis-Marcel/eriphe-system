package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Graph is a Provider over the Microsoft Graph calendar API (Outlook.com,
// Microsoft 365), with an OAuth access token lent by eriphe-core
// (Calendars.ReadWrite).
type Graph struct {
	acct *AccountConfig
	hc   *authClient
	base string
	loc  *time.Location
}

func newGraph(acct *AccountConfig, hc *http.Client, loc *time.Location) *Graph {
	return &Graph{acct: acct, hc: &authClient{acct: acct, http: hc}, base: strings.TrimSuffix(acct.GraphURL, "/"), loc: loc}
}

type graphTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

type graphEvent struct {
	ID       string     `json:"id,omitempty"`
	Subject  *string    `json:"subject,omitempty"`
	Start    *graphTime `json:"start,omitempty"`
	End      *graphTime `json:"end,omitempty"`
	IsAllDay *bool      `json:"isAllDay,omitempty"`
	Location *struct {
		DisplayName string `json:"displayName"`
	} `json:"location,omitempty"`
	Body *struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body,omitempty"`
	BodyPreview    string `json:"bodyPreview,omitempty"`
	ShowAs         string `json:"showAs,omitempty"`
	IsCancelled    bool   `json:"isCancelled,omitempty"`
	Type           string `json:"type,omitempty"`
	SeriesMasterID string `json:"seriesMasterId,omitempty"`
}

const graphSelect = "id,subject,start,end,isAllDay,location,bodyPreview,showAs,isCancelled,type,seriesMasterId"

func (g *Graph) call(ctx context.Context, method, path string, in, out any) error {
	u := path
	if !strings.HasPrefix(path, "https://") && !strings.HasPrefix(path, "http://") {
		u = g.base + path
	} else if !strings.HasPrefix(path, g.base+"/") {
		return fmt.Errorf("%s: refusing a link outside Graph: %s", g.acct.Name, path)
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Times come back in UTC; they are shown in the configured zone.
	req.Header.Set("Prefer", `outlook.timezone="UTC", outlook.body-content-type="text"`)
	resp, err := g.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", g.acct.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&e)
		if resp.StatusCode == http.StatusNotFound {
			return errNotFound
		}
		return fmt.Errorf("%s: Graph %s: %s %s", g.acct.Name, resp.Status, e.Error.Code, e.Error.Message)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

func (g *Graph) Calendars(ctx context.Context) ([]CalendarInfo, error) {
	var out []CalendarInfo
	next := "/me/calendars?$select=id,name,canEdit&$top=100"
	for next != "" {
		var page struct {
			Value []struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				CanEdit bool   `json:"canEdit"`
			} `json:"value"`
			Next string `json:"@odata.nextLink"`
		}
		if err := g.call(ctx, http.MethodGet, next, nil, &page); err != nil {
			return nil, err
		}
		for _, c := range page.Value {
			out = append(out, CalendarInfo{ID: c.ID, Name: c.Name, ReadOnly: !c.CanEdit})
		}
		next = page.Next
	}
	return out, nil
}

func (g *Graph) Events(ctx context.Context, cal CalendarInfo, from, to time.Time) ([]Item, error) {
	q := url.Values{
		"startDateTime": {from.UTC().Format(time.RFC3339)},
		"endDateTime":   {to.UTC().Format(time.RFC3339)},
		"$select":       {graphSelect},
		"$top":          {"200"},
		"$orderby":      {"start/dateTime"},
	}
	next := "/me/calendars/" + url.PathEscape(cal.ID) + "/calendarView?" + q.Encode()
	var out []Item
	for next != "" && len(out) < 5000 {
		var page struct {
			Value []graphEvent `json:"value"`
			Next  string       `json:"@odata.nextLink"`
		}
		if err := g.call(ctx, http.MethodGet, next, nil, &page); err != nil {
			return nil, err
		}
		for _, e := range page.Value {
			if e.IsCancelled {
				continue
			}
			out = append(out, g.item(e, cal.ID))
		}
		next = page.Next
	}
	return out, nil
}

func (g *Graph) parseTime(t *graphTime) time.Time {
	if t == nil {
		return time.Time{}
	}
	loc := time.UTC
	if t.TimeZone != "" && t.TimeZone != "UTC" {
		if l, err := time.LoadLocation(t.TimeZone); err == nil {
			loc = l
		}
	}
	for _, f := range []string{"2006-01-02T15:04:05.9999999", "2006-01-02T15:04:05"} {
		if v, err := time.ParseInLocation(f, t.DateTime, loc); err == nil {
			return v
		}
	}
	return time.Time{}
}

func (g *Graph) item(e graphEvent, calID string) Item {
	it := Item{ID: e.ID, CalendarID: calID, Start: g.parseTime(e.Start), End: g.parseTime(e.End)}
	if e.Subject != nil {
		it.Title = *e.Subject
	}
	if e.Location != nil {
		it.Location = e.Location.DisplayName
	}
	it.Description = e.BodyPreview
	if e.Body != nil && e.Body.Content != "" {
		it.Description = e.Body.Content
	}
	it.AllDay = e.IsAllDay != nil && *e.IsAllDay
	if it.AllDay {
		// All-day events are dates: keep the calendar day, in the configured zone.
		s, en := it.Start, it.End
		it.Start = time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, g.loc)
		it.End = time.Date(en.Year(), en.Month(), en.Day(), 0, 0, 0, 0, g.loc)
	}
	it.Free = e.ShowAs == "free"
	it.Recurring = e.Type == "occurrence" || e.Type == "exception" || e.Type == "seriesMaster"
	it.Cancelled = e.IsCancelled
	return it
}

func (g *Graph) Get(ctx context.Context, calID, id string) (*Item, error) {
	var e graphEvent
	if err := g.call(ctx, http.MethodGet, "/me/events/"+url.PathEscape(id)+"?$select="+graphSelect+",body", nil, &e); err != nil {
		return nil, err
	}
	it := g.item(e, calID)
	return &it, nil
}

func (g *Graph) times(start, end time.Time, allDay bool) (*graphTime, *graphTime) {
	if allDay {
		s, e := start.In(g.loc), end.In(g.loc)
		return &graphTime{s.Format("2006-01-02") + "T00:00:00", "UTC"}, &graphTime{e.Format("2006-01-02") + "T00:00:00", "UTC"}
	}
	f := "2006-01-02T15:04:05"
	return &graphTime{start.UTC().Format(f), "UTC"}, &graphTime{end.UTC().Format(f), "UTC"}
}

func textBody(s string) *struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
} {
	return &struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	}{"text", s}
}

func location(s string) *struct {
	DisplayName string `json:"displayName"`
} {
	return &struct {
		DisplayName string `json:"displayName"`
	}{s}
}

func (g *Graph) Create(ctx context.Context, calID string, in EventInput) (*Item, error) {
	e := graphEvent{Subject: &in.Title, IsAllDay: &in.AllDay}
	e.Start, e.End = g.times(in.Start, in.End, in.AllDay)
	if in.Location != "" {
		e.Location = location(in.Location)
	}
	if in.Description != "" {
		e.Body = textBody(in.Description)
	}
	var out graphEvent
	if err := g.call(ctx, http.MethodPost, "/me/calendars/"+url.PathEscape(calID)+"/events", e, &out); err != nil {
		return nil, err
	}
	it := g.item(out, calID)
	return &it, nil
}

func (g *Graph) Update(ctx context.Context, calID, id string, p EventPatch) (*Item, error) {
	var e graphEvent
	e.Subject = p.Title
	if p.Location != nil {
		e.Location = location(*p.Location)
	}
	if p.Description != nil {
		e.Body = textBody(*p.Description)
	}
	if p.Start != nil || p.End != nil || p.AllDay != nil {
		cur, err := g.Get(ctx, calID, id)
		if err != nil {
			return nil, err
		}
		start, end, allDay := cur.Start, cur.End, cur.AllDay
		if p.AllDay != nil {
			allDay = *p.AllDay
		}
		if p.Start != nil {
			end = p.Start.Add(end.Sub(start))
			start = *p.Start
		}
		if p.End != nil {
			end = *p.End
		}
		e.IsAllDay = &allDay
		e.Start, e.End = g.times(start, end, allDay)
	}
	var out graphEvent
	if err := g.call(ctx, http.MethodPatch, "/me/events/"+url.PathEscape(id), e, &out); err != nil {
		return nil, err
	}
	it := g.item(out, calID)
	return &it, nil
}

func (g *Graph) Delete(ctx context.Context, calID, id string) error {
	return g.call(ctx, http.MethodDelete, "/me/events/"+url.PathEscape(id), nil, nil)
}
