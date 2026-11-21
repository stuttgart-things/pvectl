package pve_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stuttgart-things/pvectl/pkg/pve"
	"github.com/stuttgart-things/pvectl/pkg/pve/pvetest"
)

func tokenClient(t *testing.T, s *pvetest.Server, secret string) *pve.Client {
	t.Helper()
	c, err := pve.New(pve.Options{URL: s.APIURL(), TokenID: pvetest.TokenID, TokenSecret: secret, Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAPITokenReadAndWrite(t *testing.T) {
	s := pvetest.New(t)
	s.Data("GET /version", map[string]any{"version": "9.1.1"})
	s.Data("POST /nodes/ul-pve01/qemu/280/status/start", "UPID:ul-pve01:1:2:3:qmstart:280:test@pve!ci:")
	c := tokenClient(t, s, pvetest.TokenSecret)
	if v, err := c.Ping(context.Background()); err != nil || v != "9.1.1" {
		t.Fatalf("ping: %q %v", v, err)
	}
	// writes need no CSRF token with an API token
	if _, err := c.Start(context.Background(), "ul-pve01", 280); err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, call := range s.Calls() {
		if call.Path == "/access/ticket" {
			t.Fatal("token client asked for a ticket")
		}
	}
}

func TestAPITokenWrongSecretNamesTokenVars(t *testing.T) {
	s := pvetest.New(t)
	s.Data("GET /version", map[string]any{"version": "9.1.1"})
	wrong := "11111111-2222-3333-4444-555555555555"
	_, err := tokenClient(t, s, wrong).Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), pve.EnvTokenID) || strings.Contains(err.Error(), wrong) {
		t.Fatalf("got %v", err)
	}
}

func TestOptionsFromEnvToken(t *testing.T) {
	t.Setenv(pve.EnvURL, "https://pve.example:8006/api2/json")
	t.Setenv(pve.EnvUser, "")
	t.Setenv(pve.EnvPassword, "")
	t.Setenv(pve.EnvKnownHosts, t.TempDir()+"/kh")
	t.Setenv(pve.EnvTokenID, pvetest.TokenID)
	t.Setenv(pve.EnvTokenSecret, "")
	_, err := pve.OptionsFromEnv()
	var me *pve.MissingEnvError
	if !errors.As(err, &me) || len(me.Vars) != 1 || me.Vars[0] != pve.EnvTokenSecret {
		t.Fatalf("want missing %s, got %v", pve.EnvTokenSecret, err)
	}
	t.Setenv(pve.EnvTokenSecret, pvetest.TokenSecret)
	o, err := pve.OptionsFromEnv()
	if err != nil || o.TokenID != pvetest.TokenID {
		t.Fatalf("%v %+v", err, o.TokenID)
	}
}
