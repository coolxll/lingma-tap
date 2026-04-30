package mitm

import (
	"net"
	"time"
)

// Dialer handles connections to target servers.
type Dialer struct {
	Timeout time.Duration
}

func NewDialer() *Dialer {
	return &Dialer{Timeout: 10 * time.Second}
}

func (d *Dialer) Dial(network, addr string) (net.Conn, error) {
	return net.DialTimeout(network, addr, d.Timeout)
}
