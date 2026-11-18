package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/stuttgart-things/pvectl/pkg/pve"
)

var tlsFlags struct {
	fingerprint string
	yes         bool
}

var tlsCmd = &cobra.Command{
	Use:   "tls",
	Short: "Show the API certificate and how pvectl trusts it",
	Long: `Show the certificate of the Proxmox API (PVE_API_URL; no login) and how
pvectl trusts it:

  ca         the system store or PVE_CA_CERT (the cluster CA,
             /etc/pve/pve-root-ca.pem on any node) vouches for it
  pinned     its SHA-256 is pinned in the known-hosts file (pvectl tls trust)
  changed    a different certificate is pinned: renewed, or someone in between
  untrusted  neither; 'pvectl tls trust' pins it

Known-hosts file: $PVE_KNOWN_HOSTS, default ~/.config/pvectl/known_hosts.
With either in place no --insecure is needed.`,
	Example: `  sops exec-env ~/.secrets.d/labul/proxmox.env 'pvectl tls'
  pvectl tls -o json | jq -r .fingerprint`,
	Args: maxArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		o, err := tlsOptions()
		if err != nil {
			return err
		}
		sc, err := pve.ProbeCert(cmd.Context(), o)
		if err != nil {
			return err
		}
		if jsonOut() {
			return printJSON(sc)
		}
		fmt.Println(certCard(sc, o.KnownHosts.Path))
		return nil
	},
}

var tlsTrustCmd = &cobra.Command{
	Use:   "trust",
	Short: "Pin the API certificate (like SSH known_hosts), so no --insecure is needed",
	Long: `Pin the SHA-256 fingerprint of the API certificate in the known-hosts file.
Afterwards pvectl accepts exactly this certificate for this host:port, and
stops with an error if it ever changes.

Compare the fingerprint with the web UI first (node > System > Certificates,
pve-ssl.pem). Interactive: shows it and asks. Programmatic: pass the expected
--fingerprint (checked against the server), or --yes to pin whatever the
server shows.

A first interactive run of any command offers this by itself.`,
	Example: `  sops exec-env ~/.secrets.d/labul/proxmox.env 'pvectl tls trust'
  pvectl tls trust --fingerprint AB:CD:...:EF -o json`,
	Args: maxArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		o, err := tlsOptions()
		if err != nil {
			return err
		}
		sc, err := pve.ProbeCert(cmd.Context(), o)
		if err != nil {
			return err
		}
		if want := tlsFlags.fingerprint; want != "" && !sameFingerprint(want, sc.Fingerprint) {
			return &cliError{code: ExitCheckFailed, flag: "--fingerprint",
				err: fmt.Errorf("%s shows SHA-256 %s, not the expected %s: nothing pinned", sc.Host, sc.Fingerprint, want)}
		}
		if sc.Pinned == sc.Fingerprint {
			return reportTrust(sc, o.KnownHosts.Path, false)
		}
		if tlsFlags.fingerprint == "" && !tlsFlags.yes {
			if !interactive() {
				return &cliError{code: ExitUsage, flag: "--fingerprint",
					err: fmt.Errorf("pinning needs --fingerprint %s (checked against the server) or --yes", sc.Fingerprint)}
			}
			if err := askTrust(cmd.Context(), sc, o.KnownHosts.Path); err != nil {
				return err
			}
		}
		if _, err := pve.TrustHost(o.KnownHosts, o.URL, sc.Fingerprint); err != nil {
			return err
		}
		sc.Pinned, sc.Trust = sc.Fingerprint, "pinned"
		return reportTrust(sc, o.KnownHosts.Path, true)
	},
}

// tlsOptions needs only the URL (no credentials).
func tlsOptions() (pve.Options, error) {
	u := os.Getenv(pve.EnvURL)
	if u == "" {
		return pve.Options{}, &pve.MissingEnvError{Vars: []string{pve.EnvURL}}
	}
	k, err := pve.LoadKnownHosts(pve.DefaultKnownHostsPath())
	if err != nil {
		return pve.Options{}, err
	}
	return pve.Options{URL: u, CACert: os.Getenv(pve.EnvCACert), KnownHosts: k}, nil
}

func sameFingerprint(a, b string) bool {
	norm := func(s string) string { return strings.ToUpper(strings.NewReplacer(":", "", " ", "").Replace(s)) }
	return norm(a) == norm(b)
}

func trustBadge(t string) string {
	switch t {
	case "ca":
		return pill("CA VERIFIED", colMint)
	case "pinned":
		return pill("PINNED", colMint)
	case "changed":
		return pill("CHANGED", colRed)
	default:
		return pill("UNTRUSTED", colAmber)
	}
}

// certCard shows a certificate for a human to compare.
func certCard(sc pve.ServerCert, knownHosts string) string {
	kv := func(k, v string) string { return keyStyle.Render(fmt.Sprintf("%-12s", k)) + v }
	names := append(append([]string{}, sc.DNSNames...), sc.IPs...)
	left := time.Until(sc.NotAfter).Hours() / 24
	expiry := sc.NotAfter.UTC().Format("2006-01-02")
	if left < 30 {
		expiry = warnStyle.Render(fmt.Sprintf("%s (%.0f days left)", expiry, left))
	} else {
		expiry += dimStyle.Render(fmt.Sprintf("  (%.0f days left)", left))
	}
	fp := lipgloss.NewStyle().Foreground(colPink).Bold(true).Render(sc.Fingerprint)
	lines := []string{
		trustBadge(sc.Trust) + " " + titleStyle.Render(sc.Host),
		"",
		kv("subject", sc.Subject),
		kv("issuer", dimStyle.Render(sc.Issuer)),
		kv("names", strings.Join(names, ", ")),
		kv("valid until", expiry),
		kv("SHA-256", fp),
	}
	if sc.Pinned != "" && sc.Pinned != sc.Fingerprint {
		lines = append(lines, kv("pinned", errorStyle.Render(sc.Pinned)))
	}
	if knownHosts != "" {
		lines = append(lines, kv("known hosts", dimStyle.Render(knownHosts)))
	}
	if !fancy() {
		return strings.Join(lines, "\n")
	}
	style := cardStyle
	if sc.Trust == "changed" {
		style = style.BorderForeground(colRed)
	} else if sc.Trust == "untrusted" {
		style = warnBoxStyle
	}
	return card(style, strings.Join(lines, "\n"))
}

func askTrust(ctx context.Context, sc pve.ServerCert, knownHosts string) error {
	fmt.Println(certCard(sc, knownHosts))
	if sc.Trust == "changed" {
		fmt.Println(errorStyle.Render("  The pinned certificate differs. Only continue if it was renewed."))
	}
	ok := false
	q := huh.NewConfirm().
		Title("Trust this certificate for " + sc.Host + "?").
		Description("compare the SHA-256 with the web UI: node > System > Certificates").
		Affirmative("Yes, pin it").Negative("No").Value(&ok)
	if err := runForm(ctx, q); err != nil {
		return err
	}
	if !ok {
		return errAborted
	}
	return nil
}

func reportTrust(sc pve.ServerCert, knownHosts string, changed bool) error {
	if jsonOut() {
		return printJSON(map[string]any{"host": sc.Host, "fingerprint": sc.Fingerprint, "pinned": true, "changed": changed, "known_hosts": knownHosts})
	}
	if changed {
		fmt.Printf("%s pinned %s in %s\n", mark(true), sc.Host, knownHosts)
	} else {
		fmt.Printf("%s %s is already pinned with this certificate\n", mark(true), sc.Host)
	}
	return nil
}

// offerTrust runs after a failed command: on an untrusted or changed
// certificate in interactive mode it offers to pin it. true means pinned,
// run the command again.
func offerTrust(ctx context.Context, err error) bool {
	var untrusted *pve.UntrustedCertError
	var changed *pve.CertChangedError
	if !interactive() || (!errors.As(err, &untrusted) && !errors.As(err, &changed)) {
		return false
	}
	o, oerr := tlsOptions()
	if oerr != nil {
		return false
	}
	sc, perr := pve.ProbeCert(ctx, o)
	if perr != nil {
		return false
	}
	fmt.Println(dimStyle.Render("  pvectl does not know this Proxmox certificate yet."))
	if askTrust(ctx, sc, o.KnownHosts.Path) != nil {
		return false
	}
	if _, serr := pve.TrustHost(o.KnownHosts, o.URL, sc.Fingerprint); serr != nil {
		_, _ = fmt.Fprintln(os.Stderr, errorStyle.Render("✗ ")+serr.Error())
		return false
	}
	fmt.Printf("%s pinned %s in %s\n\n", mark(true), sc.Host, o.KnownHosts.Path)
	return true
}

func init() {
	f := tlsTrustCmd.Flags()
	f.StringVar(&tlsFlags.fingerprint, "fingerprint", "", "expected SHA-256 (AB:CD:... or hex); pinned only if the server shows it")
	f.BoolVarP(&tlsFlags.yes, "yes", "y", false, "pin whatever certificate the server shows (no comparison)")
	tlsCmd.AddCommand(tlsTrustCmd)
	rootCmd.AddCommand(tlsCmd)
}
