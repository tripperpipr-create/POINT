package sandboxsync

type Space struct {
	Available uint64 `json:"available"`
	Total     uint64 `json:"total"`
}

func (s Space) Low() bool {
	return s.Total > 0 && (s.Available < 5*1024*1024*1024 || s.Available < s.Total/10)
}
