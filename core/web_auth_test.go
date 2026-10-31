package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// An address is refused after 5 failures, for the rest of the window; other
// addresses are not, however many fail: there is no global lockout.
func TestLoginLimiterPerAddress(t *testing.T) {
	l := newLoginLimiter()
	now := time.Unix(1_700_000_000, 0)
	failN := func(ip string, n int) {
		for range n {
			if !l.begin(ip, now) {
				t.Fatalf("%s refused too early", ip)
			}
			l.end(ip, now, false)
		}
	}
	failN("203.0.113.1", 5)
	if l.begin("203.0.113.1", now) {
		t.Fatal("a 6th try from the same address went through")
	}
	// 200 failures from 40 other addresses.
	for i := range 40 {
		failN(fmt.Sprintf("198.51.100.%d", i), 5)
	}
	if !l.begin("192.0.2.7", now) {
		t.Fatal("the owner's address is locked out by the others' failures")
	}
	l.end("192.0.2.7", now, true)
	// The window passes.
	if !l.begin("203.0.113.1", now.Add(16*time.Minute)) {
		t.Fatal("the address is still refused after the window")
	}
	l.end("203.0.113.1", now.Add(16*time.Minute), true)
	// A success forgets the address's failures.
	failN("203.0.113.2", 4)
	if !l.begin("203.0.113.2", now) {
		t.Fatal("refused before its limit")
	}
	l.end("203.0.113.2", now, true)
	failN("203.0.113.2", 5)
}

// An IPv6 /64 is one address: rotating within it does not reset the limit.
func TestLoginLimiterIPv6Prefix(t *testing.T) {
	l := newLoginLimiter()
	now := time.Now()
	for i := range 5 {
		ip := fmt.Sprintf("2001:db8:1:2::%x", i+1)
		if !l.begin(ip, now) {
			t.Fatalf("%s refused", ip)
		}
		l.end(ip, now, false)
	}
	if l.begin("2001:db8:1:2::ffff", now) {
		t.Fatal("another address of the same /64 went through")
	}
	if !l.begin("2001:db8:1:3::1", now) {
		t.Fatal("another /64 was refused")
	}
}

// Attempts in flight count: parallel requests cannot go past the limit.
func TestLoginLimiterInFlight(t *testing.T) {
	l := newLoginLimiter()
	now := time.Now()
	started := 0
	for range 20 {
		if l.begin("203.0.113.9", now) {
			started++
		}
	}
	if started != 5 {
		t.Fatalf("%d parallel attempts started, want 5", started)
	}
}

// Past 20 failures from everywhere, attempts wait longer, up to a cap.
func TestLoginLimiterGlobalSlowdown(t *testing.T) {
	l := newLoginLimiter()
	now := time.Now()
	fail := func(n int) {
		for i := range n {
			ip := fmt.Sprintf("10.%d.%d.1", i/250, i%250)
			l.begin(ip, now)
			l.end(ip, now, false)
		}
	}
	fail(19)
	if d := l.delay(now); d != 0 {
		t.Fatalf("slowed down after 19 failures: %s", d)
	}
	fail(2)
	if d := l.delay(now); d != 2*l.step {
		t.Fatalf("delay after 21 failures: %s, want %s", d, 2*l.step)
	}
	fail(1000)
	if d := l.delay(now); d != l.maxWait {
		t.Fatalf("delay after 1021 failures: %s, want the cap %s", d, l.maxWait)
	}
	if d := l.delay(now.Add(16 * time.Minute)); d != 0 {
		t.Fatalf("still slowed down after the window: %s", d)
	}
	// The wait ends with the request.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := l.wait(ctx, now); err == nil || time.Since(start) > time.Second {
		t.Fatal("a cancelled request still waited")
	}
}

// At most two argon2 hashes run at once.
func TestArgonConcurrencyCap(t *testing.T) {
	var running, peak atomic.Int32
	orig := argonKey
	argonKey = func(pw, salt []byte, t, m uint32, p uint8, n uint32) []byte {
		cur := running.Add(1)
		for {
			old := peak.Load()
			if cur <= old || peak.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		return orig(pw, salt, 1, 8, 1, n)
	}
	defer func() { argonKey = orig }()
	hash := hashPassword("correct horse")
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			checkPassword(hash, "wrong")
		}()
	}
	wg.Wait()
	if p := peak.Load(); p > 2 {
		t.Fatalf("%d argon2 hashes at once, want at most 2", p)
	}
	if !checkPassword(hash, "correct horse") {
		t.Fatal("the right password is refused")
	}
}

func TestSetupCodeCommand(t *testing.T) {
	for _, c := range [][3]string{
		{"linux", "linux", "sudo docker exec eriphe-core eriphe-core auth setup-code"},
		{"docker", "linux", "sudo docker exec eriphe-core eriphe-core auth setup-code"},
		{"windows", "linux", "wsl -d eriphe -u root -- docker exec eriphe-core eriphe-core auth setup-code"},
		{"macos", "darwin", "~/.eriphe/bin/eriphe-core auth setup-code -data ~/.eriphe/core"},
		{"macos", "linux", "LIMA_HOME=~/.eriphe/vm ~/.eriphe/lima/bin/limactl shell eriphe sudo docker exec eriphe-core eriphe-core auth setup-code"},
		{"", "linux", "eriphe-core auth setup-code"},
	} {
		if got := setupCodeCommand(c[0], c[1]); got != c[2] {
			t.Errorf("setupCodeCommand(%q, %q) = %q", c[0], c[1], got)
		}
	}
	old := coreInstall
	t.Cleanup(func() { coreInstall = old })
	coreInstall = "windows"
	tw := newTestWeb(t)
	_, body, _ := tw.do("GET", "/setup", nil, nil)
	if !strings.Contains(body, "wsl -d eriphe -u root -- docker exec eriphe-core eriphe-core auth setup-code") {
		t.Fatalf("setup page:\n%s", body)
	}
}
