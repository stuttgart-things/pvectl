package pve_test

import (
	"context"
	"encoding/pem"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stuttgart-things/pvectl/pkg/pve"
	"github.com/stuttgart-things/pvectl/pkg/pve/pvetest"
)

func tlsFake(t *testing.T) *pvetest.Server {
	s := pvetest.New(t)
	s.Data("GET /cluster/resources", []map[string]any{})
	return s
}

func opts(s *pvetest.Server, k *pve.KnownHosts) pve.Options {
	return pve.Options{URL: s.APIURL(), User: "test@pve", Password: s.Password, KnownHosts: k}
}

func resources(t *testing.T, o pve.Options) error {
	t.Helper()
	c, err := pve.New(o)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Resources(context.Background())
	return err
}

func TestTLSUntrustedWithoutCAOrPin(t *testing.T) {
	s := tlsFake(t)
	err := resources(t, opts(s, nil))
	var ue *pve.UntrustedCertError
	if !errors.As(err, &ue) {
		t.Fatalf("want UntrustedCertError, got %v", err)
	}
	if want := pve.Fingerprint(s.Certificate()); ue.Fingerprint != want || !strings.Contains(err.Error(), "pvectl tls trust") {
		t.Fatalf("fingerprint %s (want %s), message %q", ue.Fingerprint, want, err)
	}
}

func TestTLSPinnedAndChanged(t *testing.T) {
	s := tlsFake(t)
	path := filepath.Join(t.TempDir(), "known_hosts")
	k, _ := pve.LoadKnownHosts(path)
	host, err := pve.TrustHost(k, s.APIURL(), pve.Fingerprint(s.Certificate()))
	if err != nil {
		t.Fatal(err)
	}
	// reload from disk: the pin survives
	k2, err := pve.LoadKnownHosts(path)
	if err != nil || k2.Get(host) == "" {
		t.Fatalf("reload: %v %q", err, k2.Get(host))
	}
	if err := resources(t, opts(s, k2)); err != nil {
		t.Fatalf("pinned: %v", err)
	}

	k2.Set(host, strings.Repeat("ab", 32))
	err = resources(t, opts(s, k2))
	var ce *pve.CertChangedError
	if !errors.As(err, &ce) || !strings.Contains(err.Error(), "CHANGED") {
		t.Fatalf("want CertChangedError, got %v", err)
	}
}

func TestTLSCAInlineAndFile(t *testing.T) {
	s := tlsFake(t)
	// httptest's certificate is self-signed, so it is its own CA
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}))
	o := opts(s, nil)
	o.CACert = caPEM
	if err := resources(t, o); err != nil {
		t.Fatalf("inline CA: %v", err)
	}
	sc, err := pve.ProbeCert(context.Background(), o)
	if err != nil || sc.Trust != "ca" {
		t.Fatalf("probe with CA: %v %+v", err, sc)
	}
}

func TestProbeCertStates(t *testing.T) {
	s := tlsFake(t)
	k, _ := pve.LoadKnownHosts(filepath.Join(t.TempDir(), "kh"))
	sc, err := pve.ProbeCert(context.Background(), opts(s, k))
	if err != nil || sc.Trust != "untrusted" || sc.Fingerprint == "" {
		t.Fatalf("untrusted: %v %+v", err, sc)
	}
	if _, err := pve.TrustHost(k, s.APIURL(), strings.ToLower(strings.ReplaceAll(sc.Fingerprint, ":", ""))); err != nil {
		t.Fatal(err)
	}
	sc, _ = pve.ProbeCert(context.Background(), opts(s, k))
	if sc.Trust != "pinned" {
		t.Fatalf("after trust (hex input): %+v", sc)
	}
}
