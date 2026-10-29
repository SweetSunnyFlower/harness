package jsonvalue

import (
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

func (v Value) StringUnits() ([]uint16, error) {
	if v.Kind() != KindString {
		return nil, ErrKind
	}
	raw := v.text()
	units := make([]uint16, 0, len(raw)-2)
	for i := 1; i < len(raw)-1; {
		if raw[i] == '\\' {
			i++
			escape := raw[i]
			i++
			switch escape {
			case '"', '\\', '/':
				units = append(units, uint16(escape))
			case 'b':
				units = append(units, 8)
			case 'f':
				units = append(units, 12)
			case 'n':
				units = append(units, 10)
			case 'r':
				units = append(units, 13)
			case 't':
				units = append(units, 9)
			case 'u':
				number, err := strconv.ParseUint(raw[i:i+4], 16, 16)
				if err != nil {
					return nil, ErrJSON
				}
				units = append(units, uint16(number))
				i += 4
			default:
				return nil, ErrJSON
			}
			continue
		}
		r, width := utf8.DecodeRuneInString(raw[i : len(raw)-1])
		i += width
		if r <= 0xffff {
			units = append(units, uint16(r))
		} else {
			high, low := utf16.EncodeRune(r)
			units = append(units, uint16(high), uint16(low))
		}
	}
	return units, nil
}

func StringFromUnits(units []uint16) Value {
	const hex = "0123456789abcdef"
	var out strings.Builder
	out.WriteByte('"')
	for _, unit := range units {
		out.WriteString(`\u`)
		out.WriteByte(hex[(unit>>12)&15])
		out.WriteByte(hex[(unit>>8)&15])
		out.WriteByte(hex[(unit>>4)&15])
		out.WriteByte(hex[unit&15])
	}
	out.WriteByte('"')
	return Value{raw: out.String()}
}
