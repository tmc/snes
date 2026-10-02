package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalHostAuthority(t *testing.T) {
	h := localHostHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tt := range []struct {
		host string
		want int
	}{
		{"127.0.0.1:8460", 204}, {"[::1]:8460", 204}, {"attacker.invalid:8460", 403}, {"localhost:8460", 403}, {"192.0.2.1:8460", 403}, {"127.0.0.1", 403},
	} {
		t.Run(tt.host, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://127.0.0.1:8460/", nil)
			r.Host = tt.host
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.want {
				t.Fatalf("got %d want %d", w.Code, tt.want)
			}
		})
	}
}
