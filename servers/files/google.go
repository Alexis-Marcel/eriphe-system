package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// Google Drive, over its REST API v3 (developers.google.com/workspace/drive/api/reference/rest/v3),
// with the account's refresh token: the access token is refreshed in memory
// as it expires, never written anywhere.

const (
	googleAPI    = "https://www.googleapis.com/drive/v3"
	googleUpload = "https://www.googleapis.com/upload/drive/v3"
	googleToken  = "https://oauth2.googleapis.com/token"

	scopeDrive         = "https://www.googleapis.com/auth/drive"
	scopeDriveReadonly = "https://www.googleapis.com/auth/drive.readonly"

	mimeFolder   = "application/vnd.google-apps.folder"
	mimeShortcut = "application/vnd.google-apps.shortcut"
	mimeDoc      = "application/vnd.google-apps.document"
	mimeSheet    = "application/vnd.google-apps.spreadsheet"
	mimeSlides   = "application/vnd.google-apps.presentation"
	mimeDrawing  = "application/vnd.google-apps.drawing"

	// sharedWithMe is the pseudo folder of what others shared.
	sharedWithMe = "shared-with-me"

	itemFields = "id,name,mimeType,parents,size,modifiedTime,webViewLink,trashed,shortcutDetails(targetId)"
)

type google struct {
	api, upload string
	http        *http.Client
	scopes      []string
}

func newGoogle(a AccountConfig, timeout time.Duration) (*google, error) {
	c, err := a.credentials()
	if err != nil {
		return nil, err
	}
	tokenURL := cmpOr(a.TokenURL, googleToken)
	cfg := &oauth2.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret,
		Endpoint: oauth2.Endpoint{TokenURL: tokenURL, AuthStyle: oauth2.AuthStyleInParams}}
	base := &http.Client{Timeout: timeout}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, base)
	ts := oauth2.ReuseTokenSource(nil, cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: c.RefreshToken}))
	hc := &http.Client{Timeout: timeout, Transport: &oauth2.Transport{Source: ts, Base: base.Transport}}
	return &google{api: strings.TrimRight(cmpOr(a.APIURL, googleAPI), "/"),
		upload: strings.TrimRight(cmpOr(a.UploadURL, googleUpload), "/"), http: hc, scopes: a.Scopes}, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (g *google) CanWrite() bool {
	return slices.Contains(g.scopes, scopeDrive)
}

type driveFile struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	MimeType        string   `json:"mimeType"`
	Parents         []string `json:"parents"`
	Size            string   `json:"size"`
	ModifiedTime    string   `json:"modifiedTime"`
	WebViewLink     string   `json:"webViewLink"`
	Trashed         bool     `json:"trashed"`
	ShortcutDetails *struct {
		TargetID string `json:"targetId"`
	} `json:"shortcutDetails"`
}

func (f driveFile) item() Item {
	it := Item{ID: f.ID, Name: f.Name, MimeType: f.MimeType, Folder: f.MimeType == mimeFolder,
		Link: f.WebViewLink, Parents: f.Parents, Trashed: f.Trashed}
	fmt.Sscan(f.Size, &it.Size)
	it.Modified, _ = time.Parse(time.RFC3339, f.ModifiedTime)
	if f.ShortcutDetails != nil {
		it.Target = f.ShortcutDetails.TargetID
	}
	return it
}

// call does one request and decodes its JSON answer into out.
func (g *google) call(ctx context.Context, method, u string, body io.Reader, contentType string, out any) error {
	resp, err := g.do(ctx, method, u, body, contentType)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}

func (g *google) do(ctx context.Context, method, u string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := g.http.Do(req)
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) {
			return nil, fmt.Errorf("Google refused the token (%s): reconnect Google Drive in the workspace", re.ErrorCode)
		}
		return nil, err
	}
	if resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
	switch resp.StatusCode {
	case http.StatusNotFound:
		return nil, errNotFound
	case http.StatusUnauthorized:
		return nil, errors.New("Google refused the token: reconnect Google Drive in the workspace")
	}
	return nil, fmt.Errorf("google drive: %d %s", resp.StatusCode, e.Error.Message)
}

func (g *google) url(path string, q url.Values) string {
	q.Set("supportsAllDrives", "true")
	return g.api + path + "?" + q.Encode()
}

func (g *google) Get(ctx context.Context, id string) (Item, error) {
	var f driveFile
	err := g.call(ctx, http.MethodGet, g.url("/files/"+url.PathEscape(id), url.Values{"fields": {itemFields}}), nil, "", &f)
	return f.item(), err
}

// quote writes a string literal of Drive's query language.
func quote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func (g *google) list(ctx context.Context, q, pageToken string) ([]Item, string, error) {
	v := url.Values{"q": {q}, "fields": {"nextPageToken,files(" + itemFields + ")"}, "pageSize": {"100"},
		"includeItemsFromAllDrives": {"true"}, "corpora": {"allDrives"}, "orderBy": {"folder,name"}}
	if pageToken != "" {
		v.Set("pageToken", pageToken)
	}
	var out struct {
		NextPageToken string      `json:"nextPageToken"`
		Files         []driveFile `json:"files"`
	}
	if err := g.call(ctx, http.MethodGet, g.url("/files", v), nil, "", &out); err != nil {
		return nil, "", err
	}
	items := make([]Item, 0, len(out.Files))
	for _, f := range out.Files {
		items = append(items, f.item())
	}
	return items, out.NextPageToken, nil
}

func (g *google) List(ctx context.Context, folderID string, foldersOnly bool, pageToken string) ([]Item, string, error) {
	q := quote(folderID) + " in parents and trashed = false"
	if folderID == sharedWithMe {
		q = "sharedWithMe and trashed = false"
	}
	if foldersOnly {
		q += " and mimeType = " + quote(mimeFolder)
	}
	return g.list(ctx, q, pageToken)
}

func (g *google) Search(ctx context.Context, text, pageToken string) ([]Item, string, error) {
	q := "(name contains " + quote(text) + " or fullText contains " + quote(text) + ") and trashed = false"
	return g.list(ctx, q, pageToken)
}

func (g *google) Roots(ctx context.Context) ([]Item, error) {
	mine, err := g.Get(ctx, "root")
	if err != nil {
		return nil, err
	}
	mine.Name, mine.Folder = "My Drive", true
	out := []Item{mine, {ID: sharedWithMe, Name: "Shared with me", Folder: true}}
	token := ""
	for range 10 {
		v := url.Values{"pageSize": {"100"}, "fields": {"nextPageToken,drives(id,name)"}}
		if token != "" {
			v.Set("pageToken", token)
		}
		var res struct {
			NextPageToken string `json:"nextPageToken"`
			Drives        []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"drives"`
		}
		if err := g.call(ctx, http.MethodGet, g.api+"/drives?"+v.Encode(), nil, "", &res); err != nil {
			return nil, err
		}
		for _, d := range res.Drives {
			out = append(out, Item{ID: d.ID, Name: d.Name, MimeType: mimeFolder, Folder: true})
		}
		if token = res.NextPageToken; token == "" {
			break
		}
	}
	return out, nil
}

// exports are the formats a native document is exported to, the first one
// by default.
var exports = map[string][][2]string{
	mimeDoc:     {{"markdown", "text/markdown"}, {"text", "text/plain"}, {"pdf", "application/pdf"}},
	mimeSheet:   {{"csv", "text/csv"}, {"pdf", "application/pdf"}},
	mimeSlides:  {{"text", "text/plain"}, {"pdf", "application/pdf"}},
	mimeDrawing: {{"png", "image/png"}, {"pdf", "application/pdf"}},
}

func (g *google) Read(ctx context.Context, it Item, format string, max int64) (Content, error) {
	if formats, ok := exports[it.MimeType]; ok {
		mime := ""
		for _, f := range formats {
			if format == "" || format == f[0] {
				mime = f[1]
				break
			}
		}
		if mime == "" {
			var names []string
			for _, f := range formats {
				names = append(names, f[0])
			}
			return Content{}, fmt.Errorf("this document exports to %s", strings.Join(names, ", "))
		}
		u := g.url("/files/"+url.PathEscape(it.ID)+"/export", url.Values{"mimeType": {mime}})
		c, err := g.fetch(ctx, u, mime, max)
		if it.MimeType == mimeSheet && mime == "text/csv" {
			c.Note = "first sheet only, as CSV; format pdf gives every sheet"
		}
		return c, err
	}
	if strings.HasPrefix(it.MimeType, "application/vnd.google-apps.") {
		return Content{}, fmt.Errorf("a %s cannot be read as a file", strings.TrimPrefix(it.MimeType, "application/vnd.google-apps."))
	}
	if it.Size > max {
		return Content{}, fmt.Errorf("%w: %d bytes, the limit is %d", errTooLarge, it.Size, max)
	}
	return g.fetch(ctx, g.url("/files/"+url.PathEscape(it.ID), url.Values{"alt": {"media"}}), it.MimeType, max)
}

func (g *google) fetch(ctx context.Context, u, mime string, max int64) (Content, error) {
	resp, err := g.do(ctx, http.MethodGet, u, nil, "")
	if err != nil {
		return Content{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return Content{}, err
	}
	if int64(len(data)) > max {
		return Content{}, fmt.Errorf("%w: more than %d bytes", errTooLarge, max)
	}
	return Content{MimeType: mime, Data: data}, nil
}

func (g *google) CreateFolder(ctx context.Context, parentID, name string) (Item, error) {
	meta, _ := json.Marshal(map[string]any{"name": name, "mimeType": mimeFolder, "parents": []string{parentID}})
	var f driveFile
	err := g.call(ctx, http.MethodPost, g.url("/files", url.Values{"fields": {itemFields}}), bytes.NewReader(meta), "application/json", &f)
	return f.item(), err
}

func (g *google) CreateFile(ctx context.Context, parentID, name, mimeType string, data []byte, convert bool) (Item, error) {
	meta := map[string]any{"name": name, "parents": []string{parentID}}
	if convert {
		switch {
		case mimeType == "text/csv":
			meta["mimeType"] = mimeSheet
		case strings.HasPrefix(mimeType, "text/"):
			meta["mimeType"] = mimeDoc
		}
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/json; charset=UTF-8"}})
	json.NewEncoder(part).Encode(meta)
	part, _ = w.CreatePart(textproto.MIMEHeader{"Content-Type": {mimeType}})
	part.Write(data)
	w.Close()
	u := g.upload + "/files?" + url.Values{"uploadType": {"multipart"}, "supportsAllDrives": {"true"}, "fields": {itemFields}}.Encode()
	var f driveFile
	err := g.call(ctx, http.MethodPost, u, &body, "multipart/related; boundary="+w.Boundary(), &f)
	return f.item(), err
}
