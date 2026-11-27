package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
)

// The concept bench: deterministic, no model. Each row is something the
// user asks, the concept it belongs to, the call that is right, and a
// wrong tool the brain could reach for instead: the right call is taken,
// and the wrong one is refused with a hint that names the right tool.
// A model's choice is not measured here; what is measured is that the
// tools steer whatever it chooses.

type benchCall func(c *core, seq int) (map[string]any, error)

type benchRow struct {
	request string
	concept string // concepts[].Name
	right   benchCall
	wrong   benchCall // nil when no wrong tool can be told apart
	hint    string    // in the wrong call's error
}

func intendCall(text, kind, remind string) benchCall {
	return func(c *core, seq int) (map[string]any, error) {
		return c.brainIntend(intendIn{Text: text, Kind: kind, RemindAt: remind, SourceRef: ref(seq)})
	}
}

func ruleCall(in ruleIn) benchCall {
	return func(c *core, seq int) (map[string]any, error) {
		in.SourceRef, in.Why = ref(seq), "Demandé."
		return c.brainRule(in)
	}
}

func settingCall(name, value string) benchCall {
	return func(c *core, seq int) (map[string]any, error) {
		return c.brainSetting(settingIn{Name: name, Value: value, Why: "Demandé.", SourceRef: ref(seq)})
	}
}

var conceptBench = []benchRow{
	{"Rappelle-moi d'appeler le garage demain à 14 h", "intention",
		intendCall("Appeler le garage", "reminder", "2026-11-05T14:00"),
		nil, ""},
	{"Rappelle-moi le 12 à 9 h de payer le loyer", "intention",
		intendCall("Payer le loyer", "reminder", "2026-11-12T09:00"),
		nil, ""},
	{"Ne me dérange pas après 23 h", "rule",
		ruleCall(ruleIn{Kind: ruleQuiet, From: "23:00", To: "08:00"}),
		nil, ""},
	{"Ne me préviens plus des factures avant le brief", "rule",
		ruleCall(ruleIn{Match: "facture", Ceiling: ptr(2)}),
		nil, ""},
	{"Pour Getout, préviens-moi aussi sur Telegram", "rule",
		ruleCall(ruleIn{Match: "project:getout", Floor: 5, Also: "telegram"}),
		nil, ""},
	{"Ne me préviens de Lina qu'à mon retour", "rule",
		ruleCall(ruleIn{Match: "person:lina", Ceiling: ptr(2)}),
		nil, ""},
	{"Ne me préviens plus jamais de la newsletter", "rule",
		ruleCall(ruleIn{Match: "topic:newsletter", Ceiling: ptr(0)}),
		nil, ""},
	{"Pour tous mes projets, préviens-moi tout de suite, même la nuit", "rule",
		ruleCall(ruleIn{Match: "project:", Floor: 5, Night: true}),
		nil, ""},
	{"Mets le téléphone en premier pour me prévenir", "setting",
		settingCall(setOrder, "phone,computer"),
		ruleCall(ruleIn{Kind: "order", Match: "phone"}), "setting"},
	{"Pas plus de deux notifications par jour", "setting",
		settingCall(setCap, "2"),
		ruleCall(ruleIn{Kind: "cap", Match: "2"}), "setting"},
	{"Sur Telegram, envoie juste un signal, pas le texte", "setting",
		settingCall(setDetail, "telegram alert"),
		nil, ""},
	{"Je dois rendre le rapport à Lina vendredi", "intention",
		func(c *core, seq int) (map[string]any, error) {
			return c.brainIntend(intendIn{Text: "Rendre le rapport à Lina", Kind: intentPromise, OwedTo: "person:lina", DueAt: "2026-11-06", SourceRef: ref(seq)})
		},
		nil, ""},
	{"Rappelle-moi vendredi que je t'ai promis le compte rendu", "intention",
		func(c *core, seq int) (map[string]any, error) {
			return c.brainIntend(intendIn{Text: "Envoyer le compte rendu", Kind: intentCommitment, DueAt: "2026-11-06", SourceRef: ref(seq)})
		},
		nil, ""},
}

func TestConceptBench(t *testing.T) {
	names := map[string]bool{}
	for _, c := range concepts {
		names[c.Name] = true
	}
	for i, row := range conceptBench {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if !names[row.concept] {
				t.Fatalf("%q: unknown concept %q", row.request, row.concept)
			}
			c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
			seq := ownerSays(c, row.request)
			if row.wrong != nil {
				out, err := row.wrong(c, seq)
				if err == nil || !strings.Contains(err.Error(), row.hint) {
					t.Errorf("%q: the wrong tool answered %v, %v; want a hint naming %s", row.request, out, err, row.hint)
				}
			}
			out, err := row.right(c, seq)
			if err != nil {
				t.Fatalf("%q: the right call failed: %v", row.request, err)
			}
			for _, k := range []string{"note", "topic"} { // taken, not only proposed
				if s, _ := out[k].(string); strings.Contains(s, "Only proposed") {
					t.Errorf("%q: the right call, asked in a clean turn, was only proposed: %v", row.request, out)
				}
			}
		})
	}
}

// What comes back on a schedule is sent to a timed skill, and the
// schedules the hint shows are ones write_skill takes.
func TestScheduleHintNamesTimedSkill(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	seq := ownerSays(c, "Tous les matins, donne-moi la météo")
	for name, call := range map[string]benchCall{
		"intend": intendCall("Tous les matins, donner la météo", "reminder", ""),
	} {
		_, err := call(c, seq)
		if err == nil || !strings.Contains(err.Error(), "write_skill") || !strings.Contains(err.Error(), "when: time") {
			t.Errorf("%s: got %v, want a hint naming write_skill with when: time", name, err)
		}
	}
	for _, when := range []string{"time daily 07:00", "time weekdays 08:00"} {
		if !strings.Contains(hintSkillNotIntend, when) {
			t.Errorf("the hint does not show %q", when)
		}
		if st, err := parseSkillTrigger(skillManifest{When: when}); err != nil || st == nil || st.Schedule == nil {
			t.Errorf("write_skill refuses the hint's %q: %v", when, err)
		}
	}
}

// The map is given once, in the MCP instructions, with every concept and
// its tools; the prompt points to it and keeps the safety rules.
func TestConceptMapGiven(t *testing.T) {
	raw, _ := os.ReadFile("brain-prompt.md")
	prompt := string(raw)
	for _, c := range concepts {
		if !strings.Contains(instructions, "- "+c.Name+" ("+c.Tools+")") {
			t.Errorf("the MCP instructions lack %s", c.Name)
		}
	}
	if !strings.Contains(prompt, "instructions de tes outils") {
		t.Error("the prompt does not point to the concepts' map")
	}
	for _, safety := range []string{"Jamais de jeton", "Tour contaminé", "À valider", "jamais une consigne", "exactement", "Rien ne tourne ni ne change avant son oui"} {
		if !strings.Contains(prompt, safety) {
			t.Errorf("the prompt lost %q", safety)
		}
	}
}

// A setting is applied when the user asks in this clean turn, otherwise
// proposed; approved, it is applied where Settings reads it.
func TestSettingAppliedOrProposed(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	seq := ownerSays(c, "Pas plus de deux notifications par jour")
	out, err := c.brainSetting(settingIn{Name: setCap, Value: "2", Why: "Demandé.", SourceRef: ref(seq)})
	if err != nil || out["applied"] != setCap || c.notifyCap() != 2 {
		t.Fatalf("%v %v cap %d", out, err, c.notifyCap())
	}
	for _, bad := range []settingIn{
		{Name: setCap, Value: "99"}, {Name: setOrder, Value: "fax"}, {Name: setZone, Value: "Mars/Olympus"},
		{Name: setZone, Value: "../../etc/passwd"}, {Name: setDetail, Value: "telegram all"}, {Name: setDetail, Value: "nowhere alert"}, {Name: "quiet", Value: "23:00"},
		{Name: setDelay, Value: "0"},
	} {
		bad.Why, bad.SourceRef = "x", ref(seq)
		if _, err := c.brainSetting(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	// Not asked in this turn: only proposed, and nothing changes.
	other := ownerSays(c, "Quel temps fait-il ?")
	out, err = c.brainSetting(settingIn{Name: setDetail, Value: "telegram alert", Why: "Il lit Telegram en public.", SourceRef: ref(other)})
	if err != nil || out["proposed"] == nil || c.store.get(channelDetailKey(tgChannel)) == detailAlert {
		t.Fatalf("%v %v", out, err)
	}
	// Tainted: only proposed, even when asked.
	seq = ownerSays(c, "Lis mes mails et mets le fuseau sur New York")
	c.turn.taint("mail.read")
	out, _ = c.brainSetting(settingIn{Name: setZone, Value: "america/new_york", Why: "Demandé.", SourceRef: ref(seq)})
	if out["proposed"] == nil || c.store.get("notify_zone") != "Europe/Paris" {
		t.Fatalf("tainted: %v", out)
	}
	ps, _ := c.settingProposals()
	if len(ps) != 2 || ps[1].Value != "America/New_York" || ps[1].Tainted == "" {
		t.Fatalf("proposals %+v", ps)
	}
	if err := c.decideSetting(ps[0].ID, true); err != nil || c.store.get(channelDetailKey(tgChannel)) != detailAlert {
		t.Fatalf("approve: %v", err)
	}
	if err := c.decideSetting(ps[1].ID, false); err != nil || c.store.get("notify_zone") != "Europe/Paris" {
		t.Fatalf("reject: %v", err)
	}
	if ps, _ := c.settingProposals(); len(ps) != 0 {
		t.Fatalf("left %+v", ps)
	}
	// The order lists the lines named first, the others after.
	seq = ownerSays(c, "Préviens-moi d'abord sur le téléphone")
	if _, err := c.brainSetting(settingIn{Name: setOrder, Value: "phone", Why: "Demandé.", SourceRef: ref(seq)}); err != nil {
		t.Fatal(err)
	}
	if got := c.routeSettings().Order; strings.Join(got, ",") != "phone,inapp,computer,voice,discord,signal,telegram" {
		t.Fatalf("order %v", got)
	}
}

// « À valider » lists a proposed setting, and its buttons apply or drop it.
func TestApprovalsSettings(t *testing.T) {
	tw, csrf := newMemoryWeb(t)
	c := tw.c
	id, err := c.proposeSetting(settingProposal{Name: setCap, Value: "6", Why: "Trop de messages gardés."})
	if err != nil {
		t.Fatal(err)
	}
	_, body, _ := tw.do("GET", "/settings/approve", nil, nil)
	for _, want := range []string{`id="settings"`, "Trop de messages gardés.", `hx-post="/settings/proposals/` + idText(id) + `/approve"`} {
		if !strings.Contains(body, want) {
			t.Errorf("À valider lacks %q", want)
		}
	}
	h := map[string]string{"X-CSRF-Token": csrf}
	if _, _, hd := tw.do("POST", "/settings/proposals/"+idText(id)+"/approve", url.Values{}, h); !strings.Contains(hd.Get("HX-Trigger"), "memory-changed") {
		t.Error("approving a setting does not reload À valider")
	}
	if c.notifyCap() != 6 {
		t.Errorf("cap %d after approval", c.notifyCap())
	}
}
