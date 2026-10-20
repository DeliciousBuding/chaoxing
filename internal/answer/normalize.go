package answer

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

func Normalize(q Question, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("empty answer")
	}
	switch q.Type {
	case "single":
		return normalizeSingle(q, value)
	case "multiple":
		return normalizeMultiple(q, value)
	case "judgement":
		return normalizeJudgement(value)
	case "completion":
		return normalizeLines(value), nil
	case "shortanswer", "unknown":
		return normalizeLines(value), nil
	default:
		return normalizeLines(value), nil
	}
}

func normalizeSingle(q Question, value string) (string, error) {
	letters := extractLetters(value)
	if len(letters) == 1 && validLetter(letters, q.Options) {
		return letters, nil
	}
	if letter, ok := matchOptionLetter(q.Options, value); ok {
		return letter, nil
	}
	if len(letters) > 0 && validLetter(string(letters[0]), q.Options) {
		return string(letters[0]), nil
	}
	return "", fmt.Errorf("single-choice answer has no valid option letter")
}

func normalizeMultiple(q Question, value string) (string, error) {
	letters := extractLetters(value)
	if len(letters) == 0 {
		var matched []string
		for _, part := range splitAnswer(value) {
			if letter, ok := matchOptionLetter(q.Options, part); ok {
				matched = append(matched, letter)
			}
		}
		letters = strings.Join(uniqueSorted(matched), "")
	}
	valid := ""
	for _, letter := range letters {
		if validLetter(string(letter), q.Options) {
			valid += string(letter)
		}
	}
	if len(valid) == 0 {
		return "", fmt.Errorf("multiple-choice answer has no valid option letters")
	}
	return uniqueSortedString(valid), nil
}

func normalizeJudgement(value string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(value))
	switch {
	case containsAny(v, "正确", "对", "true", "√", "yes", "t"):
		return "true", nil
	case containsAny(v, "错误", "错", "false", "×", "x", "no", "f"):
		return "false", nil
	default:
		return "", fmt.Errorf("judgement answer is not true/false")
	}
}

func extractLetters(value string) string {
	var out strings.Builder
	for _, r := range strings.ToUpper(value) {
		if r >= 'A' && r <= 'Z' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func validLetter(letter string, options []string) bool {
	if len(options) == 0 {
		return len(letter) == 1 && letter[0] >= 'A' && letter[0] <= 'Z'
	}
	return letterIndex(letter, options) >= 0
}

func letterIndex(letter string, options []string) int {
	for i, option := range options {
		if strings.HasPrefix(strings.TrimSpace(option), letter) {
			return i
		}
	}
	return -1
}

func matchOptionLetter(options []string, value string) (string, bool) {
	want := normalizeText(value)
	for i, option := range options {
		if normalizeText(option) == want {
			return string(rune('A' + i)), true
		}
	}
	return "", false
}

func splitAnswer(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ';' || r == '；' || r == ',' || r == '，' || unicode.IsSpace(r)
	})
}

func uniqueSorted(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		set[value] = true
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func uniqueSortedString(value string) string {
	set := map[rune]bool{}
	for _, r := range value {
		set[r] = true
	}
	out := make([]rune, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return string(out)
}

func normalizeLines(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func normalizeText(value string) string {
	return normalizeLines(strings.TrimLeft(strings.TrimSpace(value), "AaBbCcDdEeFf.、，:： "))
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
