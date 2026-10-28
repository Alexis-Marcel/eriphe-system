package main

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// The cache keeps, in memory only, the headers of the recent messages of a
// few folders (INBOX and Sent by default) and the text of the bodies read
// lately, so that search_messages and read_message answer at once. One
// connection per account stays open: IDLE on INBOX when the server has it,
// and a poll of every cached folder in any case. A request the cache cannot
// answer for sure (text search, older mail, another folder, a cold cache)
// goes to the server as before. Nothing is written to disk.

// CacheOptions are the cache knobs, read from the environment.
type CacheOptions struct {
	Days        int           // window: messages received in the last Days days
	MaxMessages int           // per folder; the newest are kept
	BodyBytes   int64         // bodies kept, all accounts together (LRU)
	Prefetch    int           // newest INBOX bodies fetched ahead of reads
	Poll        time.Duration // check every cached folder this often
	Folders     []string      // folder names or roles
	NoIdle      bool          // poll only
}

// cacheOptionsFromEnv reads MAIL_CACHE_*; ok is false when MAIL_CACHE=off.
func cacheOptionsFromEnv() (o CacheOptions, ok bool, err error) {
	switch strings.ToLower(os.Getenv("MAIL_CACHE")) {
	case "off", "false", "0", "no":
		return o, false, nil
	}
	o = CacheOptions{Days: 30, MaxMessages: 2000, BodyBytes: 50 << 20, Prefetch: 20, Poll: 2 * time.Minute,
		Folders: []string{"inbox", "sent"}}
	var mb int
	for _, v := range []struct {
		key string
		n   *int
		min int
	}{{"MAIL_CACHE_DAYS", &o.Days, 1}, {"MAIL_CACHE_MAX_MESSAGES", &o.MaxMessages, 1}, {"MAIL_CACHE_PREFETCH", &o.Prefetch, 0}, {"MAIL_CACHE_BODY_MB", &mb, 0}} {
		s := os.Getenv(v.key)
		if s == "" {
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < v.min {
			return o, false, fmt.Errorf("%s: a number of at least %d", v.key, v.min)
		}
		*v.n = n
	}
	if os.Getenv("MAIL_CACHE_BODY_MB") != "" {
		o.BodyBytes = int64(mb) << 20
	}
	if s := os.Getenv("MAIL_CACHE_POLL"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d < 10*time.Second {
			return o, false, errors.New("MAIL_CACHE_POLL: a duration of at least 10s, e.g. 2m")
		}
		o.Poll = d
	}
	if s := os.Getenv("MAIL_CACHE_FOLDERS"); s != "" {
		o.Folders = nil
		for _, f := range strings.Split(s, ",") {
			if f = strings.TrimSpace(f); f != "" {
				o.Folders = append(o.Folders, f)
			}
		}
	}
	switch strings.ToLower(os.Getenv("MAIL_CACHE_IDLE")) {
	case "off", "false", "0", "no":
		o.NoIdle = true
	}
	return o, true, nil
}

// MailboxChange is what a sync found changed in a cached folder: the event a
// change notification would carry. Gone counts expunged and moved messages,
// and those that left the window.
type MailboxChange struct {
	Account, Folder  string
	New, Gone, Flags int
}

// StartCache starts filling and watching the cache of every account. It
// returns at once: tools are served live until each folder is warm.
func (m *Mailer) StartCache(ctx context.Context, o CacheOptions) {
	bodies := newBodyLRU(o.BodyBytes)
	for _, a := range m.accounts {
		a.cache = &mailCache{
			a: a, opts: o, bodies: bodies, bodyChars: m.cfg.MaxBodyChars, onChange: m.OnChange,
			folders: map[string]*folderCache{}, alias: map[string]string{},
			wake: make(chan struct{}, 1), kick: make(chan struct{}, 1), ready: make(chan struct{}),
		}
		go a.cache.run(ctx)
	}
}

type mailCache struct {
	a         *Account
	opts      CacheOptions
	bodies    *bodyLRU
	bodyChars int
	onChange  func(MailboxChange)

	mu       sync.RWMutex
	folders  map[string]*folderCache // by folder name
	alias    map[string]string       // lower-case name or role -> folder name
	syncedAt time.Time               // end of the last sync

	wake chan struct{} // the server reported something (IDLE)
	kick chan struct{} // a write went through this server

	ready     chan struct{} // closed once the first sync is done
	readyOnce sync.Once
}

type folderCache struct {
	uidValidity uint32
	uidNext     imap.UID
	numMessages uint32
	modSeq      uint64
	since       time.Time // window start of the last sync
	cover       time.Time // every message received since then is here
	msgs        map[imap.UID]*header
}

func notify(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// poke asks for a sync soon, after a write.
func (mc *mailCache) poke() {
	if mc != nil {
		notify(mc.kick)
	}
}

func (mc *mailCache) bodyLimit() int {
	if mc == nil {
		return 0
	}
	return mc.bodyChars
}

// logName names the account in logs without its address.
func (a *Account) logName() string {
	if !strings.Contains(a.Name, "@") {
		return a.Name
	}
	_, domain, _ := strings.Cut(a.Name, "@")
	return "@" + domain
}

// scrub removes the account's address from an error before it is logged.
func (a *Account) scrub(err error) string {
	s := err.Error()
	for _, x := range []string{a.Address, a.Username, a.Name} {
		if strings.Contains(x, "@") {
			s = strings.ReplaceAll(s, x, a.logName())
		}
	}
	return s
}

// --- Keeping it fresh ---

// run keeps a connection open and the cache in sync, reconnecting with
// backoff (5 s doubling to 5 min) when the connection drops.
func (mc *mailCache) run(ctx context.Context) {
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := mc.watch(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > 5*time.Minute {
			backoff = 5 * time.Second
		}
		log.Printf("mail cache account=%s: %s (retry in %s)", mc.a.logName(), mc.a.scrub(err), backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 5*time.Minute)
	}
}

// within runs fn and drops the connection if it takes longer than d, so a
// server gone silent cannot stall the cache.
func within(c *imapclient.Client, d time.Duration, fn func() error) error {
	t := time.AfterFunc(d, func() { c.Close() })
	defer t.Stop()
	return fn()
}

// watch syncs, then waits for news (IDLE, poll timer, a write) and syncs
// again, until the connection fails or ctx ends.
func (mc *mailCache) watch(ctx context.Context) error {
	a := mc.a
	c, err := a.connect(&imapclient.UnilateralDataHandler{
		Expunge: func(uint32) { notify(mc.wake) },
		Mailbox: func(*imapclient.UnilateralDataMailbox) { notify(mc.wake) },
		Fetch: func(m *imapclient.FetchMessageData) {
			m.Collect()
			notify(mc.wake)
		},
	})
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer func() {
		stop()
		within(c, 5*time.Second, func() error { return c.Logout().Wait() })
		c.Close()
	}()
	var folders []string
	if err := within(c, a.timeout, func() error {
		if err := a.login(c); err != nil {
			return err
		}
		folders = mc.resolve(c)
		return nil
	}); err != nil {
		return err
	}
	if len(folders) == 0 {
		<-ctx.Done()
		return nil
	}
	caps := c.Caps()
	condstore := caps.Has(imap.CapCondStore)
	idle := !mc.opts.NoIdle && (caps.Has(imap.CapIdle) || caps.Has(imap.CapIMAP4rev2))
	// A first sync of thousands of headers takes a while; later ones are quick.
	syncTimeout := max(5*a.timeout, 5*time.Minute)
	first := true
	for {
		if err := within(c, syncTimeout, func() error { return mc.syncAll(c, folders, condstore, first) }); err != nil {
			return err
		}
		first = false
		mc.readyOnce.Do(func() { close(mc.ready) })
		// The last folder synced is still selected: IDLE watches it (INBOX).
		var cmd *imapclient.IdleCommand
		if idle {
			if cmd, err = c.Idle(); err != nil {
				return fmt.Errorf("idle: %w", err)
			}
		}
		select {
		case <-ctx.Done():
		case <-mc.wake:
		case <-mc.kick:
		case <-time.After(mc.opts.Poll):
		}
		if cmd != nil {
			if err := within(c, a.timeout, func() error {
				if err := cmd.Close(); err != nil {
					return err
				}
				return cmd.Wait()
			}); err != nil {
				return fmt.Errorf("idle: %w", err)
			}
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// resolve turns the configured folders into names, INBOX last, and records
// the names and roles the cache answers for.
func (mc *mailCache) resolve(c *imapclient.Client) []string {
	alias := map[string]string{}
	var names []string
	for _, spec := range mc.opts.Folders {
		name, err := mc.a.resolveFolder(c, spec)
		if err != nil {
			log.Printf("mail cache account=%s: folder %q not cached: %s", mc.a.logName(), spec, mc.a.scrub(err))
			continue
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
		alias[strings.ToLower(spec)] = name
	}
	if list, err := mc.a.cachedFolders(c); err == nil {
		for _, f := range list {
			if f.Role != "" && slices.Contains(names, f.Name) {
				alias[f.Role] = f.Name
			}
		}
	}
	for _, n := range names {
		alias[strings.ToLower(n)] = n
	}
	slices.SortStableFunc(names, func(x, y string) int {
		switch {
		case x == "INBOX" && y != "INBOX":
			return 1
		case y == "INBOX" && x != "INBOX":
			return -1
		}
		return 0
	})
	mc.mu.Lock()
	mc.alias = alias
	mc.mu.Unlock()
	return names
}

// windowStart is the first day of the window, in the local zone.
func (mc *mailCache) windowStart(now time.Time) time.Time {
	d := now.In(time.Local).AddDate(0, 0, -mc.opts.Days)
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.Local)
}

// syncAll syncs every cached folder and logs one line when it did any work.
func (mc *mailCache) syncAll(c *imapclient.Client, folders []string, condstore, first bool) error {
	select { // what the server said so far is about to be read
	case <-mc.wake:
	default:
	}
	start := time.Now()
	var total MailboxChange
	for _, name := range folders {
		ch, err := mc.syncFolder(c, name, condstore)
		if err != nil {
			return err
		}
		if ch.New+ch.Gone+ch.Flags > 0 {
			total.New, total.Gone, total.Flags = total.New+ch.New, total.Gone+ch.Gone, total.Flags+ch.Flags
			if mc.onChange != nil {
				mc.onChange(ch)
			}
		}
	}
	if slices.Contains(folders, "INBOX") {
		if err := mc.prefetch(c, "INBOX"); err != nil {
			return err
		}
	}
	mc.mu.Lock()
	mc.syncedAt = time.Now()
	n := 0
	for _, f := range mc.folders {
		n += len(f.msgs)
	}
	mc.mu.Unlock()
	if first || total.New+total.Gone+total.Flags > 0 {
		log.Printf("mail sync account=%s folders=%d messages=%d new=%d gone=%d flags=%d bodies_kb=%d ms=%d",
			mc.a.logName(), len(folders), n, total.New, total.Gone, total.Flags, mc.bodies.used()>>10, time.Since(start).Milliseconds())
	}
	return nil
}

// syncFolder brings one folder up to date: new messages in the window are
// fetched, expunged ones dropped, flags refreshed (only those changed, with
// CONDSTORE). Nothing is asked when the folder did not change at all.
func (mc *mailCache) syncFolder(c *imapclient.Client, name string, condstore bool) (MailboxChange, error) {
	ch := MailboxChange{Account: mc.a.Name, Folder: name}
	sel, err := c.Select(name, &imap.SelectOptions{ReadOnly: true, CondStore: condstore}).Wait()
	if err != nil {
		return ch, fmt.Errorf("open %q: %w", name, err)
	}
	since := mc.windowStart(time.Now())

	mc.mu.RLock()
	old := mc.folders[name]
	fresh := old == nil || old.uidValidity != sel.UIDValidity
	known := map[imap.UID][]imap.Flag{}
	var oldModSeq uint64
	if !fresh {
		if condstore && sel.HighestModSeq != 0 && old.modSeq == sel.HighestModSeq &&
			old.uidNext == sel.UIDNext && old.numMessages == sel.NumMessages && old.since.Equal(since) {
			mc.mu.RUnlock()
			return ch, nil
		}
		for u, h := range old.msgs {
			known[u] = h.flags
		}
		oldModSeq = old.modSeq
	}
	mc.mu.RUnlock()

	var uids []imap.UID
	if sel.NumMessages > 0 {
		res, err := c.UIDSearch(&imap.SearchCriteria{Since: since}, nil).Wait()
		if err != nil {
			return ch, fmt.Errorf("search %q: %w", name, err)
		}
		uids = res.AllUIDs()
		slices.Sort(uids)
	}
	capped := len(uids) > mc.opts.MaxMessages
	if capped {
		uids = uids[len(uids)-mc.opts.MaxMessages:]
	}
	var added, kept []imap.UID
	in := make(map[imap.UID]bool, len(uids))
	for _, u := range uids {
		in[u] = true
		if _, ok := known[u]; ok {
			kept = append(kept, u)
		} else {
			added = append(added, u)
		}
	}
	var gone []imap.UID
	for u := range known {
		if !in[u] {
			gone = append(gone, u)
		}
	}

	flags := map[imap.UID][]imap.Flag{}
	if len(kept) > 0 {
		opts := &imap.FetchOptions{UID: true, Flags: true}
		if condstore && oldModSeq != 0 {
			opts.ChangedSince = oldModSeq
		}
		msgs, err := c.Fetch(imap.UIDSetNum(kept...), opts).Collect()
		if err != nil {
			return ch, fmt.Errorf("flags %q: %w", name, err)
		}
		for _, m := range msgs {
			if f, ok := known[m.UID]; ok && !sameFlags(f, m.Flags) {
				flags[m.UID] = m.Flags
			}
		}
	}
	var hs []*header
	for i := 0; i < len(added); i += 250 {
		part, err := mc.a.headers(c, name, sel.UIDValidity, added[i:min(i+250, len(added))])
		if err != nil {
			return ch, err
		}
		hs = append(hs, part...)
	}

	mc.mu.Lock()
	defer mc.mu.Unlock()
	f := mc.folders[name]
	if f == nil || fresh {
		if f != nil {
			for _, h := range f.msgs {
				mc.bodies.drop(h.ID)
			}
		}
		f = &folderCache{msgs: map[imap.UID]*header{}}
		mc.folders[name] = f
	}
	for _, u := range gone {
		if h := f.msgs[u]; h != nil {
			mc.bodies.drop(h.ID)
			delete(f.msgs, u)
		}
	}
	for u, fl := range flags {
		if h := f.msgs[u]; h != nil {
			h.setFlags(fl)
		}
	}
	for _, h := range hs {
		f.msgs[h.uid] = h
	}
	f.uidValidity, f.uidNext, f.numMessages, f.modSeq = sel.UIDValidity, sel.UIDNext, sel.NumMessages, sel.HighestModSeq
	f.since, f.cover = since, since
	if capped {
		// Only the newest are here: the window effectively starts later.
		f.cover = time.Time{}
		for _, h := range f.msgs {
			if f.cover.IsZero() || h.internal.Before(f.cover) {
				f.cover = h.internal
			}
		}
	}
	ch.New, ch.Gone, ch.Flags = len(hs), len(gone), len(flags)
	return ch, nil
}

func sameFlags(a, b []imap.Flag) bool {
	if len(a) != len(b) {
		return false
	}
	for _, f := range a {
		if !slices.ContainsFunc(b, func(g imap.Flag) bool { return strings.EqualFold(string(f), string(g)) }) {
			return false
		}
	}
	return true
}

// prefetch fetches the bodies of the newest messages of the (selected)
// folder that the cache does not hold yet.
func (mc *mailCache) prefetch(c *imapclient.Client, name string) error {
	if mc.opts.Prefetch <= 0 {
		return nil
	}
	mc.mu.RLock()
	var hs []*header
	if f := mc.folders[name]; f != nil {
		for _, h := range f.msgs {
			hs = append(hs, h)
		}
	}
	mc.mu.RUnlock()
	slices.SortFunc(hs, func(x, y *header) int { return y.internal.Compare(x.internal) })
	type pending struct {
		h   *header
		sec *imap.FetchItemBodySection
		cmd *imapclient.FetchCommand
	}
	var todo []pending
	for _, h := range hs[:min(len(hs), mc.opts.Prefetch)] {
		if h.body == nil || mc.bodies.has(h.ID) {
			continue
		}
		sec := *h.body.section
		sec.Partial = &imap.SectionPartial{Offset: 0, Size: maxRawPart}
		cmd := c.Fetch(imap.UIDSetNum(h.uid), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{&sec}})
		todo = append(todo, pending{h, &sec, cmd})
	}
	for _, p := range todo {
		bufs, err := p.cmd.Collect()
		if err != nil {
			return fmt.Errorf("prefetch: %w", err)
		}
		if len(bufs) > 0 {
			if raw := bufs[0].FindBodySection(p.sec); raw != nil {
				mc.bodies.put(p.h.ID, makeBody(p.h.body, raw, mc.bodyChars))
			}
		}
	}
	return nil
}

// --- Answering from the cache ---

// fresh tells whether the cache is still followed: a sync within two polls.
// While the connection is down, requests go to the server (caller holds mu).
func (mc *mailCache) fresh() bool {
	return time.Since(mc.syncedAt) < 2*mc.opts.Poll+time.Minute
}

// folder finds a warm cached folder by name or role (caller holds mu).
func (mc *mailCache) folder(s string) (string, *folderCache) {
	if !mc.fresh() {
		return "", nil
	}
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		s = "inbox"
	}
	name, ok := mc.alias[s]
	if !ok {
		return "", nil
	}
	return name, mc.folders[name]
}

// search answers from the cache when the answer is sure to be the server's:
// no full-text search, a cached folder, and either a since inside the window
// or enough matches in the window to fill the limit (older mail would come
// after them). ok is false otherwise, and the caller asks the server.
func (mc *mailCache) search(q Query) ([]Summary, bool) {
	if mc == nil || strings.TrimSpace(q.Text) != "" {
		return nil, false
	}
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	_, f := mc.folder(q.Folder)
	if f == nil {
		return nil, false
	}
	var hits []Summary
	for _, h := range f.msgs {
		if q.matches(h) {
			hits = append(hits, h.Summary)
		}
	}
	sortNewest(hits)
	whole := !q.Since.IsZero() && !q.Since.Before(day(f.cover))
	whole = whole || len(hits) >= q.Limit || len(f.msgs) == int(f.numMessages)
	if !whole {
		return nil, false
	}
	if len(hits) > q.Limit {
		hits = hits[:q.Limit]
	}
	return hits, true
}

// day is the date of t, as IMAP compares dates (in t's own zone).
func day(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// matches applies the search criteria IMAP would: SENTSINCE and SENTBEFORE
// on the Date header's day, substrings of From, To and Subject in any case.
func (q Query) matches(h *header) bool {
	if !q.Since.IsZero() && day(h.time).Before(q.Since) {
		return false
	}
	if !q.Before.IsZero() && !day(h.time).Before(q.Before) {
		return false
	}
	if q.Unread && !h.Unread || q.Flagged && !h.Flagged {
		return false
	}
	has := func(s, sub string) bool {
		sub = strings.TrimSpace(sub)
		return sub == "" || strings.Contains(strings.ToLower(s), strings.ToLower(sub))
	}
	return has(h.From, q.From) && has(strings.Join(h.To, ", "), q.To) && has(h.Subject, q.Subject)
}

// read answers read_message from the cache when it holds the header and the
// body; nil otherwise.
func (mc *mailCache) read(r msgRef, maxChars int) *Message {
	if mc == nil {
		return nil
	}
	mc.mu.RLock()
	f := mc.folders[r.Folder]
	var h *header
	if f != nil && f.uidValidity == r.UIDValidity && mc.fresh() {
		h = f.msgs[r.UID]
	}
	var msg *Message
	if h != nil {
		msg = h.message()
	}
	mc.mu.RUnlock()
	if h == nil {
		return nil
	}
	if h.body == nil {
		msg.Body = wrapUntrusted("")
		return msg
	}
	b, ok := mc.bodies.get(h.ID)
	if !ok {
		return nil
	}
	msg.setBody(b, maxChars)
	return msg
}

// putBody keeps a body read from the server, if its folder is cached.
func (mc *mailCache) putBody(r msgRef, b body) {
	if mc == nil {
		return
	}
	mc.mu.RLock()
	f := mc.folders[r.Folder]
	ok := f != nil && f.uidValidity == r.UIDValidity && f.msgs[r.UID] != nil
	mc.mu.RUnlock()
	if ok {
		mc.bodies.put(r.String(), b)
	}
}

// setFlags records flags this server just stored.
func (mc *mailCache) setFlags(k folderKey, uids []imap.UID, read, flagged *bool) {
	if mc == nil {
		return
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	f := mc.folders[k.Folder]
	if f == nil || f.uidValidity != k.UIDValidity {
		return
	}
	for _, u := range uids {
		h := f.msgs[u]
		if h == nil {
			continue
		}
		fl := slices.Clone(h.flags)
		set := func(on *bool, flag imap.Flag) {
			if on == nil {
				return
			}
			fl = slices.DeleteFunc(fl, func(x imap.Flag) bool { return strings.EqualFold(string(x), string(flag)) })
			if *on {
				fl = append(fl, flag)
			}
		}
		set(read, imap.FlagSeen)
		set(flagged, imap.FlagFlagged)
		h.setFlags(fl)
	}
}

// forget drops messages this server just moved away.
func (mc *mailCache) forget(k folderKey, uids []imap.UID) {
	if mc == nil {
		return
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	f := mc.folders[k.Folder]
	if f == nil || f.uidValidity != k.UIDValidity {
		return
	}
	for _, u := range uids {
		if h := f.msgs[u]; h != nil {
			mc.bodies.drop(h.ID)
			delete(f.msgs, u)
			f.numMessages--
		}
	}
}

// --- Bodies ---

// bodyLRU keeps decoded bodies up to a total size, the least recently read
// going first.
type bodyLRU struct {
	mu    sync.Mutex
	max   int64
	size  int64
	ll    *list.List
	byKey map[string]*list.Element
}

type bodyItem struct {
	key string
	b   body
}

func newBodyLRU(max int64) *bodyLRU {
	return &bodyLRU{max: max, ll: list.New(), byKey: map[string]*list.Element{}}
}

func (it *bodyItem) cost() int64 { return int64(len(it.key)+len(it.b.text)+len(it.b.format)) + 96 }

func (l *bodyLRU) get(key string) (body, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.byKey[key]
	if !ok {
		return body{}, false
	}
	l.ll.MoveToFront(e)
	return e.Value.(*bodyItem).b, true
}

func (l *bodyLRU) has(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.byKey[key]
	return ok
}

func (l *bodyLRU) put(key string, b body) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.remove(key)
	it := &bodyItem{key, b}
	if it.cost() > l.max {
		return
	}
	l.byKey[key] = l.ll.PushFront(it)
	l.size += it.cost()
	for l.size > l.max {
		l.remove(l.ll.Back().Value.(*bodyItem).key)
	}
}

func (l *bodyLRU) drop(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.remove(key)
}

func (l *bodyLRU) remove(key string) {
	if e, ok := l.byKey[key]; ok {
		l.size -= e.Value.(*bodyItem).cost()
		l.ll.Remove(e)
		delete(l.byKey, key)
	}
}

func (l *bodyLRU) used() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.size
}
