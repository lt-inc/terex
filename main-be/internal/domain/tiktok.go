package domain

import "time"

type TikTokConnection struct {
	AccountID             int64      `json:"account_id"`
	Status                string     `json:"status"`
	RemoteAccountID       string     `json:"remote_account_id,omitempty"`
	Username              string     `json:"username,omitempty"`
	DisplayName           string     `json:"display_name,omitempty"`
	SessionRef            string     `json:"session_ref,omitempty"`
	SessionVersion        int64      `json:"session_version,omitempty"`
	Error                 string     `json:"error,omitempty"`
	PairingToken          string     `json:"pairing_token,omitempty"`
	ExpiresAt             *time.Time `json:"expires_at,omitempty"`
	PayloadURL            string     `json:"payload_url,omitempty"`
	HelpURL               string     `json:"help_url,omitempty"`
	ProxyMode             string     `json:"proxy_mode"`
	ProxyDisplay          string     `json:"proxy_display"`
	CustomProxyConfigured bool       `json:"custom_proxy_configured"`
}

// TikTokProxySettings is an input-only credential carrier. The uploader owns
// encrypted proxy storage; TikTokConnection exposes only safe display fields.
type TikTokProxySettings struct {
	Mode string  `json:"mode"`
	URL  *string `json:"url,omitempty"`
}
