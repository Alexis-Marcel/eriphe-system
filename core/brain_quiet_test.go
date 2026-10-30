package main

import "testing"

func TestQuietReply(t *testing.T) {
	for _, s := range []string{"[rien]", " [rien]\n", "`[rien]`"} {
		if !quietReply(s) {
			t.Errorf("%q should stay out of the conversation", s)
		}
	}
	for _, s := range []string{"Noté : l'outil est disponible.", "[rien] mais aussi ceci", ""} {
		if quietReply(s) {
			t.Errorf("%q should be shown", s)
		}
	}
}
