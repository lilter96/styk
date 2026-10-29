package moysklad

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

func orderCheckJSON(raw []byte) error {
	if len(raw) == 0 || len(raw) > orderResponseLimit {
		return ErrOrderSchema
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	remaining := 100000
	if err := orderJSONValue(decoder, 0, &remaining); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrOrderSchema
	}
	return nil
}

func orderJSONValue(decoder *json.Decoder, depth int, remaining *int) error {
	*remaining--
	if depth > 64 || *remaining < 0 {
		return ErrOrderSchema
	}
	token, err := decoder.Token()
	if err != nil {
		return ErrOrderSchema
	}
	if token == json.Delim('{') {
		return orderJSONObject(decoder, depth+1, remaining)
	}
	if token == json.Delim('[') {
		return orderJSONArray(decoder, depth+1, remaining)
	}
	if _, delimiter := token.(json.Delim); delimiter {
		return ErrOrderSchema
	}
	return nil
}

func orderJSONObject(decoder *json.Decoder, depth int, remaining *int) error {
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return ErrOrderSchema
		}
		key, valid := token.(string)
		if !valid || seen[key] {
			return ErrOrderSchema
		}
		seen[key] = true
		if err = orderJSONValue(decoder, depth, remaining); err != nil {
			return err
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return ErrOrderSchema
	}
	return nil
}

func orderJSONArray(decoder *json.Decoder, depth int, remaining *int) error {
	for decoder.More() {
		if err := orderJSONValue(decoder, depth, remaining); err != nil {
			return err
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return ErrOrderSchema
	}
	return nil
}
