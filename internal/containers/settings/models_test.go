package settings

import (
	"encoding/json"
	"testing"
)

func TestSettingsDTOJSONContract(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		value any
		want  string
	}{
		{name: "row zero", value: Row{}, want: `{"mesg":"","mesgNum":0,"index":0,"field":"","fieldNum":0,"known":false,"value":null,"raw":null}`},
		{name: "row empty options", value: Row{Options: []string{}}, want: `{"mesg":"","mesgNum":0,"index":0,"field":"","fieldNum":0,"known":false,"value":null,"raw":null}`},
		{name: "change zero", value: Change{}, want: `{"mesg":"","field":"","value":null}`},
		{name: "resolved zero", value: ResolvedChange{}, want: `{"mesg":"","field":"","value":null,"mesgNum":0,"fieldNum":0,"raw":0}`},
		{name: "resolved previous zero", value: ResolvedChange{Raw: -1, Previous: 0}, want: `{"mesg":"","field":"","value":null,"mesgNum":0,"fieldNum":0,"raw":-1,"previous":0}`},
		{name: "result nil", value: ApplyResult{}, want: `{"path":"","bytes":0,"changes":null,"note":""}`},
		{name: "result empty", value: ApplyResult{Changes: []ResolvedChange{}}, want: `{"path":"","bytes":0,"changes":[],"note":""}`},
		{name: "schema zero", value: SchemaField{}, want: `{"num":0,"name":"","type":"","base":"","array":false}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(test.value)
			if err != nil || string(data) != test.want {
				t.Fatalf("JSON=%s error=%v; want %s", data, err, test.want)
			}
		})
	}
}
