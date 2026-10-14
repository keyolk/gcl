package main

import "testing"

// A Korean query matches as a subsequence too, not only as an exact
// substring (the old scorer compared query bytes with text runes).
func TestFuzzyScoreMatchesKoreanSubsequence(t *testing.T) {
	if fuzzyScore("주회", "주간 회의") <= 0 {
		t.Error("주회 does not match 주간 회의")
	}
	if fuzzyScore("plan", "quarterly planning") <= fuzzyScore("plan", "p l a n") {
		t.Error("an exact substring does not outrank a scattered match")
	}
	if fuzzyScore("xyz", "quarterly planning") != 0 {
		t.Error("a non-match scored")
	}
}
