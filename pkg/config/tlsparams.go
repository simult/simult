package config

import (
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TLSParams is a configuration holder to create tls.Config
type TLSParams struct {
	CertPath string
	KeyPath  string
}

// Config creates a *tls.Config from its own variables
func (t *TLSParams) Config() (c *tls.Config, err error) {
	certPath := t.CertPath
	if certPath == "" {
		certPath = "."
	}
	certFile, err := os.Open(certPath)
	if err != nil {
		err = fmt.Errorf("cert path %q open error: %w", certPath, err)
		return
	}
	defer certFile.Close()
	certStat, err := certFile.Stat()
	if err != nil {
		err = fmt.Errorf("cert path %q stat error: %w", certPath, err)
		return
	}

	keyPath := t.KeyPath
	if keyPath == "" {
		keyPath = "."
	}
	keyFile, err := os.Open(keyPath)
	if err != nil {
		err = fmt.Errorf("key path %q open error: %w", keyPath, err)
		return
	}
	defer keyFile.Close()
	keyStat, err := keyFile.Stat()
	if err != nil {
		err = fmt.Errorf("key path %q stat error: %w", keyPath, err)
		return
	}

	if certStat.IsDir() != keyStat.IsDir() {
		err = fmt.Errorf("files on cert path %q and key path %q have different file type", certPath, keyPath)
		return
	}

	if !certStat.IsDir() {
		certs := make([]tls.Certificate, 1)
		certs[0], err = tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			err = fmt.Errorf("error loading certificate pair %q and %q: %w", certPath, keyPath, err)
			return
		}
		c = &tls.Config{
			Certificates: certs,
		}
		return
	}

	certFileInfos, err := certFile.Readdir(0)
	if err != nil {
		err = fmt.Errorf("cert path %q readdir error: %w", certPath, err)
		return
	}
	certs := make([]tls.Certificate, 0, len(certFileInfos))
	for _, certFi := range certFileInfos {
		if certFi.IsDir() {
			continue
		}
		certFn := certFi.Name()
		certExt := filepath.Ext(certFn)
		if certExt != ".crt" {
			continue
		}
		name := strings.TrimSuffix(certFn, certExt)
		keyFn := name + ".key"
		singleCertFile := certPath + "/" + certFn
		singleKeyFile := keyPath + "/" + keyFn
		var cert tls.Certificate
		cert, err = tls.LoadX509KeyPair(singleCertFile, singleKeyFile)
		if err != nil {
			err = fmt.Errorf("error loading certificate pair %q and %q: %w", singleCertFile, singleKeyFile, err)
			return
		}
		certs = append(certs, cert)
	}
	c = &tls.Config{
		Certificates:             certs,
		PreferServerCipherSuites: true,
	}
	return
}
