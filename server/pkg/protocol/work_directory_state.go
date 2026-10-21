package protocol

type WorkDirectoryState struct {
	Path    string `json:"path"`
	Missing bool   `json:"missing"`
}

type RuntimeWorkDirectoryPage struct {
	Paths []string `json:"paths"`
}
