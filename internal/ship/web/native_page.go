package web

import (
	"bytes"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/DevWolk/garminctl/internal/ship/httpx"
)

// nativeIndex compiles only the immutable embedded index, before a response can
// contain untrusted content. Never apply this transformation to rendered HTML.
func nativeIndex(index []byte) (http.Handler, error) {
	source := strings.ReplaceAll(string(index), "<script ", `<script nonce="{{.}}" `)

	page, err := template.New("native index").Parse(source)
	if err != nil {
		return nil, err
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce := r.Header.Get(httpx.PageNonceHeader)
		if !httpx.ValidPageNonce(nonce) {
			http.Error(w, "invalid page nonce", http.StatusBadRequest)
			return
		}

		var body bytes.Buffer
		if err := page.Execute(&body, nonce); err != nil {
			http.Error(w, "could not render page", http.StatusInternalServerError)
			return
		}

		w.Header().Set(httpx.PageNonceHeader, nonce)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(body.Len()))
		w.Header().Set("Cache-Control", "no-store")

		if r.Method != http.MethodHead {
			_, _ = w.Write(body.Bytes())
		}
	}), nil
}
