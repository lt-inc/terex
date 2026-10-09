package twitch

type AdmissionRequest struct {
	Epoch int64 `json:"epoch"`
	Limit int   `json:"limit"`
}
type DownloadGrant struct {
	AdmissionSequence int64  `json:"admission_sequence,omitempty"`
	RevalidationToken string `json:"revalidation_token,omitempty"`
	ClipID            string `json:"clip_id"`
	ImportID          string `json:"import_id"`
	FlowID            string `json:"flow_id"`
	FlowVersion       int    `json:"flow_version"`
	Format            string `json:"format"`
	Source            Source `json:"source"`
	Epoch             int64  `json:"epoch"`
}
type AdmissionResult struct {
	Mode           string          `json:"mode"`
	BudgetPercent  int             `json:"budget_percent"`
	DailyRemaining int             `json:"daily_remaining"`
	Grants         []DownloadGrant `json:"grants"`
}
