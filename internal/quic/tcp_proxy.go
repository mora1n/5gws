package quic

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/rules"
)

const (
	soOriginalDst = 80
	tcpSniffBytes = 4096
)

type TCPGateway struct {
	app         config.Config
	rules       *rules.Compiled
	exit        config.ExitConfig
	listener    *net.TCPListener
	originalDst func(*net.TCPConn) (*net.TCPAddr, error)
}

func listenTCPGateway(cfg config.Config, norm rules.Normalized) (*TCPGateway, error) {
	compiled, err := rules.Compile(norm)
	if err != nil {
		return nil, err
	}
	return listenTCPGatewayCompiled(cfg, compiled)
}

func listenTCPGatewayCompiled(cfg config.Config, compiled *rules.Compiled) (*TCPGateway, error) {
	exit, ok := cfg.ExitByName(cfg.Routing.FallbackExit)
	if !ok {
		return nil, fmt.Errorf("tcp gateway references unknown fallback exit %q", cfg.Routing.FallbackExit)
	}
	if exit.Type == "shadowsocks-rust" && !exit.TCPEnabled() {
		return nil, fmt.Errorf("tcp gateway fallback exit %q has tcp=false", cfg.Routing.FallbackExit)
	}
	addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(cfg.Network.TCPRedirectPort))
	tcpAddr, err := net.ResolveTCPAddr("tcp4", addr)
	if err != nil {
		return nil, fmt.Errorf("tcp gateway resolve listen address: %w", err)
	}
	listener, err := net.ListenTCP("tcp4", tcpAddr)
	if err != nil {
		return nil, fmt.Errorf("tcp gateway listen %s: %w", addr, err)
	}
	log.Printf("tcp gateway listening on %s fallback_exit=%s", listener.Addr(), cfg.Routing.FallbackExit)
	return &TCPGateway{app: cfg, rules: compiled, exit: exit, listener: listener, originalDst: getOriginalDst}, nil
}

func (g *TCPGateway) serve(ctx context.Context) error {
	for {
		if err := g.listener.SetDeadline(time.Now().Add(time.Second)); err != nil {
			return err
		}
		conn, err := g.listener.AcceptTCP()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				continue
			}
			return fmt.Errorf("tcp gateway accept failed: %w", err)
		}
		go g.handle(conn)
	}
}

func (g *TCPGateway) handle(client *net.TCPConn) {
	defer client.Close()
	start := time.Now()
	original, err := g.originalDst(client)
	if err != nil {
		log.Printf("tcp gateway src=%s rejected: original dst: %v", client.RemoteAddr(), err)
		return
	}
	targetHost := original.IP.String()
	targetPort := original.Port
	targetSource := "original_dst"
	exit := g.exit
	var initial []byte
	if isGatewayIP(original.IP, g.app.Network.GatewayIP) {
		initial, err = readInitialTCP(client)
		if err != nil {
			log.Printf("tcp gateway src=%s original=%s rejected: %v", client.RemoteAddr(), original, err)
			return
		}
		host, source := sniffTCPHost(initial)
		if host == "" {
			log.Printf("tcp gateway src=%s original=%s rejected: gateway original dst without HTTP Host or TLS SNI", client.RemoteAddr(), original)
			return
		}
		targetHost = host
		targetSource = source
		if selected, matched, err := selectTCPExit(g.app, g.rules, host); err != nil {
			log.Printf("tcp gateway src=%s original=%s host=%s rejected: %v", client.RemoteAddr(), original, host, err)
			return
		} else {
			exit = selected
			targetSource = fmt.Sprintf("%s matched=%t", targetSource, matched)
		}
	}
	backend, target, err := dialTCPBackend(targetHost, targetPort, exit)
	if err != nil {
		log.Printf("tcp gateway src=%s original=%s target=%s:%d exit=%q rejected: %v", client.RemoteAddr(), original, targetHost, targetPort, exit.Name, err)
		return
	}
	defer backend.Close()
	log.Printf("tcp gateway src=%s original=%s target=%s source=%s exit=%q", client.RemoteAddr(), original, target, targetSource, exit.Name)
	var initialBytes int64
	if len(initial) > 0 {
		initialBytes, err = writeInitial(backend, initial)
		if err != nil {
			log.Printf("tcp gateway src=%s target=%s initial write failed: %v", client.RemoteAddr(), target, err)
			return
		}
	}
	up, down := relayTCP(client, backend)
	up += initialBytes
	log.Printf("tcp gateway src=%s target=%s ended up=%d down=%d duration=%s", client.RemoteAddr(), target, up, down, time.Since(start).Round(time.Millisecond))
}

func selectTCPExit(cfg config.Config, compiled *rules.Compiled, host string) (config.ExitConfig, bool, error) {
	if rule, ok := compiled.MatchGatewayDomain(host); ok {
		exit, exists := cfg.ExitByName(rule.Exit)
		if !exists {
			return config.ExitConfig{}, false, fmt.Errorf("unknown exit %q", rule.Exit)
		}
		if exit.Type == "shadowsocks-rust" && !exit.TCPEnabled() {
			return config.ExitConfig{}, false, fmt.Errorf("exit %q has tcp=false", exit.Name)
		}
		return exit, true, nil
	}
	exit, exists := cfg.ExitByName(cfg.Routing.FallbackExit)
	if !exists {
		return config.ExitConfig{}, false, fmt.Errorf("unknown fallback exit %q", cfg.Routing.FallbackExit)
	}
	if exit.Type == "shadowsocks-rust" && !exit.TCPEnabled() {
		return config.ExitConfig{}, false, fmt.Errorf("fallback exit %q has tcp=false", exit.Name)
	}
	return exit, false, nil
}

func readInitialTCP(conn *net.TCPConn) ([]byte, error) {
	buf := make([]byte, tcpSniffBytes)
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return nil, err
	}
	n, err := conn.Read(buf)
	_ = conn.SetReadDeadline(time.Time{})
	if n > 0 {
		return buf[:n], nil
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("no initial client data")
}

func sniffTCPHost(data []byte) (string, string) {
	if host := sniffHTTPHost(data); host != "" {
		return host, "http_host"
	}
	if host := sniffTLSSNI(data); host != "" {
		return host, "tls_sni"
	}
	return "", ""
}

func sniffHTTPHost(data []byte) string {
	text := string(data)
	end := strings.Index(text, "\r\n\r\n")
	if end < 0 {
		return ""
	}
	head := text[:end]
	lines := strings.Split(head, "\r\n")
	if len(lines) == 0 || !strings.Contains(lines[0], " HTTP/") {
		return ""
	}
	for _, line := range lines[1:] {
		name, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "host") {
			continue
		}
		return normalizeHost(value)
	}
	return ""
}

func sniffTLSSNI(data []byte) string {
	if len(data) < 9 || data[0] != 0x16 {
		return ""
	}
	recordLen := int(binary.BigEndian.Uint16(data[3:5]))
	if recordLen <= 0 || len(data) < 5+recordLen {
		return ""
	}
	handshake := data[5 : 5+recordLen]
	if len(handshake) < 42 || handshake[0] != 0x01 {
		return ""
	}
	hsLen := int(handshake[1])<<16 | int(handshake[2])<<8 | int(handshake[3])
	if hsLen <= 0 || len(handshake) < 4+hsLen {
		return ""
	}
	body := handshake[4 : 4+hsLen]
	off := 2 + 32
	if len(body) < off+1 {
		return ""
	}
	sessionLen := int(body[off])
	off += 1 + sessionLen
	if len(body) < off+2 {
		return ""
	}
	cipherLen := int(binary.BigEndian.Uint16(body[off : off+2]))
	off += 2 + cipherLen
	if len(body) < off+1 {
		return ""
	}
	compressionLen := int(body[off])
	off += 1 + compressionLen
	if len(body) < off+2 {
		return ""
	}
	extensionsLen := int(binary.BigEndian.Uint16(body[off : off+2]))
	off += 2
	if len(body) < off+extensionsLen {
		return ""
	}
	extensions := body[off : off+extensionsLen]
	for len(extensions) >= 4 {
		extType := binary.BigEndian.Uint16(extensions[:2])
		extLen := int(binary.BigEndian.Uint16(extensions[2:4]))
		extensions = extensions[4:]
		if len(extensions) < extLen {
			return ""
		}
		if extType == 0 {
			return parseSNIExtension(extensions[:extLen])
		}
		extensions = extensions[extLen:]
	}
	return ""
}

func parseSNIExtension(data []byte) string {
	if len(data) < 2 {
		return ""
	}
	listLen := int(binary.BigEndian.Uint16(data[:2]))
	data = data[2:]
	if len(data) < listLen {
		return ""
	}
	list := data[:listLen]
	for len(list) >= 3 {
		nameType := list[0]
		nameLen := int(binary.BigEndian.Uint16(list[1:3]))
		list = list[3:]
		if len(list) < nameLen {
			return ""
		}
		if nameType == 0 {
			return normalizeHost(string(list[:nameLen]))
		}
		list = list[nameLen:]
	}
	return ""
}

func normalizeHost(value string) string {
	host := strings.ToLower(strings.TrimSpace(value))
	host = strings.TrimSuffix(host, ".")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = strings.TrimSuffix(h, ".")
	}
	return host
}

func isGatewayIP(ip net.IP, gateway string) bool {
	parsed := net.ParseIP(gateway)
	return parsed != nil && ip.Equal(parsed)
}

func dialTCPBackend(host string, port int, exit config.ExitConfig) (net.Conn, string, error) {
	switch exit.Type {
	case "direct":
		conn, err := dialDirectTCP(host, port, exit.FWMark)
		return conn, net.JoinHostPort(host, strconv.Itoa(port)), err
	case "shadowsocks-rust":
		proxyAddr := net.JoinHostPort(exit.ListenAddress, strconv.Itoa(exit.ListenPort))
		conn, err := dialSocksTCP(proxyAddr, host, port)
		return conn, proxyAddr + " -> " + net.JoinHostPort(host, strconv.Itoa(port)), err
	default:
		return nil, "", fmt.Errorf("unsupported exit type %q", exit.Type)
	}
}

func dialDirectTCP(host string, port, mark int) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	if mark != 0 {
		dialer.Control = func(network, address string, c syscall.RawConn) error {
			var setErr error
			err := c.Control(func(fd uintptr) {
				setErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, mark)
			})
			if err != nil {
				return err
			}
			return setErr
		}
	}
	return dialer.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
}

func dialSocksTCP(proxyAddr, host string, port int) (net.Conn, error) {
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
	addr, err := socksAddr(host, port)
	if err != nil {
		tcp.Close()
		return nil, err
	}
	req := append([]byte{0x05, 0x01, 0x00}, addr...)
	if _, err := tcp.Write(req); err != nil {
		tcp.Close()
		return nil, err
	}
	if _, _, err := readSocksAddr(tcp); err != nil {
		tcp.Close()
		return nil, err
	}
	if err := tcp.SetDeadline(time.Time{}); err != nil {
		tcp.Close()
		return nil, err
	}
	return tcp, nil
}

func socksAddr(host string, port int) ([]byte, error) {
	var out []byte
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
	return binary.BigEndian.AppendUint16(out, uint16(port)), nil
}

type tcpCopyResult struct {
	name string
	n    int64
	err  error
}

func writeInitial(backend net.Conn, initial []byte) (int64, error) {
	written := 0
	for written < len(initial) {
		n, err := backend.Write(initial[written:])
		written += n
		if err != nil {
			return int64(written), err
		}
		if n == 0 {
			return int64(written), io.ErrUnexpectedEOF
		}
	}
	return int64(written), nil
}

func relayTCP(client net.Conn, backend net.Conn) (int64, int64) {
	done := make(chan tcpCopyResult, 2)
	go func() {
		n, err := io.Copy(backend, client)
		closeWrite(backend)
		done <- tcpCopyResult{name: "up", n: n, err: err}
	}()
	go func() {
		n, err := io.Copy(client, backend)
		closeWrite(client)
		done <- tcpCopyResult{name: "down", n: n, err: err}
	}()
	first := <-done
	if first.err != nil {
		client.Close()
		backend.Close()
	}
	second := <-done
	var up, down int64
	for _, result := range []tcpCopyResult{first, second} {
		if result.name == "up" {
			up = result.n
		} else {
			down = result.n
		}
	}
	return up, down
}

func closeWrite(conn net.Conn) {
	if c, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = c.CloseWrite()
	}
}

func getOriginalDst(conn *net.TCPConn) (*net.TCPAddr, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}
	var addr syscall.RawSockaddrInet4
	var sockErr error
	err = raw.Control(func(fd uintptr) {
		size := uint32(unsafe.Sizeof(addr))
		_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, uintptr(syscall.SOL_IP), uintptr(soOriginalDst), uintptr(unsafe.Pointer(&addr)), uintptr(unsafe.Pointer(&size)), 0)
		if errno != 0 {
			sockErr = errno
		}
	})
	if err != nil {
		return nil, err
	}
	if sockErr != nil {
		return nil, sockErr
	}
	if addr.Family != syscall.AF_INET {
		return nil, fmt.Errorf("unsupported original dst family %d", addr.Family)
	}
	port := int((addr.Port&0xff)<<8 | addr.Port>>8)
	ip := net.IPv4(addr.Addr[0], addr.Addr[1], addr.Addr[2], addr.Addr[3])
	return &net.TCPAddr{IP: ip, Port: port}, nil
}

func (g *TCPGateway) close() {
	_ = g.listener.Close()
}
