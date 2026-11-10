package updatetransport

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"fmt"
)

const packageSignatureSize = 3072 / 8

// ValidatePublicKey accepts only a canonical RSA-3072 SubjectPublicKeyInfo DER.
// Composition supplies this build-time pin; release metadata cannot select it.
func ValidatePublicKey(der []byte) error {
	_, err := parsePublicKey(der)
	return err
}

func parsePublicKey(der []byte) (*rsa.PublicKey, error) {
	if len(der) == 0 || len(der) > 1024 {
		return nil, errors.New("application staging requires a compiled RSA-3072 public key")
	}

	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("invalid compiled application public key: %w", err)
	}

	key, ok := parsed.(*rsa.PublicKey)
	if !ok || key.N.BitLen() != 3072 {
		return nil, errors.New("application staging requires an RSA-3072 public key")
	}

	canonical, err := x509.MarshalPKIXPublicKey(key)
	if err != nil || !bytes.Equal(der, canonical) {
		return nil, errors.New("application public key must use canonical SubjectPublicKeyInfo DER")
	}

	return key, nil
}

func verifyPackageSignature(ctx context.Context, key *rsa.PublicKey, digest, signature []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest, signature); err != nil {
		return fmt.Errorf("release package signature verification failed: %w", err)
	}

	return ctx.Err()
}
