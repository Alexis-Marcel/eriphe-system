package main

import (
	"slices"
	"testing"
	"time"
)

// A standing thread with nothing for the user (quiet, running) is a chip;
// yours, urgent or new, it is a tile again, in the usual order. Opened or
// placed by the user, it is a tile all the same. Other surfaces, quiet or
// not, stay tiles.
func TestLayoutFoldsQuietStanding(t *testing.T) {
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	tile := func(id, state string, folds bool) surface {
		return surface{ID: id, Title: id, Author: "thread:1", Weight: 50, Updated: at, Look: surfaceLook{State: state, Folds: folds}}
	}
	pool := []surface{
		tile("work", tileQuiet, false),
		tile("mails", tileQuiet, true),
		tile("alerts", tileRunning, true),
		tile("agenda", tileNew, true),
		tile("bills", tileYours, true),
		tile("watch", tileUrgent, true),
	}
	l := layoutOf(pool, "")
	var tiles []string
	for _, x := range l.Items {
		tiles = append(tiles, x.ID)
	}
	if want := []string{"bills", "watch", "agenda", "work"}; !slices.Equal(tiles, want) {
		t.Fatalf("tiles %v, want %v", tiles, want)
	}
	if got := chipsOf(l); !slices.Equal(got, []string{"alerts", "mails"}) {
		t.Fatalf("chips %v", got)
	}

	// Opened from its chip: on stage, as any surface.
	l = layoutOf(pool, "mails")
	if tl, ok := tileOf(l, "mails"); !ok || tl.Size != sizeXL || l.Focus != "mails" {
		t.Fatalf("opened: %s focus %q", tilesOf(l), l.Focus)
	}
	// Placed by the user: a tile.
	l = layoutStage(pool, "", "", nil, userPlacement{Pins: map[string]tilePin{"alerts": {Size: sizeM, Order: 0, At: 1, By: placedByUser}}})
	if _, ok := tileOf(l, "alerts"); !ok || slices.Contains(chipsOf(l), "alerts") {
		t.Fatalf("placed: %s / %v", tilesOf(l), chipsOf(l))
	}
	if _, ok := tileOf(l, "mails"); ok {
		t.Fatalf("quiet standing a tile: %s", tilesOf(l))
	}
}
