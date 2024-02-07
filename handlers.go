package websocket

import "io"

type OnConnectedHandler func(*Client)

type ReConnectedHandler func(*Client)

type DecompressHandler func(io.Reader) (io.Reader, error)

type OnCloseHandler func(code int, text string) error

type PingHandler func(appData string) error

type PongHandler func(appData string) error

type ErrorHandler func(FrameType, Event, error)

type HeartbeatHandler func(*Client)

type BeforeReconnectHandler func(*Client)

type Unmarshaler interface {
	Unmarshal(frameType FrameType, reader io.Reader) (any, error)
}

type Receiver interface {
	Unmarshaler
	OnMessage(any)
}

type ReaderReceiver struct{}

func (ReaderReceiver) Unmarshal(_ FrameType, reader io.Reader) (any, error) {
	return reader, nil
}

type Logger interface {
	Info(string, ...any)
	Error(string, ...any)
}
