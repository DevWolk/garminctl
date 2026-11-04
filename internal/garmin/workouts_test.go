package garmin

import (
	"encoding/json"
)

func sampleSpec() *WorkoutSpec {
	var w WorkoutSpec

	_ = json.Unmarshal([]byte(`{"name":"4x400 test","sport":"running","steps":[
	  {"type":"warmup","duration":"10:00"},
	  {"repeat":4,"steps":[{"type":"interval","distance":"400m","target":"pace 4:30-4:45"},{"type":"recovery","duration":"90s","target":"hr zone 2"}]},
	  {"type":"cooldown","duration":"5m","target":"hr 110-130"}]}`), &w)

	return &w
}
