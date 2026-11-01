package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// Home Assistant's own API, for presence (ha_presence.go): eriphe-core
// reads the house's registries and follows a few entities over HA's
// WebSocket API, and asks a speaker to announce, with the long-lived token
// the user gave the catalog's Home Assistant tool (the home-assistant
// preset): no other token, nothing to install in Home Assistant. What it
// reads stays in code: names are the user's own text, kept apart as
// external data; states become closed signals.

const (
	haReadLimit   = 16 << 20 // the registries of a big house
	haCallTime    = 20 * time.Second
	haMaxEntities = 5000
)

// haEndpoint is how eriphe-core reaches Home Assistant.
type haEndpoint struct {
	Base  string // http(s)://host[:port]
	Token string
}

func (e haEndpoint) wsURL() string {
	u := strings.Replace(e.Base, "https://", "wss://", 1)
	u = strings.Replace(u, "http://", "ws://", 1)
	return u + "/api/websocket"
}

var errNoHA = errors.New("Home Assistant is not connected: add it in Settings → Tools → Home Assistant")

// haLink finds the catalog's Home Assistant (its MCP server's address,
// /api/mcp) and its bearer token. The first one, by name, when there are
// several.
func (c *core) haLink() (haEndpoint, error) {
	if c.ha.endpoint != nil {
		return c.ha.endpoint()
	}
	conf := c.conf()
	names := make([]string, 0, len(conf.Servers))
	for name, s := range conf.Servers {
		if !s.local() && homeAssistantURL(s.URL) && s.authKind() == "bearer" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return haEndpoint{}, errNoHA
	}
	slices.Sort(names)
	s := conf.Servers[names[0]]
	tok, err := readSecret(s.Auth.BearerFile)
	if err != nil {
		return haEndpoint{}, fmt.Errorf("Home Assistant's token: %w", err)
	}
	p, err := url.Parse(s.URL)
	if err != nil || (p.Scheme != "http" && p.Scheme != "https") {
		return haEndpoint{}, errNoHA
	}
	return haEndpoint{Base: p.Scheme + "://" + p.Host, Token: tok}, nil
}

// haConn is one WebSocket session with Home Assistant.
type haConn struct {
	ws   *websocket.Conn
	next int
}

// dialHA opens a session and authenticates.
func dialHA(ctx context.Context, e haEndpoint) (*haConn, error) {
	ctx, cancel := context.WithTimeout(ctx, haCallTime)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, e.wsURL(), nil)
	if err != nil {
		return nil, fmt.Errorf("home assistant: %w", err)
	}
	ws.SetReadLimit(haReadLimit)
	h := &haConn{ws: ws}
	var hello struct {
		Type string `json:"type"`
	}
	if err := h.read(ctx, &hello); err != nil || hello.Type != "auth_required" {
		h.close()
		return nil, fmt.Errorf("home assistant: no auth_required (%v)", err)
	}
	if err := h.write(ctx, map[string]string{"type": "auth", "access_token": e.Token}); err != nil {
		h.close()
		return nil, err
	}
	if err := h.read(ctx, &hello); err != nil || hello.Type != "auth_ok" {
		h.close()
		return nil, errors.New("home assistant: the token was refused")
	}
	return h, nil
}

func (h *haConn) close() { h.ws.Close(websocket.StatusNormalClosure, "") }

func (h *haConn) read(ctx context.Context, v any) error {
	_, raw, err := h.ws.Read(ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func (h *haConn) write(ctx context.Context, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return h.ws.Write(ctx, websocket.MessageText, raw)
}

// haMsg is any message of Home Assistant's.
type haMsg struct {
	ID      int             `json:"id"`
	Type    string          `json:"type"`
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Event   json.RawMessage `json:"event"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// send sends a command and returns its id.
func (h *haConn) send(ctx context.Context, typ string, extra map[string]any) (int, error) {
	h.next++
	msg := map[string]any{"id": h.next, "type": typ}
	for k, v := range extra {
		msg[k] = v
	}
	return h.next, h.write(ctx, msg)
}

// command sends a command and waits for its result. Only before a
// subscription: other messages are dropped.
func (h *haConn) command(ctx context.Context, typ string, extra map[string]any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, haCallTime)
	defer cancel()
	id, err := h.send(ctx, typ, extra)
	if err != nil {
		return err
	}
	for {
		var m haMsg
		if err := h.read(ctx, &m); err != nil {
			return err
		}
		if m.ID != id || m.Type != "result" {
			continue
		}
		if !m.Success {
			code := "error"
			if m.Error != nil {
				code = m.Error.Code
			}
			return fmt.Errorf("home assistant: %s: %s", typ, code)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(m.Result, out)
	}
}

// haEntity is one entity as discovery keeps it: an id, a domain, a class,
// a room (its area, or its device's) and its state now. Name and area are
// the user's own words in Home Assistant: external data for the model,
// never put in a signal as such.
type haEntity struct {
	ID     string `json:"entity_id"`
	Name   string `json:"name,omitempty"`
	Domain string `json:"domain"`
	Class  string `json:"class,omitempty"`
	Area   string `json:"area,omitempty"`
	State  string `json:"state,omitempty"`
}

// haHouse is what discovery found: the candidates for presence, the
// house's areas, and its text-to-speech entities.
type haHouse struct {
	At       time.Time
	Entities []haEntity
	Areas    []string
	TTS      []string
	Partial  bool // the registries could not be read (a user that is not an admin): no areas
}

func (h haHouse) entity(id string) (haEntity, bool) {
	i := slices.IndexFunc(h.Entities, func(e haEntity) bool { return e.ID == id })
	if i < 0 {
		return haEntity{}, false
	}
	return h.Entities[i], true
}

// discover reads the registries and the states, and keeps the candidates.
func (h *haConn) discover(ctx context.Context) (haHouse, error) {
	var states []struct {
		ID    string         `json:"entity_id"`
		State string         `json:"state"`
		Attrs map[string]any `json:"attributes"`
	}
	if err := h.command(ctx, "get_states", nil, &states); err != nil {
		return haHouse{}, err
	}
	var areas []struct {
		ID   string `json:"area_id"`
		Name string `json:"name"`
	}
	var devices []struct {
		ID   string `json:"id"`
		Area string `json:"area_id"`
	}
	var entities []struct {
		ID       string `json:"entity_id"`
		Device   string `json:"device_id"`
		Area     string `json:"area_id"`
		Class    string `json:"device_class"`
		OrgClass string `json:"original_device_class"`
		Disabled string `json:"disabled_by"`
	}
	house := haHouse{At: time.Now()}
	errA := h.command(ctx, "config/area_registry/list", nil, &areas)
	errD := h.command(ctx, "config/device_registry/list", nil, &devices)
	errE := h.command(ctx, "config/entity_registry/list", nil, &entities)
	house.Partial = errA != nil || errD != nil || errE != nil
	areaName := map[string]string{}
	for _, a := range areas {
		areaName[a.ID] = a.Name
		if n := cleanRoom(a.Name); n != "" && !slices.Contains(house.Areas, n) {
			house.Areas = append(house.Areas, n)
		}
	}
	deviceArea := map[string]string{}
	for _, d := range devices {
		deviceArea[d.ID] = d.Area
	}
	type reg struct{ area, class string }
	regs := map[string]reg{}
	for _, e := range entities {
		if e.Disabled != "" {
			continue
		}
		r := reg{area: e.Area, class: e.Class}
		if r.area == "" {
			r.area = deviceArea[e.Device]
		}
		if r.class == "" {
			r.class = e.OrgClass
		}
		regs[e.ID] = r
	}
	for _, s := range states {
		domain, _, _ := strings.Cut(s.ID, ".")
		if domain == "tts" {
			house.TTS = append(house.TTS, s.ID)
			continue
		}
		r := regs[s.ID]
		class := r.class
		if class == "" {
			class, _ = s.Attrs["device_class"].(string)
		}
		name, _ := s.Attrs["friendly_name"].(string)
		e := haEntity{ID: s.ID, Name: name, Domain: domain, Class: class, Area: cleanRoom(areaName[r.area]), State: s.State}
		if haCandidate(e) && len(house.Entities) < haMaxEntities {
			house.Entities = append(house.Entities, e)
		}
	}
	sort.Slice(house.Entities, func(i, j int) bool { return house.Entities[i].ID < house.Entities[j].ID })
	slices.Sort(house.TTS)
	return house, nil
}

// haCandidate tells an entity that may say something of presence, or
// speak in a room. Everything else in the house is left out.
func haCandidate(e haEntity) bool {
	switch e.Domain {
	case "person", "device_tracker", "media_player", "assist_satellite":
		return true
	case "binary_sensor":
		return slices.Contains([]string{"occupancy", "presence", "motion"}, e.Class) || haFocusEntity(e.ID)
	case "sensor":
		return haRoomTracker(e.ID) || haFocusEntity(e.ID)
	case "input_boolean":
		return haSleepWord(e.ID) || haFocusEntity(e.ID) || strings.Contains(e.ID, "dnd") || strings.Contains(e.ID, "disturb")
	}
	return false
}

// haRoomTracker: a sensor that tells the room of a device (Bermuda's
// "Area" sensor, ESPresense's room), by its id.
func haRoomTracker(id string) bool {
	return strings.HasSuffix(id, "_area") || strings.HasSuffix(id, "_room")
}

// haFocusEntity: the companion app's focus sensor.
func haFocusEntity(id string) bool {
	return strings.HasSuffix(id, "_focus") || strings.HasSuffix(id, ".focus")
}

func haSleepWord(id string) bool {
	return strings.Contains(id, "sleep") || strings.Contains(id, "sommeil") || strings.Contains(id, "nuit") || strings.Contains(id, "night")
}

// haCall calls a service through the REST API: an announce, a speech.
func haCall(ctx context.Context, e haEndpoint, domain, service string, data map[string]any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, haCallTime)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Base+"/api/services/"+domain+"/"+service, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+e.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("home assistant: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("home assistant: %s.%s: %s", domain, service, resp.Status)
	}
	return nil
}
