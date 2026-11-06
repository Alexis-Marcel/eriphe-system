package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// startCore is a core with a vault, the GitHub tool on a long-lived token
// (no GitHub App yet), an agent, and one node online, nas.
func startCore(t *testing.T) *core {
	t.Helper()
	dir := t.TempDir()
	secret := filepath.Join(dir, "claude")
	os.WriteFile(secret, []byte("tok"), 0o600)
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	key, _ := vaultKey(filepath.Join(dir, "vault.key"))
	v, err := openVault(store, key)
	if err != nil {
		t.Fatal(err)
	}
	setVault(v)
	t.Cleanup(func() { setVault(nil) })
	v.set("server.github.token", "github_pat_long_lived")
	cfg := &Config{
		User:         "Sam",
		Providers:    map[string]Provider{"anthropic": {Kind: "claude-subscription", Secret: secret}},
		Agents:       map[string]Agent{"sonnet": {Runtime: "claude-code", Provider: "anthropic", Model: "sonnet"}},
		DefaultAgent: "sonnet",
		Servers: map[string]Server{"github": {URL: "https://api.githubcopilot.com/mcp/",
			Auth: &ServerAuth{BearerFile: "vault:server.github.token"}, Use: "GitHub"}},
	}
	c := newCore(cfg, store)
	store.registerNode("nas", hashSecret("x"))
	store.setNodeStatus("nas", true, []byte(`{"runtimes":["claude-code"],"cpus":8}`))
	return c
}

// assignedIDs are the tasks node nas is given.
func assignedIDs(t *testing.T, c *core) []string {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/node/assignments?rev=0&wait=0", nil)
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Eriphe-Node", "nas")
	rec := httptest.NewRecorder()
	c.handleAssignments(rec, req)
	var out struct {
		Tasks []nodeAssignment `json:"tasks"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	var ids []string
	for _, a := range out.Tasks {
		ids = append(ids, a.ID)
	}
	return ids
}

// mayPush is the decision on a push to the repository lent.
func mayPush(c *core, t *TaskRow) repoDecision {
	return decideRepo("owner/repo", accessWrite, c.credentialFacts(t))
}

// launchWrite lends a repository for write on a branch, on a start that is
// not the user's own.
func launchWrite(t *testing.T, c *core) *TaskRow {
	t.Helper()
	return launchWriteAs(t, c, false)
}

func launchWriteAs(t *testing.T, c *core, clean bool) *TaskRow {
	t.Helper()
	task, err := c.runTask(context.Background(), runRequest{Prompt: "fix the menu", askStart: true, cleanStart: clean, Resources: []ResourceRef{
		{Kind: resRepo, Ref: "owner/repo", Access: accessWrite, Branch: "eriphe/x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if clean {
		c.store.set(lentByUserKey(task.ID), "1")
	}
	return task
}

// startCard is the one task-start card made, if any.
func startCard(t *testing.T, c *core) (convoCard, bool) {
	t.Helper()
	xs, err := c.cardsWhere(`kind = ?`, cardTaskStart)
	if err != nil || len(xs) > 1 {
		t.Fatalf("cards %v %v", xs, err)
	}
	if len(xs) == 0 {
		return convoCard{}, false
	}
	return xs[0], true
}

// A write lent by a start that is not the user's own is asked on one card at
// launch, as an access is, and the task does not start until they answer.
func TestTaskStartHeld(t *testing.T) {
	c := startCore(t)
	task := launchWrite(t, c)
	got, _ := c.store.task(task.ID)
	if got.State != statePending || !strings.Contains(got.PendingReason, "Sam's approval") || !c.heldTask(task.ID) {
		t.Fatalf("not held: %+v", got)
	}
	if ids := assignedIDs(t, c); slices.Contains(ids, task.ID) {
		t.Fatalf("a held task reached its node: %v", ids)
	}
	c.placePending()
	if ids := assignedIDs(t, c); slices.Contains(ids, task.ID) {
		t.Fatal("placement started a held task")
	}
	x, ok := startCard(t, c)
	if !ok || x.State != cardPending {
		t.Fatalf("no card: %+v", x)
	}
	_, p := c.liveCard(x)
	fr, en := p.Text("fr"), p.Text("en")
	if fr != "Pour pousser `eriphe/x` sur owner/repo, il me faut ton accord." || en != "To push `eriphe/x` to owner/repo, I need your approval." {
		t.Fatalf("card text %q / %q", fr, en)
	}
	// It goes with the brain's next message of the turn.
	c.attachTurnCards(7)
	if x, _ := startCard(t, c); x.Seq != 7 {
		t.Fatalf("not attached: %+v", x)
	}
	// The brain cannot decide it: no permission waits, and answering fails.
	allow := true
	if _, err := c.answer(context.Background(), task.ID, "", "", &allow, byBrain); err == nil {
		t.Fatal("the brain answered a held start")
	}
	if !c.heldTask(task.ID) || c.store.credentialGranted(task.ID, "owner/repo", accessWrite) {
		t.Fatal("decided without the user")
	}
}

// Oui records the grant and starts the task; its push is then allowed,
// whatever it reads meanwhile.
func TestTaskStartApproved(t *testing.T) {
	c := startCore(t)
	task := launchWrite(t, c)
	x, _ := startCard(t, c)
	if _, err := c.decideCard(context.Background(), x.ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	if c.heldTask(task.ID) || !slices.Contains(assignedIDs(t, c), task.ID) {
		t.Fatal("not started")
	}
	got, _ := c.store.task(task.ID)
	if d := mayPush(c, got); d.Decision != decideAllow {
		t.Fatalf("push after the launch approval: %+v", d)
	}
	log, _ := c.store.credentialLog(task.ID, 10)
	if len(log) != 1 || log[0].By != byUser || !strings.Contains(log[0].Reason, "at launch") {
		t.Fatalf("log %+v", log)
	}
	if _, err := c.decideCard(context.Background(), x.ID, false, "web"); err == nil {
		t.Fatal("decided twice")
	}
	c.store.addEvents(task.ID, []EventRow{{Seq: 1, Type: "tool_use", Data: map[string]any{"name": "WebFetch", "input": map[string]any{"url": "https://example.org"}}}})
	if d := mayPush(c, got); d.Decision != decideAllow {
		t.Fatalf("after reading a page: %+v", d)
	}
}

// Non stops the task before it starts, and tells the brain.
func TestTaskStartDeclined(t *testing.T) {
	c := startCore(t)
	task := launchWrite(t, c)
	x, _ := startCard(t, c)
	x, err := c.decideCard(context.Background(), x.ID, false, "web")
	if err != nil || x.State != cardRejected {
		t.Fatalf("%+v %v", x, err)
	}
	got, _ := c.store.task(task.ID)
	if got.State != stateStopped || slices.Contains(assignedIDs(t, c), task.ID) || c.store.credentialGranted(task.ID, "owner/repo", accessWrite) {
		t.Fatalf("declined start: %+v", got)
	}
	msgs, _ := c.store.queryMessages(`SELECT seq, time, role, text, via FROM messages WHERE role = ?`, roleEvent)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "refusé le lancement de la tâche "+task.ID) {
		t.Fatalf("brain not told: %+v", msgs)
	}
	// A start stopped otherwise (stop_task) leaves its card gone.
	other := launchWrite(t, c)
	c.stop(context.Background(), other.ID)
	xs, _ := c.cardsWhere(`kind = ? AND ref != ?`, cardTaskStart, x.Ref)
	if y, _ := c.liveCard(xs[0]); y.State != cardGone {
		t.Fatalf("stopped start: %+v", y)
	}
}

// The user's own clean start lends a write on its branch outright, with the
// App or without: the task starts at once, without a card.
func TestTaskStartClean(t *testing.T) {
	bare := startCore(t)
	if task := launchWriteAs(t, bare, true); bare.heldTask(task.ID) {
		t.Fatal("held without the app")
	}
	c := startCore(t)
	withApp(t, c, newFakeGitHub(t))
	task := launchWriteAs(t, c, true)
	if c.heldTask(task.ID) || !slices.Contains(assignedIDs(t, c), task.ID) {
		t.Fatal("held with the app")
	}
	if _, ok := startCard(t, c); ok {
		t.Fatal("a card on a clean start")
	}
	// It pushes, and opens its pull request with gh, without a question.
	got, _ := c.store.task(task.ID)
	if d := mayPush(c, got); d.Decision != decideAllow {
		t.Fatalf("push with the app: %+v", d)
	}
	// A read-only repository asks nothing at launch either, even without the app.
	c2 := startCore(t)
	read, err := c2.runTask(context.Background(), runRequest{Prompt: "read it", askStart: true,
		Resources: []ResourceRef{{Kind: resRepo, Ref: "owner/repo"}}})
	if err != nil || c2.heldTask(read.ID) {
		t.Fatalf("read held: %v", err)
	}
}

// A watch authorised in advance needs no card: the yes given for it covers
// the repository too, even with no branch declared.
func TestTaskStartApprovedInAdvance(t *testing.T) {
	c := startCore(t)
	withApp(t, c, newFakeGitHub(t))
	task, err := c.runTask(context.Background(), runRequest{Prompt: "fix the menu", askStart: true, cleanStart: true, approvedInAdvance: true,
		Resources: []ResourceRef{{Kind: resRepo, Ref: "owner/repo", Access: accessWrite}}})
	if err != nil || c.heldTask(task.ID) {
		t.Fatalf("held: %v", err)
	}
	if _, ok := startCard(t, c); ok {
		t.Fatal("a card for a task authorised in advance")
	}
	c.store.set(lentByUserKey(task.ID), "1")
	c.grantLent(task)
	got, _ := c.store.task(task.ID)
	if d := mayPush(c, got); d.Decision != decideAllow {
		t.Fatalf("push: %+v", d)
	}
}
