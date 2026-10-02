package main

import "testing"

func TestRefuseInvalidListener(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8476", "localhost:8476", "192.0.2.1:8476", "bad"} {
		if e := run("", "", address); e == nil {
			t.Fatalf("accepted listener %q", address)
		}
	}
}
