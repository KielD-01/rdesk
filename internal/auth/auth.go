// Package auth implements the in-band session authentication.
//
// After the WebRTC connection is up, host and viewer prove knowledge of the
// host password to each other with an HMAC over a host nonce and the DTLS
// certificate fingerprints of both peers (taken from the SDP each side saw).
// The password never travels over the network, and a signaling server that
// swapped the SDP fingerprints (man in the middle) makes both proofs fail.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// Fingerprint returns the first DTLS fingerprint in an SDP, normalised.
func Fingerprint(sdp string) string {
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "a=fingerprint:"); ok {
			return strings.ToLower(strings.Join(strings.Fields(v), " "))
		}
	}
	return ""
}

// Normalize strips the separators people add or drop when typing a code.
func Normalize(pw string) string {
	r := strings.NewReplacer(" ", "", "-", "", "\t", "")
	return r.Replace(strings.TrimSpace(pw))
}

// Proof computes the proof for role ("viewer" or "host").
func Proof(password, role, nonce, offerFP, answerFP string) string {
	key := sha256.Sum256([]byte("rdesk-pw|" + Normalize(password)))
	m := hmac.New(sha256.New, key[:])
	m.Write([]byte("rdesk-v1|" + role + "|" + nonce + "|" + offerFP + "|" + answerFP))
	return hex.EncodeToString(m.Sum(nil))
}

// Equal compares proofs in constant time.
func Equal(a, b string) bool { return hmac.Equal([]byte(a), []byte(b)) }

// Nonce returns a random URL-safe nonce.
func Nonce() string {
	b := make([]byte, 18)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// OneTimeCode returns a random 8-character code shown as "xxxx-xxxx"
// (~40 bits; ambiguous characters removed).
func OneTimeCode() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 8)
	rand.Read(b)
	out := make([]byte, 0, 9)
	for i, c := range b {
		if i == 4 {
			out = append(out, '-')
		}
		out = append(out, alphabet[int(c)%len(alphabet)])
	}
	return string(out)
}
