package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The cards of the conversation in the workspace (convo_cards.go): shown
// under the brain's message they go with, answered by their own buttons,
// and kept up to date by the event stream ("cards"). A spontaneous
// message of the brain's says why it was sent, folded under it.

func (w *web) cardRoutes(mux *http.ServeMux) {
	mux.Handle("POST /cards/{id}/{action}", w.auth(w.handleCard))
}

// cardView is a card as the templates show it.
type cardView struct {
	ID      int64
	Seq     int
	Kind    string
	State   string
	Text    string
	Why     string
	Tainted string
	Digest  *digestView
}

// digestView is what a digest card unfolds.
type digestView struct {
	From  time.Time
	Items []digestItem
}

type digestItem struct {
	At       time.Time
	Summary  string
	Task     string // a task's id: its card opens in the drawer
	Link     string
	External bool
}

// msgWhy is why a spontaneous message was sent: its notification's.
type msgWhy struct {
	Why  string
	Ref  string
	Link string
}

func (w *web) cardView(x convoCard, lang string) cardView {
	x, p := w.c.liveCard(x)
	v := cardView{ID: x.ID, Seq: x.Seq, Kind: x.Kind, State: x.State, Text: x.Text}
	if x.Kind == cardDigest {
		d, items := w.c.digestItems(x)
		dv := &digestView{From: time.Unix(d.From, 0)}
		for _, h := range items {
			it := digestItem{At: h.At, Summary: h.summary(lang, true), External: h.Trust == feedExternal, Link: happenedLink(h.Ref)}
			if id, ok := strings.CutPrefix(h.Ref, "task:"); ok {
				it.Task = id
			}
			dv.Items = append(dv.Items, it)
		}
		v.Digest = dv
		return v
	}
	if t := p.Text(lang); t != "" {
		v.Text = t
	}
	v.Why, v.Tainted = p.Why, p.Tainted
	return v
}

// dressCards gives the brain's messages their cards and their why.
func (w *web) dressCards(msgs []Message, lang string) {
	var seqs []int
	for _, m := range msgs {
		if m.Role == roleBrain {
			seqs = append(seqs, m.Seq)
		}
	}
	if len(seqs) == 0 {
		return
	}
	cards := w.c.cardsOf(seqs)
	whys := w.c.whysOf(seqs)
	for i := range msgs {
		for _, x := range cards[msgs[i].Seq] {
			msgs[i].Cards = append(msgs[i].Cards, w.cardView(x, lang))
		}
		msgs[i].Why = whys[msgs[i].Seq]
	}
}

// whysOf reads the notifications the messages were sent as.
func (c *core) whysOf(seqs []int) map[int]*msgWhy {
	out := map[int]*msgWhy{}
	rows, err := c.store.db.Query(`SELECT msg, why, ref FROM notifications WHERE msg >= ? AND msg <= ?`, seqs[0], seqs[len(seqs)-1])
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var why, ref string
		if rows.Scan(&seq, &why, &ref) == nil && strings.TrimSpace(why) != "" {
			out[seq] = &msgWhy{Why: why, Ref: ref, Link: happenedLink(ref)}
		}
	}
	return out
}

// handleCard is a card's Oui or Non.
func (w *web) handleCard(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(rw, "bad id", http.StatusBadRequest)
		return
	}
	action := r.PathValue("action")
	if action != "yes" && action != "no" {
		http.Error(rw, "yes or no", http.StatusBadRequest)
		return
	}
	x, err := w.c.decideCard(r.Context(), id, action == "yes", "web")
	switch {
	case errors.Is(err, errNoCard):
		http.Error(rw, err.Error(), http.StatusNotFound)
		return
	case errors.Is(err, errCardDecided):
		rw.Header().Set("HX-Trigger", hxTrigger(tr(lang, "card.already"), false, true))
	case err != nil:
		w.toast(rw, err.Error(), false)
		return
	default:
		rw.Header().Set("HX-Trigger", hxTrigger(tr(lang, "card.did."+x.State), true, true))
	}
	w.notify()
	html, err := w.fragment(lang, "home", "card", w.cardView(x, lang))
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Write([]byte(html))
}

// liveCards are the cards the event stream keeps up to date: those still
// waiting, and those attached or decided lately.
func (w *web) liveCards() []convoCard {
	since := time.Now().Add(-2 * time.Minute).Unix()
	xs, _ := w.c.cardsWhere(`seq > 0 AND (state = ? OR attached >= ? OR decided >= ?) ORDER BY id DESC LIMIT 40`, cardPending, since, since)
	return xs
}
