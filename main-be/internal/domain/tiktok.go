package domain

import "time"

type TikTokConnection struct {
	AccountID       int64      `json:"account_id"`
	Status          string     `json:"status"`
	RemoteAccountID string     `json:"remote_account_id,omitempty"`
	Username        string     `json:"username,omitempty"`
	DisplayName     string     `json:"display_name,omitempty"`
	SessionRef      string     `json:"session_ref,omitempty"`
	SessionVersion  int64      `json:"session_version,omitempty"`
	Error           string     `json:"error,omitempty"`
	PairingToken    string     `json:"pairing_token,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	PayloadURL      string     `json:"payload_url,omitempty"`
	HelpURL         string     `json:"help_url,omitempty"`
}
