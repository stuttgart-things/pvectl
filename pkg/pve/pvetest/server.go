// Package pvetest is a fake Proxmox VE API for tests: ticket login or API
// token, plus canned responses per "METHOD /path" (path without /api2/json
// and query).
package pvetest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Ticket is the fake session ticket.
const Ticket = "PVE:test@pve:FAKE"

// TokenID and TokenSecret are the API token the fake accepts.
const (
	TokenID     = "test@pve!ci"
	TokenSecret = "00000000-0000-0000-0000-000000000000"
)

// Call is a recorded request.
type Call struct {
	Method string
	Path   string
	Query  string
	Body   map[string]any
}

// Handler answers a request: the data to wrap in {"data": …}, or an
// HTTP status >= 400 with a message.
type Handler func(c Call) (data any, status int, msg string)

// Server is a running fake.
type Server struct {
	*httptest.Server
	mu     sync.Mutex
	routes map[string]Handler
	calls  []Call
	// Password the login accepts.
	Password string
}

// New starts a fake. Close it with t.Cleanup (done for you).
func New(t testing.TB) *Server {
	s := &Server{routes: map[string]Handler{}, Password: "secret"}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// URL is the API base for pve.Options.URL.
func (s *Server) APIURL() string { return s.URL + "/api2/json" }

// Data registers a fixed answer for "METHOD /path".
func (s *Server) Data(route string, data any) {
	s.Handle(route, func(Call) (any, int, string) { return data, 200, "" })
}

// Handle registers a handler for "METHOD /path".
func (s *Server) Handle(route string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[route] = h
}

// Calls returns the recorded requests (login excluded).
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Writes returns the recorded non-GET requests.
func (s *Server) Writes() []Call {
	var out []Call
	for _, c := range s.Calls() {
		if c.Method != http.MethodGet {
			out = append(out, c)
		}
	}
	return out
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/api2/json")
	body := map[string]any{}
	if b, _ := io.ReadAll(r.Body); len(b) > 0 {
		_ = json.Unmarshal(b, &body)
	}
	if path == "/access/ticket" && r.Method == http.MethodPost {
		if body["password"] != s.Password {
			http.Error(w, "authentication failure", http.StatusUnauthorized)
			return
		}
		writeData(w, map[string]any{"ticket": Ticket, "CSRFPreventionToken": "csrf", "username": body["username"]})
		return
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		// API token: no ticket, no CSRF token
		if auth != "PVEAPIToken="+TokenID+"="+TokenSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	} else {
		cookie, err := r.Cookie("PVEAuthCookie")
		if err != nil || cookie.Value != Ticket {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("CSRFPreventionToken") != "csrf" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	call := Call{Method: r.Method, Path: path, Query: r.URL.RawQuery, Body: body}
	s.mu.Lock()
	s.calls = append(s.calls, call)
	h, ok := s.routes[r.Method+" "+path]
	s.mu.Unlock()
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = io.WriteString(w, `{"data":null,"message":"no fake route `+r.Method+" "+path+`"}`)
		return
	}
	data, status, msg := h(call)
	if status >= 400 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		b, _ := json.Marshal(map[string]any{"data": nil, "message": msg})
		_, _ = w.Write(b)
		return
	}
	writeData(w, data)
}

func writeData(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}
