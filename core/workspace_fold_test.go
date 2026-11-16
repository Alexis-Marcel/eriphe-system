package main

import (
	"slices"
	"strconv"
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

// A standing thread's surface says it folds, a work thread's does not;
// quiet once visited, the standing thread is a chip and the work thread a
// tile. A surface written before it said so is written again by the tick.
func TestThreadSurfaceFolds(t *testing.T) {
	c, _ := surfaceCore(t)
	work := lightThread(t, c)
	standing, err := c.openThread(threadIn{Title: "Mails", Goal: "Les mails.", Alias: "topic:mails", Ends: endsStanding}, nil, "skill:mails")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	c.threadVisited(work.ID, now)
	c.threadVisited(standing.ID, now)
	sid := threadSurfaceID(standing.ID)
	sf, err := c.surface(sid)
	if err != nil || !sf.Look.Folds || tileStateOf(sf) != tileQuiet {
		t.Fatalf("standing %+v %v", sf.Look, err)
	}
	if wf, _ := c.surface(threadSurfaceID(work.ID)); wf.Look.Folds {
		t.Fatal("a work thread folds")
	}
	l := c.relayout()
	if _, ok := tileOf(l, threadSurfaceID(work.ID)); !ok {
		t.Fatalf("work: %s", tilesOf(l))
	}
	if _, ok := tileOf(l, sid); ok || !slices.Contains(chipsOf(l), sid) {
		t.Fatalf("standing: %s / %v", tilesOf(l), chipsOf(l))
	}

	// Written before surfaces said whether they fold (another hash): the
	// tick writes it again.
	c.store.db.Exec(`UPDATE surfaces SET look = json_remove(look, '$.folds') WHERE id = ?`, sid)
	c.store.set(threadShownKey+strconv.FormatInt(standing.ID, 10), "an older hash")
	if sf, _ = c.surface(sid); sf.Look.Folds {
		t.Fatal("the look kept folds")
	}
	c.keepThreadSurfaces()
	if sf, _ = c.surface(sid); !sf.Look.Folds {
		t.Fatal("not written again")
	}
}
