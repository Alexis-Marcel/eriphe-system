package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	testNumber = "+15555550100" // Eriphe's
	userUUID   = "0b1c2d3e-0000-4000-8000-000000000001"
	userNumber = "+15555550101"
)

// fakeCLI is signal-cli in JSON-RPC mode, as eriphe-signal uses it.
type fakeCLI struct {
	mu      sync.Mutex
	account string
	pin     string
	calls   []string
	sent    []map[string]any
	ts      int64
	starts  int
	out     *io.PipeWriter // the running one's stdout
	kill    func()
}

func (f *fakeCLI) start() (*proc, error) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan struct{})
	var once sync.Once
	kill := func() { once.Do(func() { inR.Close(); outW.Close(); close(done) }) }
	f.mu.Lock()
	f.starts++
	f.out, f.kill = outW, kill
	f.mu.Unlock()
	go func() {
		sc := bufio.NewScanner(inR)
		for sc.Scan() {
			var req struct {
				ID     string         `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			json.Unmarshal(sc.Bytes(), &req)
			result, rerr := f.handle(req.Method, req.Params)
			resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
			if rerr != "" {
				resp["error"] = map[string]any{"code": -1, "message": rerr}
			} else {
				resp["result"] = result
			}
			raw, _ := json.Marshal(resp)
			f.mu.Lock()
			outW.Write(append(raw, '\n'))
			f.mu.Unlock()
		}
		kill()
	}()
	return &proc{in: inW, out: outR, wait: func() error { <-done; return nil }, kill: kill}, nil
}

func (f *fakeCLI) handle(method string, p map[string]any) (any, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, method)
	switch method {
	case "listAccounts":
		if f.account == "" {
			return []any{}, ""
		}
		return []any{map[string]any{"number": f.account, "aci": "aci-1"}}, ""
	case "version":
		return map[string]any{"version": "0.14.10"}, ""
	case "register":
		if p["captcha"] == nil {
			return nil, "Captcha required for verification, use --captcha CAPTCHA"
		}
		return map[string]any{}, ""
	case "verify":
		if p["verificationCode"] != "123456" {
			return nil, "Verification failed: wrong code"
		}
		f.account = p["account"].(string)
		return map[string]any{}, ""
	case "setPin":
		f.pin = p["pin"].(string)
		return map[string]any{}, ""
	case "unregister":
		return map[string]any{}, ""
	case "deleteLocalAccountData":
		f.account = ""
		return map[string]any{}, ""
	case "send":
		f.sent = append(f.sent, p)
		f.ts++
		return map[string]any{"timestamp": f.ts, "results": []any{map[string]any{"type": "SUCCESS"}}}, ""
	}
	return nil, "unknown method " + method
}

// notify is signal-cli receiving an envelope.
func (f *fakeCLI) notify(t *testing.T, env map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "receive",
		"params": map[string]any{"envelope": env, "account": testNumber}})
	f.mu.Lock()
	out := f.out
	f.mu.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	out.Write(append(raw, '\n'))
}

func (f *fakeCLI) startsNow() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts
}

// session is eriphe-signal on the fake, and an MCP client of it.
func session(t *testing.T) (*mcp.ClientSession, *fakeCLI) {
	t.Helper()
	backoffMin = time.Millisecond
	f := &fakeCLI{ts: 1700000000000}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	box := newInbox()
	c := newCLI(f.start, box.add)
	go c.run(ctx)
	srv := newServer(&signalServer{cli: c, inbox: box}, "test")
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, f
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if out != nil && !res.IsError {
		reflect.ValueOf(out).Elem().SetZero() // a field left out is not the last answer's
		raw, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatal(err)
		}
	}
	return res
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timed out: " + what)
}

// Every tool is a setup tool: none is ever the brain's or a task's.
func TestEveryToolIsSetup(t *testing.T) {
	cs, _ := session(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
		if tl.Meta["eriphe/setup"] != true {
			t.Errorf("%s is not a setup tool", tl.Name)
		}
	}
	if got := strings.Join(names, ","); got != "receive,register,remove,send,set_pin,status,verify" {
		t.Errorf("tools %s", got)
	}
}

// Registering: a captcha first, then the code; signal-cli starts again to
// receive with the new account; a PIN; the number removed.
func TestRegister(t *testing.T) {
	cs, f := session(t)
	var st statusOut
	call(t, cs, "status", nil, &st)
	if st.Number != "" || st.Version != "0.14.10" {
		t.Fatalf("status %+v", st)
	}
	var d doneOut
	call(t, cs, "register", map[string]any{"number": "0612345678"}, &d)
	if d.OK || !strings.Contains(d.Error, "international") {
		t.Fatalf("a national number: %+v", d)
	}
	call(t, cs, "register", map[string]any{"number": testNumber, "voice": true}, &d)
	if d.OK || !d.Captcha {
		t.Fatalf("no captcha: %+v", d)
	}
	call(t, cs, "register", map[string]any{"number": testNumber, "voice": true, "captcha": " signalcaptcha://signal-hcaptcha.x.y "}, &d)
	if !d.OK {
		t.Fatalf("register %+v", d)
	}
	call(t, cs, "verify", map[string]any{"number": testNumber, "code": "999-999"}, &d)
	if d.OK || d.Error == "" {
		t.Fatalf("a wrong code: %+v", d)
	}
	starts := f.startsNow()
	call(t, cs, "verify", map[string]any{"number": testNumber, "code": "123-456"}, &d)
	if !d.OK {
		t.Fatalf("verify %+v", d)
	}
	waitFor(t, "signal-cli started again", func() bool { return f.startsNow() > starts })
	call(t, cs, "status", nil, &st)
	if st.Number != testNumber {
		t.Fatalf("status %+v", st)
	}
	call(t, cs, "register", map[string]any{"number": "+15555550199", "captcha": "x"}, &d)
	if d.OK || !strings.Contains(d.Error, "another number") {
		t.Fatalf("a second number: %+v", d)
	}
	call(t, cs, "set_pin", map[string]any{"pin": "4321"}, &d)
	if !d.OK || f.pin != "4321" {
		t.Fatalf("pin %+v %q", d, f.pin)
	}
	call(t, cs, "remove", nil, &d)
	if !d.OK {
		t.Fatalf("remove %+v", d)
	}
	call(t, cs, "status", nil, &st)
	if st.Number != "" {
		t.Fatalf("still %+v", st)
	}
}

// Sending gives the message's timestamp; receiving gives what someone sent,
// texts and reactions, not sync messages nor receipts, and waits for one.
func TestSendAndReceive(t *testing.T) {
	cs, f := session(t)
	if res := call(t, cs, "send", map[string]any{"to": userNumber, "text": "hello"}, nil); !res.IsError {
		t.Fatal("sent without a number")
	}
	f.mu.Lock()
	f.account = testNumber
	f.mu.Unlock()
	var s sendOut
	call(t, cs, "send", map[string]any{"to": userUUID, "text": "Bonjour"}, &s)
	if s.Timestamp != 1700000000001 || fmt.Sprint(f.sent[0]["recipient"]) != "["+userUUID+"]" || f.sent[0]["account"] != testNumber {
		t.Fatalf("sent %d %v", s.Timestamp, f.sent)
	}

	var r receiveOut
	call(t, cs, "receive", map[string]any{"wait": 0}, &r)
	if len(r.Messages) != 0 {
		t.Fatalf("received %+v", r)
	}
	from := map[string]any{"source": userNumber, "sourceNumber": userNumber, "sourceUuid": userUUID, "sourceName": "Sam",
		"timestamp": 1, "serverReceivedTimestamp": 1, "serverDeliveredTimestamp": 1}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for a, b := range from {
			m[a] = b
		}
		m[k] = v
		return m
	}
	go func() {
		time.Sleep(20 * time.Millisecond) // receive waits for it
		f.notify(t, with("syncMessage", map[string]any{"sentMessage": map[string]any{"message": "from my laptop"}}))
		f.notify(t, with("receiptMessage", map[string]any{"isDelivery": true}))
		f.notify(t, with("dataMessage", map[string]any{"timestamp": 1, "message": "Quoi de neuf ?"}))
		f.notify(t, with("dataMessage", map[string]any{"timestamp": 2, "message": "en groupe", "groupInfo": map[string]any{"groupId": "g", "revision": 1}}))
		f.notify(t, with("dataMessage", map[string]any{"timestamp": 3, "isProfileKeyUpdate": true}))
		f.notify(t, with("dataMessage", map[string]any{"timestamp": 4, "attachments": []any{map[string]any{"id": "a"}}}))
		f.notify(t, with("dataMessage", map[string]any{"timestamp": 5, "reaction": map[string]any{"emoji": "👍",
			"targetAuthor": testNumber, "targetSentTimestamp": s.Timestamp, "isRemove": false}}))
	}()
	call(t, cs, "receive", map[string]any{"after": r.Last, "wait": 5}, &r)
	var all []message
	all = append(all, r.Messages...)
	for len(all) < 4 {
		call(t, cs, "receive", map[string]any{"after": r.Last, "wait": 5}, &r)
		all = append(all, r.Messages...)
	}
	if len(all) != 4 {
		t.Fatalf("received %+v", all)
	}
	m := all[0]
	if m.From != userUUID || m.Number != userNumber || m.Name != "Sam" || m.Text != "Quoi de neuf ?" || m.Group {
		t.Fatalf("message %+v", m)
	}
	if !all[1].Group || !all[2].Attachment || all[2].Text != "" {
		t.Fatalf("group, attachment %+v", all[1:3])
	}
	if x := all[3].Reaction; x == nil || x.Emoji != "👍" || x.Target != s.Timestamp || x.Remove {
		t.Fatalf("reaction %+v", all[3])
	}
	call(t, cs, "receive", map[string]any{"after": r.Last}, &r)
	if len(r.Messages) != 0 {
		t.Fatalf("again %+v", r.Messages)
	}
	call(t, cs, "receive", map[string]any{"after": 0, "max": 2}, &r)
	if len(r.Messages) != 2 || r.Last != r.Messages[1].Seq {
		t.Fatalf("from the start %+v", r)
	}
}

// signal-cli that stops is started again, and a request waiting then fails.
func TestRestart(t *testing.T) {
	cs, f := session(t)
	var st statusOut
	call(t, cs, "status", nil, &st)
	f.mu.Lock()
	kill := f.kill
	f.mu.Unlock()
	kill()
	waitFor(t, "started again", func() bool { return f.startsNow() >= 2 })
	call(t, cs, "status", nil, &st)
	if st.Version == "" {
		t.Fatalf("status %+v", st)
	}
}

// A subscription's notification ({subscription, result}) is read too.
func TestInboxSubscriptionForm(t *testing.T) {
	b := newInbox()
	b.add(json.RawMessage(`{"subscription":0,"result":{"envelope":{"sourceUuid":"u","timestamp":7,"dataMessage":{"timestamp":7,"message":"hi"}},"account":"+1"}}`))
	msgs, _ := b.after(context.Background(), 0, 10, 0)
	if len(msgs) != 1 || msgs[0].Text != "hi" || msgs[0].From != "u" {
		t.Fatalf("%+v", msgs)
	}
	if msgs, _ := b.after(context.Background(), msgs[0].Seq+1000, 10, 0); len(msgs) != 1 {
		t.Fatalf("a seq ahead of this run's: %+v", msgs)
	}
}

// Without signal-cli (an arm64 machine), every call says so at once.
func TestMissing(t *testing.T) {
	c := newCLI(nil, nil)
	c.missing = "signal-cli is not in this image for arm64"
	go c.run(context.Background())
	if err := c.call(context.Background(), "listAccounts", nil, nil); err == nil || err.Error() != c.missing {
		t.Fatalf("%v", err)
	}
}
