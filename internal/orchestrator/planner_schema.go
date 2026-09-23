package orchestrator

import (
	"encoding/json"
	"reflect"
	"strings"
)

// Field shape comes from the decoded contract, not a second hand-maintained
// text example. Validation still owns semantic constraints and authority.
func plannerJSONSchema() json.RawMessage {
	var shape func(reflect.Type) map[string]any
	shape = func(t reflect.Type) map[string]any {
		switch t.Kind() {
		case reflect.Struct:
			props := map[string]any{}
			required := []string{}
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				tag := strings.Split(f.Tag.Get("json"), ",")
				if tag[0] == "-" {
					continue
				}
				props[tag[0]] = shape(f.Type)
				if len(tag) == 1 {
					required = append(required, tag[0])
				}
			}
			return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
		case reflect.Slice:
			return map[string]any{"type": "array", "items": shape(t.Elem())}
		case reflect.Bool:
			return map[string]any{"type": "boolean"}
		case reflect.Int, reflect.Int64:
			return map[string]any{"type": "integer"}
		default:
			return map[string]any{"type": "string"}
		}
	}
	raw, _ := json.Marshal(shape(reflect.TypeOf(ModelPlan{})))
	return raw
}
