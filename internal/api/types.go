// Package api provides the HTTP client for the ClawWork API.
package api

// InscribeRequest is the request body for POST /skill/inscribe.
type InscribeRequest struct {
	TokenID         int    `json:"token_id"`
	AgentName       string `json:"agent_name,omitempty"`
	ChallengeID     string `json:"challenge_id,omitempty"`
	ChallengeAnswer string `json:"challenge_answer,omitempty"`

	// Session management (CLI ↔ server cooperation)
	SessionID    string `json:"session_id,omitempty"`
	SessionStart bool   `json:"session_start,omitempty"`
	SessionEnd   bool   `json:"session_end,omitempty"`
}

// InscribeResponse is the unified response from POST /skill/inscribe.
// Fields are optional depending on success/error state.
type InscribeResponse struct {
	// Metadata
	SkillVersion string `json:"skill_version,omitempty"`
	SkillDocHash string `json:"skill_doc_hash,omitempty"`

	// Success fields
	Success          *bool       `json:"success,omitempty"`
	Hash             string      `json:"hash,omitempty"`
	TokenID          int         `json:"token_id,omitempty"`
	IDStatus         string      `json:"id_status,omitempty"` // "available", "hit", "taken"
	Nonce            int         `json:"nonce,omitempty"`
	Hit              bool        `json:"hit,omitempty"`
	CWEarned         int         `json:"cw_earned,omitempty"`
	CWPerInscription int         `json:"cw_per_inscription,omitempty"`
	TrustScore       int         `json:"trust_score,omitempty"`
	NFTsRemaining    int         `json:"nfts_remaining,omitempty"`
	GenesisNFT       *GenesisNFT `json:"genesis_nft,omitempty"`
	NextChallenge    *Challenge  `json:"next_challenge,omitempty"`
	NearbyMiners     []Miner     `json:"nearby_miners,omitempty"`
	IPPenalty        *IPPenalty   `json:"ip_penalty,omitempty"`

	// Registration fields
	AgentID     string `json:"agent_id,omitempty"`
	APIKey      string `json:"api_key,omitempty"`
	Registered  bool   `json:"registered,omitempty"`
	MiningReady bool   `json:"mining_ready,omitempty"`

	// Session fields
	SessionID      string `json:"session_id,omitempty"`
	SessionEnded   bool   `json:"session_ended,omitempty"`
	ClientVerified bool   `json:"client_verified,omitempty"`

	// Version gating
	MinClientVersion    string `json:"min_client_version,omitempty"`
	LatestClientVersion string `json:"latest_client_version,omitempty"`
	UpgradeURL          string `json:"upgrade_url,omitempty"`

	// Error fields
	Error      string     `json:"error,omitempty"`
	Message    string     `json:"message,omitempty"`
	Hint       string     `json:"hint,omitempty"`
	Challenge  *Challenge `json:"challenge,omitempty"` // returned on challenge errors
	RetryAfter int        `json:"retry_after,omitempty"`
}

// Challenge represents an inscription challenge prompt.
type Challenge struct {
	ID        string `json:"id"`
	Prompt    string `json:"prompt"`
	ExpiresIn int    `json:"expires_in"`
}

// GenesisNFT represents an agent's won NFT.
type GenesisNFT struct {
	TokenID      int    `json:"token_id"`
	Image        string `json:"image"`
	Metadata     string `json:"metadata"`
	PostVerified bool   `json:"post_verified"`
}

// IPPenalty contains IP-based penalty details.
type IPPenalty struct {
	IPMultiplier   int `json:"ip_multiplier"`
	AgentsOnIP     int `json:"agents_on_ip"`
	CWBase         int `json:"cw_base"`
	CWActual       int `json:"cw_actual"`
	MinMinesBase   int `json:"min_mines_base"`
	MinMinesActual int `json:"min_mines_actual"`
}

// Miner represents a nearby miner for social features.
type Miner struct {
	AgentID     string `json:"agent_id"`
	DisplayName string `json:"display_name"`
}

// StatusResponse is the response from GET /skill/status.
type StatusResponse struct {
	Agent        StatusAgent        `json:"agent"`
	Inscriptions StatusInscriptions `json:"inscriptions"`
	GenesisNFT   *GenesisNFT        `json:"genesis_nft,omitempty"`
	Activity     StatusActivity     `json:"activity"`
}

// StatusAgent is the agent info inside a StatusResponse.
type StatusAgent struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	WalletAddress string `json:"wallet_address"`
	AvatarURL     string `json:"avatar_url,omitempty"`
}

// StatusInscriptions holds inscription stats.
type StatusInscriptions struct {
	Total          int  `json:"total"`
	Confirmed      int  `json:"confirmed"`
	TotalCW        int  `json:"total_cw"`
	Hit            bool `json:"hit"`
	AssignedTokenID *int `json:"assigned_token_id,omitempty"`
}

// StatusActivity holds platform activity info.
type StatusActivity struct {
	Status        string `json:"status"`
	NFTsRemaining int    `json:"nfts_remaining"`
}

// IsChallenge returns true if this is a challenge-related error requiring retry.
func (r *InscribeResponse) IsChallenge() bool {
	switch r.Error {
	case "CHALLENGE_REQUIRED", "CHALLENGE_FAILED", "CHALLENGE_EXPIRED",
		"CHALLENGE_INVALID", "CHALLENGE_USED", "CHALLENGE_UNAVAILABLE":
		return true
	}
	return false
}

// GetChallenge returns the challenge from this response (from either field).
func (r *InscribeResponse) GetChallenge() *Challenge {
	if r.Challenge != nil {
		return r.Challenge
	}
	return r.NextChallenge
}

// IsFatal returns true if the error cannot be recovered by retry.
func (r *InscribeResponse) IsFatal() bool {
	switch r.Error {
	case "NOT_CLAIMED", "WALLET_REQUIRED", "AGENT_BANNED",
		"INVALID_API_KEY", "REGISTRATION_DISABLED",
		"ALREADY_MINING", "UPGRADE_REQUIRED":
		return true
	}
	return false
}

// IsRateLimited returns true if the response indicates rate limiting.
func (r *InscribeResponse) IsRateLimited() bool {
	return r.Error == "RATE_LIMITED" || r.Error == "DAILY_LIMIT_REACHED"
}

// ClaimResponse is the response from POST /skill/claim.
type ClaimResponse struct {
	OK          bool   `json:"ok"`
	AgentID     string `json:"agent_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Message     string `json:"message,omitempty"`
	Error       string `json:"error,omitempty"`
}
