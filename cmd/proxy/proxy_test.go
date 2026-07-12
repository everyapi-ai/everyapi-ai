package proxy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyStatusFailsWhenNotRunning(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	if err := proxyStatus([]string{"--listen", addr}); err == nil {
		t.Fatal("proxyStatus returned success when no sanitizer was listening")
	}
}

func TestProxyStatusFailsForWrongServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not a sanitizer", http.StatusNotFound)
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	if err := proxyStatus([]string{"--listen", addr}); err == nil {
		t.Fatal("proxyStatus returned success for a foreign HTTP server")
	}
}
