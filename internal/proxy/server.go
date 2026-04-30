package proxy

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/lynn/lingma-tap/internal/ca"
	"github.com/lynn/lingma-tap/internal/mitm"
)

// Server is an HTTP CONNECT proxy with MITM interception.
type Server struct {
	listener  net.Listener
	intercept *mitm.Interceptor
	onRecord  mitm.OnRecordFunc
	port      int
}

func NewServer(caCert *ca.CA, onRecord mitm.OnRecordFunc) *Server {
	return &Server{
		intercept: mitm.NewInterceptor(caCert, onRecord),
		onRecord:  onRecord,
	}
}

// Start begins listening on the given port.
func (s *Server) Start(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	s.listener = ln
	s.port = port
	log.Printf("[proxy] listening on %s", ln.Addr())

	go s.accept()
	return nil
}

// Stop shuts down the proxy server.
func (s *Server) Stop() {
	if s.listener != nil {
		s.listener.Close()
	}
}

// Port returns the port the server is listening on.
func (s *Server) Port() int {
	return s.port
}

func (s *Server) accept() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	reader := bufio.NewReader(conn)

	// Read the first request line
	line, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return
	}
	line = strings.TrimSpace(line)

	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 {
		conn.Close()
		return
	}

	method := parts[0]
	target := parts[1]

	if method == "CONNECT" {
		s.handleConnect(conn, reader, target)
	} else {
		s.handlePlainRequest(conn, reader, method, target, line)
	}
}

func (s *Server) handleConnect(conn net.Conn, reader *bufio.Reader, target string) {
	host, port := splitHostPort(target)
	if port == 0 {
		port = 443
	}

	log.Printf("[proxy] CONNECT %s (resolved: %s:%d)", target, host, port)

	// Consume remaining CONNECT headers (up to blank line)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			conn.Close()
			return
		}
		if line == "\r\n" || line == "\n" || line == "" {
			break
		}
	}

	// Send 200 Connection Established
	_, err := fmt.Fprintf(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
	if err != nil {
		log.Printf("[proxy] send 200 error: %v", err)
		conn.Close()
		return
	}
	log.Printf("[proxy] sent 200 to client for %s:%d", host, port)

	// Detect if TLS
	peekConn := mitm.NewPeekableConn(conn)
	isTLS, sni, err := mitm.DetectTLSWithSNI(peekConn)
	if err != nil {
		log.Printf("[proxy] TLS detect error for %s:%d: %v", host, port, err)
		conn.Close()
		return
	}

	if sni != "" {
		host = sni
	}

	log.Printf("[proxy] TLS=%v SNI=%q for %s:%d", isTLS, sni, host, port)

	if isTLS {
		s.intercept.Intercept(peekConn, host, port)
	} else {
		s.intercept.InterceptPlain(peekConn, host, port)
	}
}

func (s *Server) handlePlainRequest(conn net.Conn, reader *bufio.Reader, method, target, firstLine string) {
	// For plain HTTP, forward directly
	host, port := splitHostPort(target)
	if port == 0 {
		port = 80
	}

	s.intercept.InterceptPlain(&prependConn{Conn: conn, prefix: []byte(firstLine + "\r\n"), reader: reader}, host, port)
}

func splitHostPort(addr string) (string, int) {
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		host := addr[:idx]
		port := 0
		fmt.Sscanf(addr[idx+1:], "%d", &port)
		return host, port
	}
	return addr, 0
}

type prependConn struct {
	net.Conn
	prefix []byte
	reader *bufio.Reader
	read   bool
}

func (c *prependConn) Read(p []byte) (int, error) {
	if !c.read && len(c.prefix) > 0 {
		c.read = true
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		if n < len(p) {
			extra, err := c.reader.Read(p[n:])
			return n + extra, err
		}
		return n, nil
	}
	return c.reader.Read(p)
}
