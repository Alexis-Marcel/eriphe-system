// Package a2ui speaks A2UI v1.0 (https://github.com/a2ui-project/a2ui), the
// format of a surface of Eriphe's workspace, and owns the catalog of shapes
// a surface is made of.
//
// A model writes a surface; this package checks it before anything shows
// it: the published v1.0 JSON Schemas (embedded in spec/, Apache-2.0), the
// catalog's components and functions, then what a schema cannot say (one
// root, references that resolve, no cycle, allowed parents and children,
// relative paths only inside a list template). Its errors have A2UI's shape
// (VALIDATION_FAILED, a path, a message), ready to go back to the author.
// It also tells a model what the catalog offers (Catalog.Prompt).
//
// The catalog holds shapes only (text, metric, chart, table, message, day,
// …): no component belongs to a tool, and nothing here knows one.
package a2ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Version is the A2UI version this package speaks.
const Version = "1.0"

// Message is one agent-to-renderer message: exactly one of its fields
// besides Version is set.
type Message struct {
	Version          string            `json:"version"`
	CreateSurface    *CreateSurface    `json:"createSurface,omitempty"`
	UpdateComponents *UpdateComponents `json:"updateComponents,omitempty"`
	UpdateDataModel  *UpdateDataModel  `json:"updateDataModel,omitempty"`
	DeleteSurface    *DeleteSurface    `json:"deleteSurface,omitempty"`
}

// CreateSurface creates a surface, with its components and first data
// when they are known.
type CreateSurface struct {
	SurfaceID     string          `json:"surfaceId"`
	CatalogID     string          `json:"catalogId,omitempty"`
	SendDataModel bool            `json:"sendDataModel,omitempty"`
	Components    []Component     `json:"components,omitempty"`
	DataModel     json.RawMessage `json:"dataModel,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
}

// UpdateComponents adds or replaces components of a surface, by id.
type UpdateComponents struct {
	SurfaceID  string      `json:"surfaceId"`
	Components []Component `json:"components"`
}

// UpdateDataModel replaces the value at Path (a JSON pointer; empty or "/"
// for the whole model). A Value of null deletes it.
type UpdateDataModel struct {
	SurfaceID string          `json:"surfaceId"`
	Path      string          `json:"path,omitempty"`
	Value     json.RawMessage `json:"value"`
}

// DeleteSurface removes a surface and its data.
type DeleteSurface struct {
	SurfaceID string `json:"surfaceId"`
}

// Component is one entry of a surface's flat list: its id, its type in the
// catalog, and its other properties as written (the @-prefixed mixins such
// as @accessibility included).
type Component struct {
	ID        string
	Component string
	Props     map[string]json.RawMessage
}

// MarshalJSON writes the component as one flat object, keys sorted.
func (c Component) MarshalJSON() ([]byte, error) {
	keys := make([]string, 0, len(c.Props))
	for k := range c.Props {
		if k != "id" && k != "component" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b bytes.Buffer
	b.WriteByte('{')
	id, _ := json.Marshal(c.ID)
	kind, _ := json.Marshal(c.Component)
	fmt.Fprintf(&b, `"id":%s,"component":%s`, id, kind)
	for _, k := range keys {
		name, _ := json.Marshal(k)
		v := c.Props[k]
		if len(v) == 0 {
			v = json.RawMessage("null")
		}
		fmt.Fprintf(&b, ",%s:%s", name, v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// UnmarshalJSON reads a flat component object.
func (c *Component) UnmarshalJSON(raw []byte) error {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return err
	}
	*c = Component{}
	if v, ok := all["id"]; ok {
		if err := json.Unmarshal(v, &c.ID); err != nil {
			return fmt.Errorf("component id: %w", err)
		}
		delete(all, "id")
	}
	if v, ok := all["component"]; ok {
		if err := json.Unmarshal(v, &c.Component); err != nil {
			return fmt.Errorf("component type: %w", err)
		}
		delete(all, "component")
	}
	if len(all) > 0 {
		c.Props = all
	}
	return nil
}

// Binding points a property at a value of the surface's data model: a JSON
// pointer, absolute ("/sales/total") or, inside a list template, relative
// to the item ("subject").
type Binding struct {
	Path string `json:"@path"`
}

// FunctionCall calls one of the catalog's functions (or the system
// function @index) where a value is expected.
type FunctionCall struct {
	Call      string                     `json:"@call"`
	Args      map[string]json.RawMessage `json:"args,omitempty"`
	CatalogID string                     `json:"catalogId,omitempty"`
}

// ChildList is a container's children: fixed ids, or a template repeated
// for each item of a list in the data model.
type ChildList struct {
	IDs      []string
	Template *Template
}

// Template repeats the component ComponentID for each item at Path.
type Template struct {
	Path        string `json:"path"`
	ComponentID string `json:"componentId"`
}

// MarshalJSON writes the ids, or the template.
func (l ChildList) MarshalJSON() ([]byte, error) {
	if l.Template != nil {
		return json.Marshal(l.Template)
	}
	if l.IDs == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(l.IDs)
}

// UnmarshalJSON reads an array of ids or a template object.
func (l *ChildList) UnmarshalJSON(raw []byte) error {
	*l = ChildList{}
	if t := bytes.TrimSpace(raw); len(t) > 0 && t[0] == '{' {
		l.Template = &Template{}
		return json.Unmarshal(raw, l.Template)
	}
	return json.Unmarshal(raw, &l.IDs)
}

// Event is one renderer-to-agent message the workspace sends back: a user's
// action, or an error. Exactly one of Action and Error is set.
type Event struct {
	Version string  `json:"version"`
	Action  *Action `json:"action,omitempty"`
	Error   *Error  `json:"error,omitempty"`
}

// Action reports a user's action on a component, its context resolved.
type Action struct {
	Name              string                     `json:"name"`
	SurfaceID         string                     `json:"surfaceId"`
	SourceComponentID string                     `json:"sourceComponentId"`
	Timestamp         string                     `json:"timestamp"`
	Context           map[string]json.RawMessage `json:"context"`
	UserMessage       string                     `json:"userMessage,omitempty"`
}

// Error is an error the renderer reports. Path is set for validation
// errors; FunctionCallID replaces SurfaceID for a function call's error.
type Error struct {
	Code           string `json:"code"`
	SurfaceID      string `json:"surfaceId,omitempty"`
	Path           string `json:"path,omitempty"`
	Message        string `json:"message"`
	FunctionCallID string `json:"functionCallId,omitempty"`
}

// The codes of a validation error.
const (
	CodeValidationFailed = "VALIDATION_FAILED"
	CodeUnallowedParent  = "UNALLOWED_PARENT"
	CodeUnallowedChild   = "UNALLOWED_CHILD"
)

// ValidationError is what is wrong with what an author wrote, in A2UI's
// standard validation error shape: Path is a JSON pointer into what was
// validated, Message one short sentence.
type ValidationError struct {
	Code      string `json:"code"`
	SurfaceID string `json:"surfaceId"`
	Path      string `json:"path"`
	Message   string `json:"message"`
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s at %q: %s", e.Code, e.Path, e.Message)
}

// Event wraps the error in a renderer-to-agent message, as a renderer
// would send it to the author.
func (e ValidationError) Event() Event {
	return Event{Version: Version, Error: &Error{Code: e.Code, SurfaceID: e.SurfaceID, Path: e.Path, Message: e.Message}}
}
