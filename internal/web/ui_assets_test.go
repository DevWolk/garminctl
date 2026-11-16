package web

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestUIStartupAssetsAreEmbedded(t *testing.T) {
	t.Parallel()

	page, err := uiFS.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}

	root, err := html.Parse(strings.NewReader(string(page)))
	if err != nil {
		t.Fatal(err)
	}

	for node := range root.Descendants() {
		if node.Type != html.ElementNode {
			continue
		}

		var attribute string

		switch node.Data {
		case "script", "img":
			attribute = "src"
		case "link":
			attribute = "href"
		default:
			continue
		}

		for _, attr := range node.Attr {
			if attr.Key == attribute && attr.Val != "" {
				checkEmbeddedUIAsset(t, attr.Val)
			}
		}
	}
}

func checkEmbeddedUIAsset(t *testing.T, source string) {
	t.Helper()

	if !strings.HasPrefix(source, "/") || strings.HasPrefix(source, "//") {
		t.Errorf("startup asset must not wait for an external server: %s", source)
		return
	}

	data, err := uiFS.ReadFile("ui" + strings.SplitN(source, "?", 2)[0])
	if err != nil || len(data) == 0 {
		t.Errorf("startup asset %s is missing or empty: %v", source, err)
	}
}
