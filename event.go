package websocket

//go:generate enumer -type Event -text -values -json -trimprefix Event -output event_string.go
type Event uint

const (
	EventNormal Event = iota
	EventRead
	EventWrite
	EventDecompress
	EventUnmarshal
	EventReconnect
)
