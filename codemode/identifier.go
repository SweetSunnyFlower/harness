package codemode

// ToCodemodeIdentifier maps an arbitrary tool name to the JavaScript
// identifier the sandbox exposes, matching pi's toCodemodeIdentifier:
// characters that are not valid in an identifier become "_".
func ToCodemodeIdentifier(name string) string {
	var out []byte
	for i := 0; i < len(name); i++ {
		c := name[i]
		valid := c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if i > 0 {
			valid = valid || (c >= '0' && c <= '9')
		}
		if valid {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "_"
	}
	return string(out)
}
