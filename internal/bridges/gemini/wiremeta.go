package gemini

import "encoding/json"

func encodeWireMeta(parts []part) json.RawMessage {
	if len(parts) == 0 {
		return nil
	}
	b, err := json.Marshal(wireMetaPayload{Driver: driverID, ModelParts: parts})
	if err != nil {
		return nil
	}
	return b
}

func decodeWireMeta(raw json.RawMessage) (wireMetaPayload, bool) {
	if len(raw) == 0 {
		return wireMetaPayload{}, false
	}
	var payload wireMetaPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return wireMetaPayload{}, false
	}
	if payload.Driver != "" && payload.Driver != driverID {
		return wireMetaPayload{}, false
	}
	if len(payload.ModelParts) == 0 {
		return wireMetaPayload{}, false
	}
	return payload, true
}

func functionCallNames(parts []part) []string {
	var names []string
	for _, p := range parts {
		if p.FunctionCall != nil && p.FunctionCall.Name != "" {
			names = append(names, p.FunctionCall.Name)
		}
	}
	return names
}
