package websocket

//go:generate enumer -type Status -text -values -trimprefix Status -output status_string.go
type Status uint32

const (
	StatusDisconnected Status = iota
	StatusConnecting
	StatusDisconnecting
	StatusEstablish // unused
	StatusInactive  // unused
	StatusConnected
	StatusReConnecting
)
