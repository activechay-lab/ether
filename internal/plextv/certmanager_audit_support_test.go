package plextv

import (
	"crypto"

	"crypto/x509"

	"encoding/pem"
	"errors"
)

// keyFromPEM parses a PKCS#8/PKCS#1/EC private key (tests and Install
// callers holding PEM).
func keyFromPEM(b []byte) (crypto.Signer, error) {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("no PEM key")
	}
	if k, err := x509.ParsePKCS8PrivateKey(blk.Bytes); err == nil {
		if s, ok := k.(crypto.Signer); ok {
			return s, nil
		}
	}
	if k, err := x509.ParsePKCS1PrivateKey(blk.Bytes); err == nil {
		return k, nil
	}
	if k, err := x509.ParseECPrivateKey(blk.Bytes); err == nil {
		return k, nil
	}
	return nil, errors.New("unsupported private key")
}
