//go:build desktop

package app_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/DevWolk/garminctl/internal/app"
	applicationnative "github.com/DevWolk/garminctl/internal/containers/application/ui/native"
)

type unavailableNativeScope struct{ reads int }

func (*unavailableNativeScope) Acquire() (context.Context, func(), error) {
	return nil, nil, context.Canceled
}

func (s *unavailableNativeScope) Backend() (string, bool, error) {
	s.reads++
	return "", false, errors.New("not started")
}

func TestApplicationNativeCompositionRegistersConcreteReadServiceWithoutIO(t *testing.T) {
	t.Parallel()

	scope := &unavailableNativeScope{}
	binding := app.NewApplicationNativeService("v1.2.3", "", scope)

	service, ok := binding.Instance().(*applicationnative.Service)
	if !ok {
		t.Fatalf("binding=%T", binding.Instance())
	}

	typ := reflect.TypeOf(service).Elem()
	if prefix := typ.PkgPath() + "." + typ.Name(); prefix != "github.com/DevWolk/garminctl/internal/containers/application/ui/native.Service" {
		t.Fatalf("native method prefix=%s", prefix)
	}

	if _, err := service.Check(false); !errors.Is(err, context.Canceled) {
		t.Fatalf("check=%v", err)
	}

	if scope.reads != 0 {
		t.Fatalf("constructor or rejected call inspected backend %d times", scope.reads)
	}
}
