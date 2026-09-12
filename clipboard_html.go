package main

import (
	stdhtml "html"
	"strings"
)

func decodeHTMLClipboardText(data []byte) string {
	raw := strings.TrimRight(string(data), "\x00")
	const startMarker = "<!--StartFragment-->"
	const endMarker = "<!--EndFragment-->"
	if start := strings.Index(raw, startMarker); start >= 0 {
		raw = raw[start+len(startMarker):]
		if end := strings.Index(raw, endMarker); end >= 0 {
			raw = raw[:end]
		}
	} else if start := strings.Index(strings.ToLower(raw), "<html"); start >= 0 {
		raw = raw[start:]
	}

	blockTags := map[string]bool{
		"address": true, "blockquote": true, "div": true, "h1": true, "h2": true, "h3": true,
		"h4": true, "h5": true, "h6": true, "li": true, "p": true, "pre": true, "tr": true,
	}
	var out strings.Builder
	skipTag := ""
	writeBreak := func() {
		value := out.String()
		if value != "" && !strings.HasSuffix(value, "\n") {
			out.WriteByte('\n')
		}
	}
	for i := 0; i < len(raw); {
		if raw[i] != '<' {
			end := strings.IndexByte(raw[i:], '<')
			if end < 0 {
				end = len(raw) - i
			}
			if skipTag == "" {
				text := stdhtml.UnescapeString(raw[i : i+end])
				text = strings.ReplaceAll(text, "\u00a0", " ")
				out.WriteString(text)
			}
			i += end
			continue
		}
		end := strings.IndexByte(raw[i:], '>')
		if end < 0 {
			break
		}
		tag := strings.TrimSpace(strings.ToLower(raw[i+1 : i+end]))
		closing := strings.HasPrefix(tag, "/")
		tag = strings.TrimLeft(tag, "/")
		if space := strings.IndexAny(tag, " \t\r\n/"); space >= 0 {
			tag = tag[:space]
		}
		if tag == "script" || tag == "style" {
			if closing && skipTag == tag {
				skipTag = ""
			} else if !closing && skipTag == "" {
				skipTag = tag
			}
		} else if skipTag == "" && (tag == "br" || closing && blockTags[tag]) {
			writeBreak()
		}
		i += end + 1
	}

	text := strings.ReplaceAll(out.String(), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			if len(cleaned) > 0 && !blank {
				cleaned = append(cleaned, "")
				blank = true
			}
			continue
		}
		cleaned = append(cleaned, line)
		blank = false
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}
