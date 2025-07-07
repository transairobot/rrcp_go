package robot

import (
	"crypto/tls"
)

func loadCert(certFile, privateFile string) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certFile, privateFile)
	if err != nil {
		return tls.Certificate{}, err
	}

	return cert, nil
}
