package toolrepair

import (
	"encoding/json"
	"strings"

	"github.com/benjaminwestern/agentic-control/internal/idgen"
	"github.com/benjaminwestern/agentic-control/pkg/toolrepair/jsonrepair"
)

// ToolCall represents an extracted tool call.
type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
	Raw       string
	Error     error
}

// ExtractToolCalls attempts to parse known tool call formats from raw text.
func ExtractToolCalls(content string) []ToolCall {
	// 1. Try Gemma format: call:name{args}
	gemmaCalls := parseGemma(content)
	if len(gemmaCalls) > 0 {
		return gemmaCalls
	}

	// 2. Try Qwen XML format: <function=name>...</function>
	qwenCalls := parseQwenXML(content)
	if len(qwenCalls) > 0 {
		return qwenCalls
	}

	// 3. Try GPT OSS format: .NAME <|message|>{args}
	gptCalls := parseGPTToolCall(content)
	if len(gptCalls) > 0 {
		return gptCalls
	}

	// 4. Fallback to generic JSON blocks
	return parseJSONToolCall(content)
}

func parseJSONToolCall(content string) []ToolCall {
	var toolCalls []ToolCall

	remaining := content
	for len(remaining) > 0 {
		remaining = strings.TrimLeft(remaining, " \t\n\r")
		if len(remaining) == 0 {
			break
		}

		if remaining[0] != '{' {
			idx := strings.Index(remaining, "{")
			if idx == -1 {
				break
			}
			remaining = remaining[idx:]
		}

		jsonEnd := findJSONObjectEnd(remaining)
		if jsonEnd == -1 {
			jsonEnd = len(remaining)
		}

		call := remaining[:jsonEnd]
		remaining = remaining[jsonEnd:]

		var fn struct {
			Name      string `json:"name"`
			Arguments any    `json:"arguments"`
		}

		err := jsonrepair.Unmarshal(call, &fn)
		if err != nil {
			continue // Not a valid tool call, skip
		}

		if fn.Name == "" {
			continue // Generic JSON, not a tool call
		}

		var args map[string]any
		switch a := fn.Arguments.(type) {
		case map[string]any:
			args = a
		case string:
			_ = jsonrepair.Unmarshal(a, &args)
		}

		toolCalls = append(toolCalls, ToolCall{
			ID:        newToolCallID(),
			Name:      strings.TrimPrefix(fn.Name, "."),
			Arguments: args,
			Raw:       call,
			Error:     err,
		})
	}

	return toolCalls
}

func parseGPTToolCall(content string) []ToolCall {
	var jsonCalls []string
	remaining := content

	for {
		dotIdx := strings.Index(remaining, ".")
		if dotIdx == -1 {
			break
		}

		remaining = remaining[dotIdx:]

		msgIdx := strings.Index(remaining, "<|message|>")
		if msgIdx == -1 {
			break
		}

		prefix := remaining[:msgIdx]
		parts := strings.SplitN(prefix, " ", 2)
		name := strings.TrimPrefix(parts[0], ".")

		jsonStart := msgIdx + 11
		remaining = remaining[jsonStart:]

		jsonEnd := findJSONObjectEnd(remaining)
		if jsonEnd == -1 {
			jsonEnd = len(remaining)
		}

		args := remaining[:jsonEnd]
		remaining = remaining[jsonEnd:]

		jsonCall := `{"name":"` + name + `","arguments":` + args + `}`
		jsonCalls = append(jsonCalls, jsonCall)
	}

	if len(jsonCalls) == 0 {
		return nil
	}
	return parseJSONToolCall(strings.Join(jsonCalls, "\n"))
}

func parseQwenXML(content string) []ToolCall {
	var toolCalls []ToolCall

	for {
		funcStart := strings.Index(content, "<function=")
		if funcStart == -1 {
			break
		}

		funcEnd := strings.Index(content[funcStart:], ">")
		if funcEnd == -1 {
			break
		}

		name := strings.TrimSpace(content[funcStart+10 : funcStart+funcEnd])

		bodyStart := funcStart + funcEnd + 1
		closeFunc := strings.Index(content[bodyStart:], "</function>")
		if closeFunc == -1 {
			break
		}
		closeFunc += bodyStart

		funcBody := content[bodyStart:closeFunc]
		args := make(map[string]any)

		remaining := funcBody
		for {
			paramStart := strings.Index(remaining, "<parameter=")
			if paramStart == -1 {
				break
			}

			paramNameEnd := strings.Index(remaining[paramStart:], ">")
			if paramNameEnd == -1 {
				break
			}

			paramName := strings.TrimSpace(remaining[paramStart+11 : paramStart+paramNameEnd])

			valueStart := paramStart + paramNameEnd + 1
			paramCloseRel := strings.Index(remaining[valueStart:], "</parameter>")
			if paramCloseRel == -1 {
				break
			}
			paramClose := valueStart + paramCloseRel

			paramValue := strings.TrimSpace(remaining[valueStart:paramClose])

			switch {
			case len(paramValue) == 0:
				args[paramName] = paramValue

			case paramValue[0] == '{', paramValue[0] == '[', paramValue[0] == '"':
				args[paramName] = paramValue
				var parsed any
				if err := json.Unmarshal([]byte(paramValue), &parsed); err == nil {
					args[paramName] = parsed
				}
			default:
				var parsed any
				if err := json.Unmarshal([]byte(paramValue), &parsed); err == nil {
					args[paramName] = parsed
				} else {
					args[paramName] = paramValue
				}
			}

			remaining = remaining[paramClose+12:]
		}

		toolCalls = append(toolCalls, ToolCall{
			ID:        newToolCallID(),
			Name:      name,
			Arguments: args,
			Raw:       content[funcStart : closeFunc+11],
		})

		content = content[closeFunc+11:]
	}

	return toolCalls
}

func parseGemma(content string) []ToolCall {
	var toolCalls []ToolCall

	remaining := content
	for {
		callIdx := strings.Index(remaining, "call:")
		if callIdx == -1 {
			break
		}

		start := remaining[callIdx:]
		remaining = remaining[callIdx+5:]

		braceIdx := strings.Index(remaining, "{")
		if braceIdx == -1 {
			break
		}

		name := strings.TrimSpace(remaining[:braceIdx])
		remaining = remaining[braceIdx:]

		braceEnd := findGemmaBraceEnd(remaining)

		var argsRaw string
		if braceEnd == -1 {
			argsRaw = remaining[1:]
			remaining = ""
		} else {
			argsRaw = remaining[1:braceEnd]
			remaining = remaining[braceEnd+1:]
		}

		var args map[string]any
		trimmed := strings.TrimSpace(argsRaw)

		jsonCandidate := trimmed
		if len(jsonCandidate) > 0 && jsonCandidate[0] != '{' {
			jsonCandidate = "{" + jsonCandidate + "}"
		}

		if err := json.Unmarshal([]byte(jsonCandidate), &args); err != nil {
			inner := trimmed
			if len(inner) > 0 && inner[0] == '{' {
				inner = inner[1:]
				if idx := strings.LastIndex(inner, "}"); idx >= 0 {
					inner = inner[:idx]
				}
			}
			args = parseGemmaArgs(inner)
		}

		toolCalls = append(toolCalls, ToolCall{
			ID:        newToolCallID(),
			Name:      name,
			Arguments: args,
			Raw:       start[:len(start)-len(remaining)],
		})
	}

	return toolCalls
}

func findJSONObjectEnd(s string) int {
	if len(s) == 0 || s[0] != '{' {
		idx := strings.Index(s, "{")
		if idx == -1 {
			return -1
		}
		s = s[idx:]
	}

	depth := 0
	inString := false
	escape := false

	for i, c := range s {
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inString {
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}

	return -1
}

func findGemmaBraceEnd(s string) int {
	if len(s) == 0 || s[0] != '{' {
		return -1
	}

	useJSONQuotes := !strings.Contains(s, "<|\"|>")

	depth := 0
	i := 0
	for i < len(s) {
		if strings.HasPrefix(s[i:], "<|\"|>") {
			i += len("<|\"|>")
			for i < len(s) {
				if strings.HasPrefix(s[i:], "<|\"|>") {
					i += len("<|\"|>")
					break
				}
				i++
			}
			continue
		}

		if useJSONQuotes && s[i] == '"' {
			i++
			for i < len(s) {
				if s[i] == '\\' {
					i += 2
					continue
				}
				if s[i] == '"' {
					i++
					break
				}
				i++
			}
			continue
		}

		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}

	return -1
}

func findClosingGemmaQuote(s string) int {
	const token = "<|\"|>"
	searchFrom := 0

	for {
		idx := strings.Index(s[searchFrom:], token)
		if idx == -1 {
			return -1
		}

		pos := searchFrom + idx
		afterQuote := pos + len(token)

		if afterQuote >= len(s) {
			return pos
		}

		switch s[afterQuote] {
		case ',', '}', ']', '"':
			return pos
		}

		searchFrom = afterQuote
	}
}

func findGemmaStructEnd(s string) int {
	if len(s) == 0 {
		return -1
	}

	open := s[0]
	var close byte
	switch open {
	case '[':
		close = ']'
	case '{':
		close = '}'
	default:
		return -1
	}

	depth := 0
	i := 0
	for i < len(s) {
		if strings.HasPrefix(s[i:], "<|\"|>") {
			i += len("<|\"|>")
			continue
		}

		switch s[i] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i + 1
			}
		}
		i++
	}

	return -1
}

func findClosingStandardQuote(s string) int {
	searchFrom := 0

	for {
		idx := strings.Index(s[searchFrom:], "\"")
		if idx == -1 {
			return -1
		}

		pos := searchFrom + idx

		if pos > 0 && s[pos-1] == '\\' {
			searchFrom = pos + 1
			continue
		}

		afterQuote := pos + 1

		if afterQuote >= len(s) {
			return pos
		}

		next := s[afterQuote]
		if next == ',' || next == '}' || next == ' ' || next == '\n' || next == '\r' || next == '\t' {
			return pos
		}

		searchFrom = afterQuote
	}
}

func parseGemmaArgs(raw string) map[string]any {
	args := make(map[string]any)

	remaining := raw
	for len(remaining) > 0 {
		colonIdx := strings.Index(remaining, ":")
		if colonIdx == -1 {
			break
		}

		key := strings.TrimLeft(remaining[:colonIdx], ", \t\n")
		key = strings.Trim(key, "\"")
		remaining = remaining[colonIdx+1:]

		if strings.HasPrefix(remaining, "<|\"|>") {
			remaining = remaining[len("<|\"|>"):]

			endQuote := findClosingGemmaQuote(remaining)
			if endQuote == -1 {
				args[key] = strings.TrimSpace(remaining)
				break
			}

			value := remaining[:endQuote]

			trimVal := strings.TrimSpace(value)
			if len(trimVal) > 0 && (trimVal[0] == '[' || trimVal[0] == '{') {
				jsonVal := strings.ReplaceAll(trimVal, "<|\"|>", "\"")
				var parsed any
				if err := json.Unmarshal([]byte(jsonVal), &parsed); err == nil {
					args[key] = parsed
					remaining = remaining[endQuote+len("<|\"|>"):]
					continue
				}
			}

			args[key] = value
			remaining = remaining[endQuote+len("<|\"|>"):]
			continue
		}

		if strings.HasPrefix(remaining, "\"") {
			remaining = remaining[1:]

			endQuote := findClosingStandardQuote(remaining)
			if endQuote == -1 {
				args[key] = strings.TrimSpace(remaining)
				break
			}

			args[key] = remaining[:endQuote]
			remaining = remaining[endQuote+1:]
			continue
		}

		if len(remaining) > 0 && (remaining[0] == '[' || remaining[0] == '{') {
			endIdx := findGemmaStructEnd(remaining)
			if endIdx == -1 {
				args[key] = strings.TrimSpace(remaining)
				break
			}

			raw := remaining[:endIdx]
			jsonVal := strings.ReplaceAll(raw, "<|\"|>", "\"")

			var parsed any
			if err := json.Unmarshal([]byte(jsonVal), &parsed); err == nil {
				args[key] = parsed
			} else {
				args[key] = raw
			}

			remaining = remaining[endIdx:]
			continue
		}

		endIdx := strings.IndexAny(remaining, ",}")
		var rawVal string
		if endIdx == -1 {
			rawVal = strings.TrimSpace(remaining)
		} else {
			rawVal = strings.TrimSpace(remaining[:endIdx])
		}

		args[key] = rawVal

		if endIdx == -1 {
			break
		}
		remaining = remaining[endIdx:]
	}

	return args
}

func newToolCallID() string {
	return idgen.NewPrefixed("call_")
}
