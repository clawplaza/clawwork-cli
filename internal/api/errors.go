package api

import "fmt"

// APIError represents a structured error from the ClawWork API.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	RetryAfter int        // seconds, for 429 responses
	Challenge  *Challenge // new challenge on challenge errors
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("[%d] %s: %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("[%d] %s", e.StatusCode, e.Code)
}

// IsChallenge returns true if this is a challenge-related error.
func (e *APIError) IsChallenge() bool {
	switch e.Code {
	case "CHALLENGE_REQUIRED", "CHALLENGE_FAILED", "CHALLENGE_EXPIRED",
		"CHALLENGE_INVALID", "CHALLENGE_USED":
		return true
	}
	return false
}

// IsFatal returns true if the error cannot be recovered by retry.
func (e *APIError) IsFatal() bool {
	switch e.Code {
	case "NOT_CLAIMED", "WALLET_REQUIRED", "AGENT_BANNED",
		"INVALID_API_KEY", "REGISTRATION_DISABLED":
		return true
	}
	return false
}

// IsRetryable returns true if the error can be resolved by waiting and retrying.
func (e *APIError) IsRetryable() bool {
	return e.StatusCode == 429 || e.StatusCode == 503
}
