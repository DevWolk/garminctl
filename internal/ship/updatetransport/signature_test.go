package updatetransport_test

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

const stageTestKeyEnv = "GARMINCTL_SYNTHETIC_STAGE_TEST_KEY"

var (
	stagePrivateKey *rsa.PrivateKey
	stagePublicKey  []byte
	otherTestKey    = sync.OnceValues(func() ([]byte, error) {
		key, err := rsa.GenerateKey(rand.Reader, 3072)
		if err != nil {
			return nil, err
		}

		return x509.MarshalPKIXPublicKey(&key.PublicKey)
	})
)

// Only the test executable shares its ephemeral key with its test children.
// Production worker composition never reads this variable.
func initializeStageTestKey() error {
	encoded := os.Getenv(stageTestKeyEnv)
	if encoded == "" {
		key, err := rsa.GenerateKey(rand.Reader, 3072)
		if err != nil {
			return err
		}

		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return err
		}

		encoded = base64.StdEncoding.EncodeToString(der)
		if err := os.Setenv(stageTestKeyEnv, encoded); err != nil {
			return err
		}
	}

	der, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return err
	}

	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return err
	}

	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return errors.New("synthetic stage key is not RSA")
	}

	stagePrivateKey = key
	stagePublicKey, err = x509.MarshalPKIXPublicKey(&key.PublicKey)

	return err
}

func stageTestPublicKey() []byte { return bytes.Clone(stagePublicKey) }

func stageTestSign(data []byte) ([]byte, error) {
	digest := sha256.Sum256(data)
	return rsa.SignPKCS1v15(nil, stagePrivateKey, crypto.SHA256, digest[:])
}

func otherStagePublicKey(t *testing.T) []byte {
	t.Helper()

	key, err := otherTestKey()
	if err != nil {
		t.Fatal(err)
	}

	return bytes.Clone(key)
}

func TestValidatePublicKeyRejectsUnsupportedAndLegacyFormats(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		key   []byte
		valid bool
	}{
		{name: "RSA-3072 SPKI", key: stageTestPublicKey(), valid: true},
		{name: "missing"},
		{name: "raw Ed25519", key: bytes.Repeat([]byte{1}, 32)},
		{name: "trailing DER", key: append(stageTestPublicKey(), 0)},
		{name: "oversized", key: bytes.Repeat([]byte{1}, 1025)},
		{name: "PKCS1 instead of SPKI", key: x509.MarshalPKCS1PublicKey(&stagePrivateKey.PublicKey)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := updatetransport.ValidatePublicKey(tc.key); (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
		})
	}
}
