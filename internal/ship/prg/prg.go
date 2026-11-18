// Package prg reads the section table of Connect IQ .PRG application files.
package prg

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/DevWolk/garminctl/internal/ship/ciq"
)

// Section magics observed in PRG files.
const (
	MagicHeader      = 0xD000D00D
	MagicEntryPoints = 0x6060C0DE
	MagicData        = 0xDA7ABABE
	MagicCode        = 0xC0DEBABE
	MagicLinkTable   = 0xC0DE7AB1
	MagicClassDefs   = 0xC1A557B1
	MagicResources   = 0xF00D600D
	MagicPermissions = 0x6000DB01
	MagicExceptions  = 0x0ECE7105
	MagicSettings    = 0x5E771465
	MagicDevSig      = 0xE1C0DE12
	MagicStoreSig    = 0x00005161
)

// Section is one section of a PRG file.
type Section struct {
	Magic  uint32
	Offset int
	Data   []byte
}

// Sections splits a PRG file into its sections.
func Sections(b []byte) ([]Section, error) {
	var out []Section

	off := 0
	for off+8 <= len(b) {
		magic := binary.BigEndian.Uint32(b[off:])

		n := int(binary.BigEndian.Uint32(b[off+4:]))
		if magic == 0 && n == 0 {
			break
		}

		if off+8+n > len(b) {
			return out, fmt.Errorf("prg: section 0x%08X at %d exceeds file", magic, off)
		}

		out = append(out, Section{Magic: magic, Offset: off, Data: b[off+8 : off+8+n]})
		off += 8 + n
	}

	if len(out) == 0 {
		return nil, errors.New("prg: no sections")
	}

	return out, nil
}

// DefaultSettings returns the default application settings (the
// properties.xml values) stored in the SETTINGS section.
func DefaultSettings(b []byte) (ciq.Dict, error) {
	secs, err := Sections(b)
	if err != nil {
		return nil, err
	}

	for _, s := range secs {
		if s.Magic != MagicSettings {
			continue
		}

		v, err := ciq.Decode(s.Data)
		if err != nil {
			return nil, err
		}

		d, ok := v.(ciq.Dict)
		if !ok {
			return nil, fmt.Errorf("prg: settings section holds %T, not a dictionary", v)
		}

		return d, nil
	}

	return nil, errors.New("prg: no SETTINGS section")
}

// StringResources returns the string table entries of the resources
// section, in file order. Settings labels and option names live here.
func StringResources(b []byte) ([]string, error) {
	secs, err := Sections(b)
	if err != nil {
		return nil, err
	}

	var out []string

	for _, s := range secs {
		if s.Magic != MagicResources {
			continue
		}

		d := s.Data
		for i := 0; i+4 <= len(d); {
			if d[i] == 0 && d[i+1] == 1 {
				n := int(binary.BigEndian.Uint16(d[i+2:]))
				if n > 0 && i+4+n <= len(d) && validText(d[i+4:i+4+n]) {
					out = append(out, string(d[i+4:i+4+n]))
					i += 4 + n

					continue
				}
			}

			i++
		}
	}

	return out, nil
}

func validText(b []byte) bool {
	for _, c := range b {
		if c < 0x20 && c != '\n' && c != '\t' {
			return false
		}
	}

	return true
}
