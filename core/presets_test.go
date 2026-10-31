package main

import "testing"

// guessMailProvider reads the provider of the well-known domains only.
func TestGuessMailProvider(t *testing.T) {
	for in, want := range map[string]string{
		"sam@gmail.com":        "gmail",
		"Sam@GoogleMail.com":   "gmail",
		"sam@icloud.com":       "icloud",
		"sam@me.com":           "icloud",
		"sam@mac.com":          "icloud",
		"sam@outlook.fr":       "outlook",
		"sam@hotmail.com":      "outlook",
		"sam@live.co.uk":       "outlook",
		"sam@msn.com":          "outlook",
		"sam@orange.fr":        "orange",
		"sam@wanadoo.fr":       "orange",
		"sam@free.fr":          "free",
		"sam@fastmail.com":     "fastmail",
		"sam@fastmail.fm":      "fastmail",
		"sam@example.org":      "",
		"sam@mail.outlook.com": "",
		"no-domain":            "",
		"":                     "",
	} {
		if got := guessMailProvider(in); got != want {
			t.Errorf("guessMailProvider(%q) = %q, want %q", in, got, want)
		}
	}
}
