package websocket

import (
	"io"

	logger "github.com/charmbracelet/log"
)

type Frame struct {
	Type   int
	Reader io.Reader
}

type IMessage interface {
	IsPing() bool
}

func NewStringMessage(msg string) *StringMessage {
	sm := new(StringMessage)
	*sm = StringMessage(msg)
	return sm
}

type JsonMessage struct{}
type StringMessage string

func (j *JsonMessage) IsPing() bool   { return false }
func (s *StringMessage) IsPing() bool { return true }

type IWebsocket interface {
	Connect() error
	Shutdown() error
	SendMessage(message IMessage) error
}

type IWebsocketProcessor interface {
	OnReceive(frame *Frame)
	SetLogger(l *logger.Logger)
}
