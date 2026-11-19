package pve

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TLS trust, in this order:
//
//  1. the system store or PVE_CA_CERT (the cluster CA, /etc/pve/pve-root-ca.pem):
//     survives certificate renewals, the best option;
//  2. a pinned fingerprint in the known-hosts file, like SSH: `pvectl tls
//     trust` stores the SHA-256 of the node certificate once;
//  3. --insecure: no check at all, for one run.

// EnvKnownHosts overrides the known-hosts file (default
// <user config dir>/pvectl/known_hosts).
const EnvKnownHosts = "PVE_KNOWN_HOSTS"

// UntrustedCertError: neither a CA nor a pinned fingerprint vouches for the
// server certificate.
type UntrustedCertError struct {
	Host        string
	Fingerprint string
	Reason      error
}

func (e *UntrustedCertError) Error() string {
	return fmt.Sprintf("TLS: the certificate of %s is not trusted (%v; SHA-256 %s). "+
		"Run 'pvectl tls trust' once to pin it (compare the fingerprint with the web UI: node > System > Certificates), "+
		"or set %s to the cluster CA (/etc/pve/pve-root-ca.pem)", e.Host, e.Reason, e.Fingerprint, EnvCACert)
}

func (e *UntrustedCertError) Unwrap() error { return e.Reason }

// CertChangedError: the server shows a different certificate than the one
// pinned for it. Either it was renewed, or someone is in between.
type CertChangedError struct {
	Host     string
	Pinned   string
	Got      string
	HostFile string
}

func (e *CertChangedError) Error() string {
	return fmt.Sprintf("TLS: the certificate of %s CHANGED: pinned SHA-256 %s, now %s. "+
		"If it was renewed (check the web UI: node > System > Certificates), run 'pvectl tls trust' again; "+
		"otherwise do not continue (%s)", e.Host, e.Pinned, e.Got, e.HostFile)
}

// Fingerprint is the SHA-256 of a certificate in the format the Proxmox
// web UI shows (AB:CD:...).
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// normalizeFingerprint accepts AB:CD:.., abcd.. and "sha256//"-less hex.
func normalizeFingerprint(s string) string {
	s = strings.ToUpper(strings.NewReplacer(":", "", " ", "").Replace(strings.TrimSpace(s)))
	if len(s) != 64 {
		return s
	}
	parts := make([]string, 32)
	for i := range parts {
		parts[i] = s[2*i : 2*i+2]
	}
	return strings.Join(parts, ":")
}

// KnownHosts maps host:port to a pinned certificate fingerprint.
type KnownHosts struct {
	Path    string
	entries map[string]string
}

// DefaultKnownHostsPath is $PVE_KNOWN_HOSTS or <user config dir>/pvectl/known_hosts.
func DefaultKnownHostsPath() string {
	if p := os.Getenv(EnvKnownHosts); p != "" {
		return expandHome(p)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "pvectl", "known_hosts")
}

// LoadKnownHosts reads path; a missing file is an empty list.
// Format: one "host:port FINGERPRINT" per line, # comments.
func LoadKnownHosts(path string) (*KnownHosts, error) {
	k := &KnownHosts{Path: path, entries: map[string]string{}}
	if path == "" {
		return k, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return k, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			k.entries[fields[0]] = normalizeFingerprint(fields[1])
		}
	}
	return k, sc.Err()
}

// Get returns the pinned fingerprint for host:port, or "".
func (k *KnownHosts) Get(hostport string) string {
	if k == nil {
		return ""
	}
	return k.entries[hostport]
}

// Set pins fp for host:port (in memory; Save writes it).
func (k *KnownHosts) Set(hostport, fp string) { k.entries[hostport] = normalizeFingerprint(fp) }

// Save writes the file (0600, directory 0700).
func (k *KnownHosts) Save() error {
	if k.Path == "" {
		return errors.New("no known-hosts path (set " + EnvKnownHosts + ")")
	}
	if err := os.MkdirAll(filepath.Dir(k.Path), 0o700); err != nil {
		return err
	}
	hosts := make([]string, 0, len(k.entries))
	for h := range k.entries {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	var b strings.Builder
	b.WriteString("# pvectl: pinned Proxmox certificates (pvectl tls trust)\n")
	for _, h := range hosts {
		fmt.Fprintf(&b, "%s %s\n", h, k.entries[h])
	}
	tmp := k.Path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, k.Path)
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// caPool reads PVE_CA_CERT: a PEM file path (~ allowed) or the PEM itself.
// Empty means the system store (nil pool).
func caPool(v string) (*x509.CertPool, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	pem := []byte(v)
	if !strings.HasPrefix(v, "-----BEGIN") {
		b, err := os.ReadFile(expandHome(v))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", EnvCACert, err)
		}
		pem = b
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s holds no PEM certificate", EnvCACert)
	}
	return pool, nil
}

// hostPort is the host:port of an API URL (port 8006 if none).
func hostPort(apiURL string) (string, error) {
	u, err := url.Parse(apiURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%s is not a URL: %q", EnvURL, apiURL)
	}
	if u.Port() == "" {
		return net.JoinHostPort(u.Hostname(), "8006"), nil
	}
	return u.Host, nil
}

// tlsConfig builds the client TLS config: CA first, then the pin.
func tlsConfig(o Options) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if o.Insecure {
		cfg.InsecureSkipVerify = true //nolint:gosec // opt-in via --insecure
		return cfg, nil
	}
	roots, err := caPool(o.CACert)
	if err != nil {
		return nil, err
	}
	hp, err := hostPort(o.URL)
	if err != nil {
		return nil, err
	}
	known := o.KnownHosts
	// The standard check runs in VerifyConnection, so that a pin can stand
	// in for the CA; nothing is accepted that passes neither.
	cfg.InsecureSkipVerify = true //nolint:gosec // verified in VerifyConnection
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		return verifyPeer(cs, roots, hp, known)
	}
	return cfg, nil
}

func verifyPeer(cs tls.ConnectionState, roots *x509.CertPool, hp string, known *KnownHosts) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("TLS: the server sent no certificate")
	}
	leaf := cs.PeerCertificates[0]
	inter := x509.NewCertPool()
	for _, c := range cs.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	host, _, _ := net.SplitHostPort(hp)
	_, caErr := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, DNSName: host})
	if caErr == nil {
		return nil
	}
	fp := Fingerprint(leaf)
	pinned := known.Get(hp)
	switch {
	case pinned == fp:
		return nil
	case pinned != "":
		path := ""
		if known != nil {
			path = known.Path
		}
		return &CertChangedError{Host: hp, Pinned: pinned, Got: fp, HostFile: path}
	default:
		return &UntrustedCertError{Host: hp, Fingerprint: fp, Reason: caErr}
	}
}

// ServerCert describes the certificate a Proxmox API presents.
type ServerCert struct {
	Host        string    `json:"host"` // host:port
	Subject     string    `json:"subject"`
	Issuer      string    `json:"issuer"`
	DNSNames    []string  `json:"dns_names,omitempty"`
	IPs         []string  `json:"ips,omitempty"`
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
	Fingerprint string    `json:"fingerprint"` // SHA-256, AB:CD:...
	// Trust: "ca" (system store or PVE_CA_CERT), "pinned" (known_hosts),
	// "changed" (pinned, but a different certificate) or "untrusted".
	Trust  string `json:"trust"`
	Pinned string `json:"pinned,omitempty"` // the pinned fingerprint, if any
}

// ProbeCert connects to the API host (TLS only, no login) and reports its
// certificate and how it is trusted under o.
func ProbeCert(ctx context.Context, o Options) (ServerCert, error) {
	hp, err := hostPort(o.URL)
	if err != nil {
		return ServerCert{}, err
	}
	roots, err := caPool(o.CACert)
	if err != nil {
		return ServerCert{}, err
	}
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 15 * time.Second}, Config: &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, //nolint:gosec // we only read the certificate here
	}}
	conn, err := d.DialContext(ctx, "tcp", hp)
	if err != nil {
		return ServerCert{}, fmt.Errorf("connect %s: %w", hp, err)
	}
	cs := conn.(*tls.Conn).ConnectionState()
	_ = conn.Close()
	if len(cs.PeerCertificates) == 0 {
		return ServerCert{}, errors.New("TLS: the server sent no certificate")
	}
	leaf := cs.PeerCertificates[0]
	sc := ServerCert{
		Host: hp, Subject: leaf.Subject.String(), Issuer: leaf.Issuer.String(),
		DNSNames: leaf.DNSNames, NotBefore: leaf.NotBefore, NotAfter: leaf.NotAfter,
		Fingerprint: Fingerprint(leaf), Pinned: o.KnownHosts.Get(hp),
	}
	for _, ip := range leaf.IPAddresses {
		sc.IPs = append(sc.IPs, ip.String())
	}
	switch err := verifyPeer(cs, roots, hp, o.KnownHosts); {
	case err == nil && sc.Pinned == sc.Fingerprint:
		sc.Trust = "pinned"
		// a CA may vouch as well; report the stronger one
		if verifyPeer(cs, roots, hp, nil) == nil {
			sc.Trust = "ca"
		}
	case err == nil:
		sc.Trust = "ca"
	case errors.As(err, new(*CertChangedError)):
		sc.Trust = "changed"
	default:
		sc.Trust = "untrusted"
	}
	return sc, nil
}

// TrustHost pins fp for the host of apiURL in k and saves it.
func TrustHost(k *KnownHosts, apiURL, fp string) (string, error) {
	hp, err := hostPort(apiURL)
	if err != nil {
		return "", err
	}
	k.Set(hp, fp)
	return hp, k.Save()
}

// HostOf is the host:port pvectl pins for an API URL.
func HostOf(apiURL string) (string, error) { return hostPort(apiURL) }
