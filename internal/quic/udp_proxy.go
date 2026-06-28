package quic

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/morain/5gws/internal/config"
)

type UDPProxy struct {
	cfg        config.UDPProxyConfig
	exit       config.ExitConfig
	listener   *net.UDPConn
	targetHost string
	targetPort int
	mu         sync.RWMutex
	sessions   map[string]*session
}

func listenUDPProxies(cfg config.Config) ([]*UDPProxy, error) {
	proxies := make([]*UDPProxy, 0, len(cfg.UDPProxies))
	for _, proxyCfg := range cfg.UDPProxies {
		proxy, err := listenUDPProxy(cfg, proxyCfg)
		if err != nil {
			closeUDPProxies(proxies)
			return nil, err
		}
		proxies = append(proxies, proxy)
	}
	return proxies, nil
}

func listenUDPProxy(cfg config.Config, proxyCfg config.UDPProxyConfig) (*UDPProxy, error) {
	host, port, err := proxyCfg.TargetHostPort()
	if err != nil {
		return nil, fmt.Errorf("udp proxy %q: %w", proxyCfg.Name, err)
	}
	exit, ok := cfg.ExitByName(proxyCfg.Exit)
	if !ok {
		return nil, fmt.Errorf("udp proxy %q references unknown exit %q", proxyCfg.Name, proxyCfg.Exit)
	}
	if exit.Type == "shadowsocks-rust" && !exit.UDPEnabled() {
		return nil, fmt.Errorf("udp proxy %q references exit %q with udp=false", proxyCfg.Name, proxyCfg.Exit)
	}
	addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(proxyCfg.ListenPort))
	udpAddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("udp proxy %q resolve listen address: %w", proxyCfg.Name, err)
	}
	conn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("udp proxy %q listen %s: %w", proxyCfg.Name, addr, err)
	}
	log.Printf("udp proxy %s listening on %s client_port=%d target=%s exit=%s", proxyCfg.Name, conn.LocalAddr(), proxyCfg.ClientPort, proxyCfg.Target, proxyCfg.Exit)
	return &UDPProxy{
		cfg:        proxyCfg,
		exit:       exit,
		listener:   conn,
		targetHost: host,
		targetPort: port,
		sessions:   map[string]*session{},
	}, nil
}

func closeUDPProxies(proxies []*UDPProxy) {
	for _, proxy := range proxies {
		proxy.close()
	}
}

func (p *UDPProxy) serve(ctx context.Context) error {
	buf := make([]byte, 65535)
	for {
		if err := p.listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			return err
		}
		n, addr, err := p.listener.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				continue
			}
			return fmt.Errorf("udp proxy %s read failed: %w", p.cfg.Name, err)
		}
		if p.forwardExisting(addr, buf[:n]) {
			continue
		}
		packet := append([]byte(nil), buf[:n]...)
		go p.newSession(packet, addr)
	}
}

func (p *UDPProxy) forwardExisting(addr *net.UDPAddr, data []byte) bool {
	key := addr.String()
	p.mu.RLock()
	sess, ok := p.sessions[key]
	p.mu.RUnlock()
	if !ok {
		return false
	}
	sess.mu.Lock()
	sess.lastActivity = time.Now()
	conn := sess.backendConn
	sess.mu.Unlock()
	if _, err := conn.Write(data); err != nil {
		log.Printf("udp proxy %s session %s backend write failed: %v", p.cfg.Name, key, err)
	}
	return true
}

func (p *UDPProxy) newSession(data []byte, addr *net.UDPAddr) {
	backend, target, err := connectBackend(p.targetHost, p.targetPort, p.exit)
	if err != nil {
		log.Printf("udp proxy %s %s target=%s exit=%q rejected: %v", p.cfg.Name, addr, p.cfg.Target, p.exit.Name, err)
		return
	}
	log.Printf("udp proxy %s src=%s target=%s exit=%q backend=%s", p.cfg.Name, addr, p.cfg.Target, p.exit.Name, target)
	sess := &session{clientAddr: addr, backendConn: backend, lastActivity: time.Now()}
	key := addr.String()
	p.mu.Lock()
	if old, exists := p.sessions[key]; exists {
		p.mu.Unlock()
		backend.Close()
		if _, err := old.backendConn.Write(data); err != nil {
			log.Printf("udp proxy %s duplicate session %s backend write failed: %v", p.cfg.Name, key, err)
		}
		return
	}
	p.sessions[key] = sess
	p.mu.Unlock()
	if _, err := backend.Write(data); err != nil {
		log.Printf("udp proxy %s %s first write failed: %v", p.cfg.Name, key, err)
		p.remove(key)
		return
	}
	go p.relay(key, sess)
}

func (p *UDPProxy) relay(key string, sess *session) {
	buf := make([]byte, 65535)
	for {
		n, err := sess.backendConn.Read(buf)
		if err != nil {
			log.Printf("udp proxy %s %s backend read ended: %v", p.cfg.Name, key, err)
			p.remove(key)
			return
		}
		sess.mu.Lock()
		sess.lastActivity = time.Now()
		sess.mu.Unlock()
		if _, err := p.listener.WriteToUDP(buf[:n], sess.clientAddr); err != nil {
			log.Printf("udp proxy %s %s client write failed: %v", p.cfg.Name, key, err)
			p.remove(key)
			return
		}
	}
}

func (p *UDPProxy) gc(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.gcOnce(5 * time.Minute)
		}
	}
}

func (p *UDPProxy) gcOnce(maxIdle time.Duration) {
	now := time.Now()
	var stale []string
	p.mu.RLock()
	for key, sess := range p.sessions {
		sess.mu.Lock()
		idle := now.Sub(sess.lastActivity)
		sess.mu.Unlock()
		if idle > maxIdle {
			stale = append(stale, key)
		}
	}
	p.mu.RUnlock()
	for _, key := range stale {
		p.remove(key)
	}
}

func (p *UDPProxy) remove(key string) {
	p.mu.Lock()
	sess, ok := p.sessions[key]
	if ok {
		delete(p.sessions, key)
	}
	p.mu.Unlock()
	if ok {
		sess.backendConn.Close()
	}
}

func (p *UDPProxy) close() {
	_ = p.listener.Close()
	p.mu.Lock()
	sessions := p.sessions
	p.sessions = map[string]*session{}
	p.mu.Unlock()
	for _, sess := range sessions {
		sess.backendConn.Close()
	}
}
