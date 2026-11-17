package web

import (
	"io/fs"
	"path"
	"regexp"
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

// cssReference matches the targets of @import and url() in a stylesheet.
var cssReference = regexp.MustCompile(`(?i)@import\s+(?:url\(\s*)?["']?([^"'()\s;]+)|url\(\s*["']?([^"')\s]+)`)

func TestUIStylesheetsLoadOnlyEmbeddedFiles(t *testing.T) {
	t.Parallel()

	sheets := 0

	err := fs.WalkDir(uiFS, "ui", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || path.Ext(name) != ".css" {
			return err
		}

		sheets++

		data, err := uiFS.ReadFile(name)
		if err != nil {
			return err
		}

		for _, match := range cssReference.FindAllSubmatch(data, -1) {
			checkStylesheetReference(t, name, string(match[1])+string(match[2]))
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if sheets == 0 {
		t.Fatal("no embedded stylesheet was checked")
	}
}

func checkStylesheetReference(t *testing.T, sheet, target string) {
	t.Helper()

	// Fragments and inline data need no request.
	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "data:") {
		return
	}

	if strings.Contains(target, ":") || strings.HasPrefix(target, "//") {
		t.Errorf("%s must not wait for an external server: %s", sheet, target)
		return
	}

	file := path.Join(path.Dir(sheet), target)
	if strings.HasPrefix(target, "/") {
		file = path.Join("ui", target)
	}

	file, _, _ = strings.Cut(file, "?")
	file, _, _ = strings.Cut(file, "#")

	if data, err := uiFS.ReadFile(file); err != nil || len(data) == 0 {
		t.Errorf("%s refers to %s, which is missing or empty: %v", sheet, target, err)
	}
}
