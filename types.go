package websocket

//go:generate enumer -type FrameType -text -values -json -trimprefix FrameType -output types_string.go
type FrameType int

const (
	FrameTypeNoFrame FrameType = iota - 1
	FrameTypeText    FrameType = iota
	FrameTypeBinary
	FrameTypeClose FrameType = iota + 6
	FrameTypePing
	FrameTypePong
)
