package tools

// objectSchema builds a JSON Schema object node. It mirrors typebox's plain
// Type.Object({...}) with no options: no additionalProperties key is emitted.
// bash/find/grep/ls/read/write use this form (see their *.ts counterparts).
func objectSchema(props map[string]any, required []string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// strictObjectSchema mirrors typebox Type.Object({...}, {additionalProperties:
// false}). Only the edit tool uses this strict form (edit.ts:41,52).
func strictObjectSchema(props map[string]any, required []string) map[string]any {
	s := objectSchema(props, required)
	s["additionalProperties"] = false
	return s
}

func stringSchema(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func numberSchema(desc string) map[string]any {
	return map[string]any{"type": "number", "description": desc}
}
func boolSchema(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
