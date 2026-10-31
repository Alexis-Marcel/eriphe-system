package main

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A node that came back by itself twice in a day is asleep while it is
// away; one that left once, a blip, or the always-on one is not.
func TestNodeAsleep(t *testing.T) {
	c := residentCore(t)
	now := time.Now()
	away := func(name string, ago time.Duration) NodeRow {
		t.Helper()
		offlineSince(t, c.core, name, ago)
		nodes, _ := c.store.nodes()
		for _, n := range nodes {
			if n.Name == name {
				return n
			}
		}
		t.Fatalf("no node %s", name)
		return NodeRow{}
	}
	wake := func(name string, ago time.Duration) {
		t.Helper()
		away(name, ago)
		c.noteConnect(name, now)
		c.store.setNodeStatus(name, true, []byte(laptopInfo))
	}
	wake("laptop", 30*time.Second) // a blip
	if n := away("laptop", time.Minute); c.asleep(n) || len(c.wakes("laptop", now)) != 0 {
		t.Fatal("a blip counts")
	}
	wake("laptop", 15*time.Minute)
	if n := away("laptop", time.Minute); c.asleep(n) {
		t.Fatal("asleep after one return: maybe off")
	}
	wake("laptop", 15*time.Minute)
	if n := away("laptop", time.Minute); !c.asleep(n) {
		t.Fatal("not asleep after coming back twice")
	}
	c.store.setNodeStatus("laptop", true, []byte(laptopInfo))
	if nodes, _ := c.store.nodes(); c.asleep(nodes[slicesIndex(nodes, "laptop")]) {
		t.Fatal("asleep while online")
	}
	// Wakes older than a day are forgotten.
	old := strconv.FormatInt(now.Add(-25*time.Hour).Unix(), 10)
	c.store.set(wakesKey("laptop"), old+","+old)
	if n := away("laptop", time.Minute); c.asleep(n) {
		t.Fatal("asleep on wakes of yesterday")
	}
	// The always-on machine never sleeps.
	c.store.set(wakesKey("nas"), strconv.FormatInt(now.Unix(), 10)+","+strconv.FormatInt(now.Unix(), 10))
	if n := away("nas", time.Minute); c.asleep(n) {
		t.Fatal("the always-on node asleep")
	}
}

func slicesIndex(nodes []NodeRow, name string) int {
	for i, n := range nodes {
		if n.Name == name {
			return i
		}
	}
	return -1
}

// A task not started on a node asleep is placed again after the short
// grace, not the long one.
func TestStrandedOnAsleepNode(t *testing.T) {
	c := residentCore(t)
	now := strconv.FormatInt(time.Now().Unix(), 10)
	c.store.set(wakesKey("laptop"), now+","+now)
	c.store.insertTask(&TaskRow{ID: "t-1", Node: "laptop", Kind: "worker", Agent: "sonnet", Runtime: "claude-code", State: statePending})
	offlineSince(t, c.core, "laptop", 10*time.Second)
	if c.replaceStranded(); mustTask(t, c.core, "t-1").Node != "laptop" {
		t.Fatal("placed again before the grace")
	}
	offlineSince(t, c.core, "laptop", time.Minute)
	if c.replaceStranded(); mustTask(t, c.core, "t-1").Node != "nas" {
		t.Fatal("still waits for the laptop asleep")
	}
}

func mustTask(t *testing.T, c *core, id string) *TaskRow {
	t.Helper()
	task, err := c.store.task(id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// Settings → Devices says asleep or offline, in both languages.
func TestWebDeviceAsleep(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	tw.c.started = time.Now().Add(-time.Hour)
	for _, name := range []string{"laptop", "desk"} {
		tw.c.store.registerNode(name, hashSecret(name))
		tw.c.store.setNodeStatus(name, true, []byte(laptopInfo))
		offlineSince(t, tw.c, name, time.Hour)
	}
	now := strconv.FormatInt(time.Now().Unix(), 10)
	tw.c.store.set(wakesKey("laptop"), now+","+now)
	for lang, want := range map[string][2]string{"fr": {"En veille", "Hors ligne"}, "en": {"Asleep", "Offline"}} {
		tw.hc.Jar.SetCookies(mustURL(tw.srv.URL), []*http.Cookie{{Name: "lang", Value: lang, Path: "/"}})
		for name, label := range map[string]string{"laptop": want[0], "desk": want[1]} {
			code, body, _ := tw.do("GET", "/settings/devices/"+name, nil, nil)
			if code != http.StatusOK || !strings.Contains(body, label) {
				t.Fatalf("%s, %s: %d, no %q", lang, name, code, label)
			}
		}
	}
	if _, body, _ := tw.do("GET", "/settings/devices", nil, nil); !strings.Contains(body, `class="status asleep"`) {
		t.Fatal("the grid does not say asleep")
	}
}
