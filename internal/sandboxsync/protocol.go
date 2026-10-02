package sandboxsync

const ProtocolVersion = 1

type Request struct {
	Version     int      `json:"version"`
	Operation   string   `json:"operation"`
	Root        string   `json:"root"`
	Rules       string   `json:"rules"`
	Before      Manifest `json:"before"`
	Changes     []Change `json:"changes,omitempty"`
	Program     string   `json:"program,omitempty"`
	Arguments   []string `json:"arguments,omitempty"`
	Shell       string   `json:"shell,omitempty"`
	CWD         string   `json:"cwd,omitempty"`
	Environment []string `json:"environment,omitempty"`
	TimeoutMS   int64    `json:"timeoutMs,omitempty"`
}
type Frame struct {
	Version   int       `json:"version"`
	Operation string    `json:"operation"`
	Kind      string    `json:"kind"`
	Data      []byte    `json:"data,omitempty"`
	Change    *Change   `json:"change,omitempty"`
	Manifest  *Manifest `json:"manifest,omitempty"`
	ExitCode  int       `json:"exitCode,omitempty"`
	TimedOut  bool      `json:"timedOut,omitempty"`
	Error     string    `json:"error,omitempty"`
}
