package protocol

type WorkspaceFileEditResult struct {
	CommandID      string  `json:"command_id,omitempty"`
	OperationID    string  `json:"operation_id,omitempty"`
	FileID         string  `json:"file_id,omitempty"`
	FilePath       string  `json:"file_path"`
	Merged         bool    `json:"merged"`
	Conflict       bool    `json:"conflict"`
	PreviousSHA256 string  `json:"previous_sha256"`
	NewSHA256      string  `json:"new_sha256,omitempty"`
	CurrentSHA256  string  `json:"current_sha256,omitempty"`
	Bytes          int64   `json:"bytes"`
	NewContent     *string `json:"new_content,omitempty"`
}
