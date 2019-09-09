package config

import "crypto/tls"

type TLSParams struct {
	KeyPath  string
	CertPath string
}

func (t *TLSParams) ToCerts() (certs []tls.Certificate, err error) {
	// todo: return tlsconfig
	// todo
	return
}
