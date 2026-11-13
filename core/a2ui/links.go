package a2ui

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// A Link opens a page outside the workspace, in a new tab, on the user's
// click only. Its address must not carry anything out (A2UI #4045): an
// author that read something it should not pass on could otherwise build
// an address with it in its path or query, and the user's click would send
// it. So a Link's url is never computed: it is a literal the author wrote,
// or a binding to a field that is already an address as a whole (a page's
// url a tool gives); never a function call (formatString), which could
// build one from the data: the schema has no such form (LinkUrl). Code
// checks the literal when the surface is written (CheckLinks) and every
// bound value each time it is sent to the page (LinkURLOK); the page checks
// again and shows the address in clear.

// LinkURLMax is the longest address of a Link, in bytes.
const LinkURLMax = 2048

// IsLink tells whether a component's property is a Link's address.
func IsLink(componentType, prop string) bool { return componentType == "Link" && prop == "url" }

// LinkURLOK tells whether a Link may open an address: http or https, a host, no
// user or password, nothing a reader could misread (a space, a control
// character, a backslash), at most LinkURLMax bytes.
func LinkURLOK(raw string) bool {
	if raw == "" || len(raw) > LinkURLMax || strings.ContainsFunc(raw, func(r rune) bool {
		return r <= ' ' || r == 0x7f || r == '\\' || (r >= 0x80 && r <= 0x9f)
	}) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || u.User != nil || u.Hostname() == "" {
		return false
	}
	return true
}

// CheckLinks checks the Links of a surface's components as an author
// writes them: a literal url must be an address LinkURLOK accepts; a bound
// one is checked where its value is (core, with the data). Each error's
// path points at the url ("/components/2/url").
func CheckLinks(surfaceID string, components json.RawMessage) []ValidationError {
	var list []map[string]any
	if json.Unmarshal(components, &list) != nil {
		return nil
	}
	var errs []ValidationError
	for i, comp := range list {
		name, _ := comp["component"].(string)
		raw, ok := comp["url"].(string)
		if !IsLink(name, "url") || !ok {
			continue
		}
		if !LinkURLOK(raw) {
			errs = append(errs, ValidationError{Code: CodeValidationFailed, SurfaceID: surfaceID, Path: "/components/" + strconv.Itoa(i) + "/url",
				Message: "a Link opens an http(s) address with a host, written as it is: no other scheme, no user or password, no space or backslash, at most " + strconv.Itoa(LinkURLMax) + " bytes"})
		}
	}
	return errs
}
