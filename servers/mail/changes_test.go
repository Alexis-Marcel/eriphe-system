package main

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A client that subscribed to mail://changes hears each change the cache
// sees, wherever it was made, and mail://received's subscribers each new
// message; the resource says which folder changed, and when.
func TestChangesNotified(t *testing.T) {
	e := newEnv(t, rev2)
	m, err := NewMailer(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := newServer(m, "test") // before the cache, as main does
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.StartCache(ctx, testCacheOptions())
	for _, a := range m.accounts {
		select {
		case <-a.cache.ready:
		case <-time.After(10 * time.Second):
			t.Fatal("cache not warm")
		}
	}
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	var mu sync.Mutex
	var heard []string
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, &mcp.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, r *mcp.ResourceUpdatedNotificationRequest) {
			mu.Lock()
			heard = append(heard, r.Params.URI)
			mu.Unlock()
		},
	}).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	if caps := cs.InitializeResult().Capabilities; caps.Resources == nil || !caps.Resources.Subscribe {
		t.Fatalf("no subscriptions offered: %+v", caps.Resources)
	}
	for _, uri := range []string{changesURI, receivedURI} {
		if err := cs.Subscribe(ctx, &mcp.SubscribeParams{URI: uri}); err != nil {
			t.Fatal(err)
		}
	}

	deliver(t, e.alice, "INBOX", samMail)
	waitFor(t, "the notifications", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.Contains(heard, changesURI) && slices.Contains(heard, receivedURI)
	})
	res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: changesURI})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Folders []folderChange `json:"folders"`
	}
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &doc); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(doc.Folders, func(f folderChange) bool { return f.Account == "perso" && f.Folder == "INBOX" })
	if i < 0 || doc.Folders[i].New != 1 || doc.Folders[i].Changed == "" {
		t.Fatalf("changes %+v", doc.Folders)
	}
}

// A folder's last change replaces its one before.
func TestChangesLog(t *testing.T) {
	l := &changesLog{}
	now := time.Now()
	l.mailChanged(MailboxChange{Account: "perso", Folder: "INBOX", New: 2}, now)
	l.mailChanged(MailboxChange{Account: "perso", Folder: "Sent", New: 1}, now)
	l.mailChanged(MailboxChange{Account: "perso", Folder: "INBOX", Gone: 1}, now.Add(time.Minute))
	if len(l.folders) != 2 || l.folders[0].Gone != 1 || l.folders[0].New != 0 || l.folders[1].Folder != "Sent" {
		t.Fatalf("%+v", l.folders)
	}
}
