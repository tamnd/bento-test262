package tc39

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Negative describes a test that must fail, with the phase the failure has to
// happen in (parse, resolution, runtime) and the name of the error constructor.
type Negative struct {
	Phase string `yaml:"phase"`
	Type  string `yaml:"type"`
}

// Meta is the parsed test262 frontmatter, the YAML block between the /*--- and
// ---*/ markers at the top of every test file.
type Meta struct {
	Description string    `yaml:"description"`
	Info        string    `yaml:"info"`
	Esid        string    `yaml:"esid"`
	Es5id       string    `yaml:"es5id"`
	Es6id       string    `yaml:"es6id"`
	Includes    []string  `yaml:"includes"`
	Flags       []string  `yaml:"flags"`
	Features    []string  `yaml:"features"`
	Negative    *Negative `yaml:"negative"`
	Locale      []string  `yaml:"locale"`
}

// HasFlag reports whether the frontmatter carries the named flag.
func (m Meta) HasFlag(name string) bool {
	for _, f := range m.Flags {
		if f == name {
			return true
		}
	}
	return false
}

// HasFeature reports whether the frontmatter lists the named feature.
func (m Meta) HasFeature(name string) bool {
	for _, f := range m.Features {
		if f == name {
			return true
		}
	}
	return false
}

const (
	metaStart = "/*---"
	metaEnd   = "---*/"
)

// ParseMeta extracts and decodes the frontmatter block from a test source. A
// file with no block returns a zero Meta, which the spec allows for a few very
// old tests.
func ParseMeta(src string) (Meta, error) {
	var m Meta
	start := strings.Index(src, metaStart)
	if start < 0 {
		return m, nil
	}
	rest := src[start+len(metaStart):]
	end := strings.Index(rest, metaEnd)
	if end < 0 {
		return m, fmt.Errorf("unterminated frontmatter block")
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &m); err != nil {
		return m, fmt.Errorf("frontmatter: %w", err)
	}
	return m, nil
}
