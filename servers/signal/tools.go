package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Every tool is a setup tool (servers/README.md): marked eriphe/setup, it
// is left out of the server's tools, so no task and not the brain ever get
// it. eriphe-core alone registers Eriphe's number and talks through it, as
// its Signal channel.

var setupMeta = mcp.Meta{"eriphe/setup": true}

const instructions = "Eriphe's own Signal account, run by signal-cli. Every tool is for eriphe-core's Signal channel only."

var (
	numberRe    = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
	receiveWait = 50 * time.Second // the longest receive waits
)

// signalServer is the server: signal-cli and what it received.
type signalServer struct {
	cli   *cli
	inbox *inbox
	// reg runs register, verify, set_pin and remove one at a time:
	// signal-cli's account files do not take two at once.
	reg sync.Mutex
}

// account is the number registered, "" when none is.
func (s *signalServer) account(ctx context.Context) (string, error) {
	var raw []json.RawMessage
	if err := s.cli.call(ctx, "listAccounts", nil, &raw); err != nil {
		return "", err
	}
	for _, r := range raw {
		var number string
		if json.Unmarshal(r, &number) != nil {
			var a struct {
				Number string `json:"number"`
			}
			json.Unmarshal(r, &a)
			number = a.Number
		}
		if number != "" {
			return number, nil
		}
	}
	return "", nil
}

func (s *signalServer) mustAccount(ctx context.Context) (string, error) {
	number, err := s.account(ctx)
	if err == nil && number == "" {
		err = errors.New("no number is registered")
	}
	return number, err
}

type statusOut struct {
	Number  string `json:"number,omitempty"` // the number registered, none before
	Version string `json:"version,omitempty"`
}

type registerIn struct {
	Number  string `json:"number" jsonschema:"Eriphe's number, international format: +33123456789"`
	Captcha string `json:"captcha,omitempty" jsonschema:"the signalcaptcha:// link from signalcaptchas.org, when Signal asks for one"`
	Voice   bool   `json:"voice,omitempty" jsonschema:"the code by a voice call instead of an SMS (a landline)"`
}

type verifyIn struct {
	Number string `json:"number"`
	Code   string `json:"code" jsonschema:"the code Signal sent"`
	PIN    string `json:"pin,omitempty" jsonschema:"the number's registration lock PIN, when it has one"`
}

type pinIn struct {
	PIN string `json:"pin" jsonschema:"a registration lock PIN; empty removes it"`
}

// doneOut says whether it worked; Error is why not, in signal-cli's words.
type doneOut struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Captcha is set when Signal asks for a captcha first.
	Captcha bool `json:"captcha,omitempty"`
}

func done(err error) doneOut {
	if err == nil {
		return doneOut{OK: true}
	}
	msg := err.Error()
	return doneOut{Error: msg, Captcha: strings.Contains(strings.ToLower(msg), "captcha")}
}

type sendIn struct {
	To   string `json:"to" jsonschema:"the recipient: a number or an ACI (UUID)"`
	Text string `json:"text"`
}

type sendOut struct {
	Timestamp int64 `json:"timestamp"` // the message's, which a reaction to it names
}

type receiveIn struct {
	After int64 `json:"after,omitempty" jsonschema:"the last seq received; none for everything kept"`
	Wait  int   `json:"wait,omitempty" jsonschema:"seconds to wait for a message when none is there, up to 50"`
	Max   int   `json:"max,omitempty" jsonschema:"at most this many messages, 100 by default"`
}

type receiveOut struct {
	Messages []message `json:"messages"`
	Last     int64     `json:"last"` // to give as after next
}

func tool[In, Out any](s *mcp.Server, name, desc string, ann *mcp.ToolAnnotations, h func(context.Context, In) (Out, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: desc, Meta: setupMeta, Annotations: ann},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
			start := time.Now()
			out, err := h(ctx, in)
			if name != "receive" { // no line every 50 s
				log.Printf("tool=%s ms=%d ok=%t", name, time.Since(start).Milliseconds(), err == nil)
			}
			return nil, out, err
		})
}

func ptr[T any](v T) *T { return &v }

func newServer(s *signalServer, version string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "eriphe-signal", Version: version}, &mcp.ServerOptions{Instructions: instructions})
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)}
	act := &mcp.ToolAnnotations{OpenWorldHint: ptr(true)}

	tool(srv, "status", "Setup only: the number registered, if any, and signal-cli's version.", ro,
		func(ctx context.Context, _ struct{}) (statusOut, error) {
			number, err := s.account(ctx)
			if err != nil {
				return statusOut{}, err
			}
			var v struct {
				Version string `json:"version"`
			}
			s.cli.call(ctx, "version", nil, &v)
			return statusOut{Number: number, Version: v.Version}, nil
		})

	tool(srv, "register", "Setup only: ask Signal for a code to register Eriphe's number, by SMS or voice call. Signal may ask for a captcha first (captcha: true): solve it at https://signalcaptchas.org/registration/generate.html and give the signalcaptcha:// link.", act,
		func(ctx context.Context, in registerIn) (doneOut, error) {
			s.reg.Lock()
			defer s.reg.Unlock()
			if !numberRe.MatchString(in.Number) {
				return doneOut{Error: "not a number in international format (+33123456789)"}, nil
			}
			if number, err := s.account(ctx); err != nil {
				return done(err), nil
			} else if number != "" && number != in.Number {
				return doneOut{Error: "another number is registered: remove it first"}, nil
			}
			p := map[string]any{"account": in.Number, "voice": in.Voice}
			if c := strings.TrimSpace(in.Captcha); c != "" {
				p["captcha"] = c
			}
			return done(s.cli.call(ctx, "register", p, nil)), nil
		})

	tool(srv, "verify", "Setup only: finish registering the number with the code Signal sent (and its PIN, when it has one).", act,
		func(ctx context.Context, in verifyIn) (doneOut, error) {
			s.reg.Lock()
			defer s.reg.Unlock()
			if !numberRe.MatchString(in.Number) {
				return doneOut{Error: "not a number in international format (+33123456789)"}, nil
			}
			p := map[string]any{"account": in.Number, "verificationCode": strings.ReplaceAll(strings.TrimSpace(in.Code), "-", "")}
			if in.PIN != "" {
				p["pin"] = in.PIN
			}
			err := s.cli.call(ctx, "verify", p, nil)
			if err == nil {
				s.cli.restart() // to receive with the new account
			}
			return done(err), nil
		})

	tool(srv, "set_pin", "Setup only: set the number's registration lock PIN, so nobody else can register it; an empty one removes it.", act,
		func(ctx context.Context, in pinIn) (doneOut, error) {
			s.reg.Lock()
			defer s.reg.Unlock()
			number, err := s.mustAccount(ctx)
			if err != nil {
				return done(err), nil
			}
			if in.PIN == "" {
				return done(s.cli.call(ctx, "removePin", map[string]any{"account": number}, nil)), nil
			}
			return done(s.cli.call(ctx, "setPin", map[string]any{"account": number, "pin": in.PIN}, nil)), nil
		})

	tool(srv, "remove", "Setup only: unregister the number from Signal and delete its data here.", act,
		func(ctx context.Context, _ struct{}) (doneOut, error) {
			s.reg.Lock()
			defer s.reg.Unlock()
			number, err := s.mustAccount(ctx)
			if err != nil {
				return done(err), nil
			}
			if err := s.cli.call(ctx, "unregister", map[string]any{"account": number}, nil); err != nil {
				log.Printf("unregister: %v", err) // its data goes anyway
			}
			err = s.cli.call(ctx, "deleteLocalAccountData", map[string]any{"account": number, "ignoreRegistered": true}, nil)
			if err == nil {
				s.cli.restart()
			}
			return done(err), nil
		})

	tool(srv, "send", "Setup only: send a text from Eriphe's number.", act,
		func(ctx context.Context, in sendIn) (sendOut, error) {
			number, err := s.mustAccount(ctx)
			if err != nil {
				return sendOut{}, err
			}
			if strings.TrimSpace(in.To) == "" || in.Text == "" {
				return sendOut{}, errors.New("to and text are needed")
			}
			var r struct {
				Timestamp int64 `json:"timestamp"`
				Results   []struct {
					Type string `json:"type"`
				} `json:"results"`
			}
			if err := s.cli.call(ctx, "send", map[string]any{"account": number, "recipient": []string{in.To}, "message": in.Text}, &r); err != nil {
				return sendOut{}, err
			}
			for _, x := range r.Results {
				if x.Type != "" && x.Type != "SUCCESS" {
					return sendOut{}, fmt.Errorf("not sent: %s", strings.ToLower(x.Type))
				}
			}
			return sendOut{Timestamp: r.Timestamp}, nil
		})

	tool(srv, "receive", "Setup only: the messages received after a sequence number, waiting up to 50 seconds when none is there.", ro,
		func(ctx context.Context, in receiveIn) (receiveOut, error) {
			wait := min(max(time.Duration(in.Wait)*time.Second, 0), receiveWait)
			n := in.Max
			if n <= 0 || n > 100 {
				n = 100
			}
			msgs, last := s.inbox.after(ctx, in.After, n, wait)
			if msgs == nil {
				msgs = []message{}
			}
			return receiveOut{Messages: msgs, Last: last}, nil
		})
	return srv
}
