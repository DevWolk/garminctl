//go:build desktop

package native_test

import (
	"reflect"
	"testing"

	applicationnative "github.com/DevWolk/garminctl/internal/containers/application/ui/native"
)

func TestNativeApplicationBindingHasOnlyReadsAndPreparationMethods(t *testing.T) {
	t.Parallel()

	binding := applicationnative.Binding(&windowFixture{}, nil)

	typ := reflect.TypeOf(binding.Instance())
	if typ != reflect.TypeFor[*applicationnative.Service]() {
		t.Fatalf("binding=%v", typ)
	}

	if typ.NumMethod() != 6 {
		t.Fatalf("methods=%d", typ.NumMethod())
	}

	for _, name := range []string{"Info", "History", "Check", "StartDownload", "DownloadStatus", "CancelDownload"} {
		if _, ok := typ.MethodByName(name); !ok {
			t.Fatalf("missing %s", name)
		}
	}
}
