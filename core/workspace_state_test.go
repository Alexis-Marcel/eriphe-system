package main

import (
	"strings"
	"testing"
	"time"
)

// No model orders the workspace and none judges it: no composer role, and
// a session on the workspace journals no surface signal.
func TestNoComposer(t *testing.T) {
	if _, ok := assistantRoles["composer"]; ok {
		t.Fatal("a composer role is still registered")
	}
	c := focusCore(t)
	now := time.Now()
	c.focusSurface("comptes", now)
	c.focusSurface("", now)
	writeTestSurface(t, c, surfaceIn{ID: "nouveau", Title: "Nouveau", Weight: 30})
	c.relayout()
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM event_journal WHERE kind LIKE 'surface.%'`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d surface signals journaled", n)
	}
	// An assistant of the role gone, left in the catalog, is left out at
	// load; the rest loads.
	if err := c.store.putCatalog(kindAssistant, "composer", Assistant{Role: "composer", Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	d, skipped := loadCatalog(c.base, c.store)
	if _, ok := d.Assistants["composer"]; ok || !strings.Contains(skipped[kindAssistant+"/composer"], "unknown role") {
		t.Fatalf("assistants %v, skipped %v", d.Assistants, skipped)
	}
}

// What the user placed is kept past their session, until placed again,
// reset or its surface gone; what they closed only for the session.
func TestPlacementKept(t *testing.T) {
	c, _ := surfaceCore(t)
	writeTestSurface(t, c, surfaceIn{ID: "agenda", Title: "Agenda", Weight: 30})
	writeTestSurface(t, c, surfaceIn{ID: "meteo", Title: "Météo", Weight: 60})
	writeTestSurface(t, c, surfaceIn{ID: "notes", Title: "Notes", Weight: 50})
	before := time.Now().Add(-5 * time.Hour) // a session that ended
	if err := c.placeSurface("agenda", gestureResized, sizeL, 0, 0, before); err != nil {
		t.Fatal(err)
	}
	c.closeSurface("notes", before)
	c.layout.userLoaded, c.layout.user = false, userPlacement{} // a restart
	l := c.relayout()
	if tl, ok := tileOf(l, "agenda"); !ok || !tl.Pinned || tl.Size != sizeL || tl.Col != 0 || tl.Row != 0 {
		t.Fatalf("the placement went with the session: %s", tilesOf(l))
	}
	if ch, ok := chipOf(l, "notes"); ok && ch.Closed {
		t.Fatal("closed last session, still closed")
	}
}

// placementNow is the placement at now.
func (c *core) placementNow(now time.Time) userPlacement {
	start := c.workspaceSessionStart(now)
	st := &c.layout
	st.mu.Lock()
	defer st.mu.Unlock()
	return c.placementLocked(start, nil)
}
