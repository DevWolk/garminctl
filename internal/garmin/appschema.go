package garmin

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/DevWolk/garminctl/internal/ciq"
	"github.com/DevWolk/garminctl/internal/ciqstore"
)

// AppSettingItem is one setting with its label and current value.
type AppSettingItem struct {
	Key        string `json:"key"`
	Label      string `json:"label,omitempty"`
	Type       string `json:"type"` // select, toggle, text, number, or the raw Go type when no schema
	Value      any    `json:"value"`
	ValueLabel string `json:"valueLabel,omitempty"`
	ReadOnly   bool   `json:"readOnly,omitempty"`
	Options    int    `json:"options,omitempty"` // number of dropdown options (see schema)
}

// appSchema loads the settings definition for an app, trying the installed
// version first and the store's latest version second.
func (s *Service) appSchema(ctx context.Context, app *App) (*ciqstore.Schema, error) {
	if app.ID == "" || app.FileName == "" {
		return nil, fmt.Errorf("%s is not a Connect IQ store app", app.Name)
	}

	generation, cacheable := s.session.CacheGeneration()

	dx, err := s.DeviceXML(ctx)
	if err != nil {
		return nil, err
	}

	key := appSchemaKey{appID: strings.ToLower(app.ID), version: app.Version, partNumber: dx.PartNumber, unit: dx.UnitID}
	if cacheable {
		if sc := s.cachedAppSchema(key, generation); sc != nil {
			return sc, nil
		}
	}

	sc, err := loadAppSchema(ctx, app, dx.PartNumber)
	if err == nil && cacheable {
		s.rememberAppSchema(key, generation, sc)
	}

	return sc, err
}

func loadAppSchema(ctx context.Context, app *App, partNumber string) (*ciqstore.Schema, error) {
	var firstErr error

	versions := []int{app.Version}
	if latest, err := ciqstore.LatestVersion(ctx, app.ID); err == nil && latest != app.Version {
		versions = append(versions, latest)
	}

	for _, v := range versions {
		if v == 0 {
			continue
		}

		sc, err := ciqstore.Load(ctx, app.ID, v, partNumber)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}

			continue
		}

		return sc, nil
	}

	if firstErr == nil {
		firstErr = fmt.Errorf("no settings definition for %s", app.Name)
	}

	return nil, firstErr
}

// AppSchema returns the settings definition of an app (labels, options).
func (s *Service) AppSchema(ctx context.Context, ref string) (*ciqstore.Schema, error) {
	app, err := s.FindApp(ctx, ref)
	if err != nil {
		return nil, err
	}

	return s.appSchema(ctx, app)
}

func (s *Service) decorate(ctx context.Context, as *AppSettings) {
	sc, err := s.appSchema(ctx, &as.App)
	if err != nil {
		as.SchemaError = err.Error()
	} else {
		as.SchemaSource = sc.Source
	}

	for _, k := range as.Order {
		v := as.Values[k]

		item := AppSettingItem{Key: k, Value: v, Type: goTypeName(v)}
		if sc != nil {
			if st, ok := sc.Get(k); ok {
				item.Label, item.Type, item.ReadOnly, item.Options = st.Label, st.Type, st.ReadOnly, len(st.Options)
				if st.Type == "select" {
					item.ValueLabel = st.OptionLabel(valueString(v))
				}
			}
		}

		as.Items = append(as.Items, item)
	}
}

func goTypeName(v any) string {
	switch v.(type) {
	case bool:
		return "bool"
	case string:
		return "string"
	case float32, float64:
		return "float"
	case nil:
		return "null"
	}

	return "int"
}

func valueString(v any) string {
	switch x := v.(type) {
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return ""
	}

	return fmt.Sprint(v)
}

// convertWithSchema turns a user supplied value into the stored type using
// the schema: option labels become values, toggles accept on/off words,
// numbers are range-checked, read-only fields are rejected.
func convertWithSchema(st *ciqstore.Setting, existing, input any) (any, error) {
	if st.ReadOnly {
		return nil, fmt.Errorf("%s is read-only (informational)", st.Key)
	}

	in := valueString(input)

	switch st.Type {
	case "select":
		return convertSelect(st, existing, input, in)
	case "toggle":
		return convertToggle(st, existing, in)
	case "number":
		return convertNumber(st, existing, in)
	default:
		if st.MaxLength > 0 && len([]rune(in)) > st.MaxLength {
			return nil, fmt.Errorf("%s: longer than %d characters", st.Key, st.MaxLength)
		}

		return ciq.Coerce(existing, in)
	}
}

// convertSelect accepts an option label or one of the option values.
func convertSelect(st *ciqstore.Setting, existing, input any, in string) (any, error) {
	if _, isNum := input.(float64); !isNum {
		v, err := st.OptionValue(in)
		if err != nil {
			return nil, err
		}

		return ciq.Coerce(existing, v)
	}

	if st.OptionLabel(in) == "" {
		return nil, fmt.Errorf("%s: %s is not one of the allowed values", st.Key, in)
	}

	return ciq.Coerce(existing, in)
}

func convertToggle(st *ciqstore.Setting, existing any, in string) (any, error) {
	switch strings.ToLower(in) {
	case "true", "1", "on", "yes", "enabled":
		return ciq.Coerce(existing, true)
	case "false", "0", "off", "no", "disabled":
		return ciq.Coerce(existing, false)
	}

	return nil, fmt.Errorf("%s: expected on/off, got %q", st.Key, in)
}

func convertNumber(st *ciqstore.Setting, existing any, in string) (any, error) {
	f, err := strconv.ParseFloat(in, 64)
	if err != nil {
		return nil, fmt.Errorf("%s: number expected, got %q", st.Key, in)
	}

	if st.Min != "" {
		if mn, err := strconv.ParseFloat(st.Min, 64); err == nil && f < mn {
			return nil, fmt.Errorf("%s: %v is below the minimum %s", st.Key, f, st.Min)
		}
	}

	if st.Max != "" {
		if mx, err := strconv.ParseFloat(st.Max, 64); err == nil && f > mx {
			return nil, fmt.Errorf("%s: %v is above the maximum %s", st.Key, f, st.Max)
		}
	}

	if _, isInt := existing.(int32); isInt && f != math.Trunc(f) {
		return nil, fmt.Errorf("%s: whole number expected", st.Key)
	}

	return ciq.Coerce(existing, f)
}
