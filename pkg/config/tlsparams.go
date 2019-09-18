package config

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
)

type TLSParams struct {
	CertPath string
	KeyPath  string
}

func (t *TLSParams) Config() (c *tls.Config, err error) {
	certPath := t.CertPath
	if certPath == "" {
		certPath = "."
	}
	certFile, err := os.Open(certPath)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	defer certFile.Close()
	certStat, err := certFile.Stat()
	if err != nil {
		err = errors.WithStack(err)
		return
	}

	keyPath := t.KeyPath
	if keyPath == "" {
		keyPath = "."
	}
	keyFile, err := os.Open(keyPath)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	defer keyFile.Close()
	keyStat, err := keyFile.Stat()
	if err != nil {
		err = errors.WithStack(err)
		return
	}

	if certStat.IsDir() != keyStat.IsDir() {
		err = errors.New("key and cert files have different file type")
		return
	}

	if !certStat.IsDir() {
		certs := make([]tls.Certificate, 1)
		certs[0], err = tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			err = errors.WithStack(err)
			return
		}
		c = &tls.Config{
			Certificates: certs,
		}
		return
	}

	certFileInfos, err := certFile.Readdir(0)
	if err != nil {
		err = errors.WithStack(err)
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
		var cert tls.Certificate
		cert, err = tls.LoadX509KeyPair(certPath+"/"+certFn, keyPath+"/"+keyFn)
		if err != nil {
			err = errors.WithStack(err)
			return
		}
		certs = append(certs, cert)
	}
	c = &tls.Config{
		Certificates: certs,
	}
	return
}
