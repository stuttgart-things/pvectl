// Package pve is a small Proxmox VE API layer for the LabUL template work:
// cluster resources, VM configs (without notes), tasks, migrate, clone and
// HA. It wraps github.com/luthermonson/go-proxmox for transport and the
// ticket login (user/password), and keeps the typed results pvectl needs.
//
// Two rules hold for everything in this package:
//
//   - The VM `description` (the notes in the web UI) is never returned. In
//     the LabUL-Template pool it holds plaintext passwords. Config strips it
//     at the source, so no caller can print it by accident.
//   - Credentials come from the caller (Options, usually OptionsFromEnv) and
//     never appear in errors or logs.
package pve

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	proxmox "github.com/luthermonson/go-proxmox"
)

// Environment variables OptionsFromEnv reads.
const (
	EnvURL      = "PVE_API_URL"      // e.g. https://pve.example:8006/api2/json
	EnvUser     = "PVE_API_USER"     // e.g. user@pve
	EnvPassword = "PVE_API_PASSWORD" // never printed
	EnvCACert   = "PVE_CA_CERT"      // optional: the cluster CA, a PEM file path or the PEM itself
)

// Options configure a Client.
type Options struct {
	URL      string
	User     string
	Password string
	// Insecure skips TLS verification. Opt-in only (--insecure).
	Insecure bool
	// CACert verifies the server against this CA instead of the system
	// store (Proxmox signs with its own cluster CA): a PEM file path (~
	// allowed) or the PEM itself.
	CACert string
	// KnownHosts holds pinned certificate fingerprints (pvectl tls trust);
	// a pin is accepted when no CA vouches for the server. nil: no pins.
	KnownHosts *KnownHosts
	// Timeout per HTTP request; 0 means 60s.
	Timeout time.Duration
}

// MissingEnvError lists the unset environment variables.
type MissingEnvError struct{ Vars []string }

func (e *MissingEnvError) Error() string {
	return fmt.Sprintf("missing environment: %s (run through: sops exec-env ~/.secrets.d/labul/proxmox.env 'pvectl ...')",
		strings.Join(e.Vars, ", "))
}

// OptionsFromEnv reads PVE_API_URL, PVE_API_USER, PVE_API_PASSWORD, the
// optional PVE_CA_CERT and the known-hosts file (PVE_KNOWN_HOSTS).
func OptionsFromEnv() (Options, error) {
	o := Options{
		URL:      os.Getenv(EnvURL),
		User:     os.Getenv(EnvUser),
		Password: os.Getenv(EnvPassword),
		CACert:   os.Getenv(EnvCACert),
	}
	var missing []string
	for name, v := range map[string]string{EnvURL: o.URL, EnvUser: o.User, EnvPassword: o.Password} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return o, &MissingEnvError{Vars: missing}
	}
	k, err := LoadKnownHosts(DefaultKnownHostsPath())
	if err != nil {
		return o, fmt.Errorf("known hosts: %w", err)
	}
	o.KnownHosts = k
	return o, nil
}

// Client talks to one Proxmox VE cluster.
type Client struct {
	api  *proxmox.Client
	base string
}

// quietLogger drops go-proxmox's log output: its debug level prints
// response bodies (VM configs with notes), and anything on stdout would
// break -o json.
type quietLogger struct{}

func (quietLogger) Debugf(string, ...interface{}) {}
func (quietLogger) Infof(string, ...interface{})  {}
func (quietLogger) Warnf(string, ...interface{})  {}
func (quietLogger) Errorf(string, ...interface{}) {}

// New builds a client. It does not contact the server; the ticket login
// happens on the first request.
func New(o Options) (*Client, error) {
	if o.URL == "" {
		return nil, errors.New("no Proxmox API URL")
	}
	base := strings.TrimRight(o.URL, "/")
	if !strings.HasSuffix(base, "/api2/json") {
		base += "/api2/json"
	}
	tlsCfg, err := tlsConfig(o)
	if err != nil {
		return nil, err
	}
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	hc := &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: tlsCfg, Proxy: http.ProxyFromEnvironment},
	}
	api := proxmox.NewClient(base,
		proxmox.WithHTTPClient(hc),
		proxmox.WithCredentials(&proxmox.Credentials{Username: o.User, Password: o.Password}),
		proxmox.WithLogger(quietLogger{}),
		proxmox.WithUserAgent("pvectl"),
	)
	return &Client{api: api, base: base}, nil
}

// Sentinel errors callers map to exit codes.
var (
	// ErrNotFound: the VM, node, task or resource does not exist.
	ErrNotFound = errors.New("not found")
	// ErrTaskFailed: a Proxmox task ended with an exit status other than OK.
	ErrTaskFailed = errors.New("task failed")
)

// wrap turns go-proxmox errors into errors that name the request, carry
// ErrNotFound where PVE says so, and explain TLS failures.
func (c *Client) wrap(method, path string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *proxmox.APIError
	if errors.As(err, &apiErr) {
		msg := strings.TrimSpace(apiErr.Message)
		if msg == "" {
			msg = apiErr.Status
		}
		for k, v := range apiErr.Errors {
			msg += fmt.Sprintf(" [%s: %s]", k, strings.TrimSpace(v))
		}
		lower := strings.ToLower(msg)
		if apiErr.StatusCode == http.StatusNotFound || strings.Contains(lower, "does not exist") ||
			strings.Contains(lower, "no such") {
			return fmt.Errorf("%s %s: %s: %w", method, path, msg, ErrNotFound)
		}
		if path == "/access/ticket" || apiErr.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("%s %s: %d %s (check %s/%s)", method, path, apiErr.StatusCode, msg, EnvUser, EnvPassword)
		}
		return fmt.Errorf("%s %s: %d %s", method, path, apiErr.StatusCode, msg)
	}
	var untrusted *UntrustedCertError
	var changed *CertChangedError
	if errors.As(err, &untrusted) {
		return untrusted
	}
	if errors.As(err, &changed) {
		return changed
	}
	var certErr *tls.CertificateVerificationError
	var unknownCA x509.UnknownAuthorityError
	if errors.As(err, &certErr) || errors.As(err, &unknownCA) {
		return fmt.Errorf("TLS: %v at %s. Proxmox signs with its own cluster CA (/etc/pve/pve-root-ca.pem on any node): "+
			"point %s at a copy, or pin the certificate with 'pvectl tls trust'", err, c.base, EnvCACert)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s %s: %v", method, path, urlErr.Err)
	}
	return fmt.Errorf("%s %s: %w", method, path, err)
}

func (c *Client) get(ctx context.Context, path string, v any) error {
	return c.wrap(http.MethodGet, path, c.api.Get(ctx, path, v))
}

func (c *Client) post(ctx context.Context, path string, data map[string]any, v any) error {
	if data == nil {
		data = map[string]any{}
	}
	return c.wrap(http.MethodPost, path, c.api.Post(ctx, path, data, v))
}

func (c *Client) put(ctx context.Context, path string, data map[string]any, v any) error {
	return c.wrap(http.MethodPut, path, c.api.Put(ctx, path, data, v))
}

// Ping logs in and reads the API version; a cheap credential check.
func (c *Client) Ping(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
		Release string `json:"release"`
	}
	if err := c.get(ctx, "/version", &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// BaseURL is the API base the client talks to (no credentials in it).
func (c *Client) BaseURL() string { return c.base }
