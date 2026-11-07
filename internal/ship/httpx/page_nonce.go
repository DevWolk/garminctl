package httpx

import "encoding/base64"

// PageNonceHeader requests and acknowledges nonce rendering of an embedded page.
// It is a rendering contract, not authentication or permission to call an API.
const PageNonceHeader = "X-Garminctl-Page-Nonce"

// ValidPageNonce accepts a canonical base64 representation of 192 random bits.
func ValidPageNonce(nonce string) bool {
	if len(nonce) != 32 {
		return false
	}

	value, err := base64.StdEncoding.Strict().DecodeString(nonce)

	return err == nil && len(value) == 24 && base64.StdEncoding.EncodeToString(value) == nonce
}
