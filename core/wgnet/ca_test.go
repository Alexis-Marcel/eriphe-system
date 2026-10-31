package wgnet

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCA(t *testing.T) {
	dir := t.TempDir()
	network := netip.MustParsePrefix("10.9.8.0/24")
	ca, err := OpenCA(dir, network, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dir, caKeyFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v, %v", fi.Mode(), err)
	}
	again, err := OpenCA(dir, network, nil)
	if err != nil || again.Fingerprint() != ca.Fingerprint() {
		t.Fatalf("reopened: %v", err)
	}
	if _, err := OpenCA(dir, netip.MustParsePrefix("10.7.7.0/24"), nil); err == nil {
		t.Fatal("a CA for another network was reused")
	}

	cert, err := ca.GetCertificate(netip.MustParseAddr("10.9.8.1"))(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if leaf := cert.Leaf; leaf.NotAfter.Sub(leaf.NotBefore) > 398*24*time.Hour {
		t.Fatalf("leaf lives %v", leaf.NotAfter.Sub(leaf.NotBefore))
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	for _, name := range []string{"10.9.8.1", Name} {
		if _, err := cert.Leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: name}); err != nil {
			t.Errorf("verify %s: %v", name, err)
		}
	}

	// The name constraints hold: the CA vouches for nothing outside.
	for _, bad := range []struct {
		dns string
		ip  string
	}{{dns: "example.org"}, {ip: "192.0.2.1"}, {dns: "eriphe.internal.example.org"}} {
		tmpl := &x509.Certificate{SerialNumber: cert.Leaf.SerialNumber, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		name := bad.dns
		if bad.dns != "" {
			tmpl.DNSNames = []string{bad.dns}
		} else {
			tmpl.IPAddresses = []net.IP{net.ParseIP(bad.ip)}
			name = bad.ip
		}
		der, err := x509.CreateCertificate(nil, tmpl, ca.Cert, cert.Leaf.PublicKey, ca.key)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := x509.ParseCertificate(der)
		if _, err := c.Verify(x509.VerifyOptions{Roots: roots, DNSName: name}); err == nil {
			t.Errorf("the CA vouched for %s", name)
		}
	}

	mc := string(ca.MobileConfig())
	for _, want := range []string{"com.apple.security.root", "<key>PayloadType</key>\n\t<string>Configuration</string>", ca.uuid("profile")} {
		if !strings.Contains(mc, want) {
			t.Errorf("mobileconfig misses %q", want)
		}
	}
	if string(again.MobileConfig()) != mc {
		t.Error("the profile changes from one download to the next")
	}
}
