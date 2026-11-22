package yamlfile

import (
	"bytes"
	"errors"
	"io"

	"go.yaml.in/yaml/v3"
)

// DecodeStrict decodes YAML into v by its yaml tags, keeping v's values for
// absent fields; unknown fields are an error (a typo, or a retired field).
// in is the YAML text ([]byte), or a value as read from YAML (a section, a
// node), which is encoded first. Empty input changes nothing.
func DecodeStrict(in, v any) error {
	raw, ok := in.([]byte)
	if !ok {
		var err error
		if raw, err = yaml.Marshal(in); err != nil {
			return err
		}
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
