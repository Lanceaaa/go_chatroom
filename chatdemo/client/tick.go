package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

type handler struct {
	conn      net.Conn
	close     chan struct{}
	recv      chan []byte
	heartbeat time.Duration
}

func (h *handler) readloop(conn net.Conn) error {
	logrus.Info("readloop started")
	// 要求在指定时间 heartbeat (50秒) *3 内，可以读到数据
	err := h.conn.SetReadDeadline(time.Now().Add(h.heartbeat * 3))
	if err != nil {
		return err
	}
	for {
		frame, err := ws.ReadFrame(conn)
		if err != nil {
			return err
		}
		if frame.Header.OpCode == ws.OpPong {
			_ = h.conn.SetReadDeadline(time.Now().Add(h.heartbeat * 3))
		}
		if frame.Header.OpCode == ws.OpClose {
			return errors.New("remote side close the channel")
		}
		if frame.Header.OpCode == ws.OpText {
			h.recv <- frame.Payload
		}
	}
}

func (h *handler) heartbeatloop() error {
	logrus.Info("heartbeatloop started")

	tick := time.NewTimer(h.heartbeat)
	for range tick.C {
		logrus.Info("ping...")
		if err := wsutil.WriteClientMessage(h.conn, ws.OpPing, nil); err != nil {
			return err
		}
	}
	return nil
}

// func (h *handler) heartbeatloop() error {
// 	logrus.Info("heartbeatloop started")

// 	tick := time.NewTicker(h.heartbeat)
// 	for range tick.C {
// 		// 发送一个ping的心跳包给服务端
// 		logrus.Info("ping...")
// 		_ = h.conn.SetWriteDeadline(time.Now().Add(time.Second * 10))

// 		if err := wsutil.WriteClientMessage(h.conn, ws.OpPing, nil); err != nil {
// 			return err
// 		}
// 	}
// 	return nil
// }

func connect(addr string) (*handler, error) {
	_, err := url.Parse(addr)
	if err != nil {
		return nil, err
	}

	conn, _, _, err := ws.Dial(context.Background(), addr)
	if err != nil {
		return nil, err
	}

	h := handler{
		conn:      conn,
		close:     make(chan struct{}),
		recv:      make(chan []byte),
		heartbeat: time.Second * 50,
	}
	go func() {
		err := h.readloop(conn)
		if err != nil {
			logrus.Warn(err)
		}
		// 通知上层
		h.close <- struct{}{}
	}()
	go func() {
		err := h.heartbeatloop()
		if err != nil {
			logrus.Info("heartbeatloop - ", err)
		}
	}()
	return &h, nil
}

// StartOptions StartOptions
type StartOptions struct {
	address string
	user    string
}

// NewCmd NewCmd
func NewCmd(ctx context.Context) *cobra.Command {
	opts := &StartOptions{}

	cmd := &cobra.Command{
		Use:   "client",
		Short: "Start client",
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(ctx, opts)
		},
	}
	cmd.PersistentFlags().StringVarP(&opts.address, "address", "a", "ws://127.0.0.1:8080", "server address")
	cmd.PersistentFlags().StringVarP(&opts.user, "user", "u", "", "user")
	return cmd
}

func run(ctx context.Context, opts *StartOptions) error {
	url := fmt.Sprintf("%s?user=%s", opts.address, opts.user)
	logrus.Info("connect to ", url)
	//连接到服务，并返回hander对象
	h, err := connect(url)
	if err != nil {
		return err
	}

	go func() {
		for msg := range h.recv {
			// 读取消息并显示
			logrus.Infof("Receive message:", string(msg))
		}
	}()
	tk := time.NewTicker(time.Second * 6)
	for {
		select {
		case <-tk.C:
			// 每6秒发送一个消息
			err := h.sendText("hello")
			if err != nil {
				logrus.Error(err)
			}
		case <-h.close:
			logrus.Printf("connection closed")
			return nil
		}
	}
}

func (h *handler) sendText(message string) error {
	logrus.Info("send message:", message)
	return wsutil.WriteClientText(h.conn, []byte(message))
}
