package http

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func writeKeyPair(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "api.example.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	certFile, keyFile = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	if err = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

// Every handshake calls GetCertificate; a renewed certificate is picked up while handshakes run. Run with -race.
func TestCertReloaderIsSafeUnderConcurrentHandshakesAndRenewal(t *testing.T) {
	certFile, keyFile := writeKeyPair(t, t.TempDir())
	reloader := newCertReloader(certFile, keyFile)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if cert, err := reloader.GetCertificate(nil); err != nil || cert == nil {
					t.Errorf("handshake: %v", err)
					return
				}
			}
		}()
	}
	for i := 0; i < 5; i++ { // renewals touch the key file
		time.Sleep(2 * time.Millisecond)
		now := time.Now().Add(time.Duration(i+1) * time.Hour)
		_ = os.Chtimes(keyFile, now, now)
	}
	wg.Wait()
}
