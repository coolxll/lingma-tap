package mitm

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coolxll/lingma-tap/internal/ca"
	"github.com/coolxll/lingma-tap/internal/proto"
)

// OnRecordFunc is called when a traffic record is parsed.
type OnRecordFunc func(rec *proto.Record)

// Interceptor performs MITM interception on TLS connections.
type Interceptor struct {
	ca       *ca.CA
	dialer   *Dialer
	onRecord OnRecordFunc
}

func NewInterceptor(ca *ca.CA, onRecord OnRecordFunc) *Interceptor {
	return &Interceptor{
		ca:       ca,
		dialer:   NewDialer(),
		onRecord: onRecord,
	}
}

// Intercept handles a CONNECT tunnel connection.
func (i *Interceptor) Intercept(clientConn net.Conn, targetHost string, targetPort int) {
	defer clientConn.Close()

	addr := fmt.Sprintf("%s:%d", targetHost, targetPort)
	log.Printf("[mitm] intercepting %s (%s)", targetHost, addr)

	// Get cert for this host
	cert, err := i.ca.GetOrCreateCert(targetHost)
	if err != nil {
		log.Printf("[mitm] cert error for %s: %v", targetHost, err)
		return
	}
	log.Printf("[mitm] cert ready for %s (leaf=%v)", targetHost, cert.Leaf != nil)

	// TLS handshake with client
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*cert},
		NextProtos:   []string{"http/1.1"},
	}
	clientTLS := tls.Server(clientConn, tlsConfig)
	if err := clientTLS.Handshake(); err != nil {
		log.Printf("[mitm] client handshake error for %s: %v", targetHost, err)
		return
	}
	log.Printf("[mitm] client handshake OK for %s, negotiated=%s", targetHost, clientTLS.ConnectionState().NegotiatedProtocol)
	defer clientTLS.Close()

	// Connect to real server
	serverConn, err := i.dialer.Dial("tcp", addr)
	if err != nil {
		log.Printf("[mitm] dial %s error: %v", addr, err)
		return
	}
	defer serverConn.Close()

	// TLS handshake with server
	serverTLS := tls.Client(serverConn, &tls.Config{
		ServerName:         targetHost,
		InsecureSkipVerify: true,
		NextProtos:         []string{"http/1.1"},
	})
	if err := serverTLS.Handshake(); err != nil {
		log.Printf("[mitm] server handshake error for %s: %v", targetHost, err)
		return
	}
	log.Printf("[mitm] server handshake OK for %s", targetHost)
	defer serverTLS.Close()

	// Parse and forward HTTP traffic
	i.pipeHTTP(clientTLS, serverTLS, targetHost)
}

// pipeHTTP reads HTTP requests from client and responses from server, parsing both.
func (i *Interceptor) pipeHTTP(client, server net.Conn, host string) {
	sessionID := proto.GenerateSessionID()
	index := 0

	clientReader := bufio.NewReader(client)
	serverReader := bufio.NewReader(server)

	for {
		// Read request from client
		req, err := http.ReadRequest(clientReader)
		if err != nil {
			if err != io.EOF && !isClosedError(err) {
				log.Printf("[mitm] read request error: %v", err)
			}
			return
		}

		reqHost := host
		if req.Host != "" {
			reqHost = req.Host
		}

		// Read request body
		var reqBody []byte
		if req.Body != nil {
			reqBody, _ = io.ReadAll(req.Body)
			req.Body.Close()
		}

		// Parse and record request
		rec := proto.ParseRequest(req, reqBody)
		rec.Session = sessionID
		rec.Index = index
		rec.Host = reqHost
		index++

		if i.onRecord != nil {
			i.onRecord(rec)
		}

		// Forward request to server
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
		if err := req.Write(server); err != nil {
			log.Printf("[mitm] write request error: %v", err)
			return
		}

		// Read response from server
		resp, err := http.ReadResponse(serverReader, req)
		if err != nil {
			log.Printf("[mitm] read response error: %v", err)
			return
		}

		// Handle SSE streaming responses differently
		if isSSE(resp) {
			i.handleSSEResponse(resp, client, sessionID, &index)
			// After SSE stream ends, check if connection should close
			if strings.EqualFold(resp.Header.Get("Connection"), "close") {
				return
			}
			continue
		}

		// Read response body (non-SSE)
		var respBody []byte
		if resp.Body != nil {
			respBody, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
		}

		// Parse and record response
		respRec := proto.ParseResponse(resp, respBody, sessionID, index)
		index++
		if i.onRecord != nil {
			i.onRecord(respRec)
		}

		// Forward response to client
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		if err := resp.Write(client); err != nil {
			log.Printf("[mitm] write response error: %v", err)
			return
		}

		// Check for Connection: close
		if strings.EqualFold(resp.Header.Get("Connection"), "close") {
			return
		}
	}
}

// isSSE checks if the response is a Server-Sent Event stream.
func isSSE(resp *http.Response) bool {
	return strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
}

// handleSSEResponse streams an SSE response to the client while capturing the full body for recording.
func (i *Interceptor) handleSSEResponse(resp *http.Response, client net.Conn, sessionID string, index *int) {
	// Write status line to client
	statusLine := fmt.Sprintf("%s %s\r\n", resp.Proto, resp.Status)
	if _, err := client.Write([]byte(statusLine)); err != nil {
		log.Printf("[mitm] write SSE status error: %v", err)
		return
	}

	// Write headers to client (use a trimmed header set)
	resp.Header.Del("Transfer-Encoding") // Body will be written raw
	resp.Header.Write(client)

	// Write blank line (end of headers)
	client.Write([]byte("\r\n"))

	// Stream body to client while capturing
	var fullBody bytes.Buffer
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			// Forward to client immediately
			if _, werr := client.Write(buf[:n]); werr != nil {
				log.Printf("[mitm] write SSE chunk error: %v", werr)
				resp.Body.Close()
				return
			}
			// Capture for recording
			fullBody.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	resp.Body.Close()

	// Create and record the response record with the full captured body
	respRec := proto.ParseResponse(resp, fullBody.Bytes(), sessionID, *index)
	(*index)++
	if i.onRecord != nil {
		i.onRecord(respRec)
	}
}

// InterceptPlain handles a plain HTTP connection (non-TLS).
func (i *Interceptor) InterceptPlain(clientConn net.Conn, targetHost string, targetPort int) {
	defer clientConn.Close()

	addr := fmt.Sprintf("%s:%d", targetHost, targetPort)
	serverConn, err := i.dialer.Dial("tcp", addr)
	if err != nil {
		log.Printf("[mitm] dial %s error: %v", addr, err)
		return
	}
	defer serverConn.Close()

	sessionID := proto.GenerateSessionID()
	index := 0

	clientReader := bufio.NewReader(clientConn)
	serverReader := bufio.NewReader(serverConn)

	for {
		clientConn.SetReadDeadline(time.Now().Add(30 * time.Second))
		req, err := http.ReadRequest(clientReader)
		if err != nil {
			return
		}

		var reqBody []byte
		if req.Body != nil {
			reqBody, _ = io.ReadAll(req.Body)
			req.Body.Close()
		}

		rec := proto.ParseRequest(req, reqBody)
		rec.Session = sessionID
		rec.Index = index
		index++
		if i.onRecord != nil {
			i.onRecord(rec)
		}

		req.Body = io.NopCloser(bytes.NewReader(reqBody))
		req.RequestURI = ""
		if err := req.Write(serverConn); err != nil {
			return
		}

		resp, err := http.ReadResponse(serverReader, req)
		if err != nil {
			return
		}

		// Handle SSE streaming responses
		if isSSE(resp) {
			i.handleSSEResponse(resp, clientConn, sessionID, &index)
			continue
		}

		var respBody []byte
		if resp.Body != nil {
			respBody, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
		}

		respRec := proto.ParseResponse(resp, respBody, sessionID, index)
		index++
		if i.onRecord != nil {
			i.onRecord(respRec)
		}

		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		if err := resp.Write(clientConn); err != nil {
			return
		}
	}
}

func isClosedError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "use of closed") || strings.Contains(s, "connection reset")
}
