package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"local-agent-workbench/internal/egress"
)

type gatewayControl struct {
	Action    string        `json:"action"`
	Operation string        `json:"operation"`
	RunID     string        `json:"runId"`
	Policy    egress.Policy `json:"policy"`
}
type gatewayReply struct {
	Operation string `json:"operation"`
	Error     string `json:"error,omitempty"`
	Logs      string `json:"logs,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}
type boundedGatewayLog struct {
	sync.Mutex
	data      []byte
	truncated bool
}

func (l *boundedGatewayLog) Write(p []byte) (int, error) {
	l.Lock()
	defer l.Unlock()
	if len(l.data)+len(p) > 512*1024 {
		l.truncated = true
	} else {
		l.data = append(l.data, p...)
	}
	return len(p), nil
}

type connectionSet struct {
	sync.Mutex
	connections map[net.Conn]bool
	closed      bool
}

func (s *connectionSet) add(c net.Conn) net.Conn {
	s.Lock()
	defer s.Unlock()
	if s.closed {
		_ = c.Close()
	} else {
		s.connections[c] = true
	}
	return &trackedConnection{Conn: c, set: s}
}
func (s *connectionSet) closeAll() {
	s.Lock()
	defer s.Unlock()
	s.closed = true
	for c := range s.connections {
		_ = c.Close()
		delete(s.connections, c)
	}
}

type trackedConnection struct {
	net.Conn
	set *connectionSet
}

func (c *trackedConnection) Close() error {
	c.set.Lock()
	delete(c.set.connections, c.Conn)
	c.set.Unlock()
	return c.Conn.Close()
}

type trackedListener struct {
	net.Listener
	set *connectionSet
}

type activeRequests struct {
	sync.Mutex
	closed bool
	wait   sync.WaitGroup
}

func (l trackedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return l.set.add(c), nil
}

// Only Docker's host-side stdin attachment can change policy. No control TCP socket exists.
func serveGateway(input io.Reader, output io.Writer) error {
	if err := protectControl(); err != nil {
		return err
	}
	dec, enc := json.NewDecoder(input), json.NewEncoder(output)
	var server *http.Server
	var connections *connectionSet
	var active string
	var cancel context.CancelFunc
	var requests *activeRequests
	var logs *boundedGatewayLog
	stop := func() {
		if server != nil {
			requests.Lock()
			requests.closed = true
			requests.Unlock()
			cancel()
			_ = server.Close()
			connections.closeAll()
			requests.wait.Wait()
			server = nil
			active = ""
		}
	}
	defer stop()
	for {
		var control gatewayControl
		if err := dec.Decode(&control); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		reply := gatewayReply{Operation: control.Operation}
		switch control.Action {
		case "begin":
			stop()
			if control.Operation == "" || control.RunID == "" {
				reply.Error = "missing gateway identity"
				break
			}
			if err := egress.ValidateGatewayPolicy(control.Policy); err != nil {
				reply.Error = err.Error()
				break
			}
			listener, err := net.Listen("tcp", "0.0.0.0:8080")
			if err != nil {
				reply.Error = err.Error()
				break
			}
			connections = &connectionSet{connections: map[net.Conn]bool{}}
			set := connections
			gateCtx, gateCancel := context.WithCancel(context.Background())
			cancel = gateCancel
			logs = &boundedGatewayLog{}
			requests = &activeRequests{}
			inFlight := requests
			gateway := &egress.Gateway{Policy: control.Policy, RunID: control.RunID, Logger: slog.New(slog.NewJSONHandler(logs, nil)), DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				c, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, address)
				if err != nil {
					return nil, err
				}
				return set.add(c), nil
			}}
			server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				inFlight.Lock()
				if inFlight.closed {
					inFlight.Unlock()
					http.Error(w, "command network grant ended", http.StatusForbidden)
					return
				}
				inFlight.wait.Add(1)
				inFlight.Unlock()
				defer inFlight.wait.Done()
				gateway.ServeHTTP(w, r)
			}), BaseContext: func(net.Listener) context.Context { return gateCtx }, ReadHeaderTimeout: 10 * time.Second}
			active = control.Operation
			go func(s *http.Server, l net.Listener) { _ = s.Serve(l) }(server, trackedListener{listener, set})
		case "end":
			if active != control.Operation {
				reply.Error = "gateway operation differs"
			} else {
				stop()
				logs.Lock()
				reply.Logs = string(logs.data)
				reply.Truncated = logs.truncated
				logs.Unlock()
			}
		default:
			reply.Error = "unknown gateway control action"
		}
		if err := enc.Encode(reply); err != nil {
			return err
		}
	}
}
