package gc

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // HMAC-SHA1 is the OAuth 1.0 signature method Garmin's API requires
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// oauthEscape percent-encodes per RFC 3986 section 2.1 as OAuth 1.0 requires.
func oauthEscape(s string) string {
	const hexDigits = "0123456789ABCDEF"

	var b strings.Builder

	for i := range len(s) {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&15])
		}
	}

	return b.String()
}

type kv struct{ k, v string }

// signatureBase builds the OAuth 1.0 signature base string (RFC 5849 3.4.1).
func signatureBase(method string, u *url.URL, query, form url.Values, oauth map[string]string) string {
	var params []kv

	add := func(vals url.Values) {
		for k, vs := range vals {
			for _, v := range vs {
				params = append(params, kv{oauthEscape(k), oauthEscape(v)})
			}
		}
	}
	add(query)
	add(form)

	for k, v := range oauth {
		params = append(params, kv{oauthEscape(k), oauthEscape(v)})
	}

	sort.Slice(params, func(i, j int) bool {
		if params[i].k != params[j].k {
			return params[i].k < params[j].k
		}

		return params[i].v < params[j].v
	})

	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = p.k + "=" + p.v
	}

	base := strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + u.EscapedPath()

	return strings.ToUpper(method) + "&" + oauthEscape(base) + "&" + oauthEscape(strings.Join(parts, "&"))
}

func hmacSHA1(key, msg string) string {
	h := hmac.New(sha1.New, []byte(key))
	h.Write([]byte(msg))

	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// signOAuth1 adds an OAuth 1.0 HMAC-SHA1 Authorization header. form holds
// the x-www-form-urlencoded body parameters, if any.
func signOAuth1(req *http.Request, form url.Values, consumerKey, consumerSecret, token, tokenSecret string) {
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)

	oauth := map[string]string{
		"oauth_consumer_key":     consumerKey,
		"oauth_nonce":            hex.EncodeToString(nonce),
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        strconv.FormatInt(time.Now().Unix(), 10),
		"oauth_version":          "1.0",
	}
	if token != "" {
		oauth["oauth_token"] = token
	}

	base := signatureBase(req.Method, req.URL, req.URL.Query(), form, oauth)
	sig := hmacSHA1(oauthEscape(consumerSecret)+"&"+oauthEscape(tokenSecret), base)
	oauth["oauth_signature"] = sig

	keys := make([]string, 0, len(oauth))
	for k := range oauth {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = oauthEscape(k) + `="` + oauthEscape(oauth[k]) + `"`
	}

	req.Header.Set("Authorization", "OAuth "+strings.Join(parts, ", "))
}
