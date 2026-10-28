package main

import (
	"bytes"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// The workspace's side of attachments (attachments.go): the composer's
// files, and opening one from the conversation.

// messageFiles reads the files of a multipart message, if it is one.
func (w *web) messageFiles(r *http.Request) ([]*multipart.FileHeader, error) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return nil, nil
	}
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return nil, err
	}
	// The server removes the parts kept on disk once the message is handled.
	return r.MultipartForm.File["files"], nil
}

// sendError tells me, in my language, why a message was not sent.
func (w *web) sendError(rw http.ResponseWriter, r *http.Request, err error) {
	lang := w.lang(r)
	var bad errAttachment
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &bad):
		msg := tr(lang, bad.key)
		if strings.Contains(msg, "%") {
			msg = fmt.Sprintf(msg, bad.name)
		}
		code := http.StatusBadRequest
		if bad.key == "chat.file.toobig" {
			code = http.StatusRequestEntityTooLarge
		}
		http.Error(rw, msg, code)
	case errors.As(err, &tooBig):
		http.Error(rw, tr(lang, "chat.file.toomuch"), http.StatusRequestEntityTooLarge)
	default:
		http.Error(rw, "invalid message", http.StatusBadRequest)
	}
}

// handleAttachment serves an attachment of the conversation, to open it.
// Its type is the one eriphe-core found when it was sent: an image, a PDF
// or a text, never a page the browser would run.
func (w *web) handleAttachment(rw http.ResponseWriter, r *http.Request, s *session) {
	a, err := w.c.store.attachment(r.PathValue("id"))
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	data, err := os.ReadFile(w.c.store.attachmentPath(a.ID))
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	typ := a.Type
	if strings.HasPrefix(typ, "text/") {
		typ = "text/plain; charset=utf-8"
	}
	h := rw.Header()
	h.Set("Content-Type", typ)
	h.Set("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(a.Name))
	h.Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(rw, r, "", time.Time{}, bytes.NewReader(data))
}

// The composer's limits, for the browser to check before sending.
func (homeData) MaxFile() int  { return maxAttachmentSize }
func (homeData) MaxFiles() int { return maxAttachments }

// fileSize is an attachment's size, in my language.
func fileSize(lang string, n int64) string {
	s := humanSize(n)
	if lang == "fr" {
		s = strings.NewReplacer(".", ",", "MB", "Mo", "KB", "Ko", " B", " o").Replace(s)
	}
	return s
}
