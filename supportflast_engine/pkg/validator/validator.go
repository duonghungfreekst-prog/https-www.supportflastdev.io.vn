// Package validator provides input validation and sanitization helpers
// for the SupportFlast Engine.
package validator

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// emailRegex is a reasonable email validation pattern.
// It covers the vast majority of valid email addresses without being overly strict.
var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// usernameRegex allows alphanumeric characters and underscores, 3-32 chars.
var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// ValidateEmail checks whether the given string looks like a valid email address.
func ValidateEmail(email string) bool {
	email = strings.TrimSpace(email)
	if email == "" {
		return false
	}
	// Reasonable max length for emails (RFC 5321).
	if len(email) > 254 {
		return false
	}
	return emailRegex.MatchString(email)
}

// ValidateUsername checks whether a username is valid.
// Rules: 3-32 characters, alphanumeric and underscores only.
// Returns (valid, reason) where reason describes the failure if invalid.
func ValidateUsername(username string) (bool, string) {
	username = strings.TrimSpace(username)
	length := utf8.RuneCountInString(username)

	if length == 0 {
		return false, "username must not be empty"
	}
	if length < 3 {
		return false, "username must be at least 3 characters"
	}
	if length > 32 {
		return false, "username must be at most 32 characters"
	}
	if !usernameRegex.MatchString(username) {
		return false, "username may only contain letters, digits, and underscores"
	}
	return true, ""
}

// SanitizeString trims whitespace, removes null bytes, and strips CR/LF characters.
func SanitizeString(s string) string {
	// Trim leading/trailing whitespace.
	s = strings.TrimSpace(s)
	// Remove null bytes.
	s = strings.ReplaceAll(s, "\x00", "")
	// Remove carriage return and line feed.
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

// ValidatePageParams clamps pagination parameters to safe ranges.
//   - limit is clamped to [1, 500].
//   - offset is clamped to >= 0.
//
// Returns the clamped (limit, offset).
func ValidatePageParams(limit, offset int) (int, int) {
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
