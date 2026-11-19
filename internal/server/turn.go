package server

import (
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/pion/turn/v4"
)

// TURNConfig configures the embedded TURN/STUN relay.
type TURNConfig struct {
	Listen   string // e.g. "0.0.0.0:3478" (UDP and TCP)
	PublicIP string // address relayed candidates advertise
	MinPort  uint16 // relay port range
	MaxPort  uint16
	Secret   string
	Realm    string
	// AllowPrivatePeers lets the relay reach private/loopback addresses
	// (only for local testing: on a server it would expose internal services).
	AllowPrivatePeers bool
}

// publicPeer refuses relaying to loopback, private, link-local, CGNAT and
// other special-purpose ranges.
func publicPeer(_ net.Addr, ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, n := range deniedNets {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

var deniedNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "240.0.0.0/4"} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// StartTURN starts a TURN server (which also answers STUN binding requests)
// that accepts time-limited TURN REST credentials signed with Secret.
func StartTURN(c TURNConfig, log *slog.Logger) (*turn.Server, error) {
	ip := net.ParseIP(c.PublicIP)
	if ip == nil {
		return nil, fmt.Errorf("turn: invalid public IP %q", c.PublicIP)
	}
	udp, err := net.ListenPacket("udp4", c.Listen)
	if err != nil {
		return nil, fmt.Errorf("turn udp: %w", err)
	}
	tcp, err := net.Listen("tcp4", c.Listen)
	if err != nil {
		udp.Close()
		return nil, fmt.Errorf("turn tcp: %w", err)
	}
	gen := func() turn.RelayAddressGenerator {
		return &turn.RelayAddressGeneratorPortRange{
			RelayAddress: ip,
			Address:      "0.0.0.0",
			MinPort:      c.MinPort,
			MaxPort:      c.MaxPort,
		}
	}
	perm := turn.PermissionHandler(publicPeer)
	if c.AllowPrivatePeers {
		perm = nil // pion's default: allow all
	}
	srv, err := turn.NewServer(turn.ServerConfig{
		Realm:             c.Realm,
		AuthHandler:       turn.LongTermTURNRESTAuthHandler(c.Secret, nil),
		PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: udp, RelayAddressGenerator: gen(), PermissionHandler: perm}},
		ListenerConfigs:   []turn.ListenerConfig{{Listener: tcp, RelayAddressGenerator: gen(), PermissionHandler: perm}},
	})
	if err != nil {
		udp.Close()
		tcp.Close()
		return nil, err
	}
	log.Info("turn/stun listening", "addr", c.Listen, "public_ip", c.PublicIP, "relay_ports", fmt.Sprintf("%d-%d", c.MinPort, c.MaxPort))
	return srv, nil
}

func turnCredentials(secret, user string, ttl time.Duration) (string, string, error) {
	return turn.GenerateLongTermTURNRESTCredentials(secret, user, ttl)
}
