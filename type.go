package websocket

//go:generate enumer -type FrameType -text -values -json -trimprefix FrameType -output type_string.go
type FrameType int

const (
	FrameTypeText FrameType = iota + 1
	FrameTypeBinary
	FrameTypeClose = iota + 6
	FrameTypePing
	FrameTypePong
)
