package tool

import (
	"bytes"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// DecodeYAMLDocument decodes exactly one YAML document and rejects unknown
// fields. Tool registry and target binding files are configuration contracts;
// accepting misspellings or trailing documents would silently change links.
func DecodeYAMLDocument(data []byte, out any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple YAML documents are not supported")
		}
		return err
	}
	return nil
}
