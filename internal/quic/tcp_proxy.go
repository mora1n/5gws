package quic

import (
	"bytes"
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
)

const (
	soOriginalDst = 80
	tcpSniffBytes = 4096
)

type TCPProxy struct {
	cfg         config.TCPProxyConfig
	app         config.Config
	exit        config.ExitConfig
	listener    *net.TCPListener
	originalDst func(*net.TCPConn) (*net.TCPAddr, error)
}

func listenTCPProxies(cfg config.Config) ([]*TCPProxy, error) {
	proxies := make([]*TCPProxy, 0, len(cfg.TCPProxies))
	for _, proxyCfg := range cfg.TCPProxies {
		proxy, err := listenTCPProxy(cfg, proxyCfg)
		if err != nil {
			closeTCPProxies(proxies)
			return nil, err
		}
		proxies = append(proxies, proxy)
	}
	return proxies, nil
}

func listenTCPProxy(cfg config.Config, proxyCfg config.TCPProxyConfig) (*TCPProxy, error) {
	exit, ok := cfg.ExitByName(proxyCfg.Exit)
	if !ok {
		return nil, fmt.Errorf("tcp proxy %q references unknown exit %q", proxyCfg.Name, proxyCfg.Exit)
	}
	if exit.Type == "shadowsocks-rust" && !exit.TCPEnabled() {
		return nil, fmt.Errorf("tcp proxy %q references exit %q with tcp=false", proxyCfg.Name, proxyCfg.Exit)
	}
	addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(proxyCfg.ListenPort))
	tcpAddr, err := net.ResolveTCPAddr("tcp4", addr)
	if err != nil {
		return nil, fmt.Errorf("tcp proxy %q resolve listen address: %w", proxyCfg.Name, err)
	}
	listener, err := net.ListenTCP("tcp4", tcpAddr)
	if err != nil {
		return nil, fmt.Errorf("tcp proxy %q listen %s: %w", proxyCfg.Name, addr, err)
	}
	log.Printf("tcp proxy %s listening on %s client_port=%d exit=%s", proxyCfg.Name, listener.Addr(), proxyCfg.ClientPort, proxyCfg.Exit)
	return &TCPProxy{cfg: proxyCfg, app: cfg, exit: exit, listener: listener, originalDst: getOriginalDst}, nil
}

func closeTCPProxies(proxies []*TCPProxy) {
	for _, proxy := range proxies {
		proxy.close()
	}
}

func (p *TCPProxy) serve(ctx context.Context) error {
	for {
		if err := p.listener.SetDeadline(time.Now().Add(time.Second)); err != nil {
			return err
		}
		conn, err := p.listener.AcceptTCP()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				continue
			}
			return fmt.Errorf("tcp proxy %s accept failed: %w", p.cfg.Name, err)
		}
		go p.handle(conn)
	}
}

func (p *TCPProxy) handle(client *net.TCPConn) {
	defer client.Close()
	start := time.Now()
	original, err := p.originalDst(client)
	if err != nil {
		log.Printf("tcp proxy %s src=%s rejected: original dst: %v", p.cfg.Name, client.RemoteAddr(), err)
		return
	}
	targetHost := original.IP.String()
	targetPort := original.Port
	targetSource := "original_dst"
	var initial []byte
	if isGatewayIP(original.IP, p.app.Network.GatewayIP) {
		initial, err = readInitialTCP(client)
		if err != nil {
			log.Printf("tcp proxy %s src=%s original=%s rejected: %v", p.cfg.Name, client.RemoteAddr(), original, err)
			return
		}
		host, source := sniffTCPHost(initial)
		if host == "" {
			log.Printf("tcp proxy %s src=%s original=%s rejected: gateway original dst without HTTP Host or TLS SNI", p.cfg.Name, client.RemoteAddr(), original)
			return
		}
		targetHost = host
		targetPort = p.cfg.ClientPort
		targetSource = source
	}
	backend, target, err := dialTCPBackend(targetHost, targetPort, p.exit)
	if err != nil {
		log.Printf("tcp proxy %s src=%s original=%s target=%s:%d exit=%q rejected: %v", p.cfg.Name, client.RemoteAddr(), original, targetHost, targetPort, p.exit.Name, err)
		return
	}
	defer backend.Close()
	log.Printf("tcp proxy %s src=%s original=%s target=%s source=%s exit=%q", p.cfg.Name, client.RemoteAddr(), original, target, targetSource, p.exit.Name)
	clientReader := io.Reader(client)
	if len(initial) > 0 {
		clientReader = io.MultiReader(bytes.NewReader(initial), client)
	}
	up, down := relayTCP(client, clientReader, backend)
	log.Printf("tcp proxy %s src=%s target=%s ended up=%d down=%d duration=%s", p.cfg.Name, client.RemoteAddr(), target, up, down, time.Since(start).Round(time.Millisecond))
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

func relayTCP(client net.Conn, clientReader io.Reader, backend net.Conn) (int64, int64) {
	done := make(chan tcpCopyResult, 2)
	go func() {
		n, err := io.Copy(backend, clientReader)
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

func (p *TCPProxy) close() {
	_ = p.listener.Close()
}
