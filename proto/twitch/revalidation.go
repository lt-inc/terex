package twitch

// RevalidateImportRequest identifies one persisted collector failure, rather
// than granting permission to reset whichever upload happens to exist now.
type RevalidateImportRequest struct {
	Epoch             int64  `json:"epoch"`
	AdmissionSequence int64  `json:"admission_sequence"`
	UploadGeneration  int64  `json:"upload_generation"`
	FailureToken      string `json:"failure_token"`
	FlowID            string `json:"flow_id"`
	FlowVersion       int    `json:"flow_version"`
	Reason            string `json:"reason"`
	Manual            bool   `json:"manual"`
	Uploaded          bool   `json:"uploaded"`
}

type RevalidationReceipt struct {
	Epoch             int64  `json:"epoch"`
	AdmissionSequence int64  `json:"admission_sequence"`
	UploadGeneration  int64  `json:"upload_generation"`
	FailureToken      string `json:"failure_token"`
	FlowID            string `json:"flow_id"`
	FlowVersion       int    `json:"flow_version"`
	State             string `json:"state"` // discovered, verification_retry, ready
}

type AbortImportRequest struct {
	FailureToken      string `json:"failure_token,omitempty"`
	Epoch             int64  `json:"epoch"`
	AdmissionSequence int64  `json:"admission_sequence"`
	UploadGeneration  int64  `json:"upload_generation"`
	Reason            string `json:"reason"`
}
