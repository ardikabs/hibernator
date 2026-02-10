package k8sutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShortenName(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{
			name:   "no change when under maxLen",
			input:  "runner-job",
			maxLen: 20,
			want:   "runner-job",
		},
		{
			name:   "exactly maxLen",
			input:  "runner-job",
			maxLen: 10,
			want:   "runner-job",
		},
		{
			name:   "remove vowels when over maxLen",
			input:  "runner-hibernateplan-shutdown",
			maxLen: 25,
			want:   "rnnr-hbrntpln-shtdwn",
		},
		{
			name:   "keep first letter of each segment even if vowel",
			input:  "all-ec2-instances",
			maxLen: 15,
			want:   "all-ec2-instncs",
		},
		{
			name:   "truncate if still too long after vowel removal",
			input:  "this-is-a-very-long-name-that-needs-compression",
			maxLen: 10,
			want:   "ths-is-a-v",
		},
		{
			name:   "empty string",
			input:  "",
			maxLen: 10,
			want:   "",
		},
		{
			name:   "very short maxLen",
			input:  "runner",
			maxLen: 2,
			want:   "rn",
		},
		{
			name:   "multiple hyphens and vowels",
			input:  "dev-backend-shutdown-all-ec2-instances",
			maxLen: 30,
			want:   "dv-bcknd-shtdwn-all-ec2-instnc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShortenName(tt.input, tt.maxLen)
			assert.Equal(t, tt.want, got)
			assert.LessOrEqual(t, len(got), tt.maxLen, "output length must not exceed maxLen")
		})
	}
}

func TestIsVowel(t *testing.T) {
	assert.True(t, isVowel('a'))
	assert.True(t, isVowel('e'))
	assert.True(t, isVowel('i'))
	assert.True(t, isVowel('o'))
	assert.True(t, isVowel('u'))
	assert.False(t, isVowel('b'))
	assert.False(t, isVowel('z'))
	assert.False(t, isVowel('-'))
}
