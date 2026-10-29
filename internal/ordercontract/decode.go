package ordercontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

const (
	maxPayloadBytes = 1 << 20
	maxDepth        = 64
	maxNodes        = 50000
	maxIssues       = 64
)

var errPayload = errors.New("invalid or unbounded contract payload")

type decodeBudget struct{ nodes int }

func decodePayload(data []byte) (any, error) {
	if len(data) == 0 || len(data) > maxPayloadBytes {
		return nil, errPayload
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	budget := decodeBudget{}
	value, err := readValue(decoder, &budget, 0)
	if err != nil {
		return nil, errPayload
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errPayload
	}
	return value, nil
}

func readValue(decoder *json.Decoder, budget *decodeBudget, depth int) (any, error) {
	budget.nodes++
	if depth > maxDepth || budget.nodes > maxNodes {
		return nil, errPayload
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, errPayload
	}
	if token == json.Delim('{') {
		return readObject(decoder, budget, depth+1)
	}
	if token == json.Delim('[') {
		return readArray(decoder, budget, depth+1)
	}
	if delimiter, ok := token.(json.Delim); ok && delimiter != 0 {
		return nil, errPayload
	}
	return token, nil
}

func readObject(decoder *json.Decoder, budget *decodeBudget, depth int) (map[string]any, error) {
	object := make(map[string]any)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, errPayload
		}
		key, ok := token.(string)
		if !ok {
			return nil, errPayload
		}
		if _, exists := object[key]; exists {
			return nil, errPayload
		}
		value, err := readValue(decoder, budget, depth)
		if err != nil {
			return nil, errPayload
		}
		object[key] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, errPayload
	}
	return object, nil
}

func readArray(decoder *json.Decoder, budget *decodeBudget, depth int) ([]any, error) {
	array := make([]any, 0)
	for decoder.More() {
		value, err := readValue(decoder, budget, depth)
		if err != nil {
			return nil, errPayload
		}
		array = append(array, value)
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return nil, errPayload
	}
	return array, nil
}
