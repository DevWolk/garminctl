package gc

import (
	"net/url"
	"testing"
)

// RFC 5849 section 3.4.1.1 example.
func TestSignatureBaseRFC5849(t *testing.T) {
	u, _ := url.Parse("http://example.com/request?b5=%3D%253D&a3=a&c%40=&a2=r%20b")
	form := url.Values{}
	form.Add("c2", "")
	form.Add("a3", "2 q")
	oauth := map[string]string{
		"oauth_consumer_key":     "9djdj82h48djs9d2",
		"oauth_token":            "kkk9d7dh3k39sjv7",
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        "137131201",
		"oauth_nonce":            "7d8f3e4a",
	}
	got := signatureBase("POST", u, u.Query(), form, oauth)
	want := "POST&http%3A%2F%2Fexample.com%2Frequest&a2%3Dr%2520b%26a3%3D2%2520q%26a3%3Da%26b5%3D%253D%25253D%26c%2540%3D%26c2%3D%26oauth_consumer_key%3D9djdj82h48djs9d2%26oauth_nonce%3D7d8f3e4a%26oauth_signature_method%3DHMAC-SHA1%26oauth_timestamp%3D137131201%26oauth_token%3Dkkk9d7dh3k39sjv7"
	if got != want {
		t.Fatalf("base string mismatch\n got %s\nwant %s", got, want)
	}
}

func TestOAuthEscape(t *testing.T) {
	if got := oauthEscape("a b~-._/?é"); got != "a%20b~-._%2F%3F%C3%A9" {
		t.Fatal(got)
	}
}
