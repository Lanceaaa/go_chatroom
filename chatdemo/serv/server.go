package serv

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/sirupsen/logrus"
)

type Server struct {
	once    sync.Once
	id      string
	address string
	sync.Mutex
	// 会话列表
	users map[string]net.Conn
}

func NewServer(id, address string) *Server {
	return newServer(id, address)
}

func newServer(id, address string) *Server {
	return &Server{
		id:      id,
		address: address,
		users:   make(map[string]net.Conn, 100),
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	log := logrus.WithFields(logrus.Fields{
		"module": "Server",
		"listen": s.address,
		"id":     s.id,
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 升级
		conn, _, _, err := ws.UpgradeHTTP(r, w)
		if err != nil {
			conn.Close()
			return
		}
		// 读取userid
		user := r.URL.Query().Get("user")
		if user == "" {
			conn.Close()
			return
		}
		// 添加到会话管理中
		old, ok := s.AddUser(user, conn)
		if ok {
			// 断开旧的连接
			old.Close()
		}
		log.Infof("user %s in", user)
		go func(user string, conn net.Conn) {
			// 读取消息
			err := s.readloop(user, conn)
			if err != nil {
				log.Error(err)
			}
			conn.Close()
			// 连接断开，删除用户
			s.DelUser(user)

			log.Infof("connection of %s closed", user)
		}(user, conn)
	})
	log.Infoln("started")
	return http.ListenAndServe(s.address, mux)
}

func (s *Server) AddUser(user string, conn net.Conn) (net.Conn, bool) {
	s.Lock()
	defer s.Unlock()
	old, ok := s.users[user]
	s.users[user] = conn
	return old, ok
}

func (s *Server) DelUser(user string) {
	s.Lock()
	defer s.Unlock()
	delete(s.users, user)
}

func (s *Server) readloop(user string, conn net.Conn) error {
	readwait := time.Minute * 2
	for {
		_ = conn.SetReadDeadline(time.Now().Add(readwait))

		frame, err := ws.ReadFrame(conn)
		if err != nil {
			return err
		}
		if frame.Header.OpCode == ws.OpPing {
			// 返回一个pong消息
			_ = wsutil.WriteServerMessage(conn, ws.OpPong, nil)
			continue
		}
		if frame.Header.OpCode == ws.OpClose {
			return errors.New("remote side close the conn")
		}
		if frame.Header.Masked {
			ws.Cipher(frame.Payload, frame.Header.Mask, 0)
		}
		// 接收文本帧内容
		if frame.Header.OpCode == ws.OpText {
			go s.handle(user, string(frame.Payload))
		}
	}
}

func (s *Server) handle(user, message string) {
	logrus.Infof("recv message %s from %s", message, user)
	s.Lock()
	defer s.Unlock()
	broadcast := fmt.Sprintf("%s -- FROM %s", message, user)
	for u, conn := range s.users {
		if u == user {
			continue
		}
		logrus.Infof("send to %s : %s", u, broadcast)
		err := s.writeText(conn, message)
		if err != nil {
			logrus.Errorf("write to %s failed, error: %v", user, err)
		}
	}
}

func (s *Server) writeText(conn net.Conn, message string) error {
	// 创建文本帧数据
	f := ws.NewTextFrame([]byte(message))
	return ws.WriteFrame(conn, f)
}

func (s *Server) Shutdown() {
	s.once.Do(func() {
		s.Lock()
		defer s.Unlock()
		for _, conn := range s.users {
			conn.Close()
		}
	})
}

const (
	CommandPing = 100
	CommandPong = 101
)

func (s *Server) handleBinary(user string, message []byte) {
	logrus.Infof("recv message %v from %s", message, user)
	s.Lock()
	defer s.Unlock()

	i := 0
	command := binary.BigEndian.Uint16(message[i : i+2])
	i += 2
	payloadLen := binary.BigEndian.Uint32(message[i : i+4])
	logrus.Infof("command: %v payloadLen: %v", command, payloadLen)
	if command == CommandPing {
		u := s.users[user]
		err := wsutil.WriteServerBinary(u, []byte{0, CommandPong, 0, 0, 0, 0})
		if err != nil {
			logrus.Errorf("write to %s failed, error: %v", user, err)
		}
	}
}
