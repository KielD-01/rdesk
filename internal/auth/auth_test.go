package auth

import "testing"

// Vectors computed by the browser viewer (web/static/viewer.js), so the Go
// and JS implementations stay in sync.
func TestProofMatchesViewer(t *testing.T) {
	got := Proof(" Abcd-efgh ", "viewer", "nonce1", "sha-256 aa:bb", "sha-256 cc:dd")
	if got != viewerVector {
		t.Fatalf("proof %s, viewer computed %s", got, viewerVector)
	}
	if fp := Fingerprint("v=0\r\na=fingerprint:SHA-256  AA:BB\r\n"); fp != "sha-256 aa:bb" {
		t.Fatalf("fingerprint %q", fp)
	}
}

func TestOneTimeCode(t *testing.T) {
	c := OneTimeCode()
	if len(c) != 9 || c[4] != '-' {
		t.Fatalf("bad code %q", c)
	}
}

const viewerVector = "30fcac2a4322dd1b8f1595af63dd63cdb2b9da21fee3709c98babc6bbf45f782"
