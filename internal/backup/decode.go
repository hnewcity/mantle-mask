package backup

import (
	"encoding/json"
	"errors"
)

// DecodeObject walks one JSON object member by member, so a large document can
// be decoded without buffering it whole. field must consume exactly one value
// from dec for name. A JSON null is treated as an object without members, as
// encoding/json does when decoding null into a struct.
func DecodeObject(dec *json.Decoder, field func(name string) error) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if t == nil {
		return nil
	}
	if t != json.Delim('{') {
		return errors.New("expected a JSON object")
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if err := field(t.(string)); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

// DecodeArray calls item once per element of a JSON array; item must consume
// exactly one value. It reports whether the value was null instead.
func DecodeArray(dec *json.Decoder, item func() error) (null bool, err error) {
	t, err := dec.Token()
	if err != nil {
		return false, err
	}
	if t == nil {
		return true, nil
	}
	if t != json.Delim('[') {
		return false, errors.New("expected a JSON array")
	}
	for dec.More() {
		if err := item(); err != nil {
			return false, err
		}
	}
	_, err = dec.Token()
	return false, err
}
