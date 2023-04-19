package websocket

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gorilla/websocket"
	json "github.com/json-iterator/go"
	"go.uber.org/atomic"
	"gopkg.in/errgo.v2/errors"
)

const (
	defaultWait       = time.Minute * 3
	defaultPingPeriod = time.Second * 5
)

var closedErr = errors.New("has been closed.")

type msgManager struct {
	mutex sync.RWMutex
	data  map[IMessage]struct{}
}

func (m *msgManager) clear() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.data = make(map[IMessage]struct{}, 0)
}

func (m *msgManager) update(message IMessage, subscribe bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if subscribe {
		m.data[message] = struct{}{}
	} else {
		delete(m.data, message)
	}
}

func (m *msgManager) getData() map[IMessage]struct{} {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	temp := make(map[IMessage]struct{})
	for msg := range m.data {
		temp[msg] = struct{}{}
	}
	return temp
}

type Client struct {
	ctx       context.Context
	URL       string
	conn      *websocket.Conn
	processor Receiver
	subscr    chan IMessage
	unsub     chan IMessage
	wchan     chan struct{}
	rchan     chan struct{}
	cchan     chan struct{}
	status    atomic.Value
	mutex     sync.RWMutex
	mmer      *msgManager
	logger    *log.Logger
	opts      *Options
}

func NewClient(ctx context.Context, url string, receiver Receiver, opts ...Option) *Client {
	wc := &Client{
		URL:       url,
		ctx:       ctx,
		processor: receiver,
		subscr:    make(chan IMessage, 5),
		unsub:     make(chan IMessage),
		wchan:     make(chan struct{}),
		rchan:     make(chan struct{}),
		cchan:     make(chan struct{}),
		mmer:      &msgManager{data: make(map[IMessage]struct{})},
	}
	wc.status.Store(StatusWaiting)
	wc.getOpts(opts...)
	if wc.opts.logger == nil {
		if !wc.opts.hideURL {
			if len(wc.opts.prefix) > 0 {
				wc.opts.prefix += "  "
			}
			wc.opts.prefix += wc.URL
		}
		wc.logger = log.WithPrefix(wc.opts.prefix)
	} else {
		wc.logger = wc.opts.logger
		wc.opts.logger = nil
	}
	if r, ok := receiver.(interface {
		SetLogger(*log.Logger)
	}); ok {
		r.SetLogger(wc.logger)
	}
	return wc
}

func (wc *Client) getOpts(opts ...Option) {
	wc.opts = &Options{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(wc.opts)
	}
}

func durationDefault(v, d time.Duration) time.Duration {
	if v == 0 {
		return d
	}
	return v
}

func (wc *Client) Shutdown() error {
	wc.mutex.Lock()
	defer wc.mutex.Unlock()

	if wc.status.Load() == StatusDisconnected {
		return nil
	}

	wc.status.Store(StatusDisconnecting)
	wc.logger.Info("closing websocket connection")
	wc.mmer.clear()
	wc.status.Store(StatusDisconnected)
	close(wc.subscr)
	err := wc.conn.Close()
	if err != nil {
		return err
	}
	wc.cchan <- struct{}{}
	<-wc.rchan
	<-wc.wchan
	wc.logger.Info("websocket connection has be closed")
	return nil
}

func (wc *Client) Connect() error {
	return wc.connect(false)
}

func (wc *Client) connect(reconnect bool) error {
	wc.mutex.Lock()
	defer wc.mutex.Unlock()

	status := wc.status.Load()
	if status == StatusDisconnected {
		return closedErr
	} else if reconnect {
		wc.status.Store(StatusReConnecting)
	} else if status == StatusConnected {
		return nil
	} else {
		wc.status.Store(StatusConnecting)
	}
	dialCtx, cancel := context.WithTimeout(context.Background(), durationDefault(wc.opts.timeout, defaultWait))
	defer cancel()
	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, wc.URL, nil)
	if err != nil {
		wc.logger.Error("connect", "err", err)
		return err
	}

	wc.status.Store(StatusConnected)

	wc.conn = conn
	if !reconnect {
		go wc.accept()
		go wc.writePump()
	}

	if err == nil && wc.opts.onConnected != nil {
		wc.opts.onConnected(wc)
	}
	return nil
}

func (wc *Client) Reconnect() {
	if wc.opts.disableReconnect {
		return
	}

	err := wc.connect(true)
	if err != nil {
		if err == closedErr {
			wc.logger.Warn("websocket closed, cancel reconnect...")
			return
		}

		time.Sleep(time.Second)
		wc.Reconnect()
		return
	}
	wc.logger.Warn("websocket reconnected")
	wc.resendMessages()
}

func (wc *Client) resendMessages() {
	for msg := range wc.mmer.getData() {
		err := wc.Subscribe(msg)
		if err != nil {
			if wc.status.Load() == StatusDisconnected {
				return
			}

			time.Sleep(time.Second)
			wc.resendMessages()
		}
	}
}

func (wc *Client) accept() {
	defer func() {
		if err := recover(); err != nil {
			wc.logger.Error("accept", "err", err)
		}
		wc.logger.Info("stopped acceptor...")
		wc.rchan <- struct{}{}
	}()

	for {
		mt, r, err := wc.conn.NextReader()
		msgType := MessageType(mt)
		if err != nil {
			switch wc.status.Load() {
			case StatusDisconnected:
				return
			case StatusReConnecting, StatusConnecting, StatusWaiting:
				time.Sleep(time.Second)
				continue
			}

			wc.logger.Error("read message", "type", msgType.String(), "err", err)
			if mt == -1 || strings.Contains(err.Error(), "use of closed network connection") ||
				websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) ||
				websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				if wc.opts.disableReconnect {
					_ = wc.Shutdown()
					return
				}
				wc.Reconnect()
			}

			continue
		}

		wc.processor.OnReceive(&Frame{
			Type:   msgType,
			Reader: r,
		})
	}
}

func (wc *Client) writePump() {
	period := durationDefault(wc.opts.period, defaultPingPeriod)
	ticker := time.NewTicker(period)
	defer func() {
		ticker.Stop()
		wc.logger.Info("stopped writer...")
		wc.wchan <- struct{}{}
	}()

	for {
		select {
		case <-ticker.C:
			switch wc.status.Load() {
			case StatusDisconnected:
				return
			case StatusReConnecting, StatusDisconnecting,
				StatusConnecting, StatusWaiting:
				time.Sleep(period)
				continue
			}

			if wc.opts.ping == nil {
				ticker.Stop()
				continue
			}

			wc.subscr <- wc.opts.ping
		case <-wc.cchan:
			return
		case msg, ok := <-wc.subscr:
			wc.writeMessage(msg, ok, true)
		case msg, ok := <-wc.unsub:
			wc.writeMessage(msg, ok, false)
		case <-wc.ctx.Done():
			err := wc.Shutdown()
			if err != nil {
				wc.logger.Error("shutdown websocket", "err", err)
			}
		}
	}
}

func (wc *Client) writeMessage(msg IMessage, ok bool, subscribe bool) {
	if !ok {
		return
	}

	wc.mmer.update(msg, subscribe)
	var buf []byte
	var err error
	if sm, ok := msg.(*StringMessage); ok {
		buf = []byte(*sm)
	} else {
		buf, err = json.Marshal(msg)
		if err != nil {
			wc.logger.Error("encode message", "message", msg, "err", err)
			return
		}
	}

	err = wc.conn.WriteMessage(websocket.TextMessage, buf)
	if err != nil {
		wc.logger.Error("send message", "err", err)
		return
	}
	if msg.IsPing() {
		wc.logger.Debug("send [ping]", "message", string(buf))
	} else {
		wc.logger.Info("send", "message", string(buf))
	}
}

func (wc *Client) Subscribe(message IMessage) error {
	return wc.send(true, message)
}

func (wc *Client) Unsubscribe(message IMessage) error {
	return wc.send(false, message)
}

func (wc *Client) send(subscribe bool, message IMessage) error {
	switch wc.status.Load() {
	case StatusDisconnected:
		return fmt.Errorf("websocket disconnected: %+v", message)
	case StatusReConnecting, StatusConnecting, StatusWaiting:
		return fmt.Errorf("websocket connecting: %+v", message)
	case StatusDisconnecting:
		return fmt.Errorf("websocket disconecting: %+v", message)
	}
	if subscribe {
		wc.subscr <- message
	} else {
		wc.unsub <- message
	}
	return nil
}
