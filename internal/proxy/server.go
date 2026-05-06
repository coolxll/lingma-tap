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
	proxyServer *proxy.Proxy
	port        int
}

func NewServer(caCert *ca.CA, onRecord mitm.OnRecordFunc) *Server {
	opts := &proxy.Options{
		Addr:              "127.0.0.1:0",
		StreamLargeBodies: 1024 * 1024 * 5, // 5MB
		NewCaFunc: func() (cert.CA, error) {
			return caCert.GetGoMitmproxyCA(), nil
		},
	}

	p, err := proxy.NewProxy(opts)
	if err != nil {
		log.Fatalf("[proxy] failed to create proxy: %v", err)
	}

	// Add interceptor addon
	p.AddAddon(mitm.NewMitmProxyAddon(onRecord))

	return &Server{
		proxyServer: p,
	}
}

// Start begins listening on the given port.
func (s *Server) Start(port int) error {
	s.proxyServer.Opts.Addr = fmt.Sprintf("127.0.0.1:%d", port)
	s.port = port
	log.Printf("[proxy] listening on %s", s.proxyServer.Opts.Addr)

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
	}
}

// Port returns the port the server is listening on.
func (s *Server) Port() int {
	return s.port
}
