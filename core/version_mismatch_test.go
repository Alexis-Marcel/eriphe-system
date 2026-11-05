package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// One of Eriphe's own tools that is not eriphe-core's version says so: in
// its row, to the CLI and the brain (list_tools), and with an error of its,
// so that the brain does not guess another cause.
func TestToolOfAnotherVersion(t *testing.T) {
	was := version
	t.Cleanup(func() { version = was })
	version = "0.79.1"
	tw, cc, _ := catalogWeb(t, nil)
	running := "0.71.0"
	cc.pool.dial = func(ctx context.Context, u *upstream) (mcp.Transport, error) {
		s := mcp.NewServer(&mcp.Implementation{Name: "eriphe-mail", Version: running}, nil)
		mcp.AddTool(s, &mcp.Tool{Name: "search_messages"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "secret MAIL_PASSWORD_X is empty"}}}, nil, nil
		})
		ct, st := mcp.NewInMemoryTransports()
		if _, err := s.Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return ct, nil
	}
	if err := cc.putAccount(mailService, accountPut{Lang: "fr", Account: svcAccount{Name: "sam@example.org", Address: "sam@example.org", Provider: "ovh"},
		Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	u := cc.pool.get("mail")
	waitStatus(t, u, statusReady)

	if info := cc.listServers("mail", false); len(info) != 1 || info[0].Version != "0.71.0" || info[0].Want != "0.79.1" {
		t.Fatalf("list: %+v", info)
	}
	res, err := u.call(context.Background(), "search_messages", nil)
	if err != nil || !res.IsError || len(res.Content) != 2 || !strings.Contains(res.Content[1].(*mcp.TextContent).Text, "runs version 0.71.0, Eriphe 0.79.1") {
		t.Fatalf("call: %v %+v", err, res)
	}
	_, body, _ := tw.do("GET", "/settings/tools", nil, nil)
	if !strings.Contains(body, "Cet outil tourne en version 0.71.0, Eriphe en 0.79.1") {
		t.Fatalf("row: %s", body[strings.Index(body, `id="tools-list"`):strings.Index(body, `id="add-dlg"`)])
	}

	// The right version: nothing said.
	running = "0.79.1"
	u.reconnect()
	for deadline := time.Now().Add(10 * time.Second); u.version() != "0.79.1"; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("version %q", u.version())
		}
	}
	if info := cc.listServers("mail", false); info[0].Want != "" {
		t.Fatalf("still behind: %+v", info[0])
	}
	if res, _ := u.call(context.Background(), "search_messages", nil); len(res.Content) != 1 {
		t.Fatalf("a note with the right version: %+v", res.Content)
	}
	// A development build of eriphe-core says nothing.
	version = "dev"
	if ownToolVersion(cc.conf().Servers["mail"]) != "" || ownToolVersion(Server{URL: "https://docs.example/mcp"}) != "" {
		t.Fatal("a version asked of a dev build, or of another server")
	}
}
