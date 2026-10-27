package garmin

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

//go:embed knowledge/device_settings.json
var deviceSettingsKnowledge []byte

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

// Note is a hand-maintained note about an undocumented message or field.
type Note struct {
	Name       string `json:"name,omitempty"`
	Note       string `json:"note,omitempty"`
	Confidence string `json:"confidence,omitempty"`
}

// Knowledge holds notes keyed by "<mesg>" and "<mesg>.<field>".
type Knowledge struct {
	Messages       map[string]Note `json:"messages"`
	Fields         map[string]Note `json:"fields"`
	ConnectManaged []string        `json:"connectManaged"`
	managed        map[string]bool
}

// ManagedByConnect reports whether the field is in the Connect-managed set.
func (k *Knowledge) ManagedByConnect(mesg uint16, field uint8) bool {
	key := fmt.Sprintf("%d.%d", mesg, field)
	if k.managed != nil {
		return k.managed[key]
	}

	// Caller-created or decoded snapshots have no index. Lookups stay pure;
	// deriving a map here would race when the same snapshot is read concurrently.
	return slices.Contains(k.ConnectManaged, key)
}

var (
	knowledgeOnce sync.Once
	knowledgeVal  *Knowledge
)

// DefaultKnowledge returns an independent snapshot of the embedded notes.
func DefaultKnowledge() *Knowledge {
	return defaultKnowledge().snapshot()
}

func defaultKnowledge() *Knowledge {
	knowledgeOnce.Do(func() {
		var k Knowledge
		if err := json.Unmarshal(deviceSettingsKnowledge, &k); err != nil {
			panic("knowledge/device_settings.json: " + err.Error())
		}

		k.managed = make(map[string]bool, len(k.ConnectManaged))
		for _, key := range k.ConnectManaged {
			k.managed[key] = true
		}

		knowledgeVal = &k
	})

	return knowledgeVal
}

func (k *Knowledge) snapshot() *Knowledge {
	return &Knowledge{
		Messages: maps.Clone(k.Messages), Fields: maps.Clone(k.Fields),
		ConnectManaged: slices.Clone(k.ConnectManaged),
	}
}
