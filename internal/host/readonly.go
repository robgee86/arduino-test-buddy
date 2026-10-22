// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package host

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
)

// readOnly is the registry as a board reaches it through the tunnel: GET and HEAD pass to the registry, every other method is refused, so a board can pull but never push or delete.
type readOnly struct {
	ln  net.Listener
	srv *http.Server
}

// serveReadOnly listens on a fresh loopback port of the developer machine, for as long as the turn lasts.
func (h *Host) serveReadOnly() (*readOnly, error) {
	target, err := url.Parse(h.url)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) { r.SetURL(target) }}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "the registry is read-only from a board", http.StatusMethodNotAllowed)
			return
		}
		proxy.ServeHTTP(w, r)
	})}
	go func() { _ = srv.Serve(ln) }()
	return &readOnly{ln: ln, srv: srv}, nil
}

// Port is the loopback port the board's tunnel forwards to.
func (r *readOnly) Port() string { return strconv.Itoa(r.ln.Addr().(*net.TCPAddr).Port) }

// Close stops serving; connections in flight are cut, the turn is over.
func (r *readOnly) Close() { _ = r.srv.Close() }
