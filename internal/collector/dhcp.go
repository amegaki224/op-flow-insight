package collector

import (
	"os"
	"strings"
)

// readStaticDHCPHostnames reads the names configured in OpenWrt's static DHCP
// host sections. dnsmasq does not necessarily rewrite an existing lease when
// an administrator renames that host, so the lease file alone can keep showing
// the client-supplied old name until the next DHCP renewal.
func readStaticDHCPHostnames(path string) map[string]string {
	out := make(map[string]string)
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}

	inHost := false
	name := ""
	var macs []string
	flush := func() {
		name = strings.TrimSpace(name)
		if inHost && name != "" {
			for _, rawMAC := range macs {
				if mac := normalizeMAC(rawMAC); mac != "" {
					out[mac] = name
				}
			}
		}
		name = ""
		macs = nil
	}

	for _, line := range strings.Split(string(raw), "\n") {
		fields := splitUCIFields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "config" {
			flush()
			inHost = len(fields) >= 2 && fields[1] == "host"
			continue
		}
		if !inHost || len(fields) < 3 {
			continue
		}
		switch {
		case fields[0] == "option" && fields[1] == "name":
			name = fields[2]
		case (fields[0] == "option" || fields[0] == "list") && fields[1] == "mac":
			macs = append(macs, splitMACValues(fields[2])...)
		}
	}
	flush()
	return out
}

func splitMACValues(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', ';', ' ', '\t', '\r', '\n':
			return true
		default:
			return false
		}
	})
}

// splitUCIFields handles the quoted values emitted by UCI and ignores comments
// outside quotes. It intentionally only returns complete logical fields; DHCP
// host names and MAC addresses never require shell-style expansion.
func splitUCIFields(line string) []string {
	var out []string
	for index := 0; index < len(line); {
		for index < len(line) && (line[index] == ' ' || line[index] == '\t' || line[index] == '\r') {
			index++
		}
		if index >= len(line) || line[index] == '#' {
			break
		}

		var value strings.Builder
		quote := byte(0)
		for index < len(line) {
			char := line[index]
			if quote == 0 {
				switch char {
				case ' ', '\t', '\r':
					index++
					goto fieldDone
				case '#':
					index = len(line)
					goto fieldDone
				case '\'', '"':
					quote = char
					index++
					continue
				}
			} else if char == quote {
				quote = 0
				index++
				continue
			}
			value.WriteByte(char)
			index++
		}
	fieldDone:
		out = append(out, value.String())
	}
	return out
}
