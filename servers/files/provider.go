package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// A Provider is one cloud storage API: Google Drive now, Dropbox and
// OneDrive later. It only knows items by id; which items a call may reach
// is the guard's business (guard.go), never the provider's. A provider has
// no call that modifies or deletes an existing item: the server cannot
// touch the user's originals even by mistake.
type Provider interface {
	// Get reads one item's metadata, with its parents.
	Get(ctx context.Context, id string) (Item, error)
	// List lists a folder's direct children; foldersOnly keeps the folders.
	List(ctx context.Context, folderID string, foldersOnly bool, pageToken string) ([]Item, string, error)
	// Search finds items by name or content anywhere in the account.
	Search(ctx context.Context, text string, pageToken string) ([]Item, string, error)
	// Read gives a file's content: a native document exported to format
	// (markdown, text, csv or pdf; "" for the provider's best text), any
	// other file as stored. At most max bytes.
	Read(ctx context.Context, it Item, format string, max int64) (Content, error)
	// CreateFolder makes a new folder in parent.
	CreateFolder(ctx context.Context, parentID, name string) (Item, error)
	// CreateFile makes a new file in parent; convert asks the provider to
	// turn it into its native document (Google Docs, Sheets).
	CreateFile(ctx context.Context, parentID, name, mimeType string, data []byte, convert bool) (Item, error)
	// Roots are the top places to browse from: the user's own drive, shared
	// drives, what was shared with them.
	Roots(ctx context.Context) ([]Item, error)
	// CanWrite tells whether the granted scopes allow creating files.
	CanWrite() bool
}

var providers = []string{"google"}

func newProvider(a AccountConfig, timeout time.Duration) (Provider, error) {
	switch a.Provider {
	case "google":
		return newGoogle(a, timeout)
	}
	return nil, fmt.Errorf("unknown provider %q", a.Provider)
}

// Item is a file or a folder.
type Item struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	MimeType string    `json:"mime_type"`
	Folder   bool      `json:"folder,omitempty"`
	Size     int64     `json:"size,omitempty"`
	Modified time.Time `json:"modified,omitzero"`
	Link     string    `json:"link,omitempty"`
	Parents  []string  `json:"-"`
	// Target is a shortcut's target id: the guard checks it, not the
	// shortcut's own place.
	Target  string `json:"-"`
	Trashed bool   `json:"-"`
}

// Content is a file's content, as text or as bytes.
type Content struct {
	MimeType  string
	Text      string // set when the content is text
	Data      []byte // otherwise
	Truncated bool
	Note      string // how it was made, e.g. "first sheet only"
}

var (
	errNotFound = errors.New("not found")
	errTooLarge = errors.New("too large")
)
