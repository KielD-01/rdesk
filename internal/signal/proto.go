// Package signal defines the JSON messages exchanged over the signaling
// WebSocket between the server, hosts and viewers.
package signal

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// Message types.
const (
	TRegister   = "register"   // host -> server: claim the ID derived from Token
	TRegistered = "registered" // server -> host: ID + ICE servers
	TConnect    = "connect"    // viewer -> server: connect to host ID
	TSession    = "session"    // server -> host: a viewer wants a session (Sid)
	TReady      = "ready"      // server -> viewer: host notified, ICE servers attached
	TSignal     = "signal"     // both ways: SDP / ICE candidate, routed by Sid
	TBye        = "bye"        // a side of session Sid went away
	TError      = "error"      // server -> client
)

// Msg is the envelope for every signaling message.
type Msg struct {
	T     string          `json:"t"`
	Token string          `json:"token,omitempty"`
	ID    string          `json:"id,omitempty"`
	Sid   string          `json:"sid,omitempty"`
	ICE   []ICEServer     `json:"ice,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// ICEServer mirrors RTCIceServer.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// Signal is the payload of a TSignal message.
type Signal struct {
	SDP       *SDP       `json:"sdp,omitempty"`
	Candidate *Candidate `json:"candidate,omitempty"`
}

type SDP struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

type Candidate struct {
	Candidate     string  `json:"candidate"`
	SDPMid        *string `json:"sdpMid,omitempty"`
	SDPMLineIndex *uint16 `json:"sdpMLineIndex,omitempty"`
}

// IDFromToken derives the public 9-digit host ID from the host's secret token.
// Only the holder of the token can register under that ID.
func IDFromToken(token string) string {
	sum := sha256.Sum256([]byte("rdesk-id|" + token))
	n := binary.BigEndian.Uint64(sum[:8]) % 900_000_000
	return fmt.Sprintf("%d", n+100_000_000)
}
