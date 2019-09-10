package app

import "crypto/tls"

type TLSParams struct {
	KeyPath  string
	CertPath string
}

func (t *TLSParams) Config() (c *tls.Config, err error) {
	if t == nil {
		return
	}
	// todo: return tlsconfig
	// todo
	return
}
