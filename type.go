package websocket

//go:generate enumer -type MessageType -text -values -json -trimprefix Message -output type_string.go
type MessageType int

const (
	MessageText MessageType = iota + 1
	MessageBinary
	MessageClose
	MessagePing
	MessagePong
)
