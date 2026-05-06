package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lqqyt2423/go-mitmproxy/cert"
)

// CA manages a self-signed root CA that can issue per-host leaf certificates.
type CA struct {
	certDir   string
	caCert    *x509.Certificate
	caKey     *ecdsa.PrivateKey
	certMu    sync.Mutex
	certCache map[string]*tls.Certificate
}

func (ca *CA) GetGoMitmproxyCA() cert.CA {
	return ca
}

func (ca *CA) GetRootCA() *x509.Certificate {
	return ca.caCert
}

func (ca *CA) GetCert(commonName string) (*tls.Certificate, error) {
	return ca.GetOrCreateCert(commonName)
}

func New(certDir string) (*CA, error) {
	certDir = expandPath(certDir)
	if err := os.MkdirAll(certDir, 0755); err != nil {
		return nil, fmt.Errorf("create cert dir: %w", err)
	}

	ca := &CA{
		certDir:   certDir,
		certCache: make(map[string]*tls.Certificate),
	}

	caCertPath := filepath.Join(certDir, "ca.crt")
	caKeyPath := filepath.Join(certDir, "ca.key")

	if fileExists(caCertPath) && fileExists(caKeyPath) {
		if err := ca.loadCA(caCertPath, caKeyPath); err != nil {
			return nil, fmt.Errorf("load CA: %w", err)
		}
		return ca, nil
	}

	if err := ca.generateCA(caCertPath, caKeyPath); err != nil {
		return nil, fmt.Errorf("generate CA: %w", err)
	}
	return ca, nil
}

// CertPath returns the CA certificate file path.
func (ca *CA) CertPath() string {
	return filepath.Join(ca.certDir, "ca.crt")
}

// GetOrCreateCert returns a TLS certificate for the given host, signed by the CA.
func (ca *CA) GetOrCreateCert(host string) (*tls.Certificate, error) {
	ca.certMu.Lock()
	defer ca.certMu.Unlock()

	if cert, ok := ca.certCache[host]; ok {
		return cert, nil
	}

	cert, err := ca.issueCert(host)
	if err != nil {
		return nil, err
	}
	ca.certCache[host] = cert
	return cert, nil
}

func (ca *CA) loadCA(certPath, keyPath string) error {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return fmt.Errorf("no PEM block in cert file")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}

	block, _ = pem.Decode(keyPEM)
	if block == nil {
		return fmt.Errorf("no PEM block in key file")
	}
	caKey, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return err
	}

	ca.caCert = caCert
	ca.caKey = caKey
	return nil
}

func (ca *CA) generateCA(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "Lingma Tap CA",
			Organization: []string{"lingma-tap"},
		},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return err
	}

	if err := savePEM(certPath, "CERTIFICATE", certDER, 0644); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := savePEM(keyPath, "EC PRIVATE KEY", keyDER, 0600); err != nil {
		return err
	}

	ca.caCert = cert
	ca.caKey = key
	return nil
}

func (ca *CA) issueCert(host string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   host,
			Organization: []string{"lingma-tap"},
		},
		NotBefore:   time.Now().Add(-24 * time.Hour),
		NotAfter:    time.Now().AddDate(1, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	// Set SANs
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, ca.caCert, &key.PublicKey, ca.caKey)
	if err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, err
	}

	return &tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
		Leaf:        cert,
	}, nil
}

func savePEM(path, blockType string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: data})
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}
