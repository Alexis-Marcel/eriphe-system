package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBearer(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	for _, c := range []struct {
		token, header string
		want          int
	}{
		{"", "", 204},
		{"s3cret", "", 401},
		{"s3cret", "Bearer nope", 401},
		{"s3cret", "Bearer s3cret", 204},
	} {
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
		if c.header != "" {
			r.Header.Set("Authorization", c.header)
		}
		w := httptest.NewRecorder()
		auth(c.token, ok).ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("token %q header %q: %d, want %d", c.token, c.header, w.Code, c.want)
		}
	}
}
