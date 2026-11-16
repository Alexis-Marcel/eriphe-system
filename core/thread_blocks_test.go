package main

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// sampleSteps are the steps of a made-up thread, as its surface shows
// them: two done (a call, its card decided), one that waits for the
// user's yes, one running, one to come.
func sampleSteps(lang string, now time.Time) []threadSurfaceStep {
	en := lang == "en"
	pick := func(fr, eng string) string {
		if en {
			return eng
		}
		return fr
	}
	return []threadSurfaceStep{
		{Key: "plan", Label: pick("Préparer la page", "Prepare the page"), Kind: stepTask, State: "done", Started: now.Add(-3 * time.Hour), Ended: now.Add(-150 * time.Minute), At: now.Add(-150 * time.Minute)},
		{Key: "merge", Label: pick("Fusionner la branche", "Merge the branch"), Kind: stepCall, State: "done", Card: "access:5", Started: now.Add(-2 * time.Hour), Ended: now.Add(-110 * time.Minute), At: now.Add(-110 * time.Minute)},
		{Key: "publish", Label: pick("Publier le site", "Publish the site"), Kind: stepApproval, State: "current", Card: "card:20", Waits: true, Started: now.Add(-10 * time.Minute), At: now.Add(-10 * time.Minute)},
		{Key: "photos", Label: pick("Redimensionner les photos", "Resize the photos"), Kind: stepTask, State: "current", Note: pick("12 sur 30", "12 of 30"), Started: now.Add(-20 * time.Minute), At: now.Add(-20 * time.Minute)},
		{Key: finalStep, Label: tr(lang, "thread.surface.final"), Kind: stepCheck, State: "pending"},
	}
}

// itemsThread is a thread whose source lists accounts, three of them
// noted: one to do with a due date, one to look at written after reading
// outside, one with nothing to do; one not noted yet.
func itemsThread(t *testing.T, c *core, l *fakeLedger) Thread {
	t.Helper()
	l.set(func(l *fakeLedger) {
		l.accounts = []ledgerAccount{{Name: "Courant", Balance: 120}, {Name: "Épargne", Balance: 3400}, {Name: "Joint", Balance: 80}, {Name: "Livret", Balance: 900}}
	})
	th := lightThread(t, c)
	src := threadSource{Name: "ledger", Tool: "ledger/balances", Items: &sourceItems{Path: "accounts", ID: "name", Detail: "balance"}}
	if _, err := c.addThreadSource(th.ID, src, byBrain); err != nil {
		t.Fatal(err)
	}
	c.surf.refreshing.Wait()
	due := time.Now().Add(26 * time.Hour).Format("2006-01-02")
	for _, n := range []struct {
		in    noteItemIn
		taint []string
	}{
		{noteItemIn{Source: "ledger", Item: "Courant", Note: "Solde bas avant le loyer.", State: itemStateAct, Due: due}, nil},
		{noteItemIn{Source: "ledger", Item: "Joint", Note: "Un virement inconnu, d'après le relevé.", State: itemStateLook}, []string{"mail.read"}},
		{noteItemIn{Source: "ledger", Item: "Livret", Note: "Rien de neuf.", State: itemStateNone}, nil},
	} {
		if _, err := c.noteItem(th.ID, n.in, byBrain, n.taint); err != nil {
			t.Fatal(err)
		}
	}
	th, _ = c.thread(th.ID)
	return th
}

// withoutEnds are the components without what reads how a thread ends:
// the head's buttons, and its goal.
func withoutEnds(t *testing.T, comps json.RawMessage) json.RawMessage {
	t.Helper()
	var list []map[string]any
	json.Unmarshal(comps, &list)
	gone := func(id string) bool { return strings.HasPrefix(id, "thread_control") || id == "thread_goal" }
	list = slices.DeleteFunc(list, func(comp map[string]any) bool {
		id, _ := comp["id"].(string)
		return gone(id)
	})
	for _, comp := range list {
		if kids, ok := comp["children"].([]any); ok {
			comp["children"] = slices.DeleteFunc(kids, func(x any) bool { id, _ := x.(string); return gone(id) })
		}
	}
	raw, _ := json.Marshal(list)
	return raw
}

// rootChildren are the ids the root of a surface shows, in order.

func rootChildren(t *testing.T, comps json.RawMessage) []string {
	t.Helper()
	var list []map[string]any
	json.Unmarshal(comps, &list)
	for _, comp := range list {
		if comp["id"] == "root" {
			var out []string
			for _, x := range comp["children"].([]any) {
				out = append(out, x.(string))
			}
			return out
		}
	}
	t.Fatal("no root")
	return nil
}

// A work thread and a standing one are drawn by the same blocks, in one
// order: the same thread with its ends changed gives the same surface
// while it is active. Only the banner of an ended thread reads ends: «
// Ranger » for one that ends on its goal. A decided card is one line
// (compact), folded with the steps done.
func TestThreadBlocksSameForEveryThread(t *testing.T) {
	c, l := surfaceCore(t)
	th := itemsThread(t, c, l)
	now := time.Now()
	steps := sampleSteps("fr", now)
	compose := func(s Thread) surfaceIn {
		t.Helper()
		in, ok := c.threadSurfaceIn(s, steps, nil, nil, now)
		if !ok {
			t.Fatalf("no surface for %+v", s)
		}
		return in
	}
	th.Summary = "La page est prête."
	work, standing := th, th
	work.Ends, standing.Ends = endsGoal, endsStanding
	a, b := compose(work), compose(standing)
	// A standing thread is drawn the same but for Stop (it never ends) and
	// its goal (what it keeps: its title and its state line say it).
	var dataA map[string]any
	json.Unmarshal(a.Data, &dataA)
	delete(dataA[threadDataKey].(map[string]any), "goal")
	noGoal, _ := json.Marshal(dataA)
	if string(withoutEnds(t, a.Components)) != string(b.Components) || string(noGoal) != string(b.Data) || a.Weight != b.Weight {
		t.Fatalf("a standing thread is drawn otherwise:\n%s\n%s", a.Components, b.Components)
	}
	if !strings.Contains(string(a.Components), `"thread_goal"`) || strings.Contains(string(b.Components), `"thread_goal"`) {
		t.Fatalf("goal: %s\n%s", a.Components, b.Components)
	}
	if !strings.Contains(string(a.Components), `"thread_control_stop"`) || strings.Contains(string(b.Components), `"thread_controls"`) {
		t.Fatalf("Stop: %s\n%s", a.Components, b.Components)
	}
	day := th
	day.Ends = endsDay
	if c := compose(day); string(c.Components) != string(a.Components) {
		t.Fatal("a day thread is drawn otherwise")
	}

	// Every block, in its order.
	kids := rootChildren(t, a.Components)
	order := []string{"thread_goal", "thread_progress", "thread_state", "thread_yours", "thread_card_2", "thread_src_0_groups",
		"thread_where", "thread_done", "thread_steps"}
	at := -1
	for _, id := range order {
		i := slices.Index(kids, id)
		if i <= at {
			t.Fatalf("%s out of order: %v", id, kids)
		}
		at = i
	}
	comps := string(a.Components)
	// The decided card is folded with the steps done, one line; the one
	// that waits is the user's, whole.
	if slices.Contains(kids, "thread_card_1") || !strings.Contains(comps, `{"compact":true,"component":"Proposal","id":"thread_card_1","proposalId":"access:5"}`) ||
		strings.Contains(comps, `"compact":true,"component":"Proposal","id":"thread_card_2"`) {
		t.Errorf("cards: %s", comps)
	}
	var data map[string]any
	json.Unmarshal(a.Data, &data)
	thread := data[threadDataKey].(map[string]any)
	if thread["donelabel"] != "2 étapes faites" || thread["progress"].(map[string]any)["label"] != "À toi" {
		t.Errorf("head and fold: %v", thread)
	}
	// Items by their notes' state, counted on their groups' lines and in
	// the state line; « extérieur » once, there, not on the tainted row,
	// which carries no action. No source line, no counts in boxes.
	if state, _ := thread["state"].(string); !strings.HasPrefix(state, "1 à faire · 1 à regarder · 1 pas encore trié · 1 rien à faire · trié à ") ||
		!strings.HasSuffix(state, " · extérieur") || strings.Count(comps, "extérieur")+strings.Count(string(a.Data), "extérieur") != 1 {
		t.Errorf("state line %q", state)
	}
	if strings.Contains(comps, `"Metric"`) || strings.Contains(comps, "thread_src_0_by") {
		t.Errorf("counts in boxes, or a source line: %s", comps)
	}
	src := thread["sources"].([]any)[0].(map[string]any)
	var groups []string
	for _, g := range src["groups"].([]any) {
		groups = append(groups, g.(map[string]any)["label"].(string))
	}
	if strings.Join(groups, "|") != "À faire · 1|À regarder · 1|Pas encore trié · 1|Rien à faire · 1" {
		t.Errorf("groups %v", groups)
	}
	rows := map[string]map[string]any{}
	for _, r := range sourceRows(src) {
		rows[r.(map[string]any)["id"].(string)] = r.(map[string]any)
	}
	if rows["Joint"]["external"] != true || rows["Joint"]["mark"] != nil || rows["Courant"]["level"] != "high" || rows["Courant"]["due"] == "" || rows["Épargne"]["detail"] != "3400" || rows["Livret"]["level"] != "low" {
		t.Errorf("rows %v", rows)
	}
	if strings.Contains(comps, `"ask"`) {
		t.Errorf("an ask on the surface: %s", comps)
	}

	// Ended: the banner; « Ranger » only for what ends on its goal.
	work.State, work.Ended = threadDone, now
	ended := compose(work)
	if kids := rootChildren(t, ended.Components); kids[2] != "thread_ended" || !strings.Contains(string(ended.Components), `"id":"`+threadTidyID+`"`) {
		t.Errorf("ended: %v", kids)
	}
	if strings.Contains(string(ended.Components), "thread_where") || strings.Contains(string(ended.Components), "thread_yours") {
		t.Errorf("an ended thread says where it stands twice, or asks: %s", ended.Components)
	}
	day.State, day.Ended = threadDone, now
	if _, ok := c.threadSurfaceIn(day, steps, nil, nil, now); ok {
		t.Error("a day thread ended is still drawn")
	}
}

// threadsFixture is the file of the threads' surfaces as code draws them,
// for the page's tests and its dev server (web/espace/src/a2ui).
const threadsFixture = "web/espace/src/a2ui/threads.json"

// The page is tested on the surfaces code composes for threads: a work
// thread, one that lists items, one ended, a watcher's mails. ERIPHE_UPDATE_THREADS=1 writes
// them again from the code; they hold to the catalog.
func TestThreadSurfacesFixture(t *testing.T) {
	if os.Getenv("ERIPHE_UPDATE_THREADS") == "1" {
		out := map[string][]a2ui.Message{}
		for _, lang := range []string{"fr", "en"} {
			out[lang] = threadFixtures(t, lang)
		}
		raw, _ := json.MarshalIndent(out, "", " ")
		if err := os.WriteFile(threadsFixture, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(threadsFixture)
	if err != nil {
		t.Fatal(err)
	}
	var langs map[string]json.RawMessage
	if err := json.Unmarshal(raw, &langs); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"fr", "en"} {
		for _, e := range a2ui.Validate(langs[lang]) {
			t.Errorf("%s: %+v", lang, e)
		}
		for _, want := range []string{`"Progress"`, `"Item"`, `"Fold"`, `"compact"`, `"thread_tidy"`} {
			if !strings.Contains(string(langs[lang]), want) {
				t.Errorf("%s: no %s", lang, want)
			}
		}
	}
}

// threadFixtures composes the fixture's threads in a language. Their cards
// are the page's made-up ones (a2ui/fixtures.ts).
func threadFixtures(t *testing.T, lang string) []a2ui.Message {
	c, l := surfaceCore(t)
	c.store.set(userLangKey, lang)
	th := itemsThread(t, c, l)
	now := time.Now()
	en := lang == "en"
	pick := func(fr, eng string) string {
		if en {
			return eng
		}
		return fr
	}
	// An action offered on two items, one noted after reading outside
	// (code's words on its button); clicked on the first: its card on its
	// row (calls:1, the page's made-up card).
	move := noteAction{Label: pick("Virer depuis l'épargne", "Move from savings"), Tool: "ledger/move_funds", Args: map[string]any{"to": offerItemArg}}
	due := time.Now().Add(26 * time.Hour).Format("2006-01-02")
	if _, err := c.noteItem(th.ID, noteItemIn{Source: "ledger", Item: "Courant", Note: "Solde bas avant le loyer.", State: itemStateAct, Due: due, Actions: []noteAction{move}}, byBrain, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.noteItem(th.ID, noteItemIn{Source: "ledger", Item: "Joint", Note: "Un virement inconnu, d'après le relevé.", State: itemStateLook, Actions: []noteAction{move}}, byBrain, []string{"mail.read"}); err != nil {
		t.Fatal(err)
	}
	move.Tool = "mcp__ledger__move_funds"
	calls, err := c.offerCalls(th.ID, "ledger", "Courant", "", move.key())
	if err != nil {
		t.Fatal(err)
	}
	if ref, err := c.offer(context.Background(), th.ID, calls); err != nil || ref != "calls:1" {
		t.Fatalf("offer %q %v", ref, err)
	}
	// A watcher's standing thread of mails, as a skill opens it: a tool
	// that lists them by opaque ids, from a server not trusted, its items
	// titled by code from their own fields, all noted after reading them:
	// one for information, two with nothing to do and an action each,
	// worded by code (a note from outside never words its action).
	mails, err := c.openThread(threadIn{Title: pick("Mes mails", "My mails"), Goal: pick("Trie les mails reçus.", "Sorts the mails received."), Alias: "topic:mails", Ends: endsStanding, exact: true}, nil, "skill:mails")
	if err != nil {
		t.Fatal(err)
	}
	c.putThreadSource(mails.ID, threadSource{Name: "mails", Tool: "mail/list_received", Items: &sourceItems{Path: "messages", ID: "id"}}, "skill:mails")
	listed, _ := json.Marshal(map[string]any{"messages": []any{
		map[string]any{"id": "bTEfYWxlcnQx", "from": "Sam <sam@example.org>", "subject": pick("Le devis de la cuisine", "The kitchen quote")},
		map[string]any{"id": "bTEfbmV3czEy", "from": "Les Nouvelles <news@example.org>", "subject": pick("La lettre de novembre", "The November letter")},
		map[string]any{"id": "bTEfcHJvbW8z", "from": "Boutique <shop@example.org>", "subject": pick("-20 % ce week-end", "20% off this weekend")},
	}})
	c.store.db.Exec(`UPDATE thread_sources SET value = ? WHERE thread = ? AND name = ?`, string(listed), mails.ID, "mails")
	archive := noteAction{Label: pick("Archiver", "Archive"), Tool: "ledger/move_funds", Args: map[string]any{"to": offerItemArg}}
	for _, n := range []struct {
		in    noteItemIn
		taint []string
	}{
		{noteItemIn{Source: "mails", Item: "bTEfYWxlcnQx", Note: pick("Sam envoie le devis signé ; rien à répondre.", "Sam sends the signed quote; nothing to answer."), State: itemStateInfo}, []string{"mail.read"}},
		{noteItemIn{Source: "mails", Item: "bTEfbmV3czEy", State: itemStateNone, Actions: []noteAction{archive}}, []string{"mail.read"}},
		{noteItemIn{Source: "mails", Item: "bTEfcHJvbW8z", State: itemStateNone, Actions: []noteAction{archive}}, []string{"mail.read"}},
	} {
		if _, err := c.noteItem(mails.ID, n.in, "skill:mails", n.taint); err != nil {
			t.Fatal(err)
		}
	}
	// Sorted at 9 h 12 today: the same words at each run.
	y, m, d := now.In(c.userZone()).Date()
	c.store.db.Exec(`UPDATE thread_items SET noted = ?`, time.Date(y, m, d, 9, 12, 0, 0, c.userZone()).Unix())
	mails, _ = c.thread(mails.ID)

	var out []a2ui.Message
	// Composed as the thread whose data it shows (the one with the source,
	// or one with none), shown under the fixture's id.
	add := func(id int64, s Thread, steps []threadSurfaceStep) {
		if s.Ends != endsStanding {
			s.ID = id
		}
		in, ok := c.threadSurfaceIn(s, steps, nil, nil, now)
		if !ok {
			t.Fatalf("thread %d: no surface", id)
		}
		meta, _ := json.Marshal(map[string]any{"extensions": map[string]any{surfaceExtension: surfaceMeta{Title: in.Title, Author: threadAuthor(id),
			Weight: in.Weight, Why: in.Why, Answers: true, Summary: in.summary, State: in.state, New: in.fresh}}})
		var comps []a2ui.Component
		json.Unmarshal(in.Components, &comps)
		out = append(out, a2ui.Message{Version: a2ui.Version, CreateSurface: &a2ui.CreateSurface{SurfaceID: threadSurfaceID(id), CatalogID: a2ui.CatalogID,
			Components: comps, DataModel: in.Data, Metadata: meta}})
	}
	work := th
	work.Title, work.Goal = pick("Le site du club", "The club's site"), pick("Le site du club est en ligne, avec ses photos.", "The club's site is online, with its photos.")
	work.Summary, work.summarized = pick("La page est prête ; il reste les photos et ton accord pour publier.", "The page is ready; the photos and your yes to publish are left."), now.Add(-30*time.Minute).Unix()
	add(101, work, sampleSteps(lang, now))
	standing := th
	standing.Title, standing.Goal, standing.Ends = pick("Mes comptes", "My accounts"), pick("Les comptes suivis, et ce qui est à faire.", "The accounts followed, and what is to do."), endsStanding
	standing.Summary, standing.summarized = "", 0
	add(102, standing, nil)
	done := work
	done.Title, done.State, done.Ended = pick("La terrasse", "The terrace"), threadDone, now.Add(-time.Hour)
	done.Summary = pick("Le devis est signé et les travaux commencent lundi.", "The quote is signed and the work starts on Monday.")
	steps := sampleSteps(lang, now)
	for i := range steps {
		steps[i].State, steps[i].Waits, steps[i].Card = "done", false, ""
	}
	steps[1].Card = "access:5"
	add(103, done, steps)
	add(104, mails, nil)
	return out
}
