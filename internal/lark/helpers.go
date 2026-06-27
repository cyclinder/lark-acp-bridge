package lark

import "encoding/json"

func jsonMarshal(v any) ([]byte, error)       { return json.Marshal(v) }
func jsonUnmarshal(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}
