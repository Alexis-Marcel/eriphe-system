package main

import (
	"github.com/emersion/go-sasl"
)

// xoauth2 is the XOAUTH2 SASL mechanism (Gmail, Microsoft) with a bearer
// token obtained elsewhere.
type xoauth2 struct{ user, token string }

func newXOAuth2Client(user, token string) sasl.Client { return &xoauth2{user, token} }

func (x *xoauth2) Start() (string, []byte, error) {
	return "XOAUTH2", []byte("user=" + x.user + "\x01auth=Bearer " + x.token + "\x01\x01"), nil
}

// Next answers the server's error challenge (a JSON status) with an empty
// response, after which the server fails the exchange.
func (x *xoauth2) Next(challenge []byte) ([]byte, error) { return []byte{}, nil }
