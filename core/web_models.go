package main

import (
	"errors"
	"log"
	"net/http"
	"slices"
	"strings"
)

// The models of a self-hosted model server, in the provider form: Eriphe
// asks the server for them (GET /v1/models, through the device that lends
// it or at its address) and offers them as boxes to tick, instead of names
// to type. A device chosen lists its models at once; an address is asked
// with "Detect the models". Names typed by hand stay possible when the
// server cannot be asked. On an edit, the models the server serves that
// the account does not use yet are offered as new.

// modelPick is one model offered.
type modelPick struct {
	ID      string
	Checked bool
	New     bool // served, not used yet by the account being edited
	Gone    bool // used or ticked, but no longer served
}

// modelPicker is the form's list of models.
type modelPicker struct {
	Name   string // the provider being edited: its stored device, address and key are asked
	Picks  []modelPick
	Manual string    // names typed by hand
	Result *testView // what the server answered, once asked
	Auto   bool      // ask the server as soon as shown
}

// ManualOpen tells whether the names typed by hand are offered: when the
// server could not be asked, served nothing, or names were typed.
func (p modelPicker) ManualOpen() bool {
	return p.Manual != "" || p.Result != nil && p.Result.State != "ok"
}

// Ticked are the models ticked, in their order: the first is the default.
func (p modelPicker) Ticked() []string {
	var out []string
	for _, m := range p.Picks {
		if m.Checked {
			out = append(out, m.ID)
		}
	}
	return out
}

// pickModels lists the models to offer: the server's, in its order, then
// those used or ticked that it does not list. ticked is what the form
// posted, nil when it showed no list yet: then an edit ticks the models
// the account uses, and a new account the server's first.
func pickModels(used, ticked []string, edit bool, served *testView) []modelPick {
	var listed []string
	if served != nil && served.State == "ok" {
		listed = served.Models
	}
	chosen := ticked
	if ticked == nil {
		chosen = used
		if !edit && len(listed) > 0 {
			chosen = listed[:1]
		}
	}
	var out []modelPick
	seen := map[string]bool{}
	for _, id := range listed {
		if !seen[id] {
			seen[id] = true
			out = append(out, modelPick{ID: id, Checked: slices.Contains(chosen, id), New: edit && !slices.Contains(used, id)})
		}
	}
	for _, id := range slices.Concat(used, ticked) {
		if !seen[id] && checkModelIDs([]string{id}) == nil {
			seen[id] = true
			out = append(out, modelPick{ID: id, Checked: slices.Contains(chosen, id), Gone: listed != nil})
		}
	}
	return out
}

// postedTicks are the models a form ticked, nil when it showed no list
// (or asked to start over: another device or address).
func postedTicks(r *http.Request) []string {
	if r.PostFormValue("picked") == "" || r.PostFormValue("reset") != "" {
		return nil
	}
	out := []string{}
	for _, m := range r.PostForm["model"] {
		if m = strings.TrimSpace(m); m != "" && !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

// selfHostedTarget is the model server a form names: on a device or at an
// address, with its key. On an edit (name), what the form leaves empty is
// the stored one's.
func (w *web) selfHostedTarget(r *http.Request, name string) (where, base, node, key string, err error) {
	where = r.PostFormValue("where")
	base = strings.TrimRight(strings.TrimSpace(r.PostFormValue("base_url")), "/")
	node, key = r.PostFormValue("node"), r.PostFormValue("key")
	if name != "" {
		p, ok := w.c.conf().Providers[name]
		if !ok || p.Kind != selfHostedKind {
			return "", "", "", "", errors.New("ptest.unknown")
		}
		if base == "" {
			base = p.BaseURL
		}
		if node == "" {
			node = p.Node
		}
		if strings.TrimSpace(key) == "" && p.Secret != "" {
			if key, _ = readSecret(p.Secret); key == "" {
				return "", "", "", "", errors.New("ptest.nosecret")
			}
		}
		if where == "" && p.Node != "" {
			where = whereNode
		}
	}
	if where != whereNode {
		where = whereAddress
	}
	return where, base, node, key, nil
}

// askModels asks the form's model server for its models; nil when there
// is nothing to ask yet (no address typed).
func (w *web) askModels(r *http.Request, s *session, name string) *testView {
	lang := w.lang(r)
	where, base, node, key, err := w.selfHostedTarget(r, name)
	switch {
	case err != nil:
		return &testView{State: "err", Text: tr(lang, err.Error())}
	case where == whereAddress && base == "":
		return nil
	case !w.testLimit.allow(s.ID, w.now()):
		return &testView{State: "err", Text: tr(lang, "test.ratelimit")}
	}
	v := w.checkSelfHostedForm(r, lang, where, base, node, key)
	log.Printf("web: self-hosted models asked (%s): %s, %d", where, v.State, len(v.Models)) // never the key
	return &v
}

// handleDetectModels lists the form's model server's models (POST
// /settings/provider-models, /settings/provider-models/{name} on an edit).
func (w *web) handleDetectModels(rw http.ResponseWriter, r *http.Request, s *session) {
	r.ParseForm()
	name := r.PathValue("name")
	var used []string
	if name != "" {
		used = w.c.conf().Providers[name].Models
	}
	pk := modelPicker{Name: name, Manual: r.PostFormValue("models"), Result: w.askModels(r, s, name)}
	pk.Picks = pickModels(used, postedTicks(r), name != "", pk.Result)
	w.render(rw, r, s, "settings", "model-picker-fragment", page{Data: pk})
}
