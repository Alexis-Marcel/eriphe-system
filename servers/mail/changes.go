package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// What changed in the mail, for a client that keeps a view of it fresh:
// the resource mail://changes says, for each cached folder of each
// account, when the cache last saw it change (a message new or gone,
// flags changed), wherever the change was made (another mail app, the
// webmail, this server). A client subscribes to it (resources/subscribe)
// and hears notifications/resources/updated at each change, as it does
// for mail://received at each new message: eriphe-core reads its views of
// the mail again then (core/README.md, Live blocks).
//
//	{"folders": [{"account", "folder", "changed", "new", "gone", "flags"}]}
//
// Nothing is said without the cache: it is what sees the changes (IDLE,
// or its poll).

const changesURI = "mail://changes"

// folderChange is a folder's last change.
type folderChange struct {
	Account string `json:"account"`
	Folder  string `json:"folder"`
	Changed string `json:"changed"`
	New     int    `json:"new"`
	Gone    int    `json:"gone"`
	Flags   int    `json:"flags"`
}

type changesLog struct {
	mu      sync.Mutex
	folders []folderChange
}

func (l *changesLog) mailChanged(ch MailboxChange, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := folderChange{Account: ch.Account, Folder: ch.Folder, Changed: now.UTC().Format(time.RFC3339), New: ch.New, Gone: ch.Gone, Flags: ch.Flags}
	if i := slices.IndexFunc(l.folders, func(x folderChange) bool { return x.Account == ch.Account && x.Folder == ch.Folder }); i >= 0 {
		l.folders[i] = f
		return
	}
	l.folders = append(l.folders, f)
}

func (l *changesLog) doc() ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return json.Marshal(map[string]any{"folders": append([]folderChange{}, l.folders...)})
}

// subscriptions are the server's options that let a client subscribe to
// its resources: the SDK keeps who subscribed to what, and
// Server.ResourceUpdated tells them.
func subscriptions() *mcp.ServerOptions {
	return &mcp.ServerOptions{
		Instructions:       instructions,
		SubscribeHandler:   func(context.Context, *mcp.SubscribeRequest) error { return nil },
		UnsubscribeHandler: func(context.Context, *mcp.UnsubscribeRequest) error { return nil },
	}
}

// addChanges offers mail://changes, fed by the cache's changes (after
// any OnChange set earlier: mail://received is listed first), and tells
// the subscribers of each resource that changed: mail://changes at each
// change, mail://received when a new message was listed there.
func addChanges(s *mcp.Server, m *Mailer, l *changesLog) {
	prev := m.OnChange
	m.OnChange = func(ch MailboxChange) {
		if prev != nil {
			prev(ch)
		}
		l.mailChanged(ch, time.Now())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: changesURI})
		if strings.EqualFold(ch.Folder, "INBOX") && len(ch.Added) > 0 {
			s.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: receivedURI})
		}
	}
	s.AddResource(&mcp.Resource{URI: changesURI, Name: "changes", MIMEType: "application/json",
		Description: "When each cached folder of each account last changed (a message new or gone, flags changed), wherever the change was made. Subscribe to it to hear each change."},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			raw, err := l.doc()
			if err != nil {
				return nil, err
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: changesURI, MIMEType: "application/json", Text: string(raw)}}}, nil
		})
}
