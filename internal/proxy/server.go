package proxy

import (
	"fmt"
	"log"

	"github.com/coolxll/lingma-tap/internal/ca"
	"github.com/coolxll/lingma-tap/internal/mitm"
	"github.com/lqqyt2423/go-mitmproxy/cert"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// Server is an HTTP/HTTPS proxy using go-mitmproxy.
type Server struct {
	caCert    *ca.CA
	onRecord  mitm.OnRecordFunc
	proxyServer *proxy.Proxy
	port        int
}

func NewServer(caCert *ca.CA, onRecord mitm.OnRecordFunc) *Server {
	return &Server{
		caCert:   caCert,
		onRecord: onRecord,
	}
}

// Start begins listening on the given port.
func (s *Server) Start(port int) error {
	if s.proxyServer != nil {
		s.proxyServer.Close()
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	opts := &proxy.Options{
		Addr:              addr,
		StreamLargeBodies: 1024 * 1024 * 5, // 5MB
		NewCaFunc: func() (cert.CA, error) {
			return s.caCert.GetGoMitmproxyCA(), nil
		},
	}

	p, err := proxy.NewProxy(opts)
	if err != nil {
		return fmt.Errorf("failed to create proxy: %w", err)
	}

	p.AddAddon(mitm.NewMitmProxyAddon(s.onRecord))

	s.proxyServer = p
	s.port = port
	log.Printf("[proxy] listening on %s", addr)

	go func() {
		if err := s.proxyServer.Start(); err != nil {
			log.Printf("[proxy] server error: %v", err)
		}
	}()
	return nil
}

// Stop shuts down the proxy server.
func (s *Server) Stop() {
	if s.proxyServer != nil {
		s.proxyServer.Close()
		s.proxyServer = nil
	}
	s.port = 0
}

// Port returns the port the server is listening on.
func (s *Server) Port() int {
	return s.port
}
