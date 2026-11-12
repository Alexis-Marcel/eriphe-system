package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
)

// The workspace reads its objects by ref (objects.go), as JSON.

// objectsParallel is as many objects as one batch reads at once.
const objectsParallel = 8

// objectError is what a ref that could not be read says, and its status.
type objectError struct {
	Error  string `json:"error"`
	Status int    `json:"status"`
}

// objectFailure is the status and the short reason of a read that failed.
func objectFailure(ref string, err error) objectError {
	var bad errBadRef
	var refused objectRefused
	var source objectSourceError
	switch {
	case errors.As(err, &bad):
		return objectError{bad.Error(), http.StatusNotFound}
	case errors.Is(err, errObjectUnknown):
		return objectError{"no such object", http.StatusNotFound}
	case errors.Is(err, errObjectUnsupported):
		return objectError{err.Error(), http.StatusNotImplemented}
	case errors.As(err, &refused):
		return objectError{refused.why, http.StatusForbidden}
	case errors.As(err, &source):
		return objectError{source.why, http.StatusBadGateway}
	}
	log.Printf("objects: %s: %v", ref, err)
	return objectError{"the object could not be read", http.StatusInternalServerError}
}

// handleObject is GET /api/objects/{ref}: one object.
func (w *web) handleObject(rw http.ResponseWriter, r *http.Request, _ *session) {
	ref := r.PathValue("ref")
	o, err := w.c.object(withObjectLang(r.Context(), w.lang(r)), ref)
	if err != nil {
		f := objectFailure(ref, err)
		writeJSON(rw, f.Status, f)
		return
	}
	writeJSON(rw, http.StatusOK, o)
}

// objectsIn is the body of POST /api/objects.
type objectsIn struct {
	Refs []string `json:"refs"`
}

// objectOut is one ref of a batch: its object, or why not.
type objectOut struct {
	Ref    string  `json:"ref"`
	Object *Object `json:"object,omitempty"`
	*objectError
}

// handleObjects is POST /api/objects: several objects, each read on its
// own, in the order asked; one that fails says why beside the others.
func (w *web) handleObjects(rw http.ResponseWriter, r *http.Request, _ *session) {
	var in objectsIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(rw, http.StatusBadRequest, objectError{"the body is {\"refs\": [...]}", http.StatusBadRequest})
		return
	}
	if len(in.Refs) > objectsMax {
		writeJSON(rw, http.StatusBadRequest, objectError{fmt.Sprintf("at most %d refs at once", objectsMax), http.StatusBadRequest})
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"objects": w.c.objects(withObjectLang(r.Context(), w.lang(r)), in.Refs)})
}

// objects reads several refs, a few at once.
func (c *core) objects(ctx context.Context, refs []string) []objectOut {
	out := make([]objectOut, len(refs))
	sem := make(chan struct{}, objectsParallel)
	var wg sync.WaitGroup
	for i, ref := range refs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i].Ref = ref
			o, err := c.object(ctx, ref)
			if err != nil {
				f := objectFailure(ref, err)
				out[i].objectError = &f
				return
			}
			out[i].Object = o
		}()
	}
	wg.Wait()
	return out
}
