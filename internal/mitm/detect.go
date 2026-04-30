package mitm

import (
	"bufio"
	"io"
	"net"
)

const (
	tlsRecordTypeHandshake = 22
	tlsVersion10           = 0x0301
	tlsVersion13           = 0x0304
)

// PeekableConn wraps a net.Conn with peek capability.
type PeekableConn struct {
	net.Conn
	reader *bufio.Reader
}

func NewPeekableConn(conn net.Conn) *PeekableConn {
	return &PeekableConn{
		Conn:   conn,
		reader: bufio.NewReader(conn),
	}
}

func (c *PeekableConn) Peek(n int) ([]byte, error) {
	return c.reader.Peek(n)
}

func (c *PeekableConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

// IsTLSClientHello checks if data starts with a TLS ClientHello.
func IsTLSClientHello(data []byte) bool {
	if len(data) < 6 {
		return false
	}
	if data[0] != tlsRecordTypeHandshake {
		return false
	}
	version := uint16(data[1])<<8 | uint16(data[2])
	if version < tlsVersion10 || version > tlsVersion13 {
		if version != 0x0300 {
			return false
		}
	}
	return data[5] == 0x01
}

// DetectTLSWithSNI peeks at the connection to detect TLS and extract SNI hostname.
func DetectTLSWithSNI(conn *PeekableConn) (bool, string, error) {
	data, err := conn.Peek(6)
	if err != nil {
		if err == io.EOF {
			return false, "", nil
		}
		return false, "", err
	}
	if !IsTLSClientHello(data) {
		return false, "", nil
	}

	recordLen := int(data[3])<<8 | int(data[4])
	totalLen := 5 + recordLen
	if totalLen > 16384 {
		totalLen = 16384
	}

	fullData, err := conn.Peek(totalLen)
	if err != nil && err != io.EOF {
		fullData, _ = conn.Peek(conn.reader.Buffered())
	}

	return true, extractSNI(fullData), nil
}

func extractSNI(data []byte) string {
	if len(data) < 43 {
		return ""
	}
	pos := 5 + 4 + 2 + 32 // record + handshake + version + random

	if pos >= len(data) {
		return ""
	}
	sessionIDLen := int(data[pos])
	pos++
	pos += sessionIDLen

	if pos+2 > len(data) {
		return ""
	}
	cipherLen := int(data[pos])<<8 | int(data[pos+1])
	pos += 2 + cipherLen

	if pos >= len(data) {
		return ""
	}
	compLen := int(data[pos])
	pos++
	pos += compLen

	if pos+2 > len(data) {
		return ""
	}
	extLen := int(data[pos])<<8 | int(data[pos+1])
	pos += 2

	end := pos + extLen
	if end > len(data) {
		end = len(data)
	}

	for pos+4 <= end {
		extType := int(data[pos])<<8 | int(data[pos+1])
		extDataLen := int(data[pos+2])<<8 | int(data[pos+3])
		pos += 4
		if pos+extDataLen > end {
			break
		}
		if extType == 0 && extDataLen > 0 {
			return parseSNI(data[pos : pos+extDataLen])
		}
		pos += extDataLen
	}
	return ""
}

func parseSNI(data []byte) string {
	if len(data) < 5 {
		return ""
	}
	listLen := int(data[0])<<8 | int(data[1])
	pos := 2
	end := pos + listLen
	if end > len(data) {
		end = len(data)
	}
	for pos+3 <= end {
		nameType := data[pos]
		nameLen := int(data[pos+1])<<8 | int(data[pos+2])
		pos += 3
		if nameLen <= 0 || pos+nameLen > end {
			break
		}
		if nameType == 0 {
			return string(data[pos : pos+nameLen])
		}
		pos += nameLen
	}
	return ""
}
