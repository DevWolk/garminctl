package garmin

import (
	_ "embed"
	"encoding/json"
	"fmt"
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

// AppHintsFor returns hints for a store app id, or nil.
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
	return appHintsVal[strings.ToLower(appID)]
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

// ManagedByConnect reports whether Garmin Connect re-sends this field on
// every sync (so local changes get overwritten).
func (k *Knowledge) ManagedByConnect(mesg uint16, field uint8) bool {
	if k.managed == nil {
		k.managed = map[string]bool{}
		for _, key := range k.ConnectManaged {
			k.managed[key] = true
		}
	}
	return k.managed[fmt.Sprintf("%d.%d", mesg, field)]
}

var (
	knowledgeOnce sync.Once
	knowledgeVal  *Knowledge
)

// DefaultKnowledge returns the embedded notes.
func DefaultKnowledge() *Knowledge {
	knowledgeOnce.Do(func() {
		var k Knowledge
		if err := json.Unmarshal(deviceSettingsKnowledge, &k); err != nil {
			panic("knowledge/device_settings.json: " + err.Error())
		}
		knowledgeVal = &k
	})
	return knowledgeVal
}
