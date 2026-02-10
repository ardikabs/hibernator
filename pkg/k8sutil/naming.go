package k8sutil

import "strings"

// ShortenName intelligently shortens a string to fit within maxLen.
// It prioritizes removing vowels from the middle of words to maintain readability.
// If that's not enough, it truncates the string.
func ShortenName(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	// Strategy 1: Remove vowels (except first letter of each segment)
	var b strings.Builder
	b.Grow(len(s))

	parts := strings.Split(s, "-")
	for i, part := range parts {
		if i > 0 {
			b.WriteByte('-')
		}
		if len(part) == 0 {
			continue
		}

		// Always keep first char
		b.WriteByte(part[0])
		for j := 1; j < len(part); j++ {
			c := part[j]
			if !isVowel(c) {
				b.WriteByte(c)
			}
		}
	}

	res := b.String()
	if len(res) <= maxLen {
		return res
	}

	// Strategy 2: If still too long, truncate from the end
	return res[:maxLen]
}

func isVowel(c byte) bool {
	return c == 'a' || c == 'e' || c == 'i' || c == 'o' || c == 'u'
}
