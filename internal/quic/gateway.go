package quic

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/rules"
)

type Gateway struct {
	cfg      config.Config
	rules    rules.Normalized
	listener *net.UDPConn
	mu       sync.RWMutex
	sessions map[string]*session
}

type session struct {
	clientAddr   *net.UDPAddr
	backendConn  packetConn
	lastActivity time.Time
	mu           sync.Mutex
}

type packetConn interface {
	Write([]byte) (int, error)
	Read([]byte) (int, error)
	Close() error
}

func Run(ctx context.Context, cfg config.Config, norm rules.Normalized) error {
	addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(cfg.Network.QUICRedirectPort))
	udpAddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	log.Printf("quic gateway listening on %s rules=%d fallback_exit=%s", conn.LocalAddr(), len(norm.GatewayRules()), cfg.Routing.FallbackExit)
	gw := &Gateway{cfg: cfg, rules: norm, listener: conn, sessions: map[string]*session{}}
	go gw.gc(ctx)
	return gw.serve(ctx)
}

func (g *Gateway) serve(ctx context.Context) error {
	buf := make([]byte, 65535)
	for {
		if err := g.listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			return err
		}
		n, addr, err := g.listener.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				continue
			}
			return err
		}
		if g.forwardExisting(addr, buf[:n]) {
			continue
		}
		packet := append([]byte(nil), buf[:n]...)
		go g.newSession(packet, addr)
	}
}

func (g *Gateway) forwardExisting(addr *net.UDPAddr, data []byte) bool {
	key := addr.String()
	g.mu.RLock()
	sess, ok := g.sessions[key]
	g.mu.RUnlock()
	if !ok {
		return false
	}
	sess.mu.Lock()
	sess.lastActivity = time.Now()
	conn := sess.backendConn
	sess.mu.Unlock()
	if _, err := conn.Write(data); err != nil {
		log.Printf("quic session %s backend write failed: %v", key, err)
	}
	return true
}

func (g *Gateway) newSession(data []byte, addr *net.UDPAddr) {
	sni, ok := ExtractSNI(data)
	if !ok || sni == "" {
		log.Printf("quic %s rejected: missing SNI or unsupported QUIC Initial", addr)
		return
	}
	exit, matched, err := selectExit(g.cfg, g.rules, sni)
	if err != nil {
		log.Printf("quic %s rejected: %v", addr, err)
		return
	}
	if exit.Type == "shadowsocks-rust" && !exit.UDPEnabled() {
		log.Printf("quic %s sni=%q rejected: exit %q has udp=false", addr, sni, exit.Name)
		return
	}
	backend, target, err := connectBackend(sni, exit)
	if err != nil {
		log.Printf("quic %s sni=%q exit=%q rejected: %v", addr, sni, exit.Name, err)
		return
	}
	log.Printf("quic %s sni=%q matched=%t exit=%q backend=%s", addr, sni, matched, exit.Name, target)
	sess := &session{clientAddr: addr, backendConn: backend, lastActivity: time.Now()}
	key := addr.String()
	g.mu.Lock()
	if old, exists := g.sessions[key]; exists {
		g.mu.Unlock()
		backend.Close()
		if _, err := old.backendConn.Write(data); err != nil {
			log.Printf("quic %s duplicate session backend write failed: %v", key, err)
		}
		return
	}
	g.sessions[key] = sess
	g.mu.Unlock()
	if _, err := backend.Write(data); err != nil {
		log.Printf("quic %s first write failed: %v", key, err)
		g.remove(key)
		return
	}
	go g.relay(key, sess)
}

func selectExit(cfg config.Config, norm rules.Normalized, sni string) (config.ExitConfig, bool, error) {
	if rule, ok := norm.MatchGatewayDomain(sni); ok {
		exit, exists := cfg.ExitByName(rule.Exit)
		if !exists {
			return config.ExitConfig{}, false, fmt.Errorf("unknown exit %q", rule.Exit)
		}
		return exit, true, nil
	}
	exit, exists := cfg.ExitByName(cfg.Routing.FallbackExit)
	if !exists {
		return config.ExitConfig{}, false, fmt.Errorf("unknown fallback exit %q", cfg.Routing.FallbackExit)
	}
	return exit, false, nil
}

func connectBackend(host string, exit config.ExitConfig) (packetConn, string, error) {
	switch exit.Type {
	case "direct":
		return resolveDirectUDP(host, exit.FWMark)
	case "shadowsocks-rust":
		proxyAddr := net.JoinHostPort(exit.ListenAddress, strconv.Itoa(exit.ListenPort))
		conn, err := dialSocksUDP(proxyAddr, host, 443)
		return conn, proxyAddr + " -> " + net.JoinHostPort(host, "443"), err
	default:
		return nil, "", fmt.Errorf("unsupported exit type %q", exit.Type)
	}
}

func resolveDirectUDP(host string, mark int) (*net.UDPConn, string, error) {
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, "", err
	}
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			target := &net.UDPAddr{IP: ip4, Port: 443}
			conn, err := net.DialUDP("udp", nil, target)
			if err != nil {
				return nil, "", err
			}
			if mark != 0 {
				if err := setMark(conn, mark); err != nil {
					conn.Close()
					return nil, "", err
				}
			}
			return conn, target.String(), nil
		}
	}
	return nil, "", fmt.Errorf("%s resolved without IPv4 address", host)
}

type socksUDPConn struct {
	tcp  net.Conn
	udp  *net.UDPConn
	host string
	port int
}

func dialSocksUDP(proxyAddr, host string, port int) (*socksUDPConn, error) {
	tcp, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if err := tcp.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		tcp.Close()
		return nil, err
	}
	if _, err := tcp.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		tcp.Close()
		return nil, err
	}
	var method [2]byte
	if _, err := io.ReadFull(tcp, method[:]); err != nil {
		tcp.Close()
		return nil, err
	}
	if method[0] != 0x05 || method[1] != 0x00 {
		tcp.Close()
		return nil, fmt.Errorf("SOCKS5 proxy rejected no-auth method")
	}
	if _, err := tcp.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		tcp.Close()
		return nil, err
	}
	relayHost, relayPort, err := readSocksAddr(tcp)
	if err != nil {
		tcp.Close()
		return nil, err
	}
	if relayHost == "0.0.0.0" || relayHost == "::" {
		host, _, splitErr := net.SplitHostPort(tcp.RemoteAddr().String())
		if splitErr != nil {
			tcp.Close()
			return nil, splitErr
		}
		relayHost = host
	}
	relay, err := net.ResolveUDPAddr("udp", net.JoinHostPort(relayHost, strconv.Itoa(relayPort)))
	if err != nil {
		tcp.Close()
		return nil, err
	}
	udp, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		tcp.Close()
		return nil, err
	}
	if err := tcp.SetDeadline(time.Time{}); err != nil {
		udp.Close()
		tcp.Close()
		return nil, err
	}
	return &socksUDPConn{tcp: tcp, udp: udp, host: host, port: port}, nil
}

func readSocksAddr(r io.Reader) (string, int, error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return "", 0, err
	}
	if head[0] != 0x05 {
		return "", 0, fmt.Errorf("invalid SOCKS version %d", head[0])
	}
	if head[1] != 0x00 {
		return "", 0, fmt.Errorf("SOCKS5 UDP associate failed with rep=%d", head[1])
	}
	var host string
	switch head[3] {
	case 0x01:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", 0, err
		}
		host = net.IP(buf).String()
	case 0x03:
		var size [1]byte
		if _, err := io.ReadFull(r, size[:]); err != nil {
			return "", 0, err
		}
		buf := make([]byte, int(size[0]))
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", 0, err
		}
		host = string(buf)
	case 0x04:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", 0, err
		}
		host = net.IP(buf).String()
	default:
		return "", 0, fmt.Errorf("unsupported SOCKS address type %d", head[3])
	}
	var port [2]byte
	if _, err := io.ReadFull(r, port[:]); err != nil {
		return "", 0, err
	}
	return host, int(binary.BigEndian.Uint16(port[:])), nil
}

func (s *socksUDPConn) Write(data []byte) (int, error) {
	packet, err := socksUDPRequest(s.host, s.port, data)
	if err != nil {
		return 0, err
	}
	if _, err := s.udp.Write(packet); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (s *socksUDPConn) Read(buf []byte) (int, error) {
	packet := make([]byte, len(buf)+512)
	n, err := s.udp.Read(packet)
	if err != nil {
		return 0, err
	}
	payload, err := socksUDPPayload(packet[:n])
	if err != nil {
		return 0, err
	}
	if len(payload) > len(buf) {
		return 0, fmt.Errorf("SOCKS5 UDP payload too large: %d", len(payload))
	}
	copy(buf, payload)
	return len(payload), nil
}

func (s *socksUDPConn) Close() error {
	udpErr := s.udp.Close()
	tcpErr := s.tcp.Close()
	if udpErr != nil {
		return udpErr
	}
	return tcpErr
}

func socksUDPRequest(host string, port int, data []byte) ([]byte, error) {
	out := []byte{0, 0, 0}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			out = append(out, 0x01)
			out = append(out, ip4...)
		} else {
			out = append(out, 0x04)
			out = append(out, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return nil, fmt.Errorf("SOCKS5 domain too long: %s", host)
		}
		out = append(out, 0x03, byte(len(host)))
		out = append(out, host...)
	}
	out = binary.BigEndian.AppendUint16(out, uint16(port))
	return append(out, data...), nil
}

func socksUDPPayload(packet []byte) ([]byte, error) {
	if len(packet) < 4 {
		return nil, fmt.Errorf("short SOCKS5 UDP packet")
	}
	if packet[2] != 0 {
		return nil, fmt.Errorf("fragmented SOCKS5 UDP packet is unsupported")
	}
	off := 4
	switch packet[3] {
	case 0x01:
		off += 4
	case 0x03:
		if len(packet) < off+1 {
			return nil, fmt.Errorf("short SOCKS5 domain packet")
		}
		off += 1 + int(packet[off])
	case 0x04:
		off += 16
	default:
		return nil, fmt.Errorf("unsupported SOCKS5 UDP address type %d", packet[3])
	}
	if len(packet) < off+2 {
		return nil, fmt.Errorf("short SOCKS5 UDP port")
	}
	return packet[off+2:], nil
}

func setMark(conn *net.UDPConn, mark int) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var setErr error
	err = raw.Control(func(fd uintptr) {
		setErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, mark)
	})
	if err != nil {
		return err
	}
	return setErr
}

func (g *Gateway) relay(key string, sess *session) {
	buf := make([]byte, 65535)
	for {
		n, err := sess.backendConn.Read(buf)
		if err != nil {
			log.Printf("quic %s backend read ended: %v", key, err)
			g.remove(key)
			return
		}
		sess.mu.Lock()
		sess.lastActivity = time.Now()
		sess.mu.Unlock()
		if _, err := g.listener.WriteToUDP(buf[:n], sess.clientAddr); err != nil {
			log.Printf("quic %s client write failed: %v", key, err)
			g.remove(key)
			return
		}
	}
}

func (g *Gateway) gc(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.gcOnce(5 * time.Minute)
		}
	}
}

func (g *Gateway) gcOnce(maxIdle time.Duration) {
	now := time.Now()
	var stale []string
	g.mu.RLock()
	for key, sess := range g.sessions {
		sess.mu.Lock()
		idle := now.Sub(sess.lastActivity)
		sess.mu.Unlock()
		if idle > maxIdle {
			stale = append(stale, key)
		}
	}
	g.mu.RUnlock()
	for _, key := range stale {
		g.remove(key)
	}
}

func (g *Gateway) remove(key string) {
	g.mu.Lock()
	sess, ok := g.sessions[key]
	if ok {
		delete(g.sessions, key)
	}
	g.mu.Unlock()
	if ok {
		sess.backendConn.Close()
	}
}
