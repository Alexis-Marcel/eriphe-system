package wgnet

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"html"
	"math/big"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CA is the certificate authority a device trusts once, so that the
// workspace reached through WireGuard has a certificate its browser accepts
// (a home-screen web app and its notifications need one).
//
// It is not the machines' CA (machine_tls.go): it signs nothing but the
// workspace's certificate, and its name constraints limit it to the
// network's addresses and to Name, so that a device which trusts it trusts
// it for nothing else, even if its key leaked.
type CA struct {
	Cert *x509.Certificate
	key  *ecdsa.PrivateKey
	now  func() time.Time

	mu   sync.Mutex
	leaf *tls.Certificate
}

const (
	caFile    = "ca.pem"
	caKeyFile = "ca-key.pem"
	// Name is the one host name the CA may vouch for, besides the network's
	// addresses (.internal is reserved for private use).
	Name     = "eriphe.internal"
	leafLife = 397 * 24 * time.Hour
)

// OpenCA loads the CA from dir, or makes it for the network, with its key
// readable by eriphe-core only.
func OpenCA(dir string, network netip.Prefix, now func() time.Time) (*CA, error) {
	if now == nil {
		now = time.Now
	}
	certPEM, err1 := os.ReadFile(filepath.Join(dir, caFile))
	keyPEM, err2 := os.ReadFile(filepath.Join(dir, caKeyFile))
	if err1 == nil && err2 == nil {
		ca, err := parseCA(certPEM, keyPEM)
		if err != nil {
			return nil, err
		}
		if !ca.covers(network) {
			return nil, fmt.Errorf("wgnet: the CA in %s is for another network", dir)
		}
		ca.now = now
		return ca, nil
	}
	if !errors.Is(err1, os.ErrNotExist) && err1 != nil {
		return nil, err1
	}
	ca, certPEM, keyPEM, err := newCA(network, now())
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, caKeyFile), keyPEM, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, caFile), certPEM, 0o644); err != nil {
		return nil, err
	}
	ca.now = now
	return ca, nil
}

func newCA(network netip.Prefix, now time.Time) (*CA, []byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	id := make([]byte, 2)
	rand.Read(id)
	network = network.Masked()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Eriphe " + strings.ToUpper(hex.EncodeToString(id)), Organization: []string{"Eriphe"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		// Only for this network's addresses and one name: nothing on the
		// internet, nothing else of the device's networks.
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{Name},
		PermittedIPRanges:           []*net.IPNet{{IP: network.Addr().AsSlice(), Mask: net.CIDRMask(network.Bits(), network.Addr().BitLen())}},
		ExcludedEmailAddresses:      []string{"invalid"},
		ExcludedURIDomains:          []string{"invalid"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, nil, err
	}
	cert, _ := x509.ParseCertificate(der)
	return &CA{Cert: cert, key: key},
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), nil
}

func parseCA(certPEM, keyPEM []byte) (*CA, error) {
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, errors.New("wgnet: bad CA files")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, err
	}
	return &CA{Cert: cert, key: key}, nil
}

func (ca *CA) covers(network netip.Prefix) bool {
	for _, r := range ca.Cert.PermittedIPRanges {
		if r.Contains(network.Addr().AsSlice()) {
			return true
		}
	}
	return false
}

// GetCertificate gives the workspace's certificate for the server's
// address and Name, made at first use and again near its end.
func (ca *CA) GetCertificate(addr netip.Addr) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		ca.mu.Lock()
		defer ca.mu.Unlock()
		if ca.leaf != nil && ca.now().Add(30*24*time.Hour).Before(ca.leaf.Leaf.NotAfter) {
			return ca.leaf, nil
		}
		leaf, err := ca.issue(addr)
		if err != nil {
			return nil, err
		}
		ca.leaf = leaf
		return leaf, nil
	}
}

func (ca *CA) issue(addr netip.Addr) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	now := ca.now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: Name},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(leafLife), // within what Apple accepts for a server certificate
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{Name},
		IPAddresses:  []net.IP{addr.AsSlice()},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der, ca.Cert.Raw}, PrivateKey: key, Leaf: leaf}, nil
}

// PEM is the CA's certificate in PEM (Android, Linux, Firefox).
func (ca *CA) PEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Cert.Raw})
}

// DER is the CA's certificate in DER, the .crt/.cer Windows opens.
func (ca *CA) DER() []byte { return ca.Cert.Raw }

// Fingerprint is the SHA-256 of the CA's certificate, as the devices show it.
func (ca *CA) Fingerprint() string {
	sum := sha256.Sum256(ca.Cert.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// uuid is a stable UUID-shaped string from the CA and a label: the same
// profile downloaded twice replaces itself instead of piling up.
func (ca *CA) uuid(label string) string {
	sum := sha256.Sum256(append([]byte(label+":"), ca.Cert.Raw...))
	h := strings.ToUpper(hex.EncodeToString(sum[:16]))
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// MobileConfig is an Apple configuration profile (iPhone, iPad, Mac) with
// the CA as its one payload (com.apple.security.root). It is not signed:
// the device says "Not Verified", which is expected for a profile made at
// home. Installing it is not enough on iOS: the CA must then be turned on
// under Settings → General → About → Certificate Trust Settings.
func (ca *CA) MobileConfig() []byte {
	name := html.EscapeString(ca.Cert.Subject.CommonName)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadCertificateFileName</key>
			<string>eriphe-ca.cer</string>
			<key>PayloadContent</key>
			<data>`)
	b.WriteString(base64.StdEncoding.EncodeToString(ca.Cert.Raw))
	fmt.Fprintf(&b, `</data>
			<key>PayloadDescription</key>
			<string>Lets this device trust your Eriphe workspace, reached through WireGuard.</string>
			<key>PayloadDisplayName</key>
			<string>%s</string>
			<key>PayloadIdentifier</key>
			<string>org.eriphe.ca.%s</string>
			<key>PayloadType</key>
			<string>com.apple.security.root</string>
			<key>PayloadUUID</key>
			<string>%s</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
		</dict>
	</array>
	<key>PayloadDescription</key>
	<string>The certificate authority of your Eriphe. It is limited to your Eriphe network and vouches for nothing else.</string>
	<key>PayloadDisplayName</key>
	<string>%s</string>
	<key>PayloadIdentifier</key>
	<string>org.eriphe.profile.%s</string>
	<key>PayloadRemovalDisallowed</key>
	<false/>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>%s</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`, name, ca.uuid("payload"), ca.uuid("payload"), name, ca.uuid("profile"), ca.uuid("profile"))
	return []byte(b.String())
}
