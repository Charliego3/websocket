package websocket

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	logger "github.com/charmbracelet/log"
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

type Option interface {
	Apply(wc *Client)
}

type dialTimeout time.Duration
type pingPeriod time.Duration
type ping struct{ IMessage }
type log struct{ l *logger.Logger }
type onConnected func(client *Client)
type prefix string

func (ww dialTimeout) Apply(wc *Client) { wc.timeout = time.Duration(ww) }
func (wp pingPeriod) Apply(wc *Client)  { wc.pingPeriod = time.Duration(wp) }
func (p ping) Apply(wc *Client)         { wc.ping = p.IMessage }
func (p log) Apply(wc *Client)          { wc.logger = p.l }
func (h onConnected) Apply(wc *Client)  { wc.onConnected = h }
func (h prefix) Apply(wc *Client)       { wc.prefix = string(h) }

func WithDialTimeout(wt time.Duration) Option { return dialTimeout(wt) }
func WithPingPeriod(wp time.Duration) Option  { return pingPeriod(wp) }
func WithPing(p IMessage) Option              { return ping{p} }
func WithLogger(l *logger.Logger) Option      { return log{l} }
func WithOnConnected(h func(*Client)) Option  { return onConnected(h) }
func WithPrefix(p string) Option              { return prefix(p) }

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
	URL        string
	ping       IMessage
	timeout    time.Duration
	pingPeriod time.Duration
	conn       *websocket.Conn
	processor  IWebsocketProcessor
	logger     *logger.Logger

	onConnected func(client *Client)

	subscr chan IMessage
	unsub  chan IMessage
	status atomic.Value
	mutex  sync.RWMutex
	mmer   *msgManager

	ctx    context.Context
	prefix string
}

func NewClient(ctx context.Context, url string, receiver IWebsocketProcessor, opts ...Option) *Client {
	wc := &Client{
		URL:       url,
		ctx:       ctx,
		processor: receiver,
		subscr:    make(chan IMessage, 5),
		unsub:     make(chan IMessage),
		mmer:      &msgManager{data: make(map[IMessage]struct{})},
	}
	wc.status.Store(StatusWaiting)
	wc.getOpts(opts...)
	if wc.logger == nil {
		if len(wc.prefix) > 0 {
			wc.prefix += "  "
		}
		prefix := wc.prefix + wc.URL
		wc.logger = logger.NewWithOptions(os.Stdout, logger.Options{
			ReportCaller:    true,
			ReportTimestamp: true,
			TimeFormat:      "3:04:05PM",
			Prefix:          prefix,
		})
	}
	if r, ok := receiver.(interface {
		SetLogger(*logger.Logger)
	}); ok {
		r.SetLogger(wc.logger)
	}
	return wc
}

func (wc *Client) getOpts(opts ...Option) {
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.Apply(wc)
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
	wc.logger.Info("即将关闭 websocket 链接")
	wc.mmer.clear()
	wc.status.Store(StatusDisconnected)
	close(wc.subscr)
	err := wc.conn.Close()
	if err != nil {
		return err
	}
	wc.logger.Info("websocket 链接已关闭")
	return nil
}

func (wc *Client) Connect() error {
	err := wc.connect(false)
	if err == nil && wc.onConnected != nil {
		wc.onConnected(wc)
	}
	return err
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
	dialCtx, cancel := context.WithTimeout(context.Background(), durationDefault(wc.timeout, defaultWait))
	defer cancel()
	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, wc.URL, nil)
	if err != nil {
		wc.logger.Error("链接失败", err)
		return err
	}

	wc.status.Store(StatusConnected)

	wc.conn = conn
	if !reconnect {
		go wc.accept()
		go wc.writePump()
	}
	return nil
}

func (wc *Client) reconnect() {
	err := wc.connect(true)
	if err != nil {
		if err == closedErr {
			wc.logger.Warn("websocket 已关闭取消重连...")
			return
		}

		time.Sleep(time.Second)
		wc.reconnect()
		return
	}
	wc.logger.Warn("websocket 链接重连成功...")

	if wc.onConnected != nil {
		wc.onConnected(wc)
	}
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
			wc.logger.Error("Accept 出现异常: ", err)
		}
		wc.logger.Info("已停止 accept...")
	}()

	for {
		mt, r, err := wc.conn.NextReader()
		if err != nil {
			switch wc.status.Load() {
			case StatusDisconnected:
				return
			case StatusReConnecting, StatusConnecting, StatusWaiting:
				time.Sleep(time.Second)
				continue
			}

			wc.logger.Errorf("读取消息出错: %d, %+v", mt, err)
			if mt == -1 || strings.Contains(err.Error(), "use of closed network connection") ||
				websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) ||
				websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				wc.reconnect()
			}

			continue
		}

		// 1:TextMessage 2:BinaryMessage 8:CloseMessage 9:PingMessage 10:PongMessage
		wc.processor.OnReceive(&Frame{
			Type:   mt,
			Reader: r,
		})
	}
}

func (wc *Client) writePump() {
	period := durationDefault(wc.pingPeriod, defaultPingPeriod)
	ticker := time.NewTicker(period)
	defer func() {
		ticker.Stop()
		wc.logger.Info("已停止 writePump...")
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

			if wc.ping == nil {
				ticker.Stop()
				continue
			}

			wc.subscr <- wc.ping
		case msg, ok := <-wc.subscr:
			wc.writeMessage(msg, ok, true)
		case msg, ok := <-wc.unsub:
			wc.writeMessage(msg, ok, false)
		case <-wc.ctx.Done():
			err := wc.Shutdown()
			if err != nil {
				wc.logger.Error("关闭 websocket 链接出错:", "err", err)
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
			wc.logger.Errorf("encode message[%+v] error: %v", msg, err)
			return
		}
	}

	err = wc.conn.WriteMessage(websocket.TextMessage, buf)
	if err != nil {
		wc.logger.Errorf("发送消息失败: %s, %+v", buf, err)
		return
	}
	if msg.IsPing() {
		wc.logger.Debugf("发送ping消息: %s", buf)
	} else {
		wc.logger.Infof("发送消息: %s", buf)
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
		return fmt.Errorf("websocket 已断开链接: %+v", message)
	case StatusReConnecting, StatusConnecting, StatusWaiting:
		return fmt.Errorf("websocket 尚未链接成功: %+v", message)
	case StatusDisconnecting:
		return fmt.Errorf("websocket 链接已关闭: %+v", message)
	}
	if subscribe {
		wc.subscr <- message
	} else {
		wc.unsub <- message
	}
	return nil
}
