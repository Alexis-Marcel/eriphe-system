package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// An OAuth secret may be a refresh grant rather than an access token:
// {"client_id", "client_secret", "refresh_token", "token_url"}, as eriphe-core
// lends it for an account connected with a provider's sign-in; "token_url"
// is where the provider refreshes it. The access token is then refreshed
// here, in memory, shortly before it ends; it is never written nor logged.

type refreshGrant struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
	TokenURL     string `json:"token_url"`
}

// errRevoked is a refresh token the provider refused: the user must
// connect the account again in the workspace.
var errRevoked = errors.New("the account's authorization was revoked or has expired: reconnect it in Eriphe (Settings → Tools)")

var refreshers = struct {
	sync.Mutex
	m map[[32]byte]oauth2.TokenSource
}{m: map[[32]byte]oauth2.TokenSource{}}

// refreshHTTP is the client of the token endpoint; tests replace it.
var refreshHTTP = &http.Client{Timeout: 30 * time.Second}

// accessToken turns a secret that holds a refresh grant into a current
// access token; ok is false for any other secret.
func accessToken(secret string) (token string, ok bool, err error) {
	var g refreshGrant
	if !strings.HasPrefix(secret, "{") || json.Unmarshal([]byte(secret), &g) != nil || g.RefreshToken == "" {
		return "", false, nil
	}
	if g.ClientID == "" {
		return "", true, errors.New("the refresh grant has no client_id")
	}
	if g.TokenURL == "" {
		return "", true, errors.New("the refresh grant has no token_url")
	}
	key := sha256.Sum256([]byte(secret))
	refreshers.Lock()
	ts := refreshers.m[key]
	if ts == nil {
		cfg := &oauth2.Config{ClientID: g.ClientID, ClientSecret: g.ClientSecret,
			Endpoint: oauth2.Endpoint{TokenURL: g.TokenURL, AuthStyle: oauth2.AuthStyleInParams}}
		ctx := context.WithValue(context.Background(), oauth2.HTTPClient, refreshHTTP)
		ts = oauth2.ReuseTokenSource(nil, cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: g.RefreshToken}))
		refreshers.m[key] = ts
	}
	refreshers.Unlock()
	tok, err := ts.Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && (re.ErrorCode == "invalid_grant" || re.ErrorCode == "unauthorized_client" || re.ErrorCode == "invalid_client") {
			return "", true, errRevoked
		}
		return "", true, fmt.Errorf("refreshing the access token: %w", err)
	}
	return tok.AccessToken, true, nil
}
