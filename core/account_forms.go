package main

import (
	"errors"
	"fmt"
	"maps"
	"net/mail"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Account forms, built from a tool's card (accountShape, tool_cards.go):
// the user picks where the account is (a choice: a provider the tool knows,
// or one written out), then fills the fields that choice asks for, then its
// password. Nothing here knows a mailbox or a calendar: the card says which
// fields there are, where each goes in the account the server reads
// (servers/README.md), which choice sets what, and what is guessed from an
// address. Testing an account is the server's own (test_account, after the
// save: web_tools_dialog.go).

// accountShape is how an account of a tool is made with a form.
type accountShape struct {
	// Words is the stem of its choices' words: <Words>.<choice> is its
	// title, and Pick, Note, Steps, Soon and Howto are patterns of keys
	// (%s: the choice).
	Words                          string
	Pick, Note, Steps, Soon, Howto string
	SoonWhy                        string // the key under the list: why some come later
	Choices                        []accountChoice
	ChoiceField                    string // the form's field of the choice
	Fields                         []accountField
	// NameBy is the field that names the account; a name that is no
	// address takes the host of NameHost's value ("sam@cloud.example.org").
	NameBy, NameHost string
	// Guess finds the choice of an address's domain (in GuessFrom);
	// NoChoice is the error when it finds none.
	Guess     map[string]string
	GuessFrom string
	NoChoice  string
	Password  passwordWords
	// Server are options of the tool's server the form also sets (the
	// calendar's time zone): kept in its configuration, not the account's.
	Server []accountField
}

// accountChoice is where an account is.
type accountChoice struct {
	ID string
	// Set are the account's keys this choice sets: a provider the server
	// knows, or servers written out.
	Set map[string]any
	// Ask are the fields asked with it of the shape's fields marked Only.
	Ask  []string
	Link string // where its password is made
	// AppPass: the provider makes app passwords; SpacedPassword: it shows
	// them in groups of four, the spaces copied being no part of it.
	AppPass, SpacedPassword bool
	Soon, Hidden            bool
	// Custom: the choice of an account no other choice describes.
	Custom bool
	// SignIn is a provider: the account is signed in there, not made with
	// the form.
	SignIn string
}

// accountField is a field of the form and where it goes in the account.
type accountField struct {
	Name string // the form's field
	Key  string // where it goes: "address", "imap.host"
	Kind string // email, host, port, url, text, zone
	// Label, Placeholder and Hint are text keys; %s is the choice.
	// Else is the placeholder's key when the choice has none; Example a
	// placeholder as it is.
	Label, Placeholder, Hint, Else, Example string
	Default                                 string
	Only                                    bool // asked only by the choices that name it (Ask)
	Advanced, Wide                          bool
}

// passwordWords says how the form words the password.
type passwordWords struct {
	Label, Hint       string // an account's own password
	AppLabel, AppHint string // an app password
	HintOf            string // a choice's own hint, a pattern
	HowtoNote         string // under the steps of an app password
}

// accountForm is a form of an account, as posted or read back.
type accountForm struct {
	Choice string
	Values map[string]string // by field name
}

// fill fills a pattern of words with a choice: "mailp.%s.note"; a key
// without %s is as it is.
func fill(pattern, choice string) string {
	if !strings.Contains(pattern, "%s") {
		return pattern
	}
	return strings.ReplaceAll(pattern, "%s", choice)
}

// LabelKey, PlaceholderKey and HintKey are a field's words for a choice.
func (fl accountField) LabelKey(c string) string       { return fill(fl.Label, c) }
func (fl accountField) PlaceholderKey(c string) string { return fill(fl.Placeholder, c) }
func (fl accountField) HintKey(c string) string        { return fill(fl.Hint, c) }

// key is where a field goes in the account, by its name.
func (s *accountShape) key(name string) string {
	for _, fl := range s.Fields {
		if fl.Name == name {
			return fl.Key
		}
	}
	return name
}

// Value is a field's value.
func (f accountForm) Value(name string) string { return f.Values[name] }

func (s *accountShape) choice(id string) (accountChoice, bool) {
	if s == nil {
		return accountChoice{}, false
	}
	i := slices.IndexFunc(s.Choices, func(c accountChoice) bool { return c.ID == id })
	if i < 0 {
		return accountChoice{}, false
	}
	return s.Choices[i], true
}

// offered are the choices the list shows.
func (s *accountShape) offered() []accountChoice {
	return slices.DeleteFunc(slices.Clone(s.Choices), func(c accountChoice) bool { return c.Hidden })
}

// fields are the fields a choice asks for.
func (s *accountShape) fields(c accountChoice, advanced bool) []accountField {
	var out []accountField
	for _, f := range s.Fields {
		if f.Advanced == advanced && (!f.Only || slices.Contains(c.Ask, f.Name)) {
			out = append(out, f)
		}
	}
	return out
}

// readAccountForm reads a posted account form of a shape.
func readAccountForm(s *accountShape, v func(string) string) accountForm {
	if s == nil {
		return accountForm{}
	}
	f := accountForm{Choice: v(s.ChoiceField), Values: map[string]string{}}
	for _, fl := range append(slices.Clone(s.Fields), s.Server...) {
		if x := v(fl.Name); x != "" {
			f.Values[fl.Name] = x
		}
	}
	return f
}

// guess finds the choice of an address.
func (s *accountShape) guess(address string) string {
	_, domain, _ := strings.Cut(strings.ToLower(strings.TrimSpace(address)), "@")
	if c, ok := s.Guess[domain]; ok {
		return c
	}
	for d, c := range s.Guess { // a prefix: "outlook." for every country
		if strings.HasSuffix(d, ".") && strings.HasPrefix(domain, d) {
			return c
		}
	}
	return ""
}

// normalize checks the form and fills what it leaves out: the choice
// guessed from the address, the defaults. old is the account edited.
func (s *accountShape) normalize(f *accountForm, old *svcAccount) error {
	if f.Values == nil {
		f.Values = map[string]string{}
	}
	if f.Choice == "" { // automatic, for the API's sake
		if f.Choice = s.guess(f.Values[s.GuessFrom]); f.Choice == "" {
			if f.Values[s.GuessFrom] == "" {
				return s.missing(s.GuessFrom)
			}
			return errors.New(cmpOr(s.NoChoice, "presets.noprovider"))
		}
	}
	c, ok := s.choice(f.Choice)
	if !ok {
		return errors.New("presets.noprovider")
	}
	if c.SignIn != "" {
		return errors.New("presets.signin." + c.SignIn)
	}
	if c.Soon { // kept as it is on an edit; not added
		if old == nil {
			return errors.New("presets.soon")
		}
		if prev, ok := s.formOf(*old); !ok || prev.Choice != c.ID {
			return errors.New("presets.soon")
		}
	}
	asked := map[string]bool{}
	for _, fl := range append(s.fields(c, false), s.fields(c, true)...) {
		asked[fl.Name] = true
		x, err := fl.check(f.Values[fl.Name])
		if err != nil {
			return err
		}
		f.Values[fl.Name] = x
	}
	for _, fl := range s.Server {
		asked[fl.Name] = true
		x, err := fl.check(f.Values[fl.Name])
		if err != nil {
			return err
		}
		f.Values[fl.Name] = x
	}
	maps.DeleteFunc(f.Values, func(k, _ string) bool { return !asked[k] })
	return nil
}

// missing is the error of a required field left empty.
func (s *accountShape) missing(name string) error {
	for _, fl := range s.Fields {
		if fl.Name == name {
			_, err := fl.check("")
			return err
		}
	}
	return errors.New("presets.nouser")
}

// check checks a field's value by its kind, and gives it as it is kept.
func (fl accountField) check(v string) (string, error) {
	v = strings.TrimSpace(v)
	switch fl.Kind {
	case "email":
		a, err := mail.ParseAddress(v)
		if err != nil || !strings.Contains(a.Address, "@") {
			return "", errors.New("presets.badaddress")
		}
		return a.Address, nil
	case "host":
		if v == "" || strings.ContainsAny(v, " /:") {
			return "", errors.New("presets.badhosts")
		}
	case "port":
		if v == "" {
			return "", nil // its default
		}
		if p, err := strconv.Atoi(v); err != nil || p <= 0 || p > 65535 {
			return "", errors.New("form.badport")
		}
	case "url":
		u, err := url.Parse(v)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return "", errors.New("presets.badcaldav")
		}
		return u.String(), nil
	case "user":
		if v == "" {
			return "", errors.New("presets.nouser")
		}
	case "zone":
		v = cmpOr(v, fl.Default)
		if _, err := time.LoadLocation(v); err != nil {
			return "", errors.New("presets.badtz")
		}
	}
	return v, nil
}

// account is the account the form makes, as the tool's server reads it;
// its password's variable is given by putAccount.
func (s *accountShape) account(f accountForm) svcAccount {
	c, _ := s.choice(f.Choice)
	a := svcAccount{Name: f.Values[s.NameBy], Fields: map[string]any{}}
	if u, err := url.Parse(f.Values[s.NameHost]); !strings.Contains(a.Name, "@") && s.NameHost != "" && err == nil && u.Host != "" {
		a.Name += "@" + u.Hostname()
	}
	for k, v := range c.Set {
		a.set(k, v)
	}
	for _, fl := range s.Fields {
		v, ok := f.Values[fl.Name]
		switch {
		case !ok:
		case fl.Kind == "port":
			p, _ := strconv.Atoi(cmpOr(v, fl.Default))
			a.set(fl.Key, p)
		case v != "":
			a.set(fl.Key, v)
		}
	}
	for _, fl := range s.Fields { // a port of a server written out, at its default
		if fl.Kind == "port" && fl.Default != "" && a.get(strings.TrimSuffix(fl.Key, ".port")+".host") != "" && a.get(fl.Key) == "" {
			p, _ := strconv.Atoi(fl.Default)
			a.set(fl.Key, p)
		}
	}
	return a
}

// formOf reads the form back from an account; ok is false for one the
// form does not make (signed in at a provider, or a choice to come).
func (s *accountShape) formOf(a svcAccount) (accountForm, bool) {
	f := accountForm{Values: map[string]string{}}
	matches := func(c accountChoice) bool {
		for k, v := range c.Set {
			if fmt.Sprint(v) != a.get(k) {
				return false
			}
		}
		return true
	}
	for _, c := range s.Choices {
		if len(c.Set) > 0 && !c.Custom && matches(c) && (f.Choice == "" || len(c.Set) > 1) {
			f.Choice = c.ID
		}
	}
	if f.Choice == "" {
		for _, c := range s.Choices {
			if c.Custom && matches(c) {
				f.Choice = c.ID
				break
			}
		}
	}
	if f.Choice == "" {
		for _, fl := range s.Fields {
			if fl.Name == s.GuessFrom {
				f.Choice = s.guess(a.get(fl.Key))
			}
		}
	}
	c, _ := s.choice(f.Choice)
	for _, fl := range append(s.fields(c, false), s.fields(c, true)...) {
		v := a.get(fl.Key)
		if fl.Kind == "port" && v == fl.Default {
			v = ""
		}
		if v != "" {
			f.Values[fl.Name] = v
		}
	}
	return f, !a.signedIn() && c.ID != "" && c.SignIn == ""
}

// same tells whether two forms name the same account, whatever the
// server's options: if not, a new password is needed.
func (s *accountShape) same(f, o accountForm) bool {
	if f.Choice != o.Choice {
		return false
	}
	for _, fl := range s.Fields {
		if !strings.EqualFold(f.Values[fl.Name], o.Values[fl.Name]) {
			return false
		}
	}
	return true
}

// choiceOf is the choice an account comes from.
func (s *accountShape) choiceOf(a svcAccount) accountChoice {
	f, _ := s.formOf(a)
	c, _ := s.choice(f.Choice)
	return c
}

// password is the password typed, without the spaces of a provider that
// shows it in groups (SpacedPassword).
func (s *accountShape) password(f accountForm, pw string) string {
	if c, ok := s.choice(f.Choice); ok && c.SpacedPassword {
		return strings.ReplaceAll(pw, " ", "")
	}
	return pw
}
