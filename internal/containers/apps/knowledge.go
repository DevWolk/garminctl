package apps

import (
	_ "embed"
	"encoding/json"
	"maps"
	"strings"
	"sync"
)

//go:embed knowledge/ciq_apps.json
var ciqAppsKnowledge []byte

// AppHints are notes about one Connect IQ app's settings.
type AppHints struct {
	Name       string            `json:"name"`
	ValueHints map[string]string `json:"valueHints,omitempty"`
	KeyHints   map[string]string `json:"keyHints,omitempty"`
}

var (
	appHintsOnce sync.Once
	appHintsVal  map[string]*AppHints
)

// AppHintsFor returns an independent hint snapshot for a store app id, or nil.
func AppHintsFor(appID string) *AppHints {
	appHintsOnce.Do(func() {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(ciqAppsKnowledge, &raw); err != nil {
			panic("knowledge/ciq_apps.json: " + err.Error())
		}

		appHintsVal = map[string]*AppHints{}

		for k, v := range raw {
			if strings.HasPrefix(k, "_") {
				continue
			}

			var h AppHints
			if err := json.Unmarshal(v, &h); err != nil {
				panic("knowledge/ciq_apps.json: " + k + ": " + err.Error())
			}

			appHintsVal[strings.ToLower(k)] = &h
		}
	})

	h := appHintsVal[strings.ToLower(appID)]
	if h == nil {
		return nil
	}

	hints := *h
	hints.ValueHints = maps.Clone(h.ValueHints)
	hints.KeyHints = maps.Clone(h.KeyHints)

	return &hints
}
